package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseComposeFileMapEnv(t *testing.T) {
	content := `services:
  app:
    build: .
    ports:
      - "8080:3000"
    environment:
      APP_ENV: production
      DB_HOST: mysql
      DB_PORT: 3306
  mysql:
    image: mysql:8
    environment:
      MYSQL_ROOT_PASSWORD: secret
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	result, err := parseComposeFile(path, "app")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.ServiceName != "app" {
		t.Errorf("ServiceName = %q, want %q", result.ServiceName, "app")
	}
	if result.Env["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q", result.Env["APP_ENV"])
	}
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q", result.Env["DB_HOST"])
	}
	if result.Env["DB_PORT"] != "3306" {
		t.Errorf("DB_PORT = %q", result.Env["DB_PORT"])
	}
	if result.Port != 3000 {
		t.Errorf("Port = %d, want 3000", result.Port)
	}
	// Should NOT include mysql service env vars
	if _, ok := result.Env["MYSQL_ROOT_PASSWORD"]; ok {
		t.Error("should not include env from other services")
	}
}

func TestParseComposeFileListEnv(t *testing.T) {
	content := `services:
  web:
    build: .
    environment:
      - APP_ENV=production
      - DB_HOST=localhost
      - APP_KEY=base64:abc=123
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	result, err := parseComposeFile(path, "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.Env["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q", result.Env["APP_ENV"])
	}
	if result.Env["APP_KEY"] != "base64:abc=123" {
		t.Errorf("APP_KEY = %q, want %q", result.Env["APP_KEY"], "base64:abc=123")
	}
}

func TestParseComposeFileLaravel(t *testing.T) {
	content := `services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8080:8000"
    environment:
      APP_NAME: Vanguard
      APP_ENV: production
      APP_KEY: "base64:dGhpcyBpcyBhIHRlc3Qga2V5"
      APP_DEBUG: false
      APP_URL: https://vanguard.dev
      DB_CONNECTION: mysql
      DB_HOST: mysql
      DB_PORT: 3306
      DB_DATABASE: vanguard
      DB_USERNAME: root
      DB_PASSWORD: secret
      REDIS_HOST: redis
      CACHE_DRIVER: redis
      SESSION_DRIVER: redis
    depends_on:
      - mysql
      - redis
  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: secret
      MYSQL_DATABASE: vanguard
    volumes:
      - mysql-data:/var/lib/mysql
  redis:
    image: redis:7-alpine
volumes:
  mysql-data:
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	// Auto-detect the app service (has build, not infra)
	result, err := parseComposeFile(path, "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.ServiceName != "app" {
		t.Errorf("ServiceName = %q, want %q (should auto-detect build service)", result.ServiceName, "app")
	}
	if result.Env["APP_NAME"] != "Vanguard" {
		t.Errorf("APP_NAME = %q", result.Env["APP_NAME"])
	}
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q", result.Env["DB_HOST"])
	}
	if result.Port != 8000 {
		t.Errorf("Port = %d, want 8000", result.Port)
	}
	if len(result.Env) != 14 {
		t.Errorf("expected 14 env vars, got %d", len(result.Env))
	}
}

func TestParseComposeFileWithEnvFile(t *testing.T) {
	envContent := `DB_HOST=localhost
DB_PORT=3306
APP_SECRET=hunter2
`
	composeContent := `services:
  app:
    build: .
    env_file:
      - .env
    environment:
      APP_ENV: production
      DB_HOST: mysql
`
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ".env"), []byte(envContent), 0644)
	os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte(composeContent), 0644)

	result, err := parseComposeFile(filepath.Join(tmp, "docker-compose.yml"), "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	// Explicit environment should override env_file
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q, want %q (explicit should override env_file)", result.Env["DB_HOST"], "mysql")
	}
	// env_file values should be present
	if result.Env["APP_SECRET"] != "hunter2" {
		t.Errorf("APP_SECRET = %q (from env_file)", result.Env["APP_SECRET"])
	}
}

func TestParseComposePortFormats(t *testing.T) {
	tests := []struct {
		ports []string
		want  int
	}{
		{[]string{"8080:3000"}, 3000},
		{[]string{"3000"}, 3000},
		{[]string{"8080:3000/tcp"}, 3000},
		{[]string{"0.0.0.0:8080:3000"}, 3000},
		{nil, 0},
		{[]string{}, 0},
	}

	for _, tt := range tests {
		got := parseComposePort(tt.ports)
		if got != tt.want {
			t.Errorf("parseComposePort(%v) = %d, want %d", tt.ports, got, tt.want)
		}
	}
}

func TestGuessAppService(t *testing.T) {
	services := map[string]composeService{
		"app":   {Build: ".", Ports: []string{"8080:3000"}},
		"mysql": {Image: "mysql:8"},
		"redis": {Image: "redis:7"},
	}

	name, _ := guessAppService(services)
	if name != "app" {
		t.Errorf("guessAppService() = %q, want %q", name, "app")
	}
}

func TestGuessAppServiceNoInfra(t *testing.T) {
	services := map[string]composeService{
		"web":      {Build: "."},
		"postgres": {Image: "postgres:15"},
		"nginx":    {Image: "nginx:latest"},
	}

	name, _ := guessAppService(services)
	if name != "web" {
		t.Errorf("guessAppService() = %q, want %q", name, "web")
	}
}

func TestFindComposeFile(t *testing.T) {
	tmp := t.TempDir()

	// No compose file
	if got := findComposeFile(tmp); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// docker-compose.yml
	os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte("services:"), 0644)
	if got := findComposeFile(tmp); got == "" {
		t.Error("expected to find docker-compose.yml")
	}
}

func TestFindComposeFileAlternateNames(t *testing.T) {
	tmp := t.TempDir()

	// compose.yml (newer convention)
	os.WriteFile(filepath.Join(tmp, "compose.yml"), []byte("services:"), 0644)
	if got := findComposeFile(tmp); got == "" {
		t.Error("expected to find compose.yml")
	}
}

func TestParseComposeFileMissingService(t *testing.T) {
	content := `services:
  app:
    build: .
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	_, err := parseComposeFile(path, "nonexistent")
	if err == nil {
		t.Error("expected error for missing service")
	}
}
