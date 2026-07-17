package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vxero/neo/internal/ssh"
)

// CrowdSec wraps SSH-based CrowdSec operations on a remote server.
type CrowdSec struct{ exec *ssh.Executor }

// NewCrowdSec creates a CrowdSec remote executor.
func NewCrowdSec(exec *ssh.Executor) *CrowdSec { return &CrowdSec{exec: exec} }

// Decision represents a single CrowdSec decision (ban entry).
type Decision struct {
	ID       int    `json:"id"`
	Origin   string `json:"origin"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
	Scenario string `json:"scenario"`
}

// IsInstalled checks whether the cscli binary is available on the remote server.
func (c *CrowdSec) IsInstalled() bool {
	return c.exec.RunQuiet("command -v cscli >/dev/null 2>&1") == nil
}

// Install installs CrowdSec and the nftables firewall bouncer.
// Detects the OS and uses the appropriate package manager (apt or dnf/yum).
// Output is streamed to w so the caller can display progress.
func (c *CrowdSec) Install(w io.Writer) error {
	osID := c.detectOSID()
	for _, step := range crowdsecInstallSteps(osID) {
		if err := c.exec.Stream(step, w); err != nil {
			return fmt.Errorf("install step failed: %w", err)
		}
	}
	return nil
}

// detectOSID reads the lowercased distro ID from /etc/os-release.
func (c *CrowdSec) detectOSID() string {
	osID, _ := c.exec.Run("grep '^ID=' /etc/os-release | cut -d= -f2 | tr -d '\"'")
	return strings.TrimSpace(strings.ToLower(osID))
}

// isRPMFamily reports whether the distro uses dnf/rpm packaging.
func isRPMFamily(osID string) bool {
	switch osID {
	case "fedora", "centos", "rhel", "almalinux", "rocky":
		return true
	default:
		return false
	}
}

// crowdsecInstallSteps returns the shell steps to install CrowdSec + the
// nftables bouncer for the given distro (adds the repo, installs, enables).
func crowdsecInstallSteps(osID string) []string {
	if isRPMFamily(osID) {
		return []string{
			"curl -s https://packagecloud.io/install/repositories/crowdsec/crowdsec/script.rpm.sh | bash",
			"dnf install -y crowdsec crowdsec-firewall-bouncer-nftables",
			"systemctl enable --now crowdsec crowdsec-firewall-bouncer",
		}
	}
	return []string{
		"curl -s https://packagecloud.io/install/repositories/crowdsec/crowdsec/script.deb.sh | bash",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y crowdsec crowdsec-firewall-bouncer-nftables",
		"systemctl enable --now crowdsec crowdsec-firewall-bouncer",
	}
}

// crowdsecUpdateSteps returns the shell steps to upgrade CrowdSec + bouncer for
// the given distro, refresh the community hub content, and restart the services.
func crowdsecUpdateSteps(osID string) []string {
	var steps []string
	if isRPMFamily(osID) {
		steps = []string{
			"dnf install -y --refresh crowdsec crowdsec-firewall-bouncer-nftables",
		}
	} else {
		steps = []string{
			"apt-get update",
			"DEBIAN_FRONTEND=noninteractive apt-get install -y --only-upgrade crowdsec crowdsec-firewall-bouncer-nftables",
		}
	}
	// Refresh community hub content, then restart to apply everything.
	return append(steps,
		"cscli hub update",
		"cscli hub upgrade",
		"systemctl restart crowdsec crowdsec-firewall-bouncer",
	)
}

// crowdsecUninstallSteps returns the shell steps to stop and remove CrowdSec +
// the nftables bouncer for the given distro. Best-effort (each step tolerant).
func crowdsecUninstallSteps(osID string) []string {
	if isRPMFamily(osID) {
		return []string{
			"systemctl disable --now crowdsec crowdsec-firewall-bouncer 2>/dev/null || true",
			"dnf remove -y crowdsec crowdsec-firewall-bouncer-nftables 2>/dev/null || true",
		}
	}
	return []string{
		"systemctl disable --now crowdsec crowdsec-firewall-bouncer 2>/dev/null || true",
		"DEBIAN_FRONTEND=noninteractive apt-get remove -y -qq crowdsec crowdsec-firewall-bouncer-nftables 2>/dev/null || true",
	}
}

// Uninstall stops and removes CrowdSec and the nftables bouncer. Best-effort;
// elevates with sudo for non-root sessions. Used by a full server teardown.
func (c *CrowdSec) Uninstall() error {
	osID := c.detectOSID()
	sudo := ""
	if c.exec.User() != "root" {
		sudo = "sudo "
	}
	for _, step := range crowdsecUninstallSteps(osID) {
		c.exec.RunQuiet(sudo + step) //nolint:errcheck
	}
	return nil
}

// Update upgrades the CrowdSec engine and nftables bouncer to their latest
// packaged versions, refreshes the hub content (community scenarios, parsers,
// and blocklists), then restarts the services. Assumes CrowdSec is already
// installed and the repo added by Install is present.
func (c *CrowdSec) Update(w io.Writer) error {
	osID := c.detectOSID()
	for _, step := range crowdsecUpdateSteps(osID) {
		if err := c.exec.Stream(step, w); err != nil {
			return fmt.Errorf("update step failed: %w", err)
		}
	}
	return nil
}

// ServiceStatus returns the systemd active state of the crowdsec service.
func (c *CrowdSec) ServiceStatus() string {
	out, err := c.exec.Run("systemctl is-active crowdsec 2>/dev/null || true")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// BouncerStatus returns the systemd active state of the nftables bouncer.
func (c *CrowdSec) BouncerStatus() string {
	out, err := c.exec.Run("systemctl is-active crowdsec-firewall-bouncer 2>/dev/null || true")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// ListDecisions returns all active CrowdSec ban decisions.
func (c *CrowdSec) ListDecisions() ([]Decision, error) {
	out, err := c.exec.Run("cscli decisions list -o json 2>/dev/null || echo '[]'")
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	raw := strings.TrimSpace(out)
	if raw == "null" || raw == "" {
		return nil, nil
	}
	var decisions []Decision
	if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
		return nil, fmt.Errorf("parse decisions: %w", err)
	}
	return decisions, nil
}

// BlockIP adds a permanent ban decision for the given IP address.
func (c *CrowdSec) BlockIP(ip, reason string) error {
	if err := validateFirewallIP(ip); err != nil {
		return err
	}
	if reason == "" {
		reason = "manual block via neo"
	}
	return c.exec.RunQuiet(fmt.Sprintf(
		"cscli decisions add --ip %s --duration 0h --reason %s",
		ssh.ShellQuote(ip), ssh.ShellQuote(reason),
	))
}

// UnblockIP removes all active ban decisions for the given IP address.
func (c *CrowdSec) UnblockIP(ip string) error {
	if err := validateFirewallIP(ip); err != nil {
		return err
	}
	return c.exec.RunQuiet(fmt.Sprintf(
		"cscli decisions delete --ip %s", ssh.ShellQuote(ip),
	))
}

// validateFirewallIP guards against shell injection — allows IPv4, IPv6, and CIDR only.
func validateFirewallIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address cannot be empty")
	}
	for _, ch := range ip {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') ||
			ch == '.' || ch == ':' || ch == '/') {
			return fmt.Errorf("invalid IP address %q — only IPv4, IPv6, and CIDR notation are accepted", ip)
		}
	}
	return nil
}
