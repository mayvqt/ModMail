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

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    prefixCommand
		wantOK  bool
	}{
		{name: "exact", content: "!close", want: prefixCommand{Name: "close"}, wantOK: true},
		{name: "with args", content: "!close resolved", want: prefixCommand{Name: "close", Args: "resolved"}, wantOK: true},
		{name: "trimmed", content: "  !note   useful context  ", want: prefixCommand{Name: "note", Args: "useful context"}, wantOK: true},
		{name: "case folded", content: "!CLOSE done", want: prefixCommand{Name: "close", Args: "done"}, wantOK: true},
		{name: "no prefix", content: "close", wantOK: false},
		{name: "prefix only", content: "!", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCommand(tt.content, "!")
			if ok != tt.wantOK {
				t.Fatalf("parseCommand(%q) ok = %v, want %v", tt.content, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("parseCommand(%q) = %#v, want %#v", tt.content, got, tt.want)
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

func TestSplitDiscordMessageHandlesMultibyteText(t *testing.T) {
	msg := strings.Repeat("界", maxMessageLength+5)
	chunks := splitDiscordMessage(msg)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	for i, chunk := range chunks {
		if len([]rune(chunk)) > maxMessageLength {
			t.Fatalf("chunk %d rune length = %d, want <= %d", i, len([]rune(chunk)), maxMessageLength)
		}
		if strings.ContainsRune(chunk, '\uFFFD') {
			t.Fatalf("chunk %d contains replacement rune", i)
		}
	}
}

func TestTruncateTextHandlesMultibyteText(t *testing.T) {
	got := truncateText("界界界", 2)
	if got != "界界" {
		t.Fatalf("truncateText() = %q, want %q", got, "界界")
	}
}

func TestRelayMessageIncludesAttachmentsAndStickers(t *testing.T) {
	got := relayMessage("**User:**", "hello", []*discordgo.MessageAttachment{{URL: "https://example.com/a.png"}}, []*discordgo.StickerItem{{Name: "wave"}})
	want := "**User:**\nhello\nhttps://example.com/a.png\n[sticker: wave]"
	if got != want {
		t.Fatalf("relayMessage() = %q, want %q", got, want)
	}
}

func TestRelayMessageEmpty(t *testing.T) {
	if got := relayMessage("**User:**", "   ", nil, nil); got != "" {
		t.Fatalf("relayMessage() = %q, want empty", got)
	}
}

func TestUserTagNilSafe(t *testing.T) {
	if got := userTag(nil); got != "unknown" {
		t.Fatalf("userTag(nil) = %q, want unknown", got)
	}
}
