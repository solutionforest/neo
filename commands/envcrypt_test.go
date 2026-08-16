package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Produced by the real Illuminate\Encryption\Encrypter — same fixture the
// internal/laravel tests use, so the command layer is pinned to Laravel's
// actual `env:encrypt` output too.
const (
	testEncKey = "base64:AQIDBAUGBwgBAgMEBQYHCAECAwQFBgcIAQIDBAUGBwg="
	testEncEnv = "eyJpdiI6IkgyeDJzelRtMFQzaExNTGhJZElSREE9PSIsInZhbHVlIjoiTFZsZExvUzRhekx3WUdIUi9BSm05VTZPWWtRZUtzTUU3QitkZVBucXAyZHh3SnJ2SFNidVR6OTd2UGtsUXNYdDY3NGFVQytJb0RyRjZaQ3NXaVdBMWhOTVAxMFRLSEY3VG9VYS96ZnZvY3NEenQ3MzRNVHQ4TDdKSC8zcmxjUWVhWFhaQS96ZU5WNUNwQUpXaVNWUVl2SE4xTCtvTE1NYVdIWVcxZXVKUmtNaDRib2xIVlF0ZFl6bm94UUlJcHJRRnZGMElUUHdPODRhK2NrSFBjNXc4Rm42NUlXeDlWeGhLK2lHRjBFcUJLaU53MUZqR09LZkVSWGdObWFsV0ZZNCIsIm1hYyI6IjYzM2M3Mjg5NGEwYzg0NjFlMmJlNDg5MTQ5NWQyZWRmY2MzYzc0ODM1NDJhNmRlMWM0ODcwNTdiMmVmNWRjMTEiLCJ0YWciOiIifQ=="
)

// isolateHome points config.Dir() at a temp directory and clears the key env
// vars, so tests never read or write the developer's real ~/.neo.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv(envKeyVar, "")
	t.Setenv(laravelEnvKeyVar, "")
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadEncryptedEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.encrypted")
	writeFile(t, path, testEncEnv)

	env, err := loadEncryptedEnvFile(path, testEncKey)
	if err != nil {
		t.Fatalf("loadEncryptedEnvFile: %v", err)
	}

	want := map[string]string{
		"APP_NAME":     "Neo Demo",
		"APP_ENV":      "production",
		"APP_DEBUG":    "false",
		"DB_PASSWORD":  "p@ss word#1",
		"UNICODE_NOTE": "中文測試",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
	if _, ok := env["# comment line"]; ok {
		t.Error("comment line was parsed as a variable")
	}
}

func TestLoadEncryptedEnvFileWrongKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.encrypted")
	writeFile(t, path, testEncEnv)

	if _, err := loadEncryptedEnvFile(path, "base64:"+strings.Repeat("A", 43)+"="); err == nil {
		t.Fatal("expected wrong key to fail")
	}
}

func TestResolveEncryptedEnvPath(t *testing.T) {
	t.Run("explicit env_encrypted", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "secrets.enc"), testEncEnv)
		path, explicit := resolveEncryptedEnvPath(dir, &NeoConfig{EnvEncrypted: "secrets.enc"})
		if path != filepath.Join(dir, "secrets.enc") || !explicit {
			t.Errorf("got (%q, %v)", path, explicit)
		}
	})

	t.Run("explicit path wins even when missing", func(t *testing.T) {
		dir := t.TempDir()
		path, explicit := resolveEncryptedEnvPath(dir, &NeoConfig{EnvEncrypted: "nope.enc"})
		if path == "" || !explicit {
			t.Errorf("missing explicit file should still resolve so deploy can fail loudly, got (%q, %v)", path, explicit)
		}
	})

	t.Run("auto-detects .env.encrypted", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)
		path, explicit := resolveEncryptedEnvPath(dir, nil)
		if path != filepath.Join(dir, ".env.encrypted") || explicit {
			t.Errorf("got (%q, %v)", path, explicit)
		}
	})

	t.Run("plaintext .env suppresses auto-detection", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)
		writeFile(t, filepath.Join(dir, ".env"), "APP_ENV=local\n")
		if path, _ := resolveEncryptedEnvPath(dir, nil); path != "" {
			t.Errorf("expected no auto-detection, got %q", path)
		}
	})

	t.Run("env_file suppresses auto-detection", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)
		if path, _ := resolveEncryptedEnvPath(dir, &NeoConfig{EnvFile: ".env.production"}); path != "" {
			t.Errorf("expected no auto-detection, got %q", path)
		}
	})

	t.Run("nothing to load", func(t *testing.T) {
		if path, _ := resolveEncryptedEnvPath(t.TempDir(), nil); path != "" {
			t.Errorf("expected empty path, got %q", path)
		}
	})
}

