# Plan: `neo firewall` — CrowdSec IP Management

## Context

Neo manages remote Ubuntu/Debian servers over SSH. Users need a way to install and manage a firewall with IP blocklist/allowlist capabilities. **CrowdSec** is the chosen tool — it's free, open-source, written in Go, lightweight (~50–100 MB RAM), and works natively on Ubuntu 24.04+ and Debian. It auto-detects attacks (SSH brute-force, HTTP scanners) and syncs community-maintained IP blocklists. The `crowdsec-firewall-bouncer-nftables` enforces bans via nftables.

## Commands

```
neo firewall install          Install CrowdSec + nftables bouncer
neo firewall status           Show service status + active decision count
neo firewall block <ip>       Permanently ban an IP (--reason optional)
neo firewall unblock <ip>     Remove a ban for an IP
neo firewall list             List all active blocked IPs
```

## Files

| Action | File |
|---|---|
| CREATE | `commands/firewall.go` |
| CREATE | `internal/remote/crowdsec.go` |
| MODIFY | `internal/state/state.go` |
| MODIFY | `commands/root.go` |

## Implementation

### 1. `internal/state/state.go` — add one field to `State`

```go
StealthMode       bool `json:"stealth_mode,omitempty"`
FirewallInstalled bool `json:"firewall_installed,omitempty"`  // ← add after StealthMode
```

### 2. `internal/remote/crowdsec.go` — new file

```go
package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vxero/neo/internal/ssh"
)

type CrowdSec struct{ exec *ssh.Executor }

func NewCrowdSec(exec *ssh.Executor) *CrowdSec { return &CrowdSec{exec: exec} }

type Decision struct {
	ID       int    `json:"id"`
	Origin   string `json:"origin"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
	Scenario string `json:"scenario"`
}

func (c *CrowdSec) IsInstalled() bool {
	return c.exec.RunQuiet("command -v cscli >/dev/null 2>&1") == nil
}

func (c *CrowdSec) Install(w io.Writer) error {
	steps := []string{
		"curl -s https://packagecloud.io/install/repositories/crowdsec/crowdsec/script.deb.sh | bash",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y crowdsec crowdsec-firewall-bouncer-nftables",
		"systemctl enable --now crowdsec crowdsec-firewall-bouncer",
	}
	for _, step := range steps {
		if err := c.exec.Stream(step, w); err != nil {
			return fmt.Errorf("install step failed: %w", err)
		}
	}
	return nil
}

func (c *CrowdSec) ServiceStatus() string {
	out, err := c.exec.Run("systemctl is-active crowdsec 2>/dev/null || true")
	if err != nil { return "unknown" }
	return strings.TrimSpace(out)
}

func (c *CrowdSec) BouncerStatus() string {
	out, err := c.exec.Run("systemctl is-active crowdsec-firewall-bouncer 2>/dev/null || true")
	if err != nil { return "unknown" }
	return strings.TrimSpace(out)
}

func (c *CrowdSec) ListDecisions() ([]Decision, error) {
	out, err := c.exec.Run("cscli decisions list -o json 2>/dev/null || echo '[]'")
	if err != nil { return nil, fmt.Errorf("list decisions: %w", err) }
	raw := strings.TrimSpace(out)
	if raw == "null" || raw == "" { return nil, nil }
	var decisions []Decision
	if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
		return nil, fmt.Errorf("parse decisions: %w", err)
	}
	return decisions, nil
}

func (c *CrowdSec) BlockIP(ip, reason string) error {
	if err := validateFirewallIP(ip); err != nil { return err }
	if reason == "" { reason = "manual block via neo" }
	return c.exec.RunQuiet(fmt.Sprintf(
		"cscli decisions add --ip %s --duration 0h --reason %s",
		ssh.ShellQuote(ip), ssh.ShellQuote(reason),
	))
}

func (c *CrowdSec) UnblockIP(ip string) error {
	if err := validateFirewallIP(ip); err != nil { return err }
	return c.exec.RunQuiet(fmt.Sprintf("cscli decisions delete --ip %s", ssh.ShellQuote(ip)))
}

// validateFirewallIP guards against shell injection — allows IPv4, IPv6, CIDR only.
func validateFirewallIP(ip string) error {
	if ip == "" { return fmt.Errorf("IP address cannot be empty") }
	for _, c := range ip {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') ||
			c == '.' || c == ':' || c == '/') {
			return fmt.Errorf("invalid IP address %q", ip)
		}
	}
	return nil
}
```

### 3. `commands/firewall.go` — new file

```go
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage CrowdSec firewall (install, block/unblock IPs, list decisions)",
	}
	cmd.AddCommand(
		newFirewallInstallCmd(),
		newFirewallStatusCmd(),
		newFirewallBlockCmd(),
		newFirewallUnblockCmd(),
		newFirewallListCmd(),
	)
	return cmd
}

// install
func newFirewallInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install CrowdSec + nftables bouncer on the server",
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallInstall() },
	}
}
func runFirewallInstall() error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil { return err }
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil { return fmt.Errorf("load state: %w", err) }
	cs := remote.NewCrowdSec(exec)
	if st.FirewallInstalled && cs.IsInstalled() {
		ui.Info("CrowdSec is already installed. Run 'neo firewall status' to check.")
		return nil
	}
	fmt.Println("\n  Installing CrowdSec + nftables bouncer...\n")
	if err := cs.Install(os.Stdout); err != nil {
		return err
	}
	st.FirewallInstalled = true
	if saveErr := state.Save(exec, st); saveErr != nil {
		ui.Error(fmt.Sprintf("Warning: could not save state: %s", saveErr))
	}
	fmt.Println()
	ui.Success("CrowdSec installed and running")
	ui.Info("Block an IP:  neo firewall block <ip>")
	ui.Info("View status:  neo firewall status")
	return nil
}

