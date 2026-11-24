package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the bot configuration read from the environment.
type Config struct {
	Token          string
	GuildID        string
	DatabasePath   string
	StoreKey       string
	BaseURL        string
	Port           string
	NotifyInterval time.Duration
	LogLevel       slog.Level
	UmamiSiteID    string
	UmamiEndpoint  string
	CheckinLead    time.Duration
}

// Load reads configuration from .env (when present) and the environment.
// Real environment variables win over .env values.
func Load() (*Config, error) {
	_ = godotenv.Load()

	interval, err := time.ParseDuration(envOr("NOTIFY_INTERVAL", "30s"))
	if err != nil {
		return nil, fmt.Errorf("NOTIFY_INTERVAL: %w", err)
	}
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	checkinLead, err := time.ParseDuration(envOr("CHECKIN_LEAD_TIME", "1h"))
	if err != nil {
		return nil, fmt.Errorf("CHECKIN_LEAD_TIME: %w", err)
	}
	if checkinLead < 0 {
		checkinLead = 0
	}

	cfg := &Config{
		Token:          strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		GuildID:        strings.TrimSpace(os.Getenv("GUILD_ID")),
		DatabasePath:   envOr("DATABASE_PATH", "./data/locksmith.db"),
		StoreKey:       strings.TrimSpace(os.Getenv("STORE_KEY")),
		BaseURL:        strings.TrimSpace(os.Getenv("CHASTISAFE_BASE_URL")),
		Port:           envOr("PORT", "8080"),
		NotifyInterval: interval,
		LogLevel:       parseLevel(os.Getenv("LOG_LEVEL")),
		UmamiSiteID:    strings.TrimSpace(os.Getenv("UMAMI_SITE_ID")),
		UmamiEndpoint:  strings.TrimSpace(os.Getenv("UMAMI_ENDPOINT")),
		CheckinLead:    checkinLead,
	}

	if cfg.Token == "" {
		return nil, errors.New("DISCORD_TOKEN is required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
