package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vxero/neo/internal/state"
)

// fsdConfig mirrors a real .neo.yml that `neo sync` corrupted: environments:
// with a per-environment domain, comments, 2-space indent and unquoted values.
const fsdConfig = `name: fsd-cms
port: 8080
https: true
edge_https: true # HTTP origin, but app sees X-Forwarded-Proto: https

env:
  CADDY_AUTO_HTTPS: on
  TRUSTED_PROXIES: "*"
  APP_DEBUG: "false"
env_file: ./docker/share.env

environments:
  dev:
    server: fsd-dev
    domain: fsd-cms-dev.gotest.dev
    basic_auth:
      user: sf
      password: sf@1234
      bypass:
        - /api/*
    env_file: ./docker/dev.env
    https: true
    edge_https: true

workers:
  queue:
    command: php artisan queue:work --tries=3
`

func writeSyncConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".neo.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestSyncWritesIntoEnvironmentNotRoot(t *testing.T) {
	// The bug: sync wrote domain: at the root, and deploy then refused the file
	// with "root-level domain:/domains: is ignored when environments: are defined".
	path := writeSyncConfig(t, fsdConfig)

	changes := []syncChange{{"~", "domain", "fsd-cms-dev.gotest.dev", "fsd-cms-new.gotest.dev"}}
	if err := writeSyncChanges(path, "dev", changes); err != nil {
		t.Fatalf("writeSyncChanges: %v", err)
	}

	out := readFile(t, path)

	cfg, err := loadNeoConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Domain != "" {
		t.Errorf("root domain was written (%q) — deploy rejects that with environments:", cfg.Domain)
	}
	if got := cfg.Environments["dev"].Domain; got != "fsd-cms-new.gotest.dev" {
		t.Errorf("environments.dev.domain = %q, want the new domain", got)
	}
	if strings.HasPrefix(out, "name: fsd-cms\ndomain:") {
		t.Error("domain was inserted at the top level")
	}
}

func TestSyncPreservesCommentsAndFormatting(t *testing.T) {
	path := writeSyncConfig(t, fsdConfig)

	changes := []syncChange{{"~", "domain", "fsd-cms-dev.gotest.dev", "fsd-cms-new.gotest.dev"}}
	if err := writeSyncChanges(path, "dev", changes); err != nil {
		t.Fatalf("writeSyncChanges: %v", err)
	}
	out := readFile(t, path)

	if !strings.Contains(out, "# HTTP origin, but app sees X-Forwarded-Proto: https") {
		t.Error("comment was stripped")
	}
	if !strings.Contains(out, "CADDY_AUTO_HTTPS: on") {
		t.Errorf("unquoted value was rewritten:\n%s", out)
	}
	if !strings.Contains(out, `TRUSTED_PROXIES: "*"`) {
		t.Errorf("double-quoted value lost its style:\n%s", out)
	}
	if !strings.Contains(out, "\n  dev:\n") {
		t.Errorf("indentation changed from 2 spaces:\n%s", out)
	}
	// Untouched blocks must survive verbatim.
	for _, want := range []string{"env_file: ./docker/share.env", "password: sf@1234", "- /api/*", "php artisan queue:work --tries=3"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q from the file:\n%s", want, out)
		}
	}
	// Key order is preserved: name still comes first.
	if !strings.HasPrefix(out, "name: fsd-cms\n") {
		t.Errorf("key order changed:\n%s", out)
	}
}

func TestSyncWritesPortAndHTTPS(t *testing.T) {
	path := writeSyncConfig(t, fsdConfig)

	changes := []syncChange{
		{"~", "port", "8080", "9000"},
		{"~", "https", "true", "false"},
	}
	if err := writeSyncChanges(path, "dev", changes); err != nil {
		t.Fatalf("writeSyncChanges: %v", err)
	}

	cfg, err := loadNeoConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	env := cfg.Environments["dev"]
	if env.Port != 9000 {
		t.Errorf("environments.dev.port = %d, want 9000", env.Port)
	}
	if env.HTTPS == nil || *env.HTTPS {
		t.Errorf("environments.dev.https = %v, want false", env.HTTPS)
	}
	// Root values are untouched.
	if cfg.Port != 8080 {
		t.Errorf("root port changed to %d", cfg.Port)
	}
}

func TestSyncRootLevelWhenNoEnvironments(t *testing.T) {
	path := writeSyncConfig(t, "name: simple\nport: 3000\ndomain: old.example.com\n")

	changes := []syncChange{{"~", "domain", "old.example.com", "new.example.com"}}
	if err := writeSyncChanges(path, "", changes); err != nil {
		t.Fatalf("writeSyncChanges: %v", err)
	}

	cfg, err := loadNeoConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Domain != "new.example.com" {
		t.Errorf("domain = %q, want new.example.com", cfg.Domain)
	}
}

