package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenvBasic(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_DOTENV_KEY=hello\n"), 0644)

	t.Setenv("TEST_DOTENV_KEY", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_KEY"); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
}

func TestLoadDotenvDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_DOTENV_EXISTING=fromfile\n"), 0644)

	t.Setenv("TEST_DOTENV_EXISTING", "fromenv")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_EXISTING"); got != "fromenv" {
		t.Errorf("expected fromenv (env takes precedence), got %s", got)
	}
}

func TestLoadDotenvComments(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "# this is a comment\nTEST_DOTENV_COMMENT=value\n# another comment\n"
	os.WriteFile(envFile, []byte(content), 0644)

	t.Setenv("TEST_DOTENV_COMMENT", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_COMMENT"); got != "value" {
		t.Errorf("expected value, got %s", got)
	}
}

func TestLoadDotenvEmptyLines(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "\n\nTEST_DOTENV_EMPTY=works\n\n"
	os.WriteFile(envFile, []byte(content), 0644)

	t.Setenv("TEST_DOTENV_EMPTY", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_EMPTY"); got != "works" {
		t.Errorf("expected works, got %s", got)
	}
}

func TestLoadDotenvQuotedValues(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "TEST_DOTENV_DQ=\"double quoted\"\nTEST_DOTENV_SQ='single quoted'\n"
	os.WriteFile(envFile, []byte(content), 0644)

	t.Setenv("TEST_DOTENV_DQ", "")
	t.Setenv("TEST_DOTENV_SQ", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_DQ"); got != "double quoted" {
		t.Errorf("expected 'double quoted', got %s", got)
	}
	if got := os.Getenv("TEST_DOTENV_SQ"); got != "single quoted" {
		t.Errorf("expected 'single quoted', got %s", got)
	}
}

func TestLoadDotenvSpacesAroundEquals(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "TEST_DOTENV_SPACES = spaced\n"
	os.WriteFile(envFile, []byte(content), 0644)

	t.Setenv("TEST_DOTENV_SPACES", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_SPACES"); got != "spaced" {
		t.Errorf("expected spaced, got %s", got)
	}
}

func TestLoadDotenvMissingFile(t *testing.T) {
	err := LoadDotenv("/nonexistent/path/.env")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadDotenvMalformedLine(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("NO_EQUALS_SIGN\n"), 0644)

	err := LoadDotenv(envFile)
	if err == nil {
		t.Error("expected error for malformed line")
	}
}

func TestLoadDotenvEmptyKey(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("=value\n"), 0644)

	err := LoadDotenv(envFile)
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestLoadDotenvEmptyValue(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_DOTENV_EMPTYVAL=\n"), 0644)

	t.Setenv("TEST_DOTENV_EMPTYVAL", "")

	err := LoadDotenv(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		line    string
		key     string
		value   string
		wantErr bool
	}{
		{"KEY=value", "KEY", "value", false},
		{"KEY = value", "KEY", "value", false},
		{"KEY=\"quoted\"", "KEY", "quoted", false},
		{"KEY='quoted'", "KEY", "quoted", false},
		{"KEY=", "KEY", "", false},
		{"KEY=value with spaces", "KEY", "value with spaces", false},
		{"NOEQ", "", "", true},
		{"=nokey", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			key, value, err := parseLine(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != tt.key {
				t.Errorf("expected key %q, got %q", tt.key, key)
			}
			if value != tt.value {
				t.Errorf("expected value %q, got %q", tt.value, value)
			}
		})
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`''`, ""},
		{`"`, `"`},
		{``, ``},
		{`"mismatched'`, `"mismatched'`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := unquote(tt.input)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
