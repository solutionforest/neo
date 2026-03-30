package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vxero/neo/internal/ssh"
)

func TestParseEnvFile(t *testing.T) {
	content := `# Database config
DB_HOST=localhost
DB_PORT=3306
DB_NAME=myapp

# Quoted values
APP_KEY="base64:abc123def456"
APP_NAME='My Cool App'

# No value
EMPTY_VAR=

# Spaces around equals
 SPACED = hello

# Comment and blank lines are skipped
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".env")
	os.WriteFile(path, []byte(content), 0644)

	env, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile() error: %v", err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{"DB_HOST", "localhost"},
		{"DB_PORT", "3306"},
		{"DB_NAME", "myapp"},
		{"APP_KEY", "base64:abc123def456"},
		{"APP_NAME", "My Cool App"},
		{"EMPTY_VAR", ""},
		{"SPACED", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := env[tt.key]
			if !ok {
				t.Fatalf("key %q not found", tt.key)
			}
			if got != tt.want {
				t.Errorf("env[%q] = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	// Comments should not appear
	for k := range env {
		if k == "#" || k[0] == '#' {
			t.Errorf("comment key should not be in env: %q", k)
		}
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	_, err := parseEnvFile("/nonexistent/.env")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseEnvFileLaravel(t *testing.T) {
	// Realistic Laravel .env file
	content := `APP_NAME=Vanguard
APP_ENV=production
APP_KEY=base64:dGhpcyBpcyBhIHRlc3Qga2V5
APP_DEBUG=false
APP_URL=https://vanguard.dev

LOG_CHANNEL=stack
LOG_LEVEL=debug

DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=vanguard
DB_USERNAME=root
DB_PASSWORD="s3cr3t!p@ss"

MAIL_MAILER=smtp
MAIL_HOST=smtp.mailgun.org
MAIL_PORT=587
MAIL_USERNAME=null
MAIL_PASSWORD=null

REDIS_HOST=127.0.0.1
REDIS_PASSWORD=null
REDIS_PORT=6379
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".env")
	os.WriteFile(path, []byte(content), 0644)

	env, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile() error: %v", err)
	}

	if env["APP_NAME"] != "Vanguard" {
		t.Errorf("APP_NAME = %q", env["APP_NAME"])
	}
	if env["DB_PASSWORD"] != "s3cr3t!p@ss" {
		t.Errorf("DB_PASSWORD = %q, want %q", env["DB_PASSWORD"], "s3cr3t!p@ss")
	}
	if env["APP_KEY"] != "base64:dGhpcyBpcyBhIHRlc3Qga2V5" {
		t.Errorf("APP_KEY = %q", env["APP_KEY"])
	}
	if env["REDIS_PORT"] != "6379" {
		t.Errorf("REDIS_PORT = %q", env["REDIS_PORT"])
	}
	if len(env) != 21 {
		t.Errorf("expected 21 env vars, got %d", len(env))
	}
}

func TestParseEnvPairs(t *testing.T) {
	pairs := []string{"KEY=value", "DB_HOST=localhost", "APP_KEY=base64:abc=123"}
	env, err := parseEnvPairs(pairs)
	if err != nil {
		t.Fatalf("parseEnvPairs() error: %v", err)
	}

	if env["KEY"] != "value" {
		t.Errorf("KEY = %q", env["KEY"])
	}
	if env["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q", env["DB_HOST"])
	}
	// Value with = sign in it
	if env["APP_KEY"] != "base64:abc=123" {
		t.Errorf("APP_KEY = %q, want %q", env["APP_KEY"], "base64:abc=123")
	}
}

func TestParseEnvPairsInvalid(t *testing.T) {
	_, err := parseEnvPairs([]string{"NOEQUALS"})
	if err == nil {
		t.Error("expected error for missing = sign")
	}

	_, err = parseEnvPairs([]string{"=value"})
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`"`, `"`},
		{`'mismatched"`, `'mismatched"`},
		{`"has spaces"`, "has spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := unquote(tt.input)
			if got != tt.want {
				t.Errorf("unquote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInterpolateEnvValues(t *testing.T) {
	env := map[string]string{
		"APP_KEY":    "secret123",
		"APP_URL":    "https://example.com",
		"FULL_URL":   "${APP_URL}/api",
		"NESTED_KEY": "${APP_KEY}",
		"NO_REF":     "plain-value",
		"UNRESOLVED": "${MISSING_VAR}",
		"MULTI":      "${APP_KEY}:${APP_URL}",
	}

	result := interpolateEnvValues(env)

	tests := []struct {
		key  string
		want string
	}{
		{"APP_KEY", "secret123"},
		{"FULL_URL", "https://example.com/api"},
		{"NESTED_KEY", "secret123"},
		{"NO_REF", "plain-value"},
		{"UNRESOLVED", "${MISSING_VAR}"},
		{"MULTI", "secret123:https://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if result[tt.key] != tt.want {
				t.Errorf("interpolateEnvValues[%q] = %q, want %q", tt.key, result[tt.key], tt.want)
			}
		})
	}
}

func TestInterpolateStringFromOS(t *testing.T) {
	os.Setenv("NEO_TEST_INTERP_VAR", "from-os")
	defer os.Unsetenv("NEO_TEST_INTERP_VAR")

	env := map[string]string{}
	result := interpolateString("val=${NEO_TEST_INTERP_VAR}", env)
	if result != "val=from-os" {
		t.Errorf("got %q, want %q", result, "val=from-os")
	}
}

func TestInterpolateStringEnvMapTakesPrecedence(t *testing.T) {
	os.Setenv("NEO_TEST_INTERP_VAR2", "from-os")
	defer os.Unsetenv("NEO_TEST_INTERP_VAR2")

	env := map[string]string{"NEO_TEST_INTERP_VAR2": "from-map"}
	result := interpolateString("${NEO_TEST_INTERP_VAR2}", env)
	if result != "from-map" {
		t.Errorf("got %q, want %q", result, "from-map")
	}
}

func TestInterpolateStringNoPattern(t *testing.T) {
	result := interpolateString("no-refs-here", nil)
	if result != "no-refs-here" {
		t.Errorf("got %q, want %q", result, "no-refs-here")
	}
}

func TestInterpolateStringPartialPattern(t *testing.T) {
	// Incomplete ${... without closing brace
	result := interpolateString("prefix${UNCLOSED", nil)
	if result != "prefix${UNCLOSED" {
		t.Errorf("got %q, want %q", result, "prefix${UNCLOSED")
	}
}

func TestLooksLikeSecret(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"APP_KEY", true},
		{"DB_PASSWORD", true},
		{"API_TOKEN", true},
		{"SECRET_KEY", true},
		{"APP_NAME", false},
		{"DB_HOST", false},
		{"LOG_LEVEL", false},
		{"PRIVATE_KEY", true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := looksLikeSecret(tt.key)
			if got != tt.want {
				t.Errorf("looksLikeSecret(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"has spaces", "'has spaces'"},
		{"with'quote", "'with'\\''quote'"},
		{"special!@#$", "'special!@#$'"},
		{"", "''"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ssh.ShellQuote(tt.input)
			if got != tt.want {
				t.Errorf("ShellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
