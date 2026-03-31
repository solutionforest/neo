//go:build integration

package license_test

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// Default test API base — the local Neo CMS (HTTPS with self-signed cert).
// Override with NEO_LICENSE_URL env var.
const defaultTestAPI = "https://neo.test/api/license"

const (
	validKey   = "TEST-NEO-PLUS-0001"
	teamKey    = "TEST-NEO-TEAM-0001"
	expiredKey = "TEST-NEO-EXPIRED-01"
	invalidKey = "INVALID-KEY-DOES-NOT-EXIST"
	machineID  = "integration-test-01"
)

func apiBase() string {
	if v := os.Getenv("NEO_LICENSE_URL"); v != "" {
		return v
	}
	return defaultTestAPI
}

type apiResponse struct {
	Valid   bool    `json:"valid"`
	Plan    *string `json:"plan"`
	Expires *string `json:"expires"`
	Error   *string `json:"error"`
}

// httpClient with TLS verification disabled for local dev certs.
var httpClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local dev only
	},
}

func postLicense(t *testing.T, endpoint, key, machine string) (*http.Response, apiResponse) {
	t.Helper()

	form := url.Values{}
	form.Set("license_key", key)
	form.Set("machine_id", machine)

	// Retry on 429 (rate limit) — CMS throttles at 30 req/min.
	// On first 429, wait for the full rate-limit window to reset.
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := httpClient.PostForm(apiBase()+endpoint, form)
		if err != nil {
			t.Fatalf("POST %s failed: %v", endpoint, err)
		}

		if resp.StatusCode == 429 {
			resp.Body.Close()
			if attempt == 0 {
				t.Logf("rate limited on %s, waiting 61s for window reset...", endpoint)
				time.Sleep(61 * time.Second)
			} else {
				t.Logf("rate limited on %s again, waiting 10s (attempt %d)...", endpoint, attempt+1)
				time.Sleep(10 * time.Second)
			}
			continue
		}

		defer resp.Body.Close()
		var result apiResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode response from %s (status %d): %v", endpoint, resp.StatusCode, err)
		}
		return resp, result
	}
	t.Fatalf("rate limited on %s after 3 retries", endpoint)
	return nil, apiResponse{}
}

// TestMain cleans up test activations before running the suite.
func TestMain(m *testing.M) {
	// Only deactivate machines each key actually uses to stay within rate limit (30/min)
	cleanup := map[string][]string{
		validKey: {machineID, "machine-limit-b", "machine-limit-c", "machine-val-b", "machine-lifecycle"},
		teamKey:  {"machine-team-01", "machine-team-02"},
	}
	for key, machines := range cleanup {
		for _, mid := range machines {
			form := url.Values{}
			form.Set("license_key", key)
			form.Set("machine_id", mid)
			resp, _ := httpClient.PostForm(apiBase()+"/deactivate", form)
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
	os.Exit(m.Run())
}

// --- Activate ---

func TestIntegration_ActivateValidKey(t *testing.T) {
	// TestMain already cleaned up all activations
	resp, result := postLicense(t, "/activate", validKey, machineID)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		errMsg := ""
		if result.Error != nil {
			errMsg = *result.Error
		}
		t.Fatalf("expected valid=true, got false (error: %s)", errMsg)
	}
	if result.Plan == nil || *result.Plan != "plus" {
		t.Fatalf("expected plan=plus, got %v", result.Plan)
	}
	// Lifetime license → expires should be null
	if result.Expires != nil {
		t.Fatalf("expected expires=null for lifetime license, got %v", *result.Expires)
	}
	t.Logf("activate OK: valid=%v plan=%v", result.Valid, *result.Plan)
}

func TestIntegration_ActivateIdempotent(t *testing.T) {
	// machineID was activated in previous test — activate again (idempotent)
	resp, result := postLicense(t, "/activate", validKey, machineID)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on second activate, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatalf("expected valid=true on idempotent activate")
	}
	t.Log("idempotent activate OK")
}

