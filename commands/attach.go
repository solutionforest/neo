package commands

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newAttachCmd() *cobra.Command {
	var name, key string

	cmd := &cobra.Command{
		Use:   "attach <user@host>",
		Short: "Register an already-initialized server in local config",
		Long: "Connects to a server someone else already ran `neo init` on, verifies its " +
			"state, and adds it to your local config so you can manage its apps.\n\n" +
			"Unlike `neo init`, attach never installs Docker/Caddy and never overwrites " +
			"remote state — it is safe to run against a live server with apps deployed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(args[0], name, key)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "server name (default: derived from host)")
	cmd.Flags().StringVarP(&key, "key", "i", "", "path to SSH private key file")
	return cmd
}

func runAttach(host, name, keyPath string) error {
	ui.PrintBanner(cliVersion)
	fmt.Println("  Attaching to server...")
	fmt.Println()

	if name == "" {
		name = deriveServerName(host)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Servers[name]; exists {
		var overwrite bool
		huh.NewConfirm().
			Title(fmt.Sprintf("Server %q already exists. Overwrite?", name)).
			Value(&overwrite).
			Run()
		if !overwrite {
			return nil
		}
	}

	// Build the SSH executor. A teammate who ran `neo key show` already has key
	// auth (neo key, ssh-agent, or ~/.ssh), so HasKeyAuth() is true and no
	// password is needed. Fall back to a password prompt only when no key exists.
	exec := ssh.New(host, 22)
	if keyPath != "" {
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("cannot read SSH key %s: %w", keyPath, err)
		}
		exec.PrivateKey = keyData
	} else if !ssh.HasKeyAuth() {
		var password string
		if err := huh.NewInput().
			Title("SSH password").
			EchoMode(huh.EchoModePassword).
			Value(&password).
			Run(); err != nil || password == "" {
			return fmt.Errorf("no SSH keys found and no password provided")
		}
		exec.Password = password
	}

	fmt.Print("  Connecting via SSH...\n")
	if err := exec.Connect(); err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer exec.Close()

	// Verify the server is already initialized — never overwrite remote state.
	spin := ui.NewSpinner("Verifying server state...")
	spin.Start()
	st, loadErr := state.Load(exec)
	spin.Stop()
	if loadErr != nil || st == nil || !st.Initialized {
		return fmt.Errorf(
			"server at %s is not neo-initialized — nothing to attach to.\n"+
				"  If this is a fresh server, run:  neo init %s",
			host, host)
	}
	ui.Success(fmt.Sprintf("Server verified — %d app(s) registered", len(st.Apps)))

	// Save local config. AddServer makes this the current server if it's the first one.
	srv := config.Server{
		Name:          name,
		Host:          host,
		Port:          22,
		InitializedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if keyPath != "" {
		srv.Key = keyPath
	}
	cfg.AddServer(srv)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Best-effort: deploy neo's managed key so future connections use key auth.
	deployNeoKey(exec)

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Server attached!")
	card.Blank()
	card.AddKV("Name", name)
	card.AddKV("Host", host)
	card.AddKV("Apps", fmt.Sprintf("%d", len(st.Apps)))
	card.Blank()
	card.Add("Manage it:")
	card.Add("  neo list")
	card.Render()

	return nil
}
