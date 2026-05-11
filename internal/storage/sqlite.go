package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Ticket struct {
	ID        int64
	UserID    string
	ChannelID string
	Status    string
	CreatedAt time.Time
	ClosedAt  sql.NullTime
}

type Store struct {
	db *sql.DB
}

var ErrOpenTicketExists = errors.New("open ticket already exists for user")

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=%s&_pragma=%s&_pragma=%s&_pragma=%s",
		url.PathEscape(path),
		url.QueryEscape("journal_mode(WAL)"),
		url.QueryEscape("synchronous(NORMAL)"),
		url.QueryEscape("busy_timeout(5000)"),
		url.QueryEscape("foreign_keys(ON)"),
	)

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

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  channel_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'open',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  closed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_tickets_user_status ON tickets(user_id, status);
CREATE INDEX IF NOT EXISTS idx_tickets_channel ON tickets(channel_id);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tickets_open_user ON tickets(user_id) WHERE status = 'open';
`)
	return err
}

func (s *Store) CreateTicket(userID, channelID string) (*Ticket, error) {
	res, err := s.db.Exec(`INSERT INTO tickets(user_id, channel_id, status) VALUES (?, ?, 'open')`, userID, channelID)
	if err != nil {
		if strings.Contains(err.Error(), "ux_tickets_open_user") || strings.Contains(err.Error(), "UNIQUE constraint failed: tickets.user_id") {
			return nil, ErrOpenTicketExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTicketByID(id)
}

func (s *Store) GetOpenTicketByUser(userID string) (*Ticket, error) {
	return s.queryOne(`SELECT id, user_id, channel_id, status, created_at, closed_at FROM tickets WHERE user_id = ? AND status = 'open' ORDER BY id DESC LIMIT 1`, userID)
}

func (s *Store) GetOpenTicketByChannel(channelID string) (*Ticket, error) {
	return s.queryOne(`SELECT id, user_id, channel_id, status, created_at, closed_at FROM tickets WHERE channel_id = ? AND status = 'open' LIMIT 1`, channelID)
}

func (s *Store) GetTicketByID(id int64) (*Ticket, error) {
	return s.queryOne(`SELECT id, user_id, channel_id, status, created_at, closed_at FROM tickets WHERE id = ?`, id)
}

func (s *Store) GetLatestTicketByChannel(channelID string) (*Ticket, error) {
	return s.queryOne(`SELECT id, user_id, channel_id, status, created_at, closed_at FROM tickets WHERE channel_id = ? ORDER BY id DESC LIMIT 1`, channelID)
}

func (s *Store) CloseTicket(channelID string) error {
	res, err := s.db.Exec(`UPDATE tickets SET status = 'closed', closed_at = CURRENT_TIMESTAMP WHERE channel_id = ? AND status = 'open'`, channelID)
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
	res, err := s.db.Exec(`UPDATE tickets SET status = 'open', closed_at = NULL WHERE channel_id = ? AND status = 'closed'`, channelID)
	if err != nil {
		if strings.Contains(err.Error(), "ux_tickets_open_user") || strings.Contains(err.Error(), "UNIQUE constraint failed: tickets.user_id") {
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

func (s *Store) queryOne(query string, args ...any) (*Ticket, error) {
	var t Ticket
	row := s.db.QueryRow(query, args...)
	if err := row.Scan(&t.ID, &t.UserID, &t.ChannelID, &t.Status, &t.CreatedAt, &t.ClosedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query ticket: %w", err)
	}
	return &t, nil
}
