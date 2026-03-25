package commands

import (
	"testing"

	"github.com/vxero/neo/internal/app"
)

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

	env := resolveEnvVars(m, "blog.example.com", userVars)

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

	env := resolveEnvVars(m, "", nil)
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

	env := resolveEnvVars(m, "example.com", nil)
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