// status
func newFirewallStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show CrowdSec service status and active decision count",
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallStatus() },
	}
}
func runFirewallStatus() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil { return err }
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil { return fmt.Errorf("load state: %w", err) }
	if !st.FirewallInstalled {
		ui.Info("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}
	cs := remote.NewCrowdSec(exec)
	decisions, _ := cs.ListDecisions()
	fmt.Printf("\n  Server: %s (%s)\n\n", ui.Bold.Render(srv.Name), srv.Host)
	fmt.Printf("  %-30s %s\n", "CrowdSec engine:", firewallStatusLabel(cs.ServiceStatus()))
	fmt.Printf("  %-30s %s\n", "nftables bouncer:", firewallStatusLabel(cs.BouncerStatus()))
	fmt.Printf("\n  %-30s %d\n\n", "Active decisions:", len(decisions))
	ui.Info("Run 'neo firewall list' to see all blocked IPs")
	return nil
}

// block
func newFirewallBlockCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "block <ip>",
		Short: "Permanently ban an IP address",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallBlock(args[0], reason) },
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason for the block")
	return cmd
}
func runFirewallBlock(ip, reason string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil { return err }
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil { return fmt.Errorf("load state: %w", err) }
	if !st.FirewallInstalled {
		ui.Error("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}
	cs := remote.NewCrowdSec(exec)
	spin := ui.NewSpinner(fmt.Sprintf("Blocking %s...", ip))
	spin.Start()
	blockErr := cs.BlockIP(ip, reason)
	spin.Stop()
	if blockErr != nil { return blockErr }
	if reason != "" {
		ui.Success(fmt.Sprintf("Blocked %s (%s)", ip, reason))
	} else {
		ui.Success(fmt.Sprintf("Blocked %s", ip))
	}
	ui.Info("Remove with: neo firewall unblock " + ip)
	return nil
}

// unblock
func newFirewallUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <ip>",
		Short: "Remove a ban for an IP address",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallUnblock(args[0]) },
	}
}
func runFirewallUnblock(ip string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil { return err }
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil { return fmt.Errorf("load state: %w", err) }
	if !st.FirewallInstalled {
		ui.Error("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}
	cs := remote.NewCrowdSec(exec)
	spin := ui.NewSpinner(fmt.Sprintf("Unblocking %s...", ip))
	spin.Start()
	unblockErr := cs.UnblockIP(ip)
	spin.Stop()
	if unblockErr != nil { return unblockErr }
	ui.Success(fmt.Sprintf("Unblocked %s", ip))
	return nil
}

// list
func newFirewallListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all active firewall decisions",
		RunE:    func(cmd *cobra.Command, args []string) error { return runFirewallList() },
	}
}
func runFirewallList() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil { return err }
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil { return fmt.Errorf("load state: %w", err) }
	if !st.FirewallInstalled {
		ui.Info("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}
	cs := remote.NewCrowdSec(exec)
	decisions, err := cs.ListDecisions()
	if err != nil { return fmt.Errorf("list decisions: %w", err) }
	fmt.Printf("\n  Server: %s (%s)\n\n", ui.Bold.Render(srv.Name), srv.Host)
	if len(decisions) == 0 {
		fmt.Println("  No active firewall decisions.\n")
		ui.Info("Block an IP: neo firewall block <ip>")
		return nil
	}
	fmt.Printf("  %-20s %-12s %-12s %-10s %s\n", "IP / RANGE", "TYPE", "ORIGIN", "DURATION", "REASON")
	fmt.Printf("  %s\n", ui.Faint.Render(strings.Repeat("─", 72)))
	for _, d := range decisions {
		dur := d.Duration
		if dur == "" || dur == "0s" { dur = "permanent" }
		reason := d.Scenario
		if reason == "" { reason = ui.Faint.Render("—") }
		fmt.Printf("  %-20s %-12s %-12s %-10s %s\n", d.Value, d.Type, d.Origin, dur, reason)
	}
	fmt.Printf("\n  %d active decision(s)\n\n", len(decisions))
	return nil
}

func firewallStatusLabel(s string) string {
	switch s {
	case "active":   return ui.Green.Render("● active")
	case "inactive": return ui.Faint.Render("○ inactive")
	case "failed":   return ui.Red.Render("✗ failed")
	default:         return ui.Faint.Render("? " + s)
	}
}
```

### 4. `commands/root.go` — register the command

Add `newFirewallCmd()` after `newStealthCmd()` in the `root.AddCommand(...)` block:

```go
newStealthCmd(),
newFirewallCmd(),   // ← add this line
newVersionCmd(),
```

## Verification

```bash
make build
./bin/neo firewall --help
./bin/neo firewall install          # on a real Ubuntu/Debian VPS
./bin/neo firewall status
./bin/neo firewall block 1.2.3.4 --reason "test"
./bin/neo firewall list
./bin/neo firewall unblock 1.2.3.4
./bin/neo firewall list             # should be empty again
```
