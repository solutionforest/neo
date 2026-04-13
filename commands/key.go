package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/ui"
)

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage SSH key access for team members",
	}
	cmd.AddCommand(
		newKeyShowCmd(),
		newKeyAddCmd(),
		newKeyListCmd(),
		newKeyRemoveCmd(),
	)
	return cmd
}

// neo key show — generate (if needed) and print the local neo public key.
func newKeyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show your Neo public key (share this with your admin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyShow()
		},
	}
}

func runKeyShow() error {
	existed := ssh.NeoKeyExists()

	spin := ui.NewSpinner("Generating your Neo key...")
	if !existed {
		spin.Start()
	}
	pubKey, err := ssh.GenerateNeoKey()
	if !existed {
		spin.Stop()
	}
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	pubKey = strings.TrimSpace(pubKey)

	fmt.Println()
	if !existed {
		ui.Success("Neo key generated")
	}

	card := ui.NewCard()
	card.Add(ui.Bold.Render("Your Neo public key:"))
	card.Blank()
	card.Add(ui.Cyan.Render(pubKey))
	card.Blank()
	card.Add(ui.Faint.Render("Send this to your admin to get server access."))
	card.Add(ui.Faint.Render("They run:  neo key add \"<paste key here>\""))
	card.Render()

	return nil
}

// neo key add "<pubkey>" — authorize a key on the current server.
func newKeyAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <public-key>",
		Short: "Authorize a teammate's public key on the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyAdd(args[0])
		},
	}
}

func runKeyAdd(rawKey string) error {
	rawKey = strings.TrimSpace(rawKey)
	if !isValidSSHPublicKey(rawKey) {
		return fmt.Errorf("invalid SSH public key — paste the full line from `neo key show`")
	}

	exec, _, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	// Add to authorized_keys (idempotent — skip if already present)
	key := ssh.ShellQuote(rawKey)
	addCmd := fmt.Sprintf(
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF %s ~/.ssh/authorized_keys 2>/dev/null || echo %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		key, key,
	)
	if err := exec.RunQuiet(addCmd); err != nil {
		return fmt.Errorf("add key to server: %w", err)
	}

	comment := keyComment(rawKey)
	ui.Success(fmt.Sprintf("Key authorized: %s", ui.Bold.Render(comment)))
	fmt.Println()
	ui.Info("Teammate can now deploy with their neo key.")
	ui.Info("Share this with them (add to .neo.yml):")
	fmt.Printf("\n    server: %s\n\n", exec.Host)
	return nil
}

// neo key list — list all keys in authorized_keys on the server.
func newKeyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List authorized keys on the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyList()
		},
	}
}

func runKeyList() error {
	exec, _, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	out, err := exec.Run("cat ~/.ssh/authorized_keys 2>/dev/null || true")
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Println()
		ui.Info("No authorized keys found on this server.")
		fmt.Println()
		return nil
	}

	// Load local neo public key to mark "(you)"
	localPub := ""
	if pub, err := ssh.LoadNeoKey(); err == nil {
		localPub = strings.TrimSpace(pub)
	}

	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		fmt.Println()
		ui.Info("No authorized keys found on this server.")
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Printf("  %s\n\n", ui.Bold.Render("Authorized keys:"))
	for i, line := range lines {
		comment := keyComment(line)
		you := ""
		if strings.TrimSpace(line) == localPub {
			you = "  " + ui.Green.Render("(you)")
		}
		fmt.Printf("  %s%-3d  %s%s\n", ui.Faint.Render("#"), i+1, comment, you)
	}
	fmt.Println()
	ui.Info("Remove a key with: neo key remove <number>")
	fmt.Println()

	return nil
}

// neo key remove <number> — revoke a key by its list number.
func newKeyRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Revoke a key by its number from `neo key list`",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeyRemove(args[0])
		},
	}
}

func runKeyRemove(numStr string) error {
	exec, _, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	out, err := exec.Run("cat ~/.ssh/authorized_keys 2>/dev/null || true")
	if err != nil || strings.TrimSpace(out) == "" {
		return fmt.Errorf("no authorized keys found")
	}

	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}

	// Parse number
	var idx int
	if _, err := fmt.Sscanf(numStr, "%d", &idx); err != nil || idx < 1 || idx > len(lines) {
		return fmt.Errorf("invalid key number — run `neo key list` to see numbers")
	}

	target := lines[idx-1]
	comment := keyComment(target)

	// Guard: don't remove own key
	localPub := ""
	if pub, err := ssh.LoadNeoKey(); err == nil {
		localPub = strings.TrimSpace(pub)
	}
	if strings.TrimSpace(target) == localPub {
		return fmt.Errorf("cannot remove your own key — you would lose server access")
	}

	// Remove the line from authorized_keys using grep -vF
	removeCmd := fmt.Sprintf(
		`grep -vF %s ~/.ssh/authorized_keys > /tmp/neo_ak_tmp && mv /tmp/neo_ak_tmp ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		ssh.ShellQuote(target),
	)
	if err := exec.RunQuiet(removeCmd); err != nil {
		return fmt.Errorf("remove key: %w", err)
	}

	ui.Success(fmt.Sprintf("Key removed: %s", ui.Bold.Render(comment)))
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

// keyComment extracts the comment field (3rd token) from an authorized_keys line.
// Falls back to a truncated key fingerprint if no comment is present.
func keyComment(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		return parts[2]
	}
	if len(parts) >= 2 {
		// No comment — show last 16 chars of key material
		k := parts[1]
		if len(k) > 16 {
			return "..." + k[len(k)-16:]
		}
		return k
	}
	return line
}

// isValidSSHPublicKey does a basic sanity check on the key format.
func isValidSSHPublicKey(s string) bool {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return false
	}
	keyType := parts[0]
	return strings.HasPrefix(keyType, "ssh-") || strings.HasPrefix(keyType, "ecdsa-")
}

