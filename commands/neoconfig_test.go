package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vxero/neo/internal/state"
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

func TestLoadNeoConfigRestartAndHealth(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
port: 8080
restart: on-failure
health:
  cmd: curl -f http://localhost:8080/health
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 40s
workers:
  queue:
    command: php artisan queue:work
    restart: always
sidecars:
  redis:
    image: redis:7
    restart: always
    health:
      cmd: redis-cli ping
      interval: 10s
environments:
  staging:
    restart: "no"
    health:
      cmd: curl -f http://localhost:8080/ping
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	// Top-level restart
	if cfg.Restart != "on-failure" {
		t.Errorf("Restart = %q, want %q", cfg.Restart, "on-failure")
	}

	// Top-level health
	if cfg.Health == nil {
		t.Fatal("Health is nil")
	}
	if cfg.Health.Cmd != "curl -f http://localhost:8080/health" {
		t.Errorf("Health.Cmd = %q", cfg.Health.Cmd)
	}
	if cfg.Health.Interval != "30s" {
		t.Errorf("Health.Interval = %q", cfg.Health.Interval)
	}
	if cfg.Health.Timeout != "10s" {
		t.Errorf("Health.Timeout = %q", cfg.Health.Timeout)
	}
	if cfg.Health.Retries != 3 {
		t.Errorf("Health.Retries = %d", cfg.Health.Retries)
	}
	if cfg.Health.StartPeriod != "40s" {
		t.Errorf("Health.StartPeriod = %q", cfg.Health.StartPeriod)
	}

	// Worker restart
	if cfg.Workers["queue"].Restart != "always" {
		t.Errorf("Worker queue Restart = %q", cfg.Workers["queue"].Restart)
	}

	// Sidecar restart + health
	redis := cfg.Sidecars["redis"]
	if redis.Restart != "always" {
		t.Errorf("Sidecar redis Restart = %q", redis.Restart)
	}
	if redis.Health == nil || redis.Health.Cmd != "redis-cli ping" {
		t.Errorf("Sidecar redis Health.Cmd = %v", redis.Health)
	}
	if redis.Health.Interval != "10s" {
		t.Errorf("Sidecar redis Health.Interval = %q", redis.Health.Interval)
	}

	// Environment override
	staging := cfg.Environments["staging"]
	if staging.Restart != "no" {
		t.Errorf("Staging Restart = %q", staging.Restart)
	}
	if staging.Health == nil || staging.Health.Cmd != "curl -f http://localhost:8080/ping" {
		t.Errorf("Staging Health = %v", staging.Health)
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

func TestLoadNeoConfigEnvironmentVolumes(t *testing.T) {
	tmp := t.TempDir()
	content := `name: neo-cms
port: 8080
volumes:
  cache:
    path: /var/cache
environments:
  staging:
    domain: staging.example.com
    volumes:
      storage:
        path: /var/www/html/storage
  production:
    domain: example.com
    volumes:
      storage:
        path: /var/www/html/storage
      backups:
        path: /var/backups
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	// Top-level volumes
	if len(cfg.Volumes) != 1 {
		t.Fatalf("expected 1 top-level volume, got %d", len(cfg.Volumes))
	}
	if cfg.Volumes["cache"].Path != "/var/cache" {
		t.Errorf("cache path = %q", cfg.Volumes["cache"].Path)
	}

	// Staging environment volumes
	staging, ok := cfg.Environments["staging"]
	if !ok {
		t.Fatal("staging environment not found")
	}
	if len(staging.Volumes) != 1 {
		t.Fatalf("expected 1 staging volume, got %d", len(staging.Volumes))
	}
	if staging.Volumes["storage"].Path != "/var/www/html/storage" {
		t.Errorf("staging storage path = %q", staging.Volumes["storage"].Path)
	}

	// Production environment volumes
	prod, ok := cfg.Environments["production"]
	if !ok {
		t.Fatal("production environment not found")
	}
	if len(prod.Volumes) != 2 {
		t.Fatalf("expected 2 production volumes, got %d", len(prod.Volumes))
	}
	if prod.Volumes["backups"].Path != "/var/backups" {
		t.Errorf("production backups path = %q", prod.Volumes["backups"].Path)
	}

	// Test merge: env volumes override top-level with same key
	merged := make(map[string]NeoVolume)
	for k, v := range cfg.Volumes {
		merged[k] = v
	}
	for k, v := range staging.Volumes {
		merged[k] = v
	}
	if len(merged) != 2 {
		t.Errorf("expected 2 merged volumes (cache + storage), got %d", len(merged))
	}
}

func TestLoadNeoConfigDevSection(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
port: 8080
env:
  APP_ENV: production
volumes:
  database:
    path: /var/www/html/database
  storage:
    path: /var/www/html/storage
dev:
  env_file: .env
  port: 8000
  volumes:
    database: ./data/db
    logs: ./logs:/var/log/app
  env:
    APP_ENV: local
    APP_DEBUG: "true"
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if cfg.Dev == nil {
		t.Fatal("Dev section is nil")
	}
	if cfg.Dev.EnvFile != ".env" {
		t.Errorf("Dev.EnvFile = %q, want %q", cfg.Dev.EnvFile, ".env")
	}
	if cfg.Dev.Port != 8000 {
		t.Errorf("Dev.Port = %d, want %d", cfg.Dev.Port, 8000)
	}
	if cfg.Dev.Env["APP_ENV"] != "local" {
		t.Errorf("Dev.Env[APP_ENV] = %q", cfg.Dev.Env["APP_ENV"])
	}
	if cfg.Dev.Env["APP_DEBUG"] != "true" {
		t.Errorf("Dev.Env[APP_DEBUG] = %q", cfg.Dev.Env["APP_DEBUG"])
	}
	if cfg.Dev.Volumes["database"] != "./data/db" {
		t.Errorf("Dev.Volumes[database] = %q", cfg.Dev.Volumes["database"])
	}
	if cfg.Dev.Volumes["logs"] != "./logs:/var/log/app" {
		t.Errorf("Dev.Volumes[logs] = %q", cfg.Dev.Volumes["logs"])
	}

	// Top-level should still be intact
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env["APP_ENV"] != "production" {
		t.Errorf("Env[APP_ENV] = %q, want production", cfg.Env["APP_ENV"])
	}
}

func TestLoadNeoConfigDevSectionAbsent(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
port: 8080
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}
	if cfg.Dev != nil {
		t.Error("expected Dev to be nil when absent")
	}
}

func TestNeoVolumeFlatString(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
volumes:
  database: /var/www/html/database
  storage: /var/www/html/storage
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if len(cfg.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(cfg.Volumes))
	}
	if cfg.Volumes["database"].Path != "/var/www/html/database" {
		t.Errorf("database path = %q", cfg.Volumes["database"].Path)
	}
	if cfg.Volumes["storage"].Path != "/var/www/html/storage" {
		t.Errorf("storage path = %q", cfg.Volumes["storage"].Path)
	}
}

func TestNeoVolumeMixedFormats(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
volumes:
  database: /var/www/html/database
  storage:
    path: /var/www/html/storage
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	if cfg.Volumes["database"].Path != "/var/www/html/database" {
		t.Errorf("database (flat) path = %q", cfg.Volumes["database"].Path)
	}
	if cfg.Volumes["storage"].Path != "/var/www/html/storage" {
		t.Errorf("storage (structured) path = %q", cfg.Volumes["storage"].Path)
	}
}

func TestResolveConfigVolumes(t *testing.T) {
	if vols := resolveConfigVolumes(nil); len(vols) != 0 {
		t.Errorf("nil config: got %d volumes", len(vols))
	}

	cfg := &NeoConfig{}
	if vols := resolveConfigVolumes(cfg); len(vols) != 0 {
		t.Errorf("empty: got %d volumes", len(vols))
	}

	cfg = &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/data/db"},
			"storage":  {Path: "/data/storage"},
		},
	}
	vols := resolveConfigVolumes(cfg)
	if len(vols) != 2 {
		t.Fatalf("expected 2, got %d", len(vols))
	}
}

func TestVolumesFromState(t *testing.T) {
	mount := "/host/path"
	stateVols := map[string]state.VolumeInfo{
		"myapp-db":    {ContainerPath: "/data/db"},
		"myapp-files": {ContainerPath: "/data/files", Mount: &mount},
	}
	vols := volumesFromState(stateVols)
	if len(vols) != 2 {
		t.Fatalf("expected 2, got %d", len(vols))
	}

	foundNamed := false
	foundMount := false
	for _, v := range vols {
		if v == "myapp-db:/data/db" {
			foundNamed = true
		}
		if v == "/host/path:/data/files" {
			foundMount = true
		}
	}
	if !foundNamed {
		t.Error("missing named volume mount")
	}
	if !foundMount {
		t.Error("missing host mount volume")
	}
}

func TestBuildDeployVolumesFirstDeploy(t *testing.T) {
	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/data/db"},
		},
	}
	vols, declared := buildDeployVolumes("myapp", cfg, nil)
	if len(vols) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(vols))
	}
	if vols[0] != "myapp-database:/data/db" {
		t.Errorf("got %q", vols[0])
	}
	if declared["myapp-database"].ContainerPath != "/data/db" {
		t.Error("missing declared volume")
	}
}

func TestBuildDeployVolumesRedeploy(t *testing.T) {
	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/data/db"},
			"newvol":   {Path: "/data/new"},
		},
	}
	existing := &state.App{
		Volumes: map[string]state.VolumeInfo{
			"myapp-database": {ContainerPath: "/data/db"},
		},
	}
	vols, declared := buildDeployVolumes("myapp", cfg, existing)
	if len(vols) != 2 {
		t.Fatalf("expected 2 volumes, got %d: %v", len(vols), vols)
	}
	if _, ok := declared["myapp-database"]; !ok {
		t.Error("missing existing volume in declared")
	}
	if _, ok := declared["myapp-newvol"]; !ok {
		t.Error("missing new volume in declared")
	}

	// Verify the new volume mount string
	foundNew := false
	for _, v := range vols {
		if strings.Contains(v, "myapp-newvol") {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("new volume not in mount strings")
	}
}

func TestNeoVolumeBindMount(t *testing.T) {
	tmp := t.TempDir()
	content := `name: my-app
volumes:
  database: /var/www/html/database
  logs: /var/log/myapp:/var/log/app
  storage:
    path: /var/www/html/storage
    mount: /mnt/data/storage
`
	os.WriteFile(filepath.Join(tmp, ".neo.yml"), []byte(content), 0644)

	cfg, err := loadNeoConfig(tmp)
	if err != nil {
		t.Fatalf("loadNeoConfig() error: %v", err)
	}

	// Plain container path (named volume)
	if cfg.Volumes["database"].Path != "/var/www/html/database" {
		t.Errorf("database path = %q", cfg.Volumes["database"].Path)
	}
	if cfg.Volumes["database"].Mount != "" {
		t.Errorf("database mount = %q, want empty", cfg.Volumes["database"].Mount)
	}

	// Flat string bind mount (host:container)
	if cfg.Volumes["logs"].Path != "/var/log/app" {
		t.Errorf("logs path = %q, want /var/log/app", cfg.Volumes["logs"].Path)
	}
	if cfg.Volumes["logs"].Mount != "/var/log/myapp" {
		t.Errorf("logs mount = %q, want /var/log/myapp", cfg.Volumes["logs"].Mount)
	}

	// Structured bind mount
	if cfg.Volumes["storage"].Path != "/var/www/html/storage" {
		t.Errorf("storage path = %q", cfg.Volumes["storage"].Path)
	}
	if cfg.Volumes["storage"].Mount != "/mnt/data/storage" {
		t.Errorf("storage mount = %q, want /mnt/data/storage", cfg.Volumes["storage"].Mount)
	}
}

func TestBuildDeployVolumesBindMount(t *testing.T) {
	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/data/db"},
			"logs":     {Path: "/var/log/app", Mount: "/var/log/myapp"},
		},
	}
	vols, declared := buildDeployVolumes("myapp", cfg, nil)
	if len(vols) != 2 {
		t.Fatalf("expected 2 volumes, got %d: %v", len(vols), vols)
	}

	foundNamed := false
	foundBind := false
	for _, v := range vols {
		if v == "myapp-database:/data/db" {
			foundNamed = true
		}
		if v == "/var/log/myapp:/var/log/app" {
			foundBind = true
		}
	}
	if !foundNamed {
		t.Errorf("missing named volume, got %v", vols)
	}
	if !foundBind {
		t.Errorf("missing bind mount volume, got %v", vols)
	}

	// Check state stores mount info
	logsInfo := declared["myapp-logs"]
	if logsInfo.Mount == nil || *logsInfo.Mount != "/var/log/myapp" {
		t.Errorf("logs declared mount = %v, want /var/log/myapp", logsInfo.Mount)
	}
}

func TestBuildDeployVolumesNilConfig(t *testing.T) {
	vols, declared := buildDeployVolumes("myapp", nil, nil)
	if len(vols) != 0 {
		t.Errorf("expected 0 volumes, got %d", len(vols))
	}
	if len(declared) != 0 {
		t.Errorf("expected 0 declared, got %d", len(declared))
	}
}
