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
	// Detect OS family for package manager selection
	osID, _ := c.exec.Run("grep '^ID=' /etc/os-release | cut -d= -f2 | tr -d '\"'")
	osID = strings.TrimSpace(strings.ToLower(osID))

	var steps []string
	switch osID {
	case "fedora", "centos", "rhel", "almalinux", "rocky":
		steps = []string{
			"curl -s https://packagecloud.io/install/repositories/crowdsec/crowdsec/script.rpm.sh | bash",
			"dnf install -y crowdsec crowdsec-firewall-bouncer-nftables",
			"systemctl enable --now crowdsec crowdsec-firewall-bouncer",
		}
	default:
		steps = []string{
			"curl -s https://packagecloud.io/install/repositories/crowdsec/crowdsec/script.deb.sh | bash",
			"DEBIAN_FRONTEND=noninteractive apt-get install -y crowdsec crowdsec-firewall-bouncer-nftables",
			"systemctl enable --now crowdsec crowdsec-firewall-bouncer",
		}
	}

	for _, step := range steps {
		if err := c.exec.Stream(step, w); err != nil {
			return fmt.Errorf("install step failed: %w", err)
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
