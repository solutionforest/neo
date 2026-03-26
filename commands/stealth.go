package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newStealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stealth",
		Short: "Toggle stealth mode — hide server from IP-based discovery",
		Long: `When stealth mode is enabled, your server does not respond to direct IP
access. The Caddy welcome page is removed and only configured domains
serve traffic. This prevents scanners from identifying your server.

Run again to disable stealth mode and restore the welcome page.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStealth()
		},
	}
}

func runStealth() error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	caddy := remote.NewCaddy(exec)

	if st.StealthMode {
		// Disable stealth → restore welcome page
		st.StealthMode = false
		if st.ServerIP != "" {
			spin := ui.NewSpinner("Restoring welcome page...")
			spin.Start()
			caddy.AddWelcomePage(st.ServerIP) //nolint:errcheck
			spin.Stop()
		}
		if err := state.Save(exec, st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		ui.Success("Stealth mode disabled — welcome page restored on " + st.ServerIP)
	} else {
		// Enable stealth → remove welcome page
		st.StealthMode = true
		spin := ui.NewSpinner("Removing welcome page...")
		spin.Start()
		caddy.RemoveWelcomePage() //nolint:errcheck
		spin.Stop()
		if err := state.Save(exec, st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		ui.Success("Stealth mode enabled — server no longer responds on IP address")
	}

	return nil
}
