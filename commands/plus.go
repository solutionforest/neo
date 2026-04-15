package commands

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/ui"
)

func newPlusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plus",
		Short: "Manage your Neo+ license",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlus()
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "activate <license-key>",
			Short: "Activate a Neo+ license key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPlusActivate(args[0])
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show current license status",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPlusStatus()
			},
		},
		&cobra.Command{
			Use:   "deactivate",
			Short: "Remove license from this machine",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPlusDeactivate()
			},
		},
	)

	return cmd
}

func runPlus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	status := license.Check(cfg.LicenseKey)
	switch {
	case status.Valid && status.Plan == license.PlanPlus:
		return tuiPlusMenuLicensed(cfg, status)
	case status.Expired:
		return tuiPlusMenuExpired(cfg, status)
	default:
		return tuiPlusMenuFree(cfg)
	}
}

// tuiPlusMenuFree shows the Neo+ menu for free users.
func tuiPlusMenuFree(cfg *config.Config) error {
	for {
		title := fmt.Sprintf("  %s\n  %s\n  %s",
			ui.Bold.Render("Neo+"),
			ui.Faint.Render("─────────────────────────────────────"),
			ui.Faint.Render("Plan: Free (1 server limit)"))

		opts := []ui.SelectOption{
			{"Activate License", "activate"},
			{fmt.Sprintf("%-22s%s", "Upgrade to Neo+", ui.Faint.Render("neo.vxero.dev")), "upgrade"},
			{"Back", "back"},
		}

		action := ui.Select(title, opts)
		switch action {
		case "activate":
			var key string
			err := huh.NewInput().
				Title("Enter your Neo+ license key").
				Placeholder("NEO-XXXX-XXXX-XXXX").
				Value(&key).
				Run()
			if err != nil || key == "" {
				continue
			}

			spin := ui.NewSpinner("Activating license...")
			spin.Start()
			status, err := license.Activate(key)
			spin.Stop()

			if err != nil {
				ui.Error(err.Error())
				continue
			}

			cfg.LicenseKey = key
			config.Save(cfg)

			card := ui.NewCard()
			card.Add(ui.Green.Render("License activated!"))
			card.Blank()
			card.AddKV("Plan", "Neo+")
			if status.Expires != "" {
				card.AddKV("Expires", status.Expires)
			} else {
				card.AddKV("Expires", "Never (lifetime)")
			}
			card.Render()
			return nil

		case "upgrade":
			openBrowser("https://neo.vxero.dev/")
			return nil

		case "", "back":
			return nil
		}
	}
}

// tuiPlusMenuLicensed shows the Neo+ menu for licensed users.
func tuiPlusMenuLicensed(cfg *config.Config, status *license.Status) error {
	for {
		expiry := "Lifetime"
		if status.Expires != "" {
			expiry = status.Expires
		}

		title := fmt.Sprintf("  %s\n  %s\n  %s",
			ui.Bold.Render("Neo+"),
			ui.Faint.Render("─────────────────────────────────────"),
			ui.Green.Render(fmt.Sprintf("Plan: Plus (active until %s)", expiry)))

		opts := []ui.SelectOption{
			{fmt.Sprintf("%-22s%s", "License", ui.Faint.Render(license.MaskKey(cfg.LicenseKey))), "info"},
			{ui.Red.Render("Deactivate License"), "deactivate"},
			{"Back", "back"},
		}

		action := ui.Select(title, opts)
		switch action {
		case "info":
			card := ui.NewCard()
			card.Add(ui.Bold.Render("Neo+ License"))
			card.Blank()
			card.AddKV("Key", license.MaskKey(cfg.LicenseKey))
			card.AddKV("Plan", "Plus")
			card.AddKV("Expires", expiry)
			card.AddKV("Machine", license.MachineID())
			card.Render()

		case "deactivate":
			var confirm bool
			huh.NewConfirm().
				Title("Deactivate Neo+ on this machine?").
				Description("You can reactivate later. Free tier limits will apply.").
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
			config.Save(cfg)
			ui.Success("License deactivated")
			return nil

		case "", "back":
			return nil
		}
	}
}

