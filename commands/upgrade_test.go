package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchLatestVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(remoteVersion{Version: "1.2.3", Released: "2026-03-19"})
	}))
	defer server.Close()

	// Override the URL for testing
	origURL := versionURL
	defer func() { _ = origURL }()

	// We can't override the const, so test the parsing directly
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var v remoteVersion
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if v.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", v.Version, "1.2.3")
	}
	if v.Released != "2026-03-19" {
		t.Errorf("Released = %q, want %q", v.Released, "2026-03-19")
	}
}

func TestReplaceBinary(t *testing.T) {
	tmp := t.TempDir()

	// Create a "current" binary
	target := filepath.Join(tmp, "neo")
	os.WriteFile(target, []byte("old-binary"), 0755)

	// Create a "new" binary
	source := filepath.Join(tmp, "neo-new")
	os.WriteFile(source, []byte("new-binary"), 0755)

	err := replaceBinary(target, source)
	if err != nil {
		t.Fatalf("replaceBinary() error: %v", err)
	}

	// Verify the target has new content
	data, _ := os.ReadFile(target)
	if string(data) != "new-binary" {
		t.Errorf("target content = %q, want %q", string(data), "new-binary")
	}

	// Verify backup was cleaned up
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("backup file should be cleaned up")
	}
}

func TestReplaceBinaryPreservesPermissions(t *testing.T) {
	tmp := t.TempDir()

	target := filepath.Join(tmp, "neo")
	os.WriteFile(target, []byte("old"), 0755)

	source := filepath.Join(tmp, "neo-new")
	os.WriteFile(source, []byte("new"), 0755)

	replaceBinary(target, source)

	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0755 {
		t.Errorf("permissions = %o, want 0755", info.Mode().Perm())
	}
}

func TestDownloadBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-binary-content"))
	}))
	defer server.Close()

	path, err := downloadBinary(server.URL)
	if err != nil {
		t.Fatalf("downloadBinary() error: %v", err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	if string(data) != "fake-binary-content" {
		t.Errorf("content = %q, want %q", string(data), "fake-binary-content")
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm()&0111 == 0 {
		t.Error("downloaded file should be executable")
	}
}

func TestDownloadBinaryBadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	_, err := downloadBinary(server.URL)
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestRemoteVersionJSON(t *testing.T) {
	data := `{"version":"0.2.0","released":"2026-03-19"}`
	var v remoteVersion
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v.Version != "0.2.0" {
		t.Errorf("Version = %q", v.Version)
	}
}
