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
)

// DefaultLicenseAPIURL is the production license validation endpoint.
const DefaultLicenseAPIURL = "https://neo.vxero.dev/api/license"

// OfflineGraceDays is how many days the CLI trusts a cached validation.
const OfflineGraceDays = 7

// Status represents the current license state.
type Status struct {
	Valid       bool   `json:"valid"`
	Plan       string `json:"plan"`       // "free" or "plus"
	Expires    string `json:"expires"`    // ISO date, empty if lifetime
	ValidatedAt string `json:"validated_at"`
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
	return DefaultLicenseAPIURL
}

// MachineID returns a stable fingerprint for this machine (hostname + OS + arch).
func MachineID() string {
	hostname, _ := os.Hostname()
	raw := fmt.Sprintf("%s-%s-%s", hostname, runtime.GOOS, runtime.GOARCH)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}

// Activate calls the license API to validate and activate a key on this machine.
func Activate(key string) (*Status, error) {
	apiURL := LicenseAPIURL() + "/activate"

	form := url.Values{}
	form.Set("license_key", key)
	form.Set("machine_id", MachineID())

	resp, err := http.PostForm(apiURL, form)
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
		Plan:        result.Plan,
		Expires:     result.Expires,
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	saveCache(status)
	return status, nil
}

// Validate checks the key against the API and registers the device.
func Validate(key string) (*Status, error) {
	apiURL := LicenseAPIURL() + "/validate"

	form := url.Values{}
	form.Set("license_key", key)
	form.Set("machine_id", MachineID())

	resp, err := http.PostForm(apiURL, form)
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

	status := &Status{
		Valid:       result.Valid,
		Plan:        PlanFree,
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if result.Valid {
		status.Plan = result.Plan
		status.Expires = result.Expires
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

	resp, err := http.PostForm(apiURL, form)
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
// This is the main function used by feature gates — it never blocks on network.
func Check(licenseKey string) *Status {
	if licenseKey == "" {
		return &Status{Valid: false, Plan: PlanFree}
	}

	// Try cached status first
	if cached := loadCache(); cached != nil {
		if cached.Valid {
			// Check if cache is within offline grace period
			if t, err := time.Parse(time.RFC3339, cached.ValidatedAt); err == nil {
				if time.Since(t) < time.Duration(OfflineGraceDays)*24*time.Hour {
					return cached
				}
			}
		}
	}

	// Cache expired or missing — try online validation (with short timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	apiURL := LicenseAPIURL() + "/validate"

	form := url.Values{}
	form.Set("license_key", licenseKey)
	form.Set("machine_id", MachineID())

	resp, err := client.PostForm(apiURL, form)
	if err != nil {
		// Network error — check if we have any cached status (even expired)
		if cached := loadCache(); cached != nil && cached.Valid {
			return cached // stale but better than blocking
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

	status := &Status{
		Valid:       result.Valid,
		Plan:        PlanFree,
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if result.Valid {
		status.Plan = result.Plan
		status.Expires = result.Expires
	}
	saveCache(status)
	return status
}

// CurrentPlan is a shorthand that loads config license key and returns the plan.
func CurrentPlan(licenseKey string) string {
	s := Check(licenseKey)
	if s.Valid {
		return s.Plan
	}
	return PlanFree
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
	data, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(filepath.Dir(cacheFile()), 0o700)
	os.WriteFile(cacheFile(), data, 0o600) //nolint:errcheck
}
