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

func TestIsCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "exact", content: "!close", want: true},
		{name: "with reason", content: "!close resolved", want: true},
		{name: "trimmed", content: "  !close  ", want: true},
		{name: "prefix only", content: "!closeplease", want: false},
		{name: "wrong command", content: "!reopen", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCommand(tt.content, "!", "close"); got != tt.want {
				t.Fatalf("isCommand(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestSplitDiscordMessage(t *testing.T) {
	msg := strings.Repeat("a", maxMessageLength) + "\n" + strings.Repeat("b", 10)
	chunks := splitDiscordMessage(msg)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) > maxMessageLength {
			t.Fatalf("chunk %d length = %d, want <= %d", i, len(chunk), maxMessageLength)
		}
	}
}
