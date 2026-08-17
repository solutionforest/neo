package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-app", "my-app"},
		{"MyApp", "myapp"},
		{"my_app_v2", "my-app-v2"},
		{"My Cool App!", "my-cool-app"},
		{"---trimmed---", "trimmed"},
		{"123-test", "123-test"},
		{"UPPERCASE", "uppercase"},
		{"a.b.c", "a-b-c"},
		{"app@v1.0", "app-v1-0"},
		{"", "app"},    // empty → fallback
		{"---", "app"}, // only dashes → fallback
		{"hello world", "hello-world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectPort(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		want       int
	}{
		{
			"simple expose",
			"FROM node:18\nEXPOSE 3000\nCMD [\"node\", \"server.js\"]\n",
			3000,
		},
		{
			"expose with protocol",
			"FROM nginx\nEXPOSE 8080/tcp\nCMD [\"nginx\"]\n",
			8080,
		},
		{
			"lowercase expose",
			"FROM python:3.12\nexpose 5000\n",
			5000,
		},
		{
			"no expose",
			"FROM alpine\nRUN echo hello\n",
			0,
		},
		{
			"multiple expose uses first",
			"FROM node\nEXPOSE 3000\nEXPOSE 9229\n",
			3000,
		},
		{
			"expose with whitespace",
			"FROM go\n  EXPOSE 8080  \n",
			8080,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write temp Dockerfile
			tmp := t.TempDir()
			df := filepath.Join(tmp, "Dockerfile")
			os.WriteFile(df, []byte(tt.dockerfile), 0644)

			got := detectPort(df)
			if got != tt.want {
				t.Errorf("detectPort() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDetectPortMissingFile(t *testing.T) {
	got := detectPort("/nonexistent/Dockerfile")
	if got != 0 {
		t.Errorf("detectPort(missing) = %d, want 0", got)
	}
}

func TestShouldIgnore(t *testing.T) {
	patterns := []string{".git", "node_modules", "*.log", "dist"}

	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{".git", true, true},
		{"node_modules", true, true},
		{"src/main.go", false, false},
		{"error.log", false, true},
		{"dist", true, true},
		{"src/app.ts", false, false},
		{"deep/node_modules/pkg", true, true}, // nested component match
		{"deep/.git/config", false, true},     // nested component match
		{"my-git-project", false, false},      // should NOT match .git
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := shouldIgnore(tt.path, tt.isDir, patterns)
			if got != tt.want {
				t.Errorf("shouldIgnore(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestLoadDockerignoreDefault(t *testing.T) {
	// No .dockerignore file → returns defaults
	tmp := t.TempDir()
	patterns := loadDockerignore(tmp)

	if len(patterns) < 2 {
		t.Fatalf("expected at least 2 default patterns, got %d", len(patterns))
	}

	found := map[string]bool{}
	for _, p := range patterns {
		found[p] = true
	}
	if !found[".git"] {
		t.Error("missing default pattern: .git")
	}
	if !found["node_modules"] {
		t.Error("missing default pattern: node_modules")
	}
}

func TestLoadDockerignoreCustom(t *testing.T) {
	tmp := t.TempDir()
	content := "dist\n# comment\n*.tmp\n\n  vendor  \n"
	os.WriteFile(filepath.Join(tmp, ".dockerignore"), []byte(content), 0644)

	patterns := loadDockerignore(tmp)

	// Defaults + custom
	found := map[string]bool{}
	for _, p := range patterns {
		found[p] = true
	}

	if !found[".git"] {
		t.Error("missing default: .git")
	}
	if !found["dist"] {
		t.Error("missing custom: dist")
	}
	if !found["*.tmp"] {
		t.Error("missing custom: *.tmp")
	}
	if !found["vendor"] {
		t.Error("missing custom: vendor")
	}
	if found["# comment"] {
		t.Error("comments should be excluded")
	}
	if found[""] {
		t.Error("blank lines should be excluded")
	}
}

func TestCreateTarGz(t *testing.T) {
	tmp := t.TempDir()

	// Create some files
	os.WriteFile(filepath.Join(tmp, "Dockerfile"), []byte("FROM alpine"), 0644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main"), 0644)
	os.MkdirAll(filepath.Join(tmp, "src"), 0755)
	os.WriteFile(filepath.Join(tmp, "src", "app.go"), []byte("package src"), 0644)

	// Create .git dir (should be ignored by default)
	os.MkdirAll(filepath.Join(tmp, ".git", "objects"), 0755)
	os.WriteFile(filepath.Join(tmp, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644)

	buf, err := createTarGz(tmp)
	if err != nil {
		t.Fatalf("createTarGz() error: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("tar.gz buffer should not be empty")
	}

	// Basic size sanity check (should be small since files are tiny)
	if buf.Len() > 10*1024 {
		t.Errorf("tar.gz seems too large: %d bytes", buf.Len())
	}
}

func TestCreateTarGzWithDockerignore(t *testing.T) {
	tmp := t.TempDir()

	os.WriteFile(filepath.Join(tmp, "Dockerfile"), []byte("FROM alpine"), 0644)
	os.WriteFile(filepath.Join(tmp, ".dockerignore"), []byte("*.md\nbuild\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Hello"), 0644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main"), 0644)
	os.MkdirAll(filepath.Join(tmp, "build"), 0755)
	os.WriteFile(filepath.Join(tmp, "build", "output"), []byte("binary"), 0644)

	buf, err := createTarGz(tmp)
	if err != nil {
		t.Fatalf("createTarGz() error: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("tar.gz should not be empty")
	}
}

func TestGroupTargetsByServer(t *testing.T) {
	// Two environments on one server must land in the same group so they deploy
	// sequentially — /etc/neo/state.json is rewritten whole, and parallel saves
	// drop each other's app entries.
	t.Run("same server shares a group", func(t *testing.T) {
		cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
			"production": {Server: "vps-1"},
			"staging":    {Server: "vps-1"},
		}}
		groups := groupTargetsByServer(cfg, "")
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
		if len(groups[0]) != 2 {
			t.Fatalf("got %d targets in the group, want 2", len(groups[0]))
		}
	})

	t.Run("different servers deploy in parallel", func(t *testing.T) {
		cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
			"production": {Server: "vps-1"},
			"staging":    {Server: "vps-2"},
		}}
		groups := groupTargetsByServer(cfg, "")
		if len(groups) != 2 {
			t.Fatalf("got %d groups, want 2", len(groups))
		}
		for _, g := range groups {
			if len(g) != 1 {
				t.Errorf("group has %d targets, want 1", len(g))
			}
		}
	})

	t.Run("unset server falls back to the current server", func(t *testing.T) {
		// One env names the active server explicitly, the other inherits it.
		// Both hit the same machine, so both belong in one group.
		cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
			"production": {Server: "vps-1"},
			"staging":    {},
		}}
		groups := groupTargetsByServer(cfg, "vps-1")
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
	})

	t.Run("top-level server applies to environments without one", func(t *testing.T) {
		cfg := &NeoConfig{Server: "vps-1", Environments: map[string]NeoEnvironment{
			"production": {},
			"staging":    {},
		}}
		if groups := groupTargetsByServer(cfg, "other"); len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
	})

	t.Run("server group fans out one target per server", func(t *testing.T) {
		cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
			"production": {Servers: []string{"vps-1", "vps-2"}},
			"staging":    {Server: "vps-1"},
		}}
		groups := groupTargetsByServer(cfg, "")
		if len(groups) != 2 {
			t.Fatalf("got %d groups, want 2 (vps-1, vps-2)", len(groups))
		}
		total, labels := 0, map[string]bool{}
		for _, g := range groups {
			total += len(g)
			for _, target := range g {
				labels[target.label()] = true
			}
		}
		if total != 3 {
			t.Errorf("got %d targets, want 3", total)
		}
		// production is pinned to two servers, so it keeps the env[server] label.
		for _, want := range []string{"production[vps-1]", "production[vps-2]", "staging"} {
			if !labels[want] {
				t.Errorf("missing target label %q (got %v)", want, labels)
			}
		}
	})

	t.Run("grouping is deterministic", func(t *testing.T) {
		cfg := &NeoConfig{Environments: map[string]NeoEnvironment{
			"a": {Server: "vps-1"}, "b": {Server: "vps-2"}, "c": {Server: "vps-3"},
		}}
		first := groupTargetsByServer(cfg, "")
		for i := 0; i < 20; i++ {
			got := groupTargetsByServer(cfg, "")
			for gi := range first {
				if got[gi][0].envName != first[gi][0].envName {
					t.Fatalf("group order changed between runs: %v vs %v", got[gi][0].envName, first[gi][0].envName)
				}
			}
		}
	})
}

func TestResolveDockerfilePath(t *testing.T) {
	dir := "/project"

	cases := []struct {
		name     string
		explicit string
		cfg      *NeoConfig
		want     string
	}{
		{"default", "", nil, "/project/Dockerfile"},
		{"config relative", "", &NeoConfig{Dockerfile: "docker/Dockerfile"}, "/project/docker/Dockerfile"},
		{"config with ./ prefix", "", &NeoConfig{Dockerfile: "./docker/Dockerfile"}, "/project/docker/Dockerfile"},
		{"flag beats config", "other/Dockerfile", &NeoConfig{Dockerfile: "docker/Dockerfile"}, "/project/other/Dockerfile"},
		{"absolute path kept", "/abs/Dockerfile", &NeoConfig{Dockerfile: "docker/Dockerfile"}, "/abs/Dockerfile"},
		{"empty config field", "", &NeoConfig{}, "/project/Dockerfile"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDockerfilePath(dir, tc.explicit, tc.cfg); got != tc.want {
				t.Errorf("resolveDockerfilePath(%q, %+v) = %q, want %q", tc.explicit, tc.cfg, got, tc.want)
			}
		})
	}
}

func TestCheckAllDockerfilesAgree(t *testing.T) {
	t.Run("all environments inherit the top level", func(t *testing.T) {
		cfg := &NeoConfig{
			Dockerfile: "docker/Dockerfile",
			Environments: map[string]NeoEnvironment{
				"dev": {}, "prod": {},
			},
		}
		if err := checkAllDockerfilesAgree(cfg, false); err != nil {
			t.Errorf("expected no conflict: %v", err)
		}
	})

	t.Run("environments agree explicitly", func(t *testing.T) {
		cfg := &NeoConfig{
			Environments: map[string]NeoEnvironment{
				"dev":  {Dockerfile: "docker/Dockerfile"},
				"prod": {Dockerfile: "docker/Dockerfile"},
			},
		}
		if err := checkAllDockerfilesAgree(cfg, false); err != nil {
			t.Errorf("expected no conflict: %v", err)
		}
	})

	t.Run("conflicting dockerfiles are rejected", func(t *testing.T) {
		// --all builds one image; shipping it as if it were both would be wrong.
		cfg := &NeoConfig{
			Environments: map[string]NeoEnvironment{
				"dev":  {Dockerfile: "docker/Dockerfile.dev"},
				"prod": {Dockerfile: "docker/Dockerfile"},
			},
		}
		if err := checkAllDockerfilesAgree(cfg, false); err == nil {
			t.Error("expected an error when environments disagree")
		}
	})

	t.Run("one environment overriding the top level conflicts", func(t *testing.T) {
		cfg := &NeoConfig{
			Dockerfile: "docker/Dockerfile",
			Environments: map[string]NeoEnvironment{
				"dev":  {Dockerfile: "docker/Dockerfile.dev"},
				"prod": {},
			},
		}
		if err := checkAllDockerfilesAgree(cfg, false); err == nil {
			t.Error("expected an error when one environment differs from the inherited default")
		}
	})

	t.Run("--dockerfile removes the conflict", func(t *testing.T) {
		cfg := &NeoConfig{
			Environments: map[string]NeoEnvironment{
				"dev":  {Dockerfile: "a/Dockerfile"},
				"prod": {Dockerfile: "b/Dockerfile"},
			},
		}
		if err := checkAllDockerfilesAgree(cfg, true); err != nil {
			t.Errorf("--dockerfile overrides every environment: %v", err)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		if err := checkAllDockerfilesAgree(nil, false); err != nil {
			t.Errorf("nil config: %v", err)
		}
	})
}

func TestEnvironmentDockerfileParsesFromYAML(t *testing.T) {
	dir := t.TempDir()
	content := `name: fsd-cms
dockerfile: ./docker/Dockerfile
environments:
  dev:
    dockerfile: ./docker/Dockerfile.dev
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
	if cfg.Dockerfile != "./docker/Dockerfile" {
		t.Errorf("top-level dockerfile = %q", cfg.Dockerfile)
	}
	if got := cfg.Environments["dev"].Dockerfile; got != "./docker/Dockerfile.dev" {
		t.Errorf("environments.dev.dockerfile = %q", got)
	}
	if got := cfg.Environments["prod"].Dockerfile; got != "" {
		t.Errorf("environments.prod.dockerfile = %q, want empty (inherits)", got)
	}
}

func TestAppCommandParsesFromYAML(t *testing.T) {
	dir := t.TempDir()
	content := `name: my-app
command: php artisan octane:frankenphp --workers=4
environments:
  dev:
    command: php artisan octane:frankenphp --workers=1 --max-requests=50
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
	if string(cfg.Command) != "php artisan octane:frankenphp --workers=4" {
		t.Errorf("top-level command = %q", cfg.Command)
	}
	if got := string(cfg.Environments["dev"].Command); got != "php artisan octane:frankenphp --workers=1 --max-requests=50" {
		t.Errorf("environments.dev.command = %q", got)
	}
	if got := string(cfg.Environments["prod"].Command); got != "" {
		t.Errorf("environments.prod.command = %q, want empty (inherits)", got)
	}
}

func TestAppCommandListFormParsesFromYAML(t *testing.T) {
	// docker-compose style list form — what people copy across when migrating.
	dir := t.TempDir()
	content := `name: my-app
command:
  - sh
  - -lc
  - >-
    php artisan octane:frankenphp --workers=2
environments:
  dev:
    command: ["php", "artisan", "serve"]
`
	if err := os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := loadNeoConfig(dir)
	if err != nil {
		t.Fatalf("loadNeoConfig: %v", err)
	}
	if want := "sh -lc php artisan octane:frankenphp --workers=2"; string(cfg.Command) != want {
		t.Errorf("command = %q, want %q", cfg.Command, want)
	}
	if got := string(cfg.Environments["dev"].Command); got != "php artisan serve" {
		t.Errorf("environments.dev.command = %q", got)
	}
}
