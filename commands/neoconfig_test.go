package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNeoConfig(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-laravel-app
domain: app.example.com
port: 8080
env_file: .env
env:
  APP_ENV: production
  APP_DEBUG: "false"
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}

	if cfg.Name != "my-laravel-app" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my-laravel-app")
	}
	if cfg.Domain != "app.example.com" {
		t.Errorf("Domain = %q, want %q", cfg.Domain, "app.example.com")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if cfg.EnvFile != ".env" {
		t.Errorf("EnvFile = %q, want %q", cfg.EnvFile, ".env")
	}
	if cfg.Env["APP_ENV"] != "production" {
		t.Errorf("Env[APP_ENV] = %q", cfg.Env["APP_ENV"])
	}
	if cfg.Env["APP_DEBUG"] != "false" {
		t.Errorf("Env[APP_DEBUG] = %q", cfg.Env["APP_DEBUG"])
	}
}

func TestLoadNeoConfigMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil for missing .neo.yml")
	}
}

func TestLoadNeoConfigMinimal(t *testing.T) {
	tmp := t.TempDir()
	content := `name: ghost-blog
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if cfg.Name != "ghost-blog" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0", cfg.Port)
	}
	if cfg.Domain != "" {
		t.Errorf("Domain = %q, want empty", cfg.Domain)
	}
	if len(cfg.Env) != 0 {
		t.Errorf("Env should be empty, got %d", len(cfg.Env))
	}
}

func TestLoadNeoConfigEnvOnly(t *testing.T) {
	tmp := t.TempDir()
	content := `env:
  DB_HOST: mysql
  DB_PORT: "3306"
  APP_KEY: "base64:abc123"
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if len(cfg.Env) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(cfg.Env))
	}
	if cfg.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q", cfg.Env["DB_HOST"])
	}
	if cfg.Env["DB_PORT"] != "3306" {
		t.Errorf("DB_PORT = %q", cfg.Env["DB_PORT"])
	}
}

func TestLoadNeoConfigInvalid(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte("{{invalid yaml"), 0644)

	_, err := loadNeoConfig(tmp)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadNeoConfigWorkers(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
workers:
  queue:
    command: php artisan queue:work --tries=3
  scheduler:
    command: php artisan schedule:work
    health_check: php artisan schedule:test
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if len(cfg.Workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(cfg.Workers))
	}
	if cfg.Workers["queue"].Command != "php artisan queue:work --tries=3" {
		t.Errorf("queue command = %q", cfg.Workers["queue"].Command)
	}
	if cfg.Workers["scheduler"].Command != "php artisan schedule:work" {
		t.Errorf("scheduler command = %q", cfg.Workers["scheduler"].Command)
	}
	if cfg.Workers["scheduler"].HealthCheck != "php artisan schedule:test" {
		t.Errorf("scheduler health_check = %q", cfg.Workers["scheduler"].HealthCheck)
	}
}

func TestLoadNeoConfigVolumes(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
volumes:
  data:
    path: /var/www/html/database
  storage:
    path: /var/www/html/storage
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if len(cfg.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(cfg.Volumes))
	}
	if cfg.Volumes["data"].Path != "/var/www/html/database" {
		t.Errorf("data path = %q", cfg.Volumes["data"].Path)
	}
	if cfg.Volumes["storage"].Path != "/var/www/html/storage" {
		t.Errorf("storage path = %q", cfg.Volumes["storage"].Path)
	}
}

func TestLoadNeoConfigWorkersAndVolumes(t *testing.T) {
	tmp := t.TempDir()
	content := `name: neo-cms
domain: neo.vxero.dev
port: 8080
env:
  QUEUE_CONNECTION: database
volumes:
  data:
    path: /var/www/html/database
workers:
  queue:
    command: php artisan queue:work --tries=3
environments:
  production:
    domain: neo.vxero.dev
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if cfg.Name != "neo-cms" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if len(cfg.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(cfg.Workers))
	}
	if len(cfg.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(cfg.Volumes))
	}
	if len(cfg.Environments) != 1 {
		t.Fatalf("expected 1 environment, got %d", len(cfg.Environments))
	}
}
