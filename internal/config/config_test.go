package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoadEmpty(t *testing.T) {
	// Use temp dir so no existing config interferes
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Current != "" {
		t.Errorf("expected empty Current, got %q", cfg.Current)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(cfg.Servers))
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &Config{
		Current: "prod",
		Servers: map[string]Server{
			"prod": {Name: "prod", Host: "root@1.2.3.4", Port: 22, InitializedAt: "2026-01-01T00:00:00Z"},
		},
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file exists
	cfgPath := filepath.Join(tmp, ".neo", "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("config file not created at %s", cfgPath)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Current != "prod" {
		t.Errorf("Current = %q, want %q", loaded.Current, "prod")
	}
	if len(loaded.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(loaded.Servers))
	}
	srv := loaded.Servers["prod"]
	if srv.Host != "root@1.2.3.4" {
		t.Errorf("Host = %q, want %q", srv.Host, "root@1.2.3.4")
	}
	if srv.Port != 22 {
		t.Errorf("Port = %d, want %d", srv.Port, 22)
	}
}

func TestCurrentServer(t *testing.T) {
	cfg := &Config{
		Current: "staging",
		Servers: map[string]Server{
			"prod":    {Name: "prod", Host: "root@1.2.3.4", Port: 22},
			"staging": {Name: "staging", Host: "root@5.6.7.8", Port: 2222},
		},
	}

	srv, err := cfg.CurrentServer()
	if err != nil {
		t.Fatalf("CurrentServer() error: %v", err)
	}
	if srv.Name != "staging" {
		t.Errorf("Name = %q, want %q", srv.Name, "staging")
	}
	if srv.Port != 2222 {
		t.Errorf("Port = %d, want %d", srv.Port, 2222)
	}
}

func TestCurrentServerNoneSelected(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{}}

	_, err := cfg.CurrentServer()
	if err == nil {
		t.Fatal("expected error when no server selected")
	}
}

func TestCurrentServerMissing(t *testing.T) {
	cfg := &Config{
		Current: "gone",
		Servers: map[string]Server{},
	}

	_, err := cfg.CurrentServer()
	if err == nil {
		t.Fatal("expected error for missing server")
	}
}

func TestAddServer(t *testing.T) {
	cfg := &Config{Servers: make(map[string]Server)}

	cfg.AddServer(Server{Name: "first", Host: "root@1.1.1.1", Port: 22})
	if cfg.Current != "first" {
		t.Errorf("Current = %q, want %q (auto-set on first add)", cfg.Current, "first")
	}

	cfg.AddServer(Server{Name: "second", Host: "root@2.2.2.2", Port: 22})
	if cfg.Current != "first" {
		t.Errorf("Current = %q, want %q (should not change)", cfg.Current, "first")
	}
	if len(cfg.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(cfg.Servers))
	}
}

func TestRemoveServer(t *testing.T) {
	cfg := &Config{
		Current: "prod",
		Servers: map[string]Server{
			"prod":    {Name: "prod", Host: "root@1.1.1.1"},
			"staging": {Name: "staging", Host: "root@2.2.2.2"},
		},
	}

	cfg.RemoveServer("prod")
	if _, ok := cfg.Servers["prod"]; ok {
		t.Error("prod should be removed")
	}
	if cfg.Current == "prod" {
		t.Error("Current should switch away from removed server")
	}
	if cfg.Current != "staging" {
		t.Errorf("Current = %q, want %q (fallback to remaining)", cfg.Current, "staging")
	}
}

func TestRemoveLastServer(t *testing.T) {
	cfg := &Config{
		Current: "only",
		Servers: map[string]Server{
			"only": {Name: "only", Host: "root@1.1.1.1"},
		},
	}

	cfg.RemoveServer("only")
	if cfg.Current != "" {
		t.Errorf("Current = %q, want empty after removing last server", cfg.Current)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(cfg.Servers))
	}
}

func TestFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &Config{Current: "x", Servers: map[string]Server{"x": {Name: "x"}}}
	Save(cfg)

	info, err := os.Stat(filepath.Join(tmp, ".neo", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permission = %o, want 0600", perm)
	}

	dirInfo, err := os.Stat(filepath.Join(tmp, ".neo"))
	if err != nil {
		t.Fatal(err)
	}
	dirPerm := dirInfo.Mode().Perm()
	if dirPerm != 0o700 {
		t.Errorf("dir permission = %o, want 0700", dirPerm)
	}
}

func TestCacheConcurrentReadWrite(t *testing.T) {
	// The dashboard renders from the cache on the main goroutine while up to ten
	// background goroutines refresh server entries. Handing out the live map
	// crashed the process with "concurrent map read and map write"; run this
	// with -race to catch a regression.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				UpdateServerCache(fmt.Sprintf("server-%d", n), ServerCache{
					AppCount:  j,
					Reachable: true,
					UpdatedAt: time.Now(),
				})
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if c := LoadCache(); c != nil {
					_ = c.Get(fmt.Sprintf("server-%d", n))
					for name := range c.Servers { // iterate the snapshot
						_ = name
					}
				}
				_ = GetServerCache(fmt.Sprintf("server-%d", n))
			}
		}(i)
	}
	wg.Wait()
}

func TestGetServerCacheReturnsCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	UpdateServerCache("box", ServerCache{AppCount: 3, Reachable: true})

	got := GetServerCache("box")
	if got == nil || got.AppCount != 3 {
		t.Fatalf("GetServerCache = %+v", got)
	}
	got.AppCount = 99 // mutating the copy must not touch the cache

	if again := GetServerCache("box"); again.AppCount != 3 {
		t.Errorf("cache was mutated through the returned pointer: %d", again.AppCount)
	}
	if GetServerCache("missing") != nil {
		t.Error("unknown server should return nil")
	}
}
