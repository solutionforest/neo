package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRelease(t *testing.T) {
	top := HookCommands{"php artisan migrate --force"}
	env := HookCommands{"php artisan storage:link"}

	// An environment's list replaces the top-level one, matching hooks:.
	if got := resolveRelease(top, env); len(got) != 1 || got[0] != "php artisan storage:link" {
		t.Errorf("environment list should replace top-level, got %v", got)
	}
	if got := resolveRelease(top, nil); len(got) != 1 || got[0] != top[0] {
		t.Errorf("empty environment list should inherit, got %v", got)
	}
	if got := resolveRelease(nil, nil); len(got) != 0 {
		t.Errorf("expected no commands, got %v", got)
	}
}

func TestReleaseCommandsNilSafe(t *testing.T) {
	var cfg *NeoConfig
	if got := cfg.ReleaseCommands(); got != nil {
		t.Errorf("nil config should yield nil, got %v", got)
	}
	if got := (&NeoConfig{}).ReleaseCommands(); len(got) != 0 {
		t.Errorf("empty config should yield nothing, got %v", got)
	}
}

func TestReleaseParsesFromYAML(t *testing.T) {
	dir := t.TempDir()
	content := `name: fsd-cms
release:
  - php artisan migrate --force
environments:
  dev:
    release:
      - php artisan storage:link
      - php artisan config:cache
  prod:
    server: prod-box
`
	if err := os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := loadNeoConfig(dir)
	if err != nil {
		t.Fatalf("loadNeoConfig: %v", err)
	}
	if len(cfg.Release) != 1 || cfg.Release[0] != "php artisan migrate --force" {
		t.Errorf("top-level release = %v", cfg.Release)
	}
	if got := cfg.Environments["dev"].Release; len(got) != 2 {
		t.Errorf("environments.dev.release = %v, want 2 commands", got)
	}
	if got := cfg.Environments["prod"].Release; len(got) != 0 {
		t.Errorf("environments.prod.release = %v, want empty (inherits)", got)
	}

	// Effective lists per environment.
	if got := resolveRelease(cfg.Release, cfg.Environments["prod"].Release); len(got) != 1 {
		t.Errorf("prod should inherit the top-level list, got %v", got)
	}
	if got := resolveRelease(cfg.Release, cfg.Environments["dev"].Release); len(got) != 2 {
		t.Errorf("dev should use its own list, got %v", got)
	}
}

func TestReleaseAcceptsSingleString(t *testing.T) {
	// HookCommands unmarshals a bare string as a one-element list.
	dir := t.TempDir()
	content := "name: app\nrelease: php artisan migrate --force\n"
	if err := os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := loadNeoConfig(dir)
	if err != nil {
		t.Fatalf("loadNeoConfig: %v", err)
	}
	if len(cfg.Release) != 1 || cfg.Release[0] != "php artisan migrate --force" {
		t.Errorf("release = %v", cfg.Release)
	}
}

func TestRunReleaseCommandsNoopWhenEmpty(t *testing.T) {
	// nil docker would panic if the function tried to run anything.
	if err := runReleaseCommands(nil, "app-x", nil); err != nil {
		t.Errorf("empty command list should be a no-op, got %v", err)
	}
}
