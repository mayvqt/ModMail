package modmail

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSanitizeChannelName(t *testing.T) {
	got := sanitizeChannelName("Mod Mail_User.#123")
	if got != "mod-mail-user-123" {
		t.Fatalf("sanitizeChannelName() = %q, want %q", got, "mod-mail-user-123")
	}

	long := sanitizeChannelName(strings.Repeat("a", 200))
	if len(long) != 90 {
		t.Fatalf("long name length = %d, want 90", len(long))
	}
}

func TestUserTag(t *testing.T) {
	legacy := userTag(&discordgo.User{Username: "alice", Discriminator: "1234"})
	if legacy != "alice#1234" {
		t.Fatalf("userTag legacy = %q", legacy)
	}

	modern := userTag(&discordgo.User{Username: "bob", Discriminator: "0"})
	if modern != "@bob" {
		t.Fatalf("userTag modern = %q", modern)
	}
}