// tuiPlusMenuExpired shows the Neo+ menu for users whose license has expired.
// They still have full feature access — this menu focuses on renewal.
func tuiPlusMenuExpired(cfg *config.Config, status *license.Status) error {
	for {
		title := fmt.Sprintf("  %s\n  %s\n  %s\n  %s",
			ui.Bold.Render("Neo+"),
			ui.Faint.Render("─────────────────────────────────────"),
			ui.Yellow.Render("⚠  License expired — "+status.Expires),
			ui.Faint.Render("All features still active. Renew to keep receiving updates."))

		opts := []ui.SelectOption{
			{fmt.Sprintf("%-22s%s", "Renew Neo+", ui.Faint.Render("neo.vxero.dev")), "renew"},
			{"Activate New Key", "activate"},
			{ui.Red.Render("Deactivate License"), "deactivate"},
			{"Back", "back"},
		}

		action := ui.Select(title, opts)
		switch action {
		case "renew":
			openBrowser("https://neo.vxero.dev/")
			return nil

		case "activate":
			var key string
			err := huh.NewInput().
				Title("Enter your new Neo+ license key").
				Placeholder("NEO-XXXX-XXXX-XXXX").
				Value(&key).
				Run()
			if err != nil || key == "" {
				continue
			}

			spin := ui.NewSpinner("Activating license...")
			spin.Start()
			newStatus, err := license.Activate(key)
			spin.Stop()

			if err != nil {
				ui.Error(err.Error())
				continue
			}

			cfg.LicenseKey = key
			config.Save(cfg)

			card := ui.NewCard()
			card.Add(ui.Green.Render("License activated!"))
			card.Blank()
			card.AddKV("Plan", "Neo+")
			if newStatus.Expires != "" {
				card.AddKV("Expires", newStatus.Expires)
			} else {
				card.AddKV("Expires", "Never (lifetime)")
			}
			card.Render()
			return nil

		case "deactivate":
			var confirm bool
			huh.NewConfirm().
				Title("Deactivate Neo+ on this machine?").
				Description("You can reactivate later. Free tier limits will apply.").
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
			config.Save(cfg)
			ui.Success("License deactivated")
			return nil

		case "", "back":
			return nil
		}
	}
}

// runPlusActivate handles `neo plus activate <key>`.
func runPlusActivate(key string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

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
	card.AddKV("Plan", "Neo+")
	if status.Expires != "" {
		card.AddKV("Expires", status.Expires)
	} else {
		card.AddKV("Expires", "Never (lifetime)")
	}
	card.Render()
	return nil
}

// runPlusStatus handles `neo plus status`.
func runPlusStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.LicenseKey == "" {
		card := ui.NewCard()
		card.Add(ui.Bold.Render("Neo+ Status"))
		card.Blank()
		card.AddKV("Plan", "Free")
		card.AddKV("Servers", "1 (limit)")
		card.AddKV("Backups", ui.Red.Render("locked"))
		card.Blank()
		card.Add(ui.Faint.Render("Upgrade: https://neo.vxero.dev/"))
		card.Render()
		return nil
	}

	spin := ui.NewSpinner("Checking license...")
	spin.Start()
	status := license.Check(cfg.LicenseKey)
	spin.Stop()

	card := ui.NewCard()
	card.Add(ui.Bold.Render("Neo+ Status"))
	card.Blank()
	card.AddKV("Key", license.MaskKey(cfg.LicenseKey))
	switch {
	case status.Valid && status.Plan == license.PlanPlus:
		card.AddKV("Plan", ui.Green.Render("Plus (active)"))
		card.AddKV("Servers", "Unlimited")
		card.AddKV("Backups", ui.Green.Render("enabled"))
		if status.Expires != "" {
			card.AddKV("Expires", status.Expires)
		} else {
			card.AddKV("Expires", "Never (lifetime)")
		}
	case status.Expired:
		card.AddKV("Plan", ui.Yellow.Render("Plus (expired)"))
		card.AddKV("Expired", status.Expires)
		card.AddKV("Features", "All features still active")
		card.Blank()
		card.Add(ui.Yellow.Render("⚠  Updates no longer included"))
		card.Add(ui.Faint.Render("Renew: neo.vxero.dev"))
		card.Add(ui.Faint.Render("Support: support@vxero.dev"))
	default:
		card.AddKV("Plan", "Free")
		card.AddKV("Status", ui.Red.Render("invalid or not activated"))
		card.Blank()
		card.Add(ui.Faint.Render("Upgrade: neo.vxero.dev"))
	}
	card.Render()
	return nil
}

// runPlusDeactivate handles `neo plus deactivate`.
func runPlusDeactivate() error {
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
	config.Save(cfg)
	ui.Success("License deactivated")
	return nil
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
