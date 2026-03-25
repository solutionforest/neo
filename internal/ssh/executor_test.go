package ssh

import (
	"testing"
)

func TestParseHostWithUser(t *testing.T) {
	user, host := parseHost("root@1.2.3.4")
	if user != "root" {
		t.Errorf("user = %q, want %q", user, "root")
	}
	if host != "1.2.3.4" {
		t.Errorf("host = %q, want %q", host, "1.2.3.4")
	}
}

func TestParseHostDefaultUser(t *testing.T) {
	user, host := parseHost("1.2.3.4")
	if user != "root" {
		t.Errorf("user = %q, want %q (default)", user, "root")
	}
	if host != "1.2.3.4" {
		t.Errorf("host = %q, want %q", host, "1.2.3.4")
	}
}

func TestParseHostCustomUser(t *testing.T) {
	user, host := parseHost("ubuntu@myserver.com")
	if user != "ubuntu" {
		t.Errorf("user = %q, want %q", user, "ubuntu")
	}
	if host != "myserver.com" {
		t.Errorf("host = %q, want %q", host, "myserver.com")
	}
}

func TestNewDefaultPort(t *testing.T) {
	e := New("root@1.2.3.4", 0)
	if e.Port != 22 {
		t.Errorf("Port = %d, want 22 (default)", e.Port)
	}
}

func TestNewCustomPort(t *testing.T) {
	e := New("root@1.2.3.4", 2222)
	if e.Port != 2222 {
		t.Errorf("Port = %d, want 2222", e.Port)
	}
}

func TestPasswordField(t *testing.T) {
	e := New("root@1.2.3.4", 22)
	if e.Password != "" {
		t.Errorf("Password should be empty by default")
	}
	e.Password = "secret"
	if e.Password != "secret" {
		t.Errorf("Password = %q", e.Password)
	}
}

func TestHasKeyAuthNoEnv(t *testing.T) {
	// Unset SSH_AUTH_SOCK and use a temp home with no keys
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())

	if HasKeyAuth() {
		t.Error("HasKeyAuth() should be false with no agent and no key files")
	}
}

func TestAuthMethodsWithPassword(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())

	e := New("root@1.2.3.4", 22)
	e.Password = "pass123"

	methods := e.authMethods()
	if len(methods) == 0 {
		t.Fatal("authMethods() should include password when set")
	}
	// Last method should be password
	// We can't inspect the type directly, but we verified it's non-empty
}

func TestAuthMethodsNoPassword(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())

	e := New("root@1.2.3.4", 22)

	methods := e.authMethods()
	// No agent, no keys, no password → empty
	if len(methods) != 0 {
		t.Errorf("expected 0 auth methods, got %d", len(methods))
	}
}
