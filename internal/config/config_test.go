package config

import "testing"

func TestLoadRejectsInvalidBool(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("GUILD_ID", "guild")
	t.Setenv("STAFF_CATEGORY_ID", "category")
	t.Setenv("ENABLE_SLASH_COMMANDS", "maybe")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid boolean error")
	}
}

func TestLoadRejectsNegativeAutoDelete(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("GUILD_ID", "guild")
	t.Setenv("STAFF_CATEGORY_ID", "category")
	t.Setenv("AUTO_DELETE_CLOSED_TICKET_AFTER", "-1s")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want negative duration error")
	}
}