func TestEnvKeyStoreRoundTrip(t *testing.T) {
	isolateHome(t)

	if got := lookupEnvKey("my-app"); got != "" {
		t.Fatalf("expected empty store, got %q", got)
	}
	if err := rememberEnvKey("my-app", testEncKey); err != nil {
		t.Fatalf("rememberEnvKey: %v", err)
	}
	if got := lookupEnvKey("my-app"); got != testEncKey {
		t.Errorf("lookupEnvKey = %q, want %q", got, testEncKey)
	}

	// The store holds plaintext keys — it must never be group/world readable.
	info, err := os.Stat(envKeyStorePath())
	if err != nil {
		t.Fatalf("stat key store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key store permissions = %o, want 600", perm)
	}

	removed, err := forgetEnvKey("my-app")
	if err != nil || !removed {
		t.Fatalf("forgetEnvKey = (%v, %v)", removed, err)
	}
	if removed, _ := forgetEnvKey("my-app"); removed {
		t.Error("forgetting an unknown app reported a removal")
	}
}

func TestResolveEnvKeyPriority(t *testing.T) {
	isolateHome(t)
	if err := rememberEnvKey("my-app", testEncKey); err != nil {
		t.Fatalf("rememberEnvKey: %v", err)
	}
	otherKey := "base64:" + strings.Repeat("B", 43) + "="

	t.Run("flag wins", func(t *testing.T) {
		t.Setenv(envKeyVar, otherKey)
		got, err := resolveEnvKey([]string{"my-app"}, testEncKey, false)
		if err != nil || got.key != testEncKey || got.source != keyFromFlag {
			t.Fatalf("got (%q, %v, %v)", got.key, got.source, err)
		}
	})

	t.Run("NEO_ENV_KEY beats saved key", func(t *testing.T) {
		t.Setenv(envKeyVar, otherKey)
		got, err := resolveEnvKey([]string{"my-app"}, "", false)
		if err != nil || got.key != otherKey || got.source != keyFromEnvVar {
			t.Fatalf("got (%q, %v, %v)", got.key, got.source, err)
		}
	})

	t.Run("LARAVEL_ENV_ENCRYPTION_KEY is honoured", func(t *testing.T) {
		t.Setenv(envKeyVar, "")
		t.Setenv(laravelEnvKeyVar, otherKey)
		got, err := resolveEnvKey([]string{"my-app"}, "", false)
		if err != nil || got.key != otherKey || got.source != keyFromEnvVar {
			t.Fatalf("got (%q, %v, %v)", got.key, got.source, err)
		}
	})

	t.Run("falls back to the saved key", func(t *testing.T) {
		got, err := resolveEnvKey([]string{"my-app"}, "", false)
		if err != nil || got.key != testEncKey || got.source != keyFromStore {
			t.Fatalf("got (%q, %v, %v)", got.key, got.source, err)
		}
	})

	t.Run("falls back to a later candidate app id", func(t *testing.T) {
		// `neo env encrypt` saves under the project name; deploying an
		// environment looks up the suffixed name first and must still find it.
		got, err := resolveEnvKey([]string{"my-app-staging", "my-app"}, "", false)
		if err != nil || got.key != testEncKey {
			t.Fatalf("got (%q, %v)", got.key, err)
		}
		if got.appID != "my-app" {
			t.Errorf("appID = %q, want the id the key was found under", got.appID)
		}
	})

	t.Run("no key without a prompt is an error", func(t *testing.T) {
		if _, err := resolveEnvKey([]string{"unknown-app"}, "", false); err == nil {
			t.Fatal("expected an error when no key can be resolved")
		}
	})

	t.Run("invalid key is rejected up front", func(t *testing.T) {
		if _, err := resolveEnvKey([]string{"my-app"}, "not-a-valid-key", false); err == nil {
			t.Fatal("expected an error for a malformed key")
		}
	})

	t.Run("resolving never writes to the key store", func(t *testing.T) {
		// A prompted key is saved by the caller only after it decrypts, so a
		// typo can't get cached and fail every later deploy.
		before := lookupEnvKey("never-seen")
		_, _ = resolveEnvKey([]string{"never-seen"}, otherKey, false)
		if after := lookupEnvKey("never-seen"); after != before {
			t.Errorf("resolveEnvKey persisted a key: %q", after)
		}
	})
}

func TestLoadDeployEnvFilesDoesNotCacheBadKey(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)

	wrong := "base64:" + strings.Repeat("A", 43) + "="
	if _, err := loadDeployEnvFiles(dir, "my-app", nil, wrong, false); err == nil {
		t.Fatal("expected the wrong key to fail")
	}
	if got := lookupEnvKey("my-app"); got != "" {
		t.Errorf("a key that failed to decrypt was cached: %q", got)
	}
}