func TestIntegration_ActivateInvalidKey(t *testing.T) {
	resp, result := postLicense(t, "/activate", invalidKey, machineID)

	if resp.StatusCode == 200 && result.Valid {
		t.Fatal("expected activation to fail for invalid key")
	}
	if result.Valid {
		t.Fatal("expected valid=false for invalid key")
	}
	t.Logf("invalid key rejected: status=%d error=%v", resp.StatusCode, result.Error)
}

func TestIntegration_ActivateExpiredKey(t *testing.T) {
	resp, result := postLicense(t, "/activate", expiredKey, machineID)

	if resp.StatusCode == 200 && result.Valid {
		t.Fatal("expected activation to fail for expired key")
	}
	if result.Valid {
		t.Fatal("expected valid=false for expired key")
	}
	t.Logf("expired key rejected: status=%d error=%v", resp.StatusCode, result.Error)
}

func TestIntegration_ActivateLimitReached(t *testing.T) {
	// machineID already activated from previous tests (slot 1 of 2)
	// Activate a second machine (slot 2 of 2)
	_, r2 := postLicense(t, "/activate", validKey, "machine-limit-b")
	if !r2.Valid {
		t.Fatal("second activation should succeed")
	}

	// Third machine should be rejected by strict activate (limit=2)
	resp, r3 := postLicense(t, "/activate", validKey, "machine-limit-c")
	if r3.Valid && resp.StatusCode == 200 {
		t.Fatal("expected third activation to fail (limit=2)")
	}
	t.Logf("limit enforced: status=%d error=%v", resp.StatusCode, r3.Error)

	// Cleanup: free slot for later tests
	postLicense(t, "/deactivate", validKey, "machine-limit-b")
	postLicense(t, "/deactivate", validKey, "machine-limit-c")
}

// --- Validate ---

func TestIntegration_ValidateActiveKey(t *testing.T) {
	// machineID still activated from earlier tests
	resp, result := postLicense(t, "/validate", validKey, machineID)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("expected valid=true for active license")
	}
	if result.Plan == nil || *result.Plan != "plus" {
		t.Fatalf("expected plan=plus, got %v", result.Plan)
	}
	t.Logf("validate OK: valid=%v plan=%v", result.Valid, *result.Plan)
}

func TestIntegration_ValidateSilentAtLimit(t *testing.T) {
	// machineID is activated (slot 1). Fill slot 2.
	postLicense(t, "/activate", validKey, "machine-val-b")

	// Validate from a NEW machine at limit — should NOT error (lenient)
	resp, result := postLicense(t, "/validate", validKey, "machine-val-new")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("validate should return valid=true even at activation limit")
	}
	t.Logf("validate at limit OK: valid=%v plan=%v", result.Valid, result.Plan)

	// Cleanup: free slot
	postLicense(t, "/deactivate", validKey, "machine-val-b")
}

// --- Deactivate ---

func TestIntegration_Deactivate(t *testing.T) {
	// machineID still activated — deactivate it
	resp, result := postLicense(t, "/deactivate", validKey, machineID)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("deactivate should return valid=true")
	}
	t.Log("deactivate OK")
}

func TestIntegration_DeactivateIdempotent(t *testing.T) {
	// Deactivate something that doesn't exist — should not error
	resp, result := postLicense(t, "/deactivate", validKey, "machine-never-activated")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for idempotent deactivate, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("idempotent deactivate should return valid=true")
	}
	t.Log("idempotent deactivate OK")
}

// --- Full Lifecycle ---

