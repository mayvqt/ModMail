package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DiscordToken    string
	GuildID         string
	StaffCategoryID string
	StaffRoleID     string
	LogChannelID    string
	DBPath          string
	CommandPrefix   string
	EnableSlash     bool
	AutoDeleteAfter time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		DiscordToken:    os.Getenv("DISCORD_TOKEN"),
		GuildID:         os.Getenv("GUILD_ID"),
		StaffCategoryID: os.Getenv("STAFF_CATEGORY_ID"),
		StaffRoleID:     os.Getenv("STAFF_ROLE_ID"),
		LogChannelID:    os.Getenv("LOG_CHANNEL_ID"),
		DBPath:          getenv("DB_PATH", "/data/modmail.sqlite"),
		CommandPrefix:   getenv("COMMAND_PREFIX", "!"),
		EnableSlash:     getenvBool("ENABLE_SLASH_COMMANDS", true),
	}
	var err error
	cfg.AutoDeleteAfter, err = getenvDuration("AUTO_DELETE_CLOSED_TICKET_AFTER", "0s")
	if err != nil {
		return cfg, err
	}

	if cfg.DiscordToken == "" {
		return cfg, fmt.Errorf("DISCORD_TOKEN is required")
	}
	if cfg.GuildID == "" {
		return cfg, fmt.Errorf("GUILD_ID is required")
	}
	if cfg.StaffCategoryID == "" {
		return cfg, fmt.Errorf("STAFF_CATEGORY_ID is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvDuration(key, fallback string) (time.Duration, error) {
	v := getenv(key, fallback)
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid duration: %w", key, err)
	}
	return d, nil
}
