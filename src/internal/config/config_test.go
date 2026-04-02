package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("SMTP_TIMEOUT", "")
	t.Setenv("HELO_DOMAIN", "")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("API_KEYS", "")

	cfg := Load()

	if cfg.Port != "8080" {
		t.Errorf("expected port 8080, got %s", cfg.Port)
	}
	if cfg.SMTPTimeout != 10*time.Second {
		t.Errorf("expected timeout 10s, got %v", cfg.SMTPTimeout)
	}
	if cfg.HELODomain != "localhost" {
		t.Errorf("expected HELO domain localhost, got %s", cfg.HELODomain)
	}
	if cfg.MailFrom != "probe@localhost" {
		t.Errorf("expected MAIL FROM probe@localhost, got %s", cfg.MailFrom)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected log level info, got %v", cfg.LogLevel)
	}
	if len(cfg.APIKeys) != 0 {
		t.Errorf("expected no API keys, got %v", cfg.APIKeys)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "3000")
	t.Setenv("SMTP_TIMEOUT", "30s")
	t.Setenv("HELO_DOMAIN", "probe.example.com")
	t.Setenv("MAIL_FROM", "verify@example.com")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := Load()

	if cfg.Port != "3000" {
		t.Errorf("expected port 3000, got %s", cfg.Port)
	}
	if cfg.SMTPTimeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", cfg.SMTPTimeout)
	}
	if cfg.HELODomain != "probe.example.com" {
		t.Errorf("expected HELO domain probe.example.com, got %s", cfg.HELODomain)
	}
	if cfg.MailFrom != "verify@example.com" {
		t.Errorf("expected MAIL FROM verify@example.com, got %s", cfg.MailFrom)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected log level debug, got %v", cfg.LogLevel)
	}
}

func TestLoadInvalidTimeout(t *testing.T) {
	t.Setenv("SMTP_TIMEOUT", "not-a-duration")

	cfg := Load()

	if cfg.SMTPTimeout != 10*time.Second {
		t.Errorf("expected default timeout 10s for invalid input, got %v", cfg.SMTPTimeout)
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	cfg := Load()

	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default log level info for invalid input, got %v", cfg.LogLevel)
	}
}

func TestLoadAllLogLevels(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.input)
			cfg := Load()
			if cfg.LogLevel != tt.expected {
				t.Errorf("expected log level %v for input %q, got %v", tt.expected, tt.input, cfg.LogLevel)
			}
		})
	}
}

func TestLoadAPIKeys(t *testing.T) {
	t.Setenv("API_KEYS", "key1,key2,key3")
	cfg := Load()

	if len(cfg.APIKeys) != 3 {
		t.Fatalf("expected 3 API keys, got %d", len(cfg.APIKeys))
	}
	if cfg.APIKeys[0] != "key1" || cfg.APIKeys[1] != "key2" || cfg.APIKeys[2] != "key3" {
		t.Errorf("unexpected API keys: %v", cfg.APIKeys)
	}
}

func TestLoadAPIKeysWithSpaces(t *testing.T) {
	t.Setenv("API_KEYS", " key1 , key2 , key3 ")
	cfg := Load()

	if len(cfg.APIKeys) != 3 {
		t.Fatalf("expected 3 API keys, got %d", len(cfg.APIKeys))
	}
	if cfg.APIKeys[0] != "key1" || cfg.APIKeys[1] != "key2" || cfg.APIKeys[2] != "key3" {
		t.Errorf("unexpected API keys: %v", cfg.APIKeys)
	}
}

func TestLoadAPIKeysSingleKey(t *testing.T) {
	t.Setenv("API_KEYS", "only-one-key")
	cfg := Load()

	if len(cfg.APIKeys) != 1 {
		t.Fatalf("expected 1 API key, got %d", len(cfg.APIKeys))
	}
	if cfg.APIKeys[0] != "only-one-key" {
		t.Errorf("expected only-one-key, got %s", cfg.APIKeys[0])
	}
}

func TestLoadAPIKeysEmpty(t *testing.T) {
	t.Setenv("API_KEYS", "")
	cfg := Load()

	if len(cfg.APIKeys) != 0 {
		t.Errorf("expected no API keys, got %v", cfg.APIKeys)
	}
}