func TestIntegration_FullLifecycle(t *testing.T) {
	machine := "machine-lifecycle"

	// 1. Clean slate
	postLicense(t, "/deactivate", validKey, machine)

	// 2. Activate
	_, r := postLicense(t, "/activate", validKey, machine)
	if !r.Valid {
		t.Fatal("step 1 activate: expected valid=true")
	}
	t.Log("1. activated")

	// 3. Validate
	_, r = postLicense(t, "/validate", validKey, machine)
	if !r.Valid {
		t.Fatal("step 2 validate: expected valid=true")
	}
	t.Log("2. validated")

	// 4. Deactivate
	_, r = postLicense(t, "/deactivate", validKey, machine)
	if !r.Valid {
		t.Fatal("step 3 deactivate: expected valid=true")
	}
	t.Log("3. deactivated")

	// 5. Re-activate (should work — slot freed)
	_, r = postLicense(t, "/activate", validKey, machine)
	if !r.Valid {
		t.Fatal("step 4 re-activate: expected valid=true")
	}
	t.Log("4. re-activated")

	// Cleanup
	postLicense(t, "/deactivate", validKey, machine)
	t.Log("lifecycle test complete")
}

// --- Team Plan ---

func TestIntegration_TeamActivate(t *testing.T) {
	resp, result := postLicense(t, "/activate", teamKey, "machine-team-01")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		errMsg := ""
		if result.Error != nil {
			errMsg = *result.Error
		}
		t.Fatalf("expected valid=true, got false (error: %s)", errMsg)
	}
	if result.Plan == nil || *result.Plan != "team" {
		t.Fatalf("expected plan=team, got %v", result.Plan)
	}
	t.Logf("team activate OK: valid=%v plan=%v", result.Valid, *result.Plan)
}

func TestIntegration_TeamValidate(t *testing.T) {
	// machine-team-01 activated in previous test
	resp, result := postLicense(t, "/validate", teamKey, "machine-team-01")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("expected valid=true for team license")
	}
	if result.Plan == nil || *result.Plan != "team" {
		t.Fatalf("expected plan=team, got %v", result.Plan)
	}
	t.Logf("team validate OK: valid=%v plan=%v", result.Valid, *result.Plan)
}

func TestIntegration_TeamActivationLimit(t *testing.T) {
	// Team key has activation_limit=1, machine-team-01 already activated
	resp, result := postLicense(t, "/activate", teamKey, "machine-team-02")

	if result.Valid && resp.StatusCode == 200 {
		t.Fatal("expected team activation to fail (limit=1, already activated on machine-team-01)")
	}
	t.Logf("team limit enforced: status=%d error=%v", resp.StatusCode, result.Error)
}

func TestIntegration_TeamDeactivate(t *testing.T) {
	resp, result := postLicense(t, "/deactivate", teamKey, "machine-team-01")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		t.Fatal("deactivate should return valid=true")
	}
	t.Log("team deactivate OK")
}

func TestIntegration_TeamReactivateAfterDeactivate(t *testing.T) {
	// Slot freed by previous deactivate — new machine should work
	resp, result := postLicense(t, "/activate", teamKey, "machine-team-02")

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !result.Valid {
		errMsg := ""
		if result.Error != nil {
			errMsg = *result.Error
		}
		t.Fatalf("expected re-activation to succeed after deactivate (error: %s)", errMsg)
	}
	t.Logf("team re-activate OK: plan=%v", *result.Plan)

	// Cleanup
	postLicense(t, "/deactivate", teamKey, "machine-team-02")
}

// --- Request Validation ---

func TestIntegration_MissingFields(t *testing.T) {
	// Missing machine_id
	form := url.Values{}
	form.Set("license_key", validKey)
	resp, err := httpClient.PostForm(apiBase()+"/activate", form)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		t.Log("server accepted request without machine_id (may be lenient)")
	} else {
		t.Logf("server rejected missing machine_id: status=%d", resp.StatusCode)
	}

	// Missing license_key
	form2 := url.Values{}
	form2.Set("machine_id", machineID)
	resp2, err := httpClient.PostForm(apiBase()+"/activate", form2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode == 200 {
		t.Log("server accepted request without license_key (may be lenient)")
	} else {
		t.Logf("server rejected missing license_key: status=%d", resp2.StatusCode)
	}
}
