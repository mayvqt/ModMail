package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Ticket struct {
	ID           int64
	UserID       string
	ChannelID    string
	Status       string
	ClaimedBy    sql.NullString
	ClaimedTag   sql.NullString
	Priority     string
	CreatedAt    time.Time
	ClosedAt     sql.NullTime
	ClosedReason sql.NullString
}

type Note struct {
	ID        int64
	TicketID  int64
	AuthorID  string
	AuthorTag string
	Body      string
	CreatedAt time.Time
}

type BlockedUser struct {
	UserID    string
	Reason    string
	CreatedBy string
	CreatedAt time.Time
}

type ScheduledDeletion struct {
	ChannelID string
	DueAt     time.Time
}

type Store struct {
	db *sql.DB
}

var ErrOpenTicketExists = errors.New("open ticket already exists for user")

const (
	TicketStatusOpen   = "open"
	TicketStatusClosed = "closed"
)

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}

	dsn := sqliteDSN(path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(path string) string {
	if path == ":memory:" {
		return "file::memory:?_pragma=foreign_keys(ON)"
	}

	values := url.Values{}
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(NORMAL)")
	values.Add("_pragma", "busy_timeout(5000)")
	values.Add("_pragma", "foreign_keys(ON)")

	return (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: values.Encode(),
	}).String()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  channel_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
  claimed_by TEXT,
  claimed_tag TEXT,
  priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  closed_at DATETIME,
  closed_reason TEXT
);
CREATE INDEX IF NOT EXISTS idx_tickets_user_status ON tickets(user_id, status);
CREATE INDEX IF NOT EXISTS idx_tickets_channel ON tickets(channel_id);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tickets_open_user ON tickets(user_id) WHERE status = 'open';

CREATE TABLE IF NOT EXISTS notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL,
  author_id TEXT NOT NULL,
  author_tag TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_notes_ticket_created ON notes(ticket_id, created_at DESC);

CREATE TABLE IF NOT EXISTS blocked_users (
  user_id TEXT PRIMARY KEY,
  reason TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scheduled_deletions (
  channel_id TEXT PRIMARY KEY,
  due_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scheduled_deletions_due ON scheduled_deletions(due_at);
`)
	return err
}

func (s *Store) CreateTicket(userID, channelID string) (*Ticket, error) {
	res, err := s.db.Exec(`INSERT INTO tickets(user_id, channel_id, status) VALUES (?, ?, ?)`, userID, channelID, TicketStatusOpen)
	if err != nil {
		if isOpenTicketConstraint(err) {
			return nil, ErrOpenTicketExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTicketByID(id)
}

func (s *Store) GetOpenTicketByUser(userID string) (*Ticket, error) {
	return s.queryOne(ticketSelectSQL+` WHERE user_id = ? AND status = ? ORDER BY id DESC LIMIT 1`, userID, TicketStatusOpen)
}

func (s *Store) GetOpenTicketByChannel(channelID string) (*Ticket, error) {
	return s.queryOne(ticketSelectSQL+` WHERE channel_id = ? AND status = ? LIMIT 1`, channelID, TicketStatusOpen)
}

func (s *Store) GetTicketByID(id int64) (*Ticket, error) {
	return s.queryOne(ticketSelectSQL+` WHERE id = ?`, id)
}

func (s *Store) GetLatestTicketByChannel(channelID string) (*Ticket, error) {
	return s.queryOne(ticketSelectSQL+` WHERE channel_id = ? ORDER BY id DESC LIMIT 1`, channelID)
}

func (s *Store) CloseTicket(channelID, reason string) error {
	reason = strings.TrimSpace(reason)
	var reasonValue any
	if reason != "" {
		reasonValue = reason
	}

	res, err := s.db.Exec(`UPDATE tickets SET status = ?, closed_at = CURRENT_TIMESTAMP, closed_reason = ? WHERE channel_id = ? AND status = ?`, TicketStatusClosed, reasonValue, channelID, TicketStatusOpen)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ReopenTicket(channelID string) error {
	res, err := s.db.Exec(`UPDATE tickets SET status = ?, closed_at = NULL, closed_reason = NULL WHERE channel_id = ? AND status = ?`, TicketStatusOpen, channelID, TicketStatusClosed)
	if err != nil {
		if isOpenTicketConstraint(err) {
			return ErrOpenTicketExists
		}
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ClaimTicket(channelID, staffID, staffTag string) error {
	res, err := s.db.Exec(`UPDATE tickets SET claimed_by = ?, claimed_tag = ? WHERE channel_id = ?`, staffID, staffTag, channelID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UnclaimTicket(channelID string) error {
	res, err := s.db.Exec(`UPDATE tickets SET claimed_by = NULL, claimed_tag = NULL WHERE channel_id = ?`, channelID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetTicketPriority(channelID, priority string) error {
	if !ValidPriority(priority) {
		return fmt.Errorf("invalid priority %q", priority)
	}
	res, err := s.db.Exec(`UPDATE tickets SET priority = ? WHERE channel_id = ?`, priority, channelID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ValidPriority(priority string) bool {
	switch priority {
	case "low", "normal", "high", "urgent":
		return true
	default:
		return false
	}
}

func (s *Store) AddNote(ticketID int64, authorID, authorTag, body string) (*Note, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("note body is required")
	}
	res, err := s.db.Exec(`INSERT INTO notes(ticket_id, author_id, author_tag, body) VALUES (?, ?, ?, ?)`, ticketID, authorID, authorTag, body)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getNoteByID(id)
}

func (s *Store) ListNotes(ticketID int64, limit int) ([]*Note, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.Query(`SELECT id, ticket_id, author_id, author_tag, body, created_at FROM notes WHERE ticket_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, ticketID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []*Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return notes, nil
}

func (s *Store) BlockUser(userID, reason, createdBy string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "No reason provided."
	}
	_, err := s.db.Exec(`
INSERT INTO blocked_users(user_id, reason, created_by)
VALUES (?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET reason = excluded.reason, created_by = excluded.created_by, created_at = CURRENT_TIMESTAMP
`, userID, reason, createdBy)
	return err
}

