package commands

import (
	"strings"
	"testing"

	"github.com/vxero/neo/internal/app"
)

func TestScaffoldGeneration(t *testing.T) {
	m := &app.Manifest{
		Name: "ghost", Title: "Ghost", Image: "ghost:5-alpine", Port: 2368,
		Volumes: []app.VolumeSpec{{Name: "ghost-content", Path: "/var/lib/ghost/content"}},
		Env: []app.EnvSpec{
			{Key: "url", From: "domain"},
			{Key: "database__connection__host", FromService: "mysql", Template: "svc-ghost-mysql"},
			{Key: "database__connection__password", FromService: "mysql", Template: "${MYSQL_PASSWORD}"},
		},
		Services: []app.ServiceSpec{{
			Name: "mysql", Image: "mysql:8.4", Port: 3306,
			Volumes: []app.VolumeSpec{{Name: "ghost-mysql", Path: "/var/lib/mysql"}},
			Env:     []app.EnvSpec{{Key: "MYSQL_PASSWORD", Generate: "hex:32"}, {Key: "MYSQL_DATABASE", Value: "ghost"}},
		}},
		Health: &app.HealthSpec{Path: "/ghost/", Interval: "15s", Retries: 5},
	}

	serviceEnvs := map[string]map[string]string{"mysql": {}}
	for _, ev := range m.Services[0].Env {
		if ev.Generate != "" {
			v, _ := app.GenerateValue(ev.Generate)
			serviceEnvs["mysql"][ev.Key] = v
		} else if ev.Value != "" {
			serviceEnvs["mysql"][ev.Key] = ev.Value
		}
	}
	appEnv := resolveScaffoldEnv(m, "blog.example.com", nil, serviceEnvs)

	if appEnv["database__connection__host"] != "mysql" {
		t.Errorf("host = %q, want compose service name 'mysql'", appEnv["database__connection__host"])
	}
	if appEnv["database__connection__password"] != serviceEnvs["mysql"]["MYSQL_PASSWORD"] {
		t.Error("app DB password not wired to the generated service secret")
	}

	compose := composeYAML(m, serviceEnvs)
	for _, want := range []string{"image: ghost:5-alpine", "image: mysql:8.4", "ghost-content:/var/lib/ghost/content", "depends_on:", "- mysql", "ghost-mysql: {}"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose missing %q\n%s", want, compose)
		}
	}
	for _, want := range []string{"name: ghost", "domain: blog.example.com", "port: 2368", "compose_service: ghost"} {
		if !strings.Contains(neoYML(m, "blog.example.com"), want) {
			t.Errorf(".neo.yml missing %q", want)
		}
	}
	if !strings.Contains(envFile(m, appEnv), "url=https://blog.example.com") {
		t.Error(".env missing resolved url")
	}
}

func TestResolveEnvVars(t *testing.T) {
	m := &app.Manifest{
		Env: []app.EnvSpec{
			{Key: "APP_URL", From: "domain"},
			{Key: "APP_HOST", From: "domain_host"},
			{Key: "SECRET_KEY", Generate: "hex:32"},
			{Key: "NODE_ENV", Value: "production"},
			{Key: "DB_URL", Template: "postgres://user:${POSTGRES_PASSWORD}@svc-app-postgres:5432/app"},
			{Key: "MAIL_USER", Ask: true},
		},
	}

	userVars := map[string]string{
		"MAIL_USER": "admin@example.com",
	}

	env := resolveScaffoldEnv(m, "blog.example.com", userVars, nil)

	if env["APP_URL"] != "https://blog.example.com" {
		t.Errorf("APP_URL = %q, want %q", env["APP_URL"], "https://blog.example.com")
	}
	if env["APP_HOST"] != "blog.example.com" {
		t.Errorf("APP_HOST = %q, want %q", env["APP_HOST"], "blog.example.com")
	}
	if len(env["SECRET_KEY"]) != 32 {
		t.Errorf("SECRET_KEY length = %d, want 32", len(env["SECRET_KEY"]))
	}
	if env["NODE_ENV"] != "production" {
		t.Errorf("NODE_ENV = %q, want %q", env["NODE_ENV"], "production")
	}
	// Template should be preserved for later expansion
	if env["DB_URL"] != "postgres://user:${POSTGRES_PASSWORD}@svc-app-postgres:5432/app" {
		t.Errorf("DB_URL = %q (template should be preserved)", env["DB_URL"])
	}
	if env["MAIL_USER"] != "admin@example.com" {
		t.Errorf("MAIL_USER = %q, want %q", env["MAIL_USER"], "admin@example.com")
	}
}

func TestResolveEnvVarsEmptyDomain(t *testing.T) {
	m := &app.Manifest{
		Env: []app.EnvSpec{
			{Key: "APP_URL", From: "domain"},
		},
	}

	env := resolveScaffoldEnv(m, "", nil, nil)
	if env["APP_URL"] != "https://" {
		t.Errorf("APP_URL = %q (empty domain case)", env["APP_URL"])
	}
}

func TestResolveEnvVarsNoUserVars(t *testing.T) {
	m := &app.Manifest{
		Env: []app.EnvSpec{
			{Key: "MAIL_USER", Ask: true},
		},
	}

	env := resolveScaffoldEnv(m, "example.com", nil, nil)
	if _, ok := env["MAIL_USER"]; ok {
		t.Error("MAIL_USER should not be set when no user vars provided")
	}
}

func TestExpandServiceVars(t *testing.T) {
	serviceEnvs := map[string]map[string]string{
		"postgres": {
			"POSTGRES_PASSWORD": "secret123",
			"POSTGRES_USER":     "admin",
		},
		"redis": {
			"REDIS_PASSWORD": "redis_pw",
		},
	}

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{
			"single var",
			"postgres://admin:${POSTGRES_PASSWORD}@db:5432/mydb",
			"postgres://admin:secret123@db:5432/mydb",
		},
		{
			"multiple vars same service",
			"${POSTGRES_USER}:${POSTGRES_PASSWORD}",
			"admin:secret123",
		},
		{
			"cross-service vars",
			"pg=${POSTGRES_PASSWORD},redis=${REDIS_PASSWORD}",
			"pg=secret123,redis=redis_pw",
		},
		{
			"no vars",
			"plain-string",
			"plain-string",
		},
		{
			"unknown var left as-is",
			"${UNKNOWN_VAR}",
			"${UNKNOWN_VAR}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandServiceVars(tt.tmpl, serviceEnvs)
			if got != tt.want {
				t.Errorf("expandServiceVars(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

func TestExpandServiceVarsEmpty(t *testing.T) {
	got := expandServiceVars("${X}", nil)
	if got != "${X}" {
		t.Errorf("expandServiceVars with nil = %q", got)
	}
}
