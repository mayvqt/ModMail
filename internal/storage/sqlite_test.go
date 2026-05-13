package storage

import (
	"errors"
	"strings"
	"testing"
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

	if err := store.CloseTicket("chan1"); err != nil {
		t.Fatalf("close ticket: %v", err)
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
