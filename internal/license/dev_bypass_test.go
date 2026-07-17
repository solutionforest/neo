package license

import "testing"

func TestIsActivatedUsesDevBypass(t *testing.T) {
	old := DevLicenseBypass
	DevLicenseBypass = "true"
	defer func() { DevLicenseBypass = old }()

	// Empty key would normally be invalid, but dev bypass activates.
	if !IsActivated("") {
		t.Fatal("expected dev bypass to report activated")
	}
}

func TestDevBypassDisabledByDefault(t *testing.T) {
	old := DevLicenseBypass
	DevLicenseBypass = "false"
	defer func() { DevLicenseBypass = old }()
	t.Setenv("NEO_DEV_PLUS", "")

	if DevBypassEnabled() {
		t.Fatal("expected dev bypass to be disabled")
	}
}