func TestLoadDeployEnvFiles(t *testing.T) {
	isolateHome(t)

	t.Run("encrypted file loads", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)

		env, err := loadDeployEnvFiles(dir, "my-app", nil, testEncKey, false)
		if err != nil {
			t.Fatalf("loadDeployEnvFiles: %v", err)
		}
		if env["APP_ENV"] != "production" {
			t.Errorf("APP_ENV = %q, want production", env["APP_ENV"])
		}
	})

	t.Run("env_file overrides the encrypted file", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)
		writeFile(t, filepath.Join(dir, ".env.override"), "APP_ENV=staging\nEXTRA=1\n")

		cfg := &NeoConfig{EnvEncrypted: ".env.encrypted", EnvFile: ".env.override"}
		env, err := loadDeployEnvFiles(dir, "my-app", cfg, testEncKey, false)
		if err != nil {
			t.Fatalf("loadDeployEnvFiles: %v", err)
		}
		if env["APP_ENV"] != "staging" {
			t.Errorf("APP_ENV = %q, want staging (env_file should win)", env["APP_ENV"])
		}
		if env["EXTRA"] != "1" {
			t.Errorf("EXTRA = %q, want 1", env["EXTRA"])
		}
		if env["APP_NAME"] != "Neo Demo" {
			t.Errorf("APP_NAME = %q — encrypted values should survive", env["APP_NAME"])
		}
	})

	t.Run("missing explicit encrypted file is fatal", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &NeoConfig{EnvEncrypted: ".env.production.encrypted"}
		if _, err := loadDeployEnvFiles(dir, "my-app", cfg, testEncKey, false); err == nil {
			t.Fatal("expected a missing env_encrypted file to fail the deploy")
		}
	})

	t.Run("undecryptable file is fatal", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env.encrypted"), testEncEnv)
		wrong := "base64:" + strings.Repeat("A", 43) + "="
		if _, err := loadDeployEnvFiles(dir, "my-app", nil, wrong, false); err == nil {
			t.Fatal("expected a wrong key to fail the deploy")
		}
	})

	t.Run("no encrypted file is not an error", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".env"), "APP_ENV=local\n")
		env, err := loadDeployEnvFiles(dir, "my-app", &NeoConfig{EnvFile: ".env"}, "", false)
		if err != nil {
			t.Fatalf("loadDeployEnvFiles: %v", err)
		}
		if env["APP_ENV"] != "local" {
			t.Errorf("APP_ENV = %q, want local", env["APP_ENV"])
		}
	})
}

func TestEnvEncryptedRoundTripThroughCommands(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, ".env")
	writeFile(t, plain, "APP_KEY=secret-value\nQUEUE=redis\n")

	if err := runEnvEncrypt(plain, testEncKey, "round-trip", false, false); err != nil {
		t.Fatalf("runEnvEncrypt: %v", err)
	}
	if _, err := os.Stat(plain + ".encrypted"); err != nil {
		t.Fatalf("encrypted file not written: %v", err)
	}

	env, err := loadEncryptedEnvFile(plain+".encrypted", testEncKey)
	if err != nil {
		t.Fatalf("loadEncryptedEnvFile: %v", err)
	}
	if env["APP_KEY"] != "secret-value" || env["QUEUE"] != "redis" {
		t.Errorf("round trip lost values: %#v", env)
	}

	// Re-encrypting must not silently clobber the committed file.
	if err := runEnvEncrypt(plain, testEncKey, "round-trip", false, false); err == nil {
		t.Error("expected an error without --force")
	}
	if err := runEnvEncrypt(plain, testEncKey, "round-trip", true, false); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestEnvEncryptPrunesPlaintext(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, ".env")
	writeFile(t, plain, "APP_KEY=secret-value\n")

	if err := runEnvEncrypt(plain, testEncKey, "pruned", false, true); err != nil {
		t.Fatalf("runEnvEncrypt: %v", err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Error("--prune should delete the plaintext file")
	}
}

func TestEnvDecryptWritesPlaintext(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	enc := filepath.Join(dir, ".env.encrypted")
	writeFile(t, enc, testEncEnv)

	if err := runEnvDecrypt(enc, testEncKey, "decrypt-test", false, false); err != nil {
		t.Fatalf("runEnvDecrypt: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("read decrypted file: %v", err)
	}
	if !strings.Contains(string(data), "APP_ENV=production") {
		t.Errorf("decrypted file missing expected content: %q", string(data))
	}

	// The plaintext contains secrets — it must not be world readable.
	info, err := os.Stat(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("stat decrypted file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("decrypted file permissions = %o, want 600", perm)
	}

	if err := runEnvDecrypt(enc, testEncKey, "decrypt-test", false, false); err == nil {
		t.Error("expected an error overwriting without --force")
	}
}

func TestMaskEnvKey(t *testing.T) {
	masked := maskEnvKey(testEncKey)
	if strings.Contains(masked, "base64:") {
		t.Errorf("mask kept the prefix: %q", masked)
	}
	if n := len([]rune(masked)); n > 20 || !strings.Contains(masked, "•") {
		t.Errorf("unexpected mask %q (%d runes)", masked, n)
	}
	if strings.Contains(testEncKey, masked) {
		t.Errorf("mask %q is a literal substring of the key", masked)
	}
	if got := maskEnvKey("short"); got != "••••••••" {
		t.Errorf("short key mask = %q", got)
	}
}
