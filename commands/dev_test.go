package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDevEnvAutoLoadsDotEnv(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ".env"), []byte("APP_KEY=secret123\nDB_HOST=localhost\n"), 0644)

	env := buildDevEnv(tmp, nil)

	if env["APP_KEY"] != "secret123" {
		t.Errorf("APP_KEY = %q, want %q", env["APP_KEY"], "secret123")
	}
	if env["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q, want %q", env["DB_HOST"], "localhost")
	}
}

func TestBuildDevEnvPriority(t *testing.T) {
	tmp := t.TempDir()

	// .env (lowest priority)
	os.WriteFile(filepath.Join(tmp, ".env"), []byte("A=from-dotenv\nB=from-dotenv\nC=from-dotenv\nD=from-dotenv\nE=from-dotenv\n"), 0644)

	// Top-level env_file
	os.WriteFile(filepath.Join(tmp, "top.env"), []byte("B=from-top-envfile\nC=from-top-envfile\nD=from-top-envfile\nE=from-top-envfile\n"), 0644)

	// Dev env_file
	os.WriteFile(filepath.Join(tmp, "dev.env"), []byte("D=from-dev-envfile\nE=from-dev-envfile\n"), 0644)

	cfg := &NeoConfig{
		EnvFile: "top.env",
		Env:     map[string]string{"C": "from-top-env", "D": "from-top-env", "E": "from-top-env"},
		Dev: &NeoDevConfig{
			EnvFile: "dev.env",
			Env:     map[string]string{"E": "from-dev-env"},
		},
	}

	env := buildDevEnv(tmp, cfg)

	// A: only in .env
	if env["A"] != "from-dotenv" {
		t.Errorf("A = %q, want from-dotenv", env["A"])
	}
	// B: .env overridden by top-level env_file
	if env["B"] != "from-top-envfile" {
		t.Errorf("B = %q, want from-top-envfile", env["B"])
	}
	// C: overridden by top-level env
	if env["C"] != "from-top-env" {
		t.Errorf("C = %q, want from-top-env", env["C"])
	}
	// D: overridden by dev env_file
	if env["D"] != "from-dev-envfile" {
		t.Errorf("D = %q, want from-dev-envfile", env["D"])
	}
	// E: overridden by dev env (highest)
	if env["E"] != "from-dev-env" {
		t.Errorf("E = %q, want from-dev-env", env["E"])
	}
}

func TestBuildDevEnvInterpolation(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ".env"), []byte("APP_KEY=secret123\n"), 0644)

	cfg := &NeoConfig{
		Dev: &NeoDevConfig{
			Env: map[string]string{
				"KEY_REF": "${APP_KEY}",
			},
		},
	}

	env := buildDevEnv(tmp, cfg)

	if env["KEY_REF"] != "secret123" {
		t.Errorf("KEY_REF = %q, want secret123", env["KEY_REF"])
	}
}

func TestBuildDevEnvNilConfig(t *testing.T) {
	tmp := t.TempDir()
	env := buildDevEnv(tmp, nil)
	if len(env) != 0 {
		t.Errorf("expected empty env, got %d vars", len(env))
	}
}

func TestBuildDevVolumesAutoMount(t *testing.T) {
	tmp := t.TempDir()

	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/var/www/html/database"},
			"storage":  {Path: "/var/www/html/storage"},
		},
	}

	mounts, err := buildDevVolumes(tmp, cfg)
	if err != nil {
		t.Fatalf("buildDevVolumes() error: %v", err)
	}

	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(mounts))
	}

	// Check that auto-mounted directories were created
	for _, name := range []string{"database", "storage"} {
		dir := filepath.Join(tmp, name)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("expected directory %s to be created", dir)
		}
	}

	// Check mount strings contain the expected paths
	found := map[string]bool{}
	for _, m := range mounts {
		if strings.Contains(m, "/var/www/html/database") {
			found["database"] = true
		}
		if strings.Contains(m, "/var/www/html/storage") {
			found["storage"] = true
		}
	}
	if !found["database"] || !found["storage"] {
		t.Errorf("missing expected mounts, got %v", mounts)
	}
}

func TestBuildDevVolumesShortForm(t *testing.T) {
	tmp := t.TempDir()

	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/var/www/html/database"},
		},
		Dev: &NeoDevConfig{
			Volumes: map[string]string{
				"database": "./data/db",
			},
		},
	}

	mounts, err := buildDevVolumes(tmp, cfg)
	if err != nil {
		t.Fatalf("buildDevVolumes() error: %v", err)
	}

	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}

	expected := filepath.Join(tmp, "data/db") + ":/var/www/html/database"
	if mounts[0] != expected {
		t.Errorf("mount = %q, want %q", mounts[0], expected)
	}
}

func TestBuildDevVolumesFullForm(t *testing.T) {
	tmp := t.TempDir()

	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/var/www/html/database"},
		},
		Dev: &NeoDevConfig{
			Volumes: map[string]string{
				"logs": "./logs:/var/log/app",
			},
		},
	}

	mounts, err := buildDevVolumes(tmp, cfg)
	if err != nil {
		t.Fatalf("buildDevVolumes() error: %v", err)
	}

	// Should have 2: auto-mounted database + full-form logs
	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d: %v", len(mounts), mounts)
	}

	foundDB := false
	foundLogs := false
	for _, m := range mounts {
		if strings.Contains(m, "/var/www/html/database") {
			foundDB = true
		}
		if strings.Contains(m, "/var/log/app") {
			foundLogs = true
		}
	}
	if !foundDB {
		t.Error("missing auto-mounted database volume")
	}
	if !foundLogs {
		t.Error("missing full-form logs volume")
	}
}

func TestBuildDevVolumesInvalidShortForm(t *testing.T) {
	tmp := t.TempDir()

	cfg := &NeoConfig{
		Volumes: map[string]NeoVolume{
			"database": {Path: "/var/www/html/database"},
		},
		Dev: &NeoDevConfig{
			Volumes: map[string]string{
				"nonexistent": "./data",
			},
		},
	}

	_, err := buildDevVolumes(tmp, cfg)
	if err == nil {
		t.Error("expected error for dev volume not matching top-level volume")
	}
}

func TestBuildDevVolumesNilConfig(t *testing.T) {
	tmp := t.TempDir()
	mounts, err := buildDevVolumes(tmp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected no mounts, got %d", len(mounts))
	}
}

func TestBuildDevVolumesNoVolumes(t *testing.T) {
	tmp := t.TempDir()
	cfg := &NeoConfig{Name: "my-app"}
	mounts, err := buildDevVolumes(tmp, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected no mounts, got %d", len(mounts))
	}
}

func TestResolveDevPort(t *testing.T) {
	// Default
	if resolveDevPort(nil) != 8080 {
		t.Errorf("nil config: got %d, want 8080", resolveDevPort(nil))
	}

	// Top-level port only
	cfg := &NeoConfig{Port: 3000}
	if resolveDevPort(cfg) != 3000 {
		t.Errorf("top-level port: got %d, want 3000", resolveDevPort(cfg))
	}

	// Dev port overrides
	cfg.Dev = &NeoDevConfig{Port: 8000}
	if resolveDevPort(cfg) != 8000 {
		t.Errorf("dev port: got %d, want 8000", resolveDevPort(cfg))
	}

	// Dev port 0 falls back to top-level
	cfg.Dev.Port = 0
	if resolveDevPort(cfg) != 3000 {
		t.Errorf("dev port 0: got %d, want 3000", resolveDevPort(cfg))
	}
}
