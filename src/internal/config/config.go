package config

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the application configuration loaded from environment variables.
type Config struct {
	Port        string
	SMTPTimeout time.Duration
	HELODomain  string
	MailFrom    string
	LogLevel    slog.Level
	APIKeys     []string
}

// Load reads configuration from environment variables and returns a Config with
// sensible defaults for any values not set.
func Load() Config {
	cfg := Config{
		Port:        "8080",
		SMTPTimeout: 10 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		LogLevel:    slog.LevelInfo,
	}

	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}

	if v := os.Getenv("SMTP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			slog.Warn("invalid SMTP_TIMEOUT, using default", "value", v, "default", cfg.SMTPTimeout)
		} else {
			cfg.SMTPTimeout = d
		}
	}

	if v := os.Getenv("HELO_DOMAIN"); v != "" {
		cfg.HELODomain = v
	}

	if v := os.Getenv("MAIL_FROM"); v != "" {
		cfg.MailFrom = v
	}

	if v := os.Getenv("LOG_LEVEL"); v != "" {
		switch v {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			slog.Warn("invalid LOG_LEVEL, using default", "value", v, "default", "info")
		}
	}

	if v := os.Getenv("API_KEYS"); v != "" {
		for _, key := range strings.Split(v, ",") {
			key = strings.TrimSpace(key)
			if key != "" {
				cfg.APIKeys = append(cfg.APIKeys, key)
			}
		}
	}

	return cfg
}