func (s *Store) UnblockUser(userID string) error {
	res, err := s.db.Exec(`DELETE FROM blocked_users WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetBlockedUser(userID string) (*BlockedUser, error) {
	var b BlockedUser
	row := s.db.QueryRow(`SELECT user_id, reason, created_by, created_at FROM blocked_users WHERE user_id = ?`, userID)
	if err := row.Scan(&b.UserID, &b.Reason, &b.CreatedBy, &b.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query blocked user: %w", err)
	}
	return &b, nil
}

func (s *Store) ScheduleDeletion(channelID string, dueAt time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO scheduled_deletions(channel_id, due_at)
VALUES (?, ?)
ON CONFLICT(channel_id) DO UPDATE SET due_at = excluded.due_at
`, channelID, dueAt.UTC())
	return err
}

func (s *Store) DeleteScheduledDeletion(channelID string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_deletions WHERE channel_id = ?`, channelID)
	return err
}

func (s *Store) GetScheduledDeletion(channelID string) (*ScheduledDeletion, error) {
	var d ScheduledDeletion
	row := s.db.QueryRow(`SELECT channel_id, due_at FROM scheduled_deletions WHERE channel_id = ?`, channelID)
	if err := row.Scan(&d.ChannelID, &d.DueAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query scheduled deletion: %w", err)
	}
	return &d, nil
}

func (s *Store) ListScheduledDeletions() ([]*ScheduledDeletion, error) {
	rows, err := s.db.Query(`SELECT channel_id, due_at FROM scheduled_deletions ORDER BY due_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deletions []*ScheduledDeletion
	for rows.Next() {
		var d ScheduledDeletion
		if err := rows.Scan(&d.ChannelID, &d.DueAt); err != nil {
			return nil, err
		}
		deletions = append(deletions, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deletions, nil
}

func (s *Store) queryOne(query string, args ...any) (*Ticket, error) {
	var t Ticket
	row := s.db.QueryRow(query, args...)
	if err := row.Scan(&t.ID, &t.UserID, &t.ChannelID, &t.Status, &t.ClaimedBy, &t.ClaimedTag, &t.Priority, &t.CreatedAt, &t.ClosedAt, &t.ClosedReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query ticket: %w", err)
	}
	return &t, nil
}

func (s *Store) getNoteByID(id int64) (*Note, error) {
	row := s.db.QueryRow(`SELECT id, ticket_id, author_id, author_tag, body, created_at FROM notes WHERE id = ?`, id)
	n, err := scanNote(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query note: %w", err)
	}
	return n, nil
}

type noteScanner interface {
	Scan(dest ...any) error
}

func scanNote(scanner noteScanner) (*Note, error) {
	var n Note
	if err := scanner.Scan(&n.ID, &n.TicketID, &n.AuthorID, &n.AuthorTag, &n.Body, &n.CreatedAt); err != nil {
		return nil, err
	}
	return &n, nil
}

func isOpenTicketConstraint(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "ux_tickets_open_user") || strings.Contains(msg, "UNIQUE constraint failed: tickets.user_id")
}

const ticketSelectSQL = `SELECT id, user_id, channel_id, status, claimed_by, claimed_tag, priority, created_at, closed_at, closed_reason FROM tickets`
