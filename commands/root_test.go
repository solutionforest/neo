package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vxero/neo/internal/config"
)

// chdir switches into dir for the duration of a test and restores the previous
// working directory afterward. resolveServer/neoConfigServer read .neo.yml from
// the process cwd, so these tests cannot run in parallel.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestNeoConfigServerReadsCwd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte("name: app\nserver: nebula-51\n"), 0644)
	chdir(t, dir)

	if got := neoConfigServer(); got != "nebula-51" {
		t.Fatalf("neoConfigServer() = %q, want nebula-51", got)
	}
}

func TestNeoConfigServerEmptyWhenNoConfig(t *testing.T) {
	chdir(t, t.TempDir())
	if got := neoConfigServer(); got != "" {
		t.Fatalf("neoConfigServer() = %q, want empty", got)
	}
}

func TestResolveServerPrefersProjectOverCurrent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte("server: nebula-51\n"), 0644)
	chdir(t, dir)

	old := serverFlag
	serverFlag = ""
	t.Cleanup(func() { serverFlag = old })

	cfg := &config.Config{
		Current: "noble-53",
		Servers: map[string]config.Server{
			"noble-53":  {Name: "noble-53", Host: "root@1.1.1.1", Port: 22},
			"nebula-51": {Name: "nebula-51", Host: "root@2.2.2.2", Port: 22},
		},
	}
	srv, err := resolveServer(cfg)
	if err != nil {
		t.Fatalf("resolveServer error: %v", err)
	}
	if srv.Name != "nebula-51" {
		t.Fatalf("resolveServer picked %q, want nebula-51 (from .neo.yml)", srv.Name)
	}
}

func TestResolveServerFlagWinsOverProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte("server: nebula-51\n"), 0644)
	chdir(t, dir)

	old := serverFlag
	serverFlag = "noble-53"
	t.Cleanup(func() { serverFlag = old })

	cfg := &config.Config{
		Current: "nebula-51",
		Servers: map[string]config.Server{
			"noble-53":  {Name: "noble-53", Host: "root@1.1.1.1", Port: 22},
			"nebula-51": {Name: "nebula-51", Host: "root@2.2.2.2", Port: 22},
		},
	}
	srv, err := resolveServer(cfg)
	if err != nil {
		t.Fatalf("resolveServer error: %v", err)
	}
	if srv.Name != "noble-53" {
		t.Fatalf("--server should win over .neo.yml: got %q, want noble-53", srv.Name)
	}
}
