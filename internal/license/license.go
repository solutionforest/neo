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
// Override at build time via: -ldflags "-X github.com/vxero/neo/internal/license.DefaultLicenseAPIURL=..."
var DefaultLicenseAPIURL = "https://neo.vxero.dev/api/license"

// DevLicenseBypass allows local development builds to exercise Neo+ gates
// without requiring a live license. It is intended only for local/staging testing.
var DevLicenseBypass = "false"

// OfflineGraceDays is how many days the CLI trusts a cached validation.
const OfflineGraceDays = 3

// Status represents the current license state.
type Status struct {
	Valid       bool   `json:"valid"`
	Expired     bool   `json:"expired"` // was Plus, now past expiry date
	Plan        string `json:"plan"`    // "free" or "plus"
	Expires     string `json:"expires"` // ISO date, empty if lifetime
	ValidatedAt string `json:"validated_at"`
	ValidatedBy string `json:"validated_by"` // license API URL that produced this cache
}

// isExpiredDate reports whether an ISO date (YYYY-MM-DD or RFC3339) is in the past.
func isExpiredDate(s string) bool {
	if s == "" {
		return false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Now().After(t)
		}
	}
	return false
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

	// Try cached status first (valid or known-expired, within grace period).
	// Reject cache from a different license server (e.g. staging cache read by production binary).
	currentAPI := LicenseAPIURL()
	if cached := loadCache(); cached != nil {
		if cached.ValidatedBy != "" && cached.ValidatedBy != currentAPI {
			// Cache was produced by a different environment — ignore it.
		} else if cached.Valid || cached.Expired {
			if t, err := time.Parse(time.RFC3339, cached.ValidatedAt); err == nil {
				if time.Since(t) < time.Duration(OfflineGraceDays)*24*time.Hour {
					return cached
				}
			}
		}
	}

	// Cache stale or missing — try online validation (with short timeout).
	client := &http.Client{Timeout: 3 * time.Second}
	apiURL := LicenseAPIURL() + "/validate"

	form := url.Values{}
	form.Set("license_key", licenseKey)
	form.Set("machine_id", MachineID())

	resp, err := client.PostForm(apiURL, form)
	if err != nil {
		// Network error — return stale cache so we don't block the user,
		// but only if it came from the same license server.
		if cached := loadCache(); cached != nil {
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
			Plan:        result.Plan,
			Expires:     result.Expires,
			ValidatedAt: now,
		}
		saveCache(status)
		return status
	}

	// License invalid — check whether this is an expired Plus license.
	// The API may or may not return plan/expires for invalid keys, so we also
	// fall back to the previously cached plan and expiry date.
	plan := result.Plan
	expires := result.Expires
	if plan == "" {
		if old := loadCache(); old != nil {
			plan = old.Plan
			if expires == "" {
				expires = old.Expires
			}
		}
	}

	status := &Status{
		Valid:       false,
		Plan:        PlanFree,
		Expires:     expires,
		ValidatedAt: now,
	}
	if plan == PlanPlus && isExpiredDate(expires) {
		// Expired Plus: keep plan so feature gates still pass, mark Expired for UI warning.
		status.Expired = true
		status.Plan = PlanPlus
	}
	saveCache(status)
	return status
}

// CurrentPlan returns the effective plan for feature gating.
// Expired Plus licenses return PlanPlus — features remain accessible, but
// the caller is responsible for showing the expiry warning separately.
func CurrentPlan(licenseKey string) string {
	if DevBypassEnabled() {
		return PlanPlus
	}
	s := Check(licenseKey)
	if s.Valid || s.Expired {
		return s.Plan
	}
	return PlanFree
}

// DevBypassEnabled reports whether local development gates should behave as Neo+.
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

// CheckDaily refreshes the cached license status if more than 24 hours have passed
// since the last check. It returns the current status so callers can show expiry
// warnings. It never blocks for more than 3 seconds and never surfaces errors.
func CheckDaily(licenseKey string) *Status {
	if licenseKey == "" {
		return nil
	}
	currentAPI := LicenseAPIURL()
	if cached := loadCache(); cached != nil {
		if cached.ValidatedBy == "" || cached.ValidatedBy == currentAPI {
			if t, err := time.Parse(time.RFC3339, cached.ValidatedAt); err == nil {
				if time.Since(t) < 24*time.Hour {
					return cached // already checked within the last 24 hours
				}
			}
		}
	}
	// Cache missing, wrong environment, or older than 24 h — refresh.
	return Check(licenseKey)
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
