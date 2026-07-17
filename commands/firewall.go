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
		Long: `Manage CrowdSec intrusion prevention and IP blocking.

CrowdSec is a free, open-source security engine that detects and blocks
malicious IPs using nftables. It auto-bans brute-force attackers and syncs
a community-maintained blocklist of known bad IPs.

Run 'neo firewall install' to set it up on your server.`,
	}
	cmd.AddCommand(
		newFirewallInstallCmd(),
		newFirewallUpdateCmd(),
		newFirewallStatusCmd(),
		newFirewallBlockCmd(),
		newFirewallUnblockCmd(),
		newFirewallListCmd(),
	)
	return cmd
}

// ── update ────────────────────────────────────────────────────────────────────

func newFirewallUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update CrowdSec engine, bouncer, and community blocklists",
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallUpdate() },
	}
}

func runFirewallUpdate() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	cs := remote.NewCrowdSec(exec)
	if !st.FirewallInstalled || !cs.IsInstalled() {
		ui.Info("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}

	fmt.Printf("\n  Updating CrowdSec on %s...\n\n", srv.Name)

	if err := cs.Update(os.Stdout); err != nil {
		return err
	}

	fmt.Println()
	ui.Success("CrowdSec updated")
	ui.Info("Run 'neo firewall status' to verify")
	return nil
}

// ── install ───────────────────────────────────────────────────────────────────

func newFirewallInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install CrowdSec + nftables bouncer on the server",
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallInstall() },
	}
}

func runFirewallInstall() error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	cs := remote.NewCrowdSec(exec)

	if st.FirewallInstalled && cs.IsInstalled() {
		ui.Info("CrowdSec is already installed. Run 'neo firewall status' to check.")
		return nil
	}

	fmt.Print("\n  Installing CrowdSec + nftables bouncer...\n\n")

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

// ── status ────────────────────────────────────────────────────────────────────

func newFirewallStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show CrowdSec service status and active decision count",
		RunE:  func(cmd *cobra.Command, args []string) error { return runFirewallStatus() },
	}
}

func runFirewallStatus() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

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

// ── block ─────────────────────────────────────────────────────────────────────

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
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if !st.FirewallInstalled {
		ui.Error("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}

	cs := remote.NewCrowdSec(exec)

	spin := ui.NewSpinner(fmt.Sprintf("Blocking %s...", ip))
	spin.Start()
	blockErr := cs.BlockIP(ip, reason)
	spin.Stop()

	if blockErr != nil {
		return blockErr
	}

	if reason != "" {
		ui.Success(fmt.Sprintf("Blocked %s (%s)", ip, reason))
	} else {
		ui.Success(fmt.Sprintf("Blocked %s", ip))
	}
	ui.Info("Remove with: neo firewall unblock " + ip)
	return nil
}

// ── unblock ───────────────────────────────────────────────────────────────────

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
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if !st.FirewallInstalled {
		ui.Error("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}

	cs := remote.NewCrowdSec(exec)

	spin := ui.NewSpinner(fmt.Sprintf("Unblocking %s...", ip))
	spin.Start()
	unblockErr := cs.UnblockIP(ip)
	spin.Stop()

	if unblockErr != nil {
		return unblockErr
	}

	ui.Success(fmt.Sprintf("Unblocked %s", ip))
	return nil
}

// ── list ──────────────────────────────────────────────────────────────────────

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
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if !st.FirewallInstalled {
		ui.Info("CrowdSec is not installed. Run: neo firewall install")
		return nil
	}

	cs := remote.NewCrowdSec(exec)

	decisions, err := cs.ListDecisions()
	if err != nil {
		return fmt.Errorf("list decisions: %w", err)
	}

	fmt.Printf("\n  Server: %s (%s)\n\n", ui.Bold.Render(srv.Name), srv.Host)

	if len(decisions) == 0 {
		fmt.Print("  No active firewall decisions.\n\n")
		ui.Info("Block an IP: neo firewall block <ip>")
		return nil
	}

	fmt.Printf("  %-20s %-12s %-12s %-10s %s\n", "IP / RANGE", "TYPE", "ORIGIN", "DURATION", "REASON")
	fmt.Printf("  %s\n", ui.Faint.Render(strings.Repeat("─", 72)))

	for _, d := range decisions {
		dur := d.Duration
		if dur == "" || dur == "0s" {
			dur = "permanent"
		}
		reason := d.Scenario
		if reason == "" {
			reason = ui.Faint.Render("—")
		}
		fmt.Printf("  %-20s %-12s %-12s %-10s %s\n", d.Value, d.Type, d.Origin, dur, reason)
	}

	fmt.Printf("\n  %d active decision(s)\n\n", len(decisions))
	return nil
}

func firewallStatusLabel(s string) string {
	switch s {
	case "active":
		return ui.Green.Render("● active")
	case "inactive":
		return ui.Faint.Render("○ inactive")
	case "failed":
		return ui.Red.Render("✗ failed")
	default:
		return ui.Faint.Render("? " + s)
	}
}
