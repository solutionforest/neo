package license

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vxero/neo/internal/config"
)

// DevLicenseBypass allows local development builds to skip activation without
// a live license. Intended only for local/staging testing.
var DevLicenseBypass = "false"

// OfflineGraceDays is how many days the CLI trusts a cached validation once a
// license has been activated at least once.
const OfflineGraceDays = 3

// Status represents the current license state.
type Status struct {
	Valid       bool   `json:"valid"`
	Key         string `json:"license_key"` // returned by /register
	Plan        string `json:"plan"`        // "free" (or grandfathered "plus"/"team")
	Expires     string `json:"expires"`     // ISO date, empty if lifetime
	ValidatedAt string `json:"validated_at"`
	ValidatedBy string `json:"validated_by"` // license API URL that produced this cache
}

// cacheFile returns the license cache path.
func cacheFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".neo", "license.json")
}

// LicenseAPIURL returns the API base URL, overridable via NEO_LICENSE_URL for dev builds.
func LicenseAPIURL() string {
	if v := os.Getenv("NEO_LICENSE_URL"); v != "" {
		return v
	}
	return config.APIBaseURL() + "/license"
}

// postForm POSTs a form and sets Accept: application/json so the server returns
// JSON errors (e.g. 422 validation) instead of an HTML redirect.
func postForm(client *http.Client, apiURL string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return client.Do(req)
}

// MachineID returns a stable fingerprint for this machine (hostname + OS + arch).
func MachineID() string {
	hostname, _ := os.Hostname()
	raw := fmt.Sprintf("%s-%s-%s", hostname, runtime.GOOS, runtime.GOARCH)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}

// Register issues a free license for an email and activates it on this machine.
// The returned Status.Key is the license key to persist in config.
func Register(email string) (*Status, error) {
	apiURL := LicenseAPIURL() + "/register"

	form := url.Values{}
	form.Set("email", email)
	form.Set("machine_id", MachineID())

	resp, err := postForm(http.DefaultClient, apiURL, form)
	if err != nil {
		return nil, fmt.Errorf("cannot reach license server: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Valid   bool   `json:"valid"`
		Key     string `json:"license_key"`
		Plan    string `json:"plan"`
		Expires string `json:"expires"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from license server")
	}
	if !result.Valid || result.Key == "" {
		msg := "registration failed"
		if result.Error != "" {
			msg = result.Error
		}
		return nil, fmt.Errorf("%s", msg)
	}

	status := &Status{
		Valid:       true,
		Key:         result.Key,
		Plan:        result.Plan,
		Expires:     result.Expires,
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	saveCache(status)
	return status, nil
}

// Activate validates and activates an existing key on this machine.
func Activate(key string) (*Status, error) {
	apiURL := LicenseAPIURL() + "/activate"

	form := url.Values{}
	form.Set("license_key", key)
	form.Set("machine_id", MachineID())

	resp, err := postForm(http.DefaultClient, apiURL, form)
	if err != nil {
		return nil, fmt.Errorf("cannot reach license server: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Valid   bool   `json:"valid"`
		Plan    string `json:"plan"`
		Expires string `json:"expires"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from license server")
	}
	if !result.Valid {
		msg := "invalid license key"
		if result.Error != "" {
			msg = result.Error
		}
		return nil, fmt.Errorf("%s", msg)
	}

	status := &Status{
		Valid:       true,
		Key:         key,
		Plan:        result.Plan,
		Expires:     result.Expires,
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	saveCache(status)
	return status, nil
}

// Deactivate removes the license from this machine.
func Deactivate(key string) error {
	apiURL := LicenseAPIURL() + "/deactivate"

	form := url.Values{}
	form.Set("license_key", key)
	form.Set("machine_id", MachineID())

	resp, err := postForm(http.DefaultClient, apiURL, form)
	if err != nil {
		return fmt.Errorf("cannot reach license server: %w", err)
	}
	defer resp.Body.Close()

	// Clear cache regardless
	os.Remove(cacheFile())
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deactivation failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// Check returns the current license status using cached data with offline grace.
// It never blocks on network for long. A key is valid once it has been
// activated (online) at least once; after that the cache carries it offline.
func Check(licenseKey string) *Status {
	if licenseKey == "" {
		return &Status{Valid: false, Plan: PlanFree}
	}

	currentAPI := LicenseAPIURL()

	// Try cached status first (within grace period, same license server).
	if cached := loadCache(); cached != nil && cached.Valid {
		if cached.ValidatedBy == "" || cached.ValidatedBy == currentAPI {
			if t, err := time.Parse(time.RFC3339, cached.ValidatedAt); err == nil {
				if time.Since(t) < time.Duration(OfflineGraceDays)*24*time.Hour {
					return cached
				}
			}
		}
	}

	// Cache stale or missing — try online validation (short timeout).
	client := &http.Client{Timeout: 3 * time.Second}
	apiURL := currentAPI + "/validate"

	form := url.Values{}
	form.Set("license_key", licenseKey)
	form.Set("machine_id", MachineID())

	resp, err := postForm(client, apiURL, form)
	if err != nil {
		// Network error — fall back to any same-server cache so we don't block.
		if cached := loadCache(); cached != nil && cached.Valid {
			if cached.ValidatedBy == "" || cached.ValidatedBy == currentAPI {
				return cached
			}
		}
		return &Status{Valid: false, Plan: PlanFree}
	}
	defer resp.Body.Close()

	var result struct {
		Valid   bool   `json:"valid"`
		Plan    string `json:"plan"`
		Expires string `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &Status{Valid: false, Plan: PlanFree}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if result.Valid {
		status := &Status{
			Valid:       true,
			Key:         licenseKey,
			Plan:        result.Plan,
			Expires:     result.Expires,
			ValidatedAt: now,
		}
		saveCache(status)
		return status
	}

	status := &Status{Valid: false, Plan: PlanFree, ValidatedAt: now}
	saveCache(status)
	return status
}

// IsActivated reports whether neo may run: a valid license, or a dev bypass.
func IsActivated(licenseKey string) bool {
	if DevBypassEnabled() {
		return true
	}
	return Check(licenseKey).Valid
}

// DevBypassEnabled reports whether activation should be skipped for local dev.
func DevBypassEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(DevLicenseBypass))
	if v == "1" || v == "true" || v == "yes" {
		return true
	}
	v = strings.ToLower(strings.TrimSpace(os.Getenv("NEO_DEV_PLUS")))
	return v == "1" || v == "true" || v == "yes"
}

// MaskKey masks a license key for display (shows first 4 and last 4 chars).
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 10 {
		return "****"
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func loadCache() *Status {
	data, err := os.ReadFile(cacheFile())
	if err != nil {
		return nil
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func saveCache(s *Status) {
	s.ValidatedBy = LicenseAPIURL()
	data, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(filepath.Dir(cacheFile()), 0o700)
	os.WriteFile(cacheFile(), data, 0o600) //nolint:errcheck
}
