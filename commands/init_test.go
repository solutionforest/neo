package commands

import (
	"strings"
	"testing"
)

func TestDeriveServerName(t *testing.T) {
	// Hostname inputs derive deterministically from the first DNS label.
	hostnameTests := []struct {
		host string
		want string
	}{
		{"root@staging.mysite.com", "staging"},   // subdomain → first part
		{"root@app.example.com", "app"},          // subdomain → first part
		{"ubuntu@myserver.dev", "myserver"},      // subdomain → first part
		{"deploy.prod.example.com", "deploy"},    // multi-level → first part
	}
	for _, tt := range hostnameTests {
		t.Run(tt.host, func(t *testing.T) {
			got := deriveServerName(tt.host)
			if got != tt.want {
				t.Errorf("deriveServerName(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}

	// Bare IP inputs get a random adjective plus the last octet (e.g. "jade-42"),
	// so assert the contract: a non-empty name ending in "-<last octet>".
	ipTests := []struct {
		host       string
		wantSuffix string
	}{
		{"root@159.65.100.42", "-42"},
		{"10.0.0.1", "-1"},
	}
	for _, tt := range ipTests {
		t.Run(tt.host, func(t *testing.T) {
			got := deriveServerName(tt.host)
			if got == tt.wantSuffix || !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("deriveServerName(%q) = %q, want a non-empty name ending in %q", tt.host, got, tt.wantSuffix)
			}
		})
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"root@159.65.100.42", "159.65.100.42"},
		{"root@10.0.0.1", "10.0.0.1"},
		{"159.65.100.42", "159.65.100.42"},
		{"root@staging.mysite.com", ""},  // hostname → empty
		{"myhost", ""},                    // bare hostname → empty
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractIP(tt.host)
			if got != tt.want {
				t.Errorf("extractIP(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestValidateOS(t *testing.T) {
	tests := []struct {
		name      string
		osID      string
		versionID string
		wantErr   bool
	}{
		{"Ubuntu 24.04", "ubuntu", "24.04", false},
		{"Ubuntu 24.10", "ubuntu", "24.10", false},
		{"Ubuntu 25.04", "ubuntu", "25.04", false},
		{"Debian 12", "debian", "12", false},
		{"Debian 11", "debian", "11", false},
		{"Ubuntu 22.04 too old", "ubuntu", "22.04", true},
		{"Ubuntu 20.04 too old", "ubuntu", "20.04", true},
		{"Ubuntu 18.04 too old", "ubuntu", "18.04", true},
		{"CentOS 9 supported", "centos", "9", false},
		{"RHEL 9 supported", "rhel", "9", false},
		{"AlmaLinux 9 supported", "almalinux", "9", false},
		{"Rocky 9 supported", "rocky", "9", false},
		{"Fedora 40 supported", "fedora", "40", false},
		{"CentOS 7 too old", "centos", "7", true},
		{"Fedora 38 too old", "fedora", "38", true},
		{"Alpine unsupported", "alpine", "3.19", true},
		{"Empty OS ID", "", "", true},
		{"Ubuntu with whitespace", "  ubuntu  ", "24.04", false},
		{"Debian with whitespace", " debian\n", "12", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOS(tt.osID, tt.versionID, "Test OS")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateOS(%q, %q) error = %v, wantErr = %v", tt.osID, tt.versionID, err, tt.wantErr)
			}
		})
	}
}

func TestIsIP(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"255.255.255.255", true},
		{"1.2.3.4", true},
		{"staging.example.com", false},
		{"localhost", false},
		{"1.2.3", false},          // too few parts
		{"1.2.3.4.5", false},      // too many parts
		{"1.2.3.abc", false},      // non-numeric
		{"1.2.3.1234", false},     // part too long
		{"1.2.3.", false},         // empty part
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isIP(tt.input)
			if got != tt.want {
				t.Errorf("isIP(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
