package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	StaffIdentity   string
	StaffReplyLabel string
	LogLevel        string
	EnableSlash     bool
	AutoDeleteAfter time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		DiscordToken:    strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		GuildID:         strings.TrimSpace(os.Getenv("GUILD_ID")),
		StaffCategoryID: strings.TrimSpace(os.Getenv("STAFF_CATEGORY_ID")),
		StaffRoleID:     strings.TrimSpace(os.Getenv("STAFF_ROLE_ID")),
		LogChannelID:    strings.TrimSpace(os.Getenv("LOG_CHANNEL_ID")),
		DBPath:          getenv("DB_PATH", "/data/modmail.sqlite"),
		CommandPrefix:   getenv("COMMAND_PREFIX", "!"),
		StaffIdentity:   strings.ToLower(getenv("STAFF_IDENTITY", "anonymous")),
		StaffReplyLabel: getenv("STAFF_REPLY_LABEL", "Moderator"),
		LogLevel:        strings.ToLower(getenv("LOG_LEVEL", "info")),
	}
	var err error
	cfg.EnableSlash, err = getenvBool("ENABLE_SLASH_COMMANDS", true)
	if err != nil {
		return cfg, err
	}
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
	if cfg.CommandPrefix == "" {
		return cfg, fmt.Errorf("COMMAND_PREFIX cannot be empty")
	}
	switch cfg.StaffIdentity {
	case "anonymous", "named", "role":
	default:
		return cfg, fmt.Errorf("STAFF_IDENTITY must be anonymous, named, or role")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return cfg, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
	if cfg.AutoDeleteAfter < 0 {
		return cfg, fmt.Errorf("AUTO_DELETE_CLOSED_TICKET_AFTER cannot be negative")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s is invalid boolean: %w", key, err)
	}
	return parsed, nil
}

func getenvDuration(key, fallback string) (time.Duration, error) {
	v := getenv(key, fallback)
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid duration: %w", key, err)
	}
	return d, nil
}
