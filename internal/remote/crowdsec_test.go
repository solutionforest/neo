package remote

import (
	"strings"
	"testing"
)

func TestIsRPMFamily(t *testing.T) {
	rpm := []string{"fedora", "centos", "rhel", "almalinux", "rocky"}
	for _, id := range rpm {
		if !isRPMFamily(id) {
			t.Errorf("isRPMFamily(%q) = false, want true", id)
		}
	}
	deb := []string{"ubuntu", "debian", "linuxmint", "", "unknown"}
	for _, id := range deb {
		if isRPMFamily(id) {
			t.Errorf("isRPMFamily(%q) = true, want false", id)
		}
	}
}

func TestCrowdsecInstallSteps(t *testing.T) {
	rpm := crowdsecInstallSteps("fedora")
	if !containsSubstr(rpm, "script.rpm.sh") {
		t.Errorf("rpm install missing rpm repo script: %v", rpm)
	}
	if !containsSubstr(rpm, "dnf install") {
		t.Errorf("rpm install missing dnf: %v", rpm)
	}
	if containsSubstr(rpm, "apt-get") {
		t.Errorf("rpm install should not use apt: %v", rpm)
	}

	deb := crowdsecInstallSteps("ubuntu")
	if !containsSubstr(deb, "script.deb.sh") {
		t.Errorf("deb install missing deb repo script: %v", deb)
	}
	if !containsSubstr(deb, "apt-get install") {
		t.Errorf("deb install missing apt: %v", deb)
	}
	if containsSubstr(deb, "dnf") {
		t.Errorf("deb install should not use dnf: %v", deb)
	}

	// Both families must enable the services on install.
	for _, steps := range [][]string{rpm, deb} {
		if !containsSubstr(steps, "systemctl enable --now crowdsec crowdsec-firewall-bouncer") {
			t.Errorf("install missing enable step: %v", steps)
		}
	}

	// Unknown distro falls back to the Debian path.
	if got := crowdsecInstallSteps("weirdos"); !containsSubstr(got, "apt-get install") {
		t.Errorf("unknown distro should fall back to apt: %v", got)
	}
}

func TestCrowdsecUpdateSteps(t *testing.T) {
	rpm := crowdsecUpdateSteps("rocky")
	if !containsSubstr(rpm, "dnf install -y --refresh") {
		t.Errorf("rpm update missing dnf refresh: %v", rpm)
	}
	if containsSubstr(rpm, "apt-get") {
		t.Errorf("rpm update should not use apt: %v", rpm)
	}

	deb := crowdsecUpdateSteps("debian")
	if !containsSubstr(deb, "apt-get update") {
		t.Errorf("deb update missing apt-get update: %v", deb)
	}
	if !containsSubstr(deb, "--only-upgrade") {
		t.Errorf("deb update should only upgrade, not fresh install: %v", deb)
	}

	// Both families refresh hub content and restart at the end.
	for _, steps := range [][]string{rpm, deb} {
		if !containsSubstr(steps, "cscli hub update") || !containsSubstr(steps, "cscli hub upgrade") {
			t.Errorf("update missing hub refresh: %v", steps)
		}
		if steps[len(steps)-1] != "systemctl restart crowdsec crowdsec-firewall-bouncer" {
			t.Errorf("update must restart services last, got: %v", steps)
		}
	}
}

// containsSubstr reports whether any step contains the given substring.
func containsSubstr(steps []string, sub string) bool {
	for _, s := range steps {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
