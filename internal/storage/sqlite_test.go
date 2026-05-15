package storage

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOpenTicketUniquenessAndReopen(t *testing.T) {
	tmp := t.TempDir() + "/test.sqlite"
	store, err := Open(tmp)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	_, err = store.CreateTicket("user1", "chan1")
	if err != nil {
		t.Fatalf("create first ticket: %v", err)
	}

	_, err = store.CreateTicket("user1", "chan2")
	if !errors.Is(err, ErrOpenTicketExists) {
		t.Fatalf("create duplicate open ticket err = %v, want ErrOpenTicketExists", err)
	}

	if err := store.CloseTicket("chan1", "resolved"); err != nil {
		t.Fatalf("close ticket: %v", err)
	}
	closed, err := store.GetLatestTicketByChannel("chan1")
	if err != nil {
		t.Fatalf("get closed ticket: %v", err)
	}
	if !closed.ClosedReason.Valid || closed.ClosedReason.String != "resolved" {
		t.Fatalf("closed reason = %#v, want resolved", closed.ClosedReason)
	}

	if err := store.ReopenTicket("chan1"); err != nil {
		t.Fatalf("reopen ticket: %v", err)
	}

	tk, err := store.GetOpenTicketByChannel("chan1")
	if err != nil {
		t.Fatalf("get reopened ticket: %v", err)
	}
	if tk.Status != "open" {
		t.Fatalf("status = %q, want open", tk.Status)
	}
	if tk.ClosedReason.Valid {
		t.Fatalf("closed reason valid = true, want false after reopen")
	}

	if err := store.ClaimTicket("chan1", "staff1", "@staff"); err != nil {
		t.Fatalf("claim ticket: %v", err)
	}
	if err := store.SetTicketPriority("chan1", "urgent"); err != nil {
		t.Fatalf("set priority: %v", err)
	}
	tk, err = store.GetOpenTicketByChannel("chan1")
	if err != nil {
		t.Fatalf("get claimed ticket: %v", err)
	}
	if !tk.ClaimedBy.Valid || tk.ClaimedBy.String != "staff1" {
		t.Fatalf("claimed by = %#v, want staff1", tk.ClaimedBy)
	}
	if tk.Priority != "urgent" {
		t.Fatalf("priority = %q, want urgent", tk.Priority)
	}
}

func TestMetadataNotesBlocksAndScheduledDeletions(t *testing.T) {
	store, err := Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ticket, err := store.CreateTicket("user1", "chan1")
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if err := store.ClaimTicket("chan1", "staff1", "Staff One"); err != nil {
		t.Fatalf("claim ticket: %v", err)
	}
	if err := store.SetTicketPriority("chan1", "urgent"); err != nil {
		t.Fatalf("set priority: %v", err)
	}
	if _, err := store.AddNote(ticket.ID, "staff1", "Staff One", "needs follow-up"); err != nil {
		t.Fatalf("add note: %v", err)
	}
	got, err := store.GetLatestTicketByChannel("chan1")
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	if !got.ClaimedBy.Valid || got.ClaimedBy.String != "staff1" || got.Priority != "urgent" {
		t.Fatalf("ticket metadata = %#v", got)
	}
	notes, err := store.ListNotes(ticket.ID, 5)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "needs follow-up" {
		t.Fatalf("notes = %#v", notes)
	}

	if err := store.BlockUser("user1", "spam", "staff1"); err != nil {
		t.Fatalf("block user: %v", err)
	}
	block, err := store.GetBlockedUser("user1")
	if err != nil {
		t.Fatalf("get blocked user: %v", err)
	}
	if block.Reason != "spam" {
		t.Fatalf("block reason = %q, want spam", block.Reason)
	}
	if err := store.UnblockUser("user1"); err != nil {
		t.Fatalf("unblock user: %v", err)
	}

	due := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := store.ScheduleDeletion("chan1", due); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	deletion, err := store.GetScheduledDeletion("chan1")
	if err != nil {
		t.Fatalf("get scheduled deletion: %v", err)
	}
	if !deletion.DueAt.Equal(due) {
		t.Fatalf("due_at = %s, want %s", deletion.DueAt, due)
	}
}

func TestNotesBlocklistAndScheduledDeletions(t *testing.T) {
	store, err := Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ticket, err := store.CreateTicket("user1", "chan1")
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	note, err := store.AddNote(ticket.ID, "staff1", "@staff", "follow up")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if note.Body != "follow up" {
		t.Fatalf("note body = %q, want follow up", note.Body)
	}
	notes, err := store.ListNotes(ticket.ID, 10)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("notes = %#v, want inserted note", notes)
	}

	if err := store.BlockUser("user1", "spam", "staff1"); err != nil {
		t.Fatalf("block user: %v", err)
	}
	block, err := store.GetBlockedUser("user1")
	if err != nil {
		t.Fatalf("get blocked user: %v", err)
	}
	if block.Reason != "spam" {
		t.Fatalf("block reason = %q, want spam", block.Reason)
	}
	if err := store.UnblockUser("user1"); err != nil {
		t.Fatalf("unblock user: %v", err)
	}
	if _, err := store.GetBlockedUser("user1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get unblocked user err = %v, want sql.ErrNoRows", err)
	}

	dueAt := ticket.CreatedAt.Add(time.Hour)
	if err := store.ScheduleDeletion("chan1", dueAt); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	deletion, err := store.GetScheduledDeletion("chan1")
	if err != nil {
		t.Fatalf("get scheduled deletion: %v", err)
	}
	if !deletion.DueAt.Equal(dueAt.UTC()) {
		t.Fatalf("due at = %s, want %s", deletion.DueAt, dueAt.UTC())
	}
	deletions, err := store.ListScheduledDeletions()
	if err != nil {
		t.Fatalf("list scheduled deletions: %v", err)
	}
	if len(deletions) != 1 {
		t.Fatalf("len(deletions) = %d, want 1", len(deletions))
	}
}

func TestSQLiteDSNUsesFileURIPath(t *testing.T) {
	dsn := sqliteDSN("/tmp/modmail/test.sqlite")
	if !strings.HasPrefix(dsn, "file:///tmp/modmail/test.sqlite?") {
		t.Fatalf("sqliteDSN() = %q, want absolute file URI", dsn)
	}
	if strings.Contains(dsn, "%2Ftmp") {
		t.Fatalf("sqliteDSN() = %q, path should not be slash-escaped", dsn)
	}
}
