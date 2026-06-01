package license

import "testing"

func TestCurrentPlanUsesDevBypass(t *testing.T) {
	old := DevLicenseBypass
	DevLicenseBypass = "true"
	defer func() { DevLicenseBypass = old }()

	if got := CurrentPlan(""); got != PlanPlus {
		t.Fatalf("expected dev bypass to return plus, got %q", got)
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