func TestSyncUnknownEnvironmentFails(t *testing.T) {
	path := writeSyncConfig(t, fsdConfig)
	err := writeSyncChanges(path, "staging", []syncChange{{"~", "domain", "", "x.example.com"}})
	if err == nil {
		t.Fatal("expected an error for an environment that isn't in the file")
	}
	// The file must be left untouched on failure.
	if !strings.Contains(readFile(t, path), "fsd-cms-dev.gotest.dev") {
		t.Error("file was modified despite the error")
	}
}

func TestResolveSyncEnvironment(t *testing.T) {
	single := &NeoConfig{Environments: map[string]NeoEnvironment{"dev": {}}}
	if got, err := resolveSyncEnvironment(single, ""); err != nil || got != "dev" {
		t.Errorf("single environment: got (%q, %v), want dev", got, err)
	}

	multi := &NeoConfig{Environments: map[string]NeoEnvironment{"dev": {}, "prod": {}}}
	if got, err := resolveSyncEnvironment(multi, "prod"); err != nil || got != "prod" {
		t.Errorf("--to prod: got (%q, %v)", got, err)
	}
	if _, err := resolveSyncEnvironment(multi, "nope"); err == nil {
		t.Error("expected an error for an unknown --to value")
	}

	none := &NeoConfig{}
	if got, err := resolveSyncEnvironment(none, ""); err != nil || got != "" {
		t.Errorf("no environments: got (%q, %v), want empty", got, err)
	}
}

func TestEffectiveSyncTargetInheritsRoot(t *testing.T) {
	httpsOn := true
	cfg := &NeoConfig{
		Port:  8080,
		HTTPS: &httpsOn,
		Environments: map[string]NeoEnvironment{
			"dev": {Domain: "dev.example.com"}, // no port/https of its own
		},
	}

	got := effectiveSyncTarget(cfg, "dev")
	if got.domain != "dev.example.com" {
		t.Errorf("domain = %q", got.domain)
	}
	if got.port != 8080 {
		t.Errorf("port = %d, want the inherited 8080", got.port)
	}
	if got.https == nil || !*got.https {
		t.Errorf("https = %v, want the inherited true", got.https)
	}
}

func TestDiffSyncTargetLeavesDomainsListAlone(t *testing.T) {
	current := syncTarget{hasDomains: true, port: 8080}
	app := state.App{Domain: "a.example.com", InternalPort: 8080}

	for _, c := range diffSyncTarget(current, app) {
		if c.field == "domain" {
			t.Error("sync tried to rewrite a domains: list, which would drop the other entries")
		}
	}
}

func TestDiffSyncTargetNoChanges(t *testing.T) {
	httpsOn := true
	current := syncTarget{domain: "a.example.com", port: 8080, https: &httpsOn}
	app := state.App{Domain: "a.example.com", InternalPort: 8080, HTTPOnly: false}

	if got := diffSyncTarget(current, app); len(got) != 0 {
		t.Errorf("expected no changes, got %+v", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSyncResolvesTheEnvironmentServer(t *testing.T) {
	// sync used to connect to whatever `neo use` had selected, so syncing an
	// environment hosted on another machine looked at the wrong state.
	cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
		"production": {Server: "prod-box"},
		"staging":    {Servers: []string{"staging-box"}},
		"dr":         {Servers: []string{"dr-a", "dr-b"}},
		"inherited":  {},
	}}
	cfg.Server = "root-box"

	if got := environmentServers(cfg.Environments["production"], cfg); len(got) != 1 || got[0] != "prod-box" {
		t.Errorf("production = %v, want [prod-box]", got)
	}
	// A one-element servers: list must resolve, not fall through to the active server.
	if got := environmentServers(cfg.Environments["staging"], cfg); len(got) != 1 || got[0] != "staging-box" {
		t.Errorf("staging = %v, want [staging-box]", got)
	}
	// A real group is ambiguous for a read — the caller must pick.
	if got := environmentServers(cfg.Environments["dr"], cfg); len(got) != 2 {
		t.Errorf("dr = %v, want two servers", got)
	}
	// No server on the environment falls back to the root-level one.
	if got := environmentServers(cfg.Environments["inherited"], cfg); len(got) != 1 || got[0] != "root-box" {
		t.Errorf("inherited = %v, want [root-box]", got)
	}
}
