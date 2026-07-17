package commands

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/ui"
)

// newLicenseCmd manages the (free) neo license. `neo plus` is a hidden alias
// kept for backwards compatibility.
func newLicenseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "license",
		Aliases: []string{"plus"},
		Short:   "Manage your neo license (free)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLicenseMenu()
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "activate [key]",
			Short: "Activate neo — registers a free license by email, or activates an existing key",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runActivate(args)
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show current license status",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runLicenseStatus()
			},
		},
		&cobra.Command{
			Use:   "deactivate",
			Short: "Remove the license from this machine",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runLicenseDeactivate()
			},
		},
	)

	return cmd
}

// newActivateCmd exposes `neo activate` at the top level for the required
// first-run activation flow.
func newActivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "activate [key]",
		Short: "Activate neo (free) — by email, or with an existing key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivate(args)
		},
	}
}

// runActivate registers a free license (by email) or activates a given key.
func runActivate(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
		return activateExistingKey(cfg, strings.TrimSpace(args[0]))
	}

	// No key — register a free license by email.
	var email string
	if err := huh.NewInput().
		Title("Activate neo (free)").
		Description("Enter your email — we'll issue a free license key.").
		Placeholder("you@example.com").
		Value(&email).
		Run(); err != nil {
		return nil
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email required to activate")
	}

	spin := ui.NewSpinner("Registering free license...")
	spin.Start()
	status, err := license.Register(email)
	spin.Stop()
	if err != nil {
		return err
	}

	cfg.LicenseKey = status.Key
	if err := config.Save(cfg); err != nil {
		return err
	}

	card := ui.NewCard()
	card.Add(ui.Green.Render("neo activated!"))
	card.Blank()
	card.AddKV("Key", license.MaskKey(status.Key))
	card.AddKV("Plan", "Free")
	card.Render()
	return nil
}

// activateExistingKey validates and saves a key the user already has.
func activateExistingKey(cfg *config.Config, key string) error {
	spin := ui.NewSpinner("Activating license...")
	spin.Start()
	status, err := license.Activate(key)
	spin.Stop()
	if err != nil {
		return err
	}

	cfg.LicenseKey = key
	if err := config.Save(cfg); err != nil {
		return err
	}

	card := ui.NewCard()
	card.Add(ui.Green.Render("License activated!"))
	card.Blank()
	card.AddKV("Key", license.MaskKey(key))
	plan := status.Plan
	if plan == "" {
		plan = "Free"
	}
	card.AddKV("Plan", strings.ToUpper(plan[:1])+plan[1:])
	card.Render()
	return nil
}

// runLicenseMenu is the interactive license screen (also reachable from the dashboard).
func runLicenseMenu() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	for {
		activated := license.IsActivated(cfg.LicenseKey)

		var statusLine string
		if activated {
			statusLine = ui.Green.Render("Activated · " + license.MaskKey(cfg.LicenseKey))
		} else {
			statusLine = ui.Yellow.Render("Not activated — free, run activate")
		}
		title := fmt.Sprintf("  %s\n  %s\n  %s",
			ui.Bold.Render("neo license"),
			ui.Faint.Render("─────────────────────────────────────"),
			statusLine)

		var opts []ui.SelectOption
		if activated {
			opts = []ui.SelectOption{
				{Label: fmt.Sprintf("%-22s%s", "License", ui.Faint.Render(license.MaskKey(cfg.LicenseKey))), Value: "info"},
				{Label: ui.Red.Render("Deactivate License"), Value: "deactivate"},
				{Label: "Back", Value: "back"},
			}
		} else {
			opts = []ui.SelectOption{
				{Label: "Activate (free)", Value: "activate"},
				{Label: "Back", Value: "back"},
			}
		}

		switch ui.Select(title, opts) {
		case "activate":
			if err := runActivate(nil); err != nil {
				ui.Error(err.Error())
			}
			cfg, _ = config.Load()
		case "info":
			card := ui.NewCard()
			card.Add(ui.Bold.Render("neo License"))
			card.Blank()
			card.AddKV("Key", license.MaskKey(cfg.LicenseKey))
			card.AddKV("Plan", "Free")
			card.AddKV("Machine", license.MachineID())
			card.Render()
		case "deactivate":
			var confirm bool
			huh.NewConfirm().
				Title("Deactivate neo on this machine?").
				Description("You'll need to activate again (free) to use neo here.").
				Value(&confirm).
				Run() //nolint:errcheck
			if !confirm {
				continue
			}
			spin := ui.NewSpinner("Deactivating...")
			spin.Start()
			license.Deactivate(cfg.LicenseKey) //nolint:errcheck
			spin.Stop()
			cfg.LicenseKey = ""
			config.Save(cfg) //nolint:errcheck
			ui.Success("License deactivated")
			// Return to the activation screen — the user must enter an email
			// again (same as a fresh start), or a different one to switch account.
			return runActivate(nil)
		case "", "back":
			return nil
		}
	}
}

// runLicenseStatus handles `neo license status`.
func runLicenseStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	card := ui.NewCard()
	card.Add(ui.Bold.Render("neo License"))
	card.Blank()

	if cfg.LicenseKey == "" {
		card.AddKV("Status", ui.Yellow.Render("not activated"))
		card.Blank()
		card.Add(ui.Faint.Render("Activate (free): neo activate"))
		card.Render()
		return nil
	}

	spin := ui.NewSpinner("Checking license...")
	spin.Start()
	status := license.Check(cfg.LicenseKey)
	spin.Stop()

	card.AddKV("Key", license.MaskKey(cfg.LicenseKey))
	if status.Valid {
		card.AddKV("Status", ui.Green.Render("activated"))
		plan := status.Plan
		if plan == "" {
			plan = "free"
		}
		card.AddKV("Plan", plan)
	} else {
		card.AddKV("Status", ui.Red.Render("invalid or not activated"))
		card.Blank()
		card.Add(ui.Faint.Render("Re-activate (free): neo activate"))
	}
	card.Render()
	return nil
}

// runLicenseDeactivate handles `neo license deactivate`.
func runLicenseDeactivate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.LicenseKey == "" {
		ui.Info("No active license")
		return nil
	}

	spin := ui.NewSpinner("Deactivating...")
	spin.Start()
	license.Deactivate(cfg.LicenseKey) //nolint:errcheck
	spin.Stop()

	cfg.LicenseKey = ""
	config.Save(cfg) //nolint:errcheck
	ui.Success("License deactivated")
	// Return to the activation screen — enter an email again to re-activate,
	// or a different one to switch account (matches the dashboard flow).
	return runActivate(nil)
}

// openBrowser opens a URL in the user's default browser.
func openBrowser(url string) {
	ui.Info("Opening " + url + " in your browser...")
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = exec.Command("open", url).Start()
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	if err != nil {
		ui.Info("Visit: " + ui.Bold.Render(url))
	}
}
