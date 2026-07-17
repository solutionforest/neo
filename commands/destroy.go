package commands

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/ui"
)

func newDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy [server]",
		Short: "Tear down neo on a server (remove containers, network, and state)",
		Long: "Removes everything neo installed on a server. Two levels:\n" +
			"  • Remove neo, keep data — deletes neo containers, the neo network, and /etc/neo (keeps data volumes + Docker).\n" +
			"  • Full wipe — also prunes data volumes and uninstalls CrowdSec + Docker.\n" +
			"Requires typing the server host to confirm, then removes it from local config.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := cfg.Current
			if len(args) == 1 {
				name = args[0]
			}
			srv, ok := cfg.Servers[name]
			if !ok {
				return fmt.Errorf("server %q not found — run: neo servers", name)
			}
			exec, err := connectSSH(&srv)
			if err != nil {
				return fmt.Errorf("connect to %s: %w", srv.Host, err)
			}
			defer exec.Close()
			return runDestroy(cfg, &srv, exec)
		},
	}
}

// runDestroy tears neo down on srv. Reusable from the Servers TUI menu.
func runDestroy(cfg *config.Config, srv *config.Server, exec *neossh.Executor) error {
	title := fmt.Sprintf("  %s\n  %s\n  %s",
		ui.Bold.Render("Destroy server setup"),
		ui.Faint.Render("─────────────────────────────────────"),
		ui.Faint.Render(srv.Name+"  ("+srv.Host+")"))

	switch ui.Select(title, []ui.SelectOption{
		{Label: "Remove neo, keep data volumes", Value: "neo"},
		{Label: ui.Red.Render("Full wipe — data + CrowdSec + Docker"), Value: "full"},
		{Label: "Cancel", Value: "cancel"},
	}) {
	case "neo":
		return destroyExecute(cfg, srv, exec, false)
	case "full":
		return destroyExecute(cfg, srv, exec, true)
	default:
		return nil
	}
}

func destroyExecute(cfg *config.Config, srv *config.Server, exec *neossh.Executor, full bool) error {
	// Show the removal plan.
	fmt.Println()
	ui.Error("This is destructive and cannot be undone.")
	fmt.Println()
	fmt.Printf("  On %s the following will be removed:\n", ui.Bold.Render(srv.Host))
	fmt.Printf("    • all neo containers (apps, workers, services, neo-caddy)\n")
	fmt.Printf("    • the %q Docker network\n", config.DockerNetwork)
	fmt.Printf("    • /etc/neo (state, Caddy config, secrets)\n")
	if full {
		fmt.Printf("    • %s\n", ui.Red.Render("ALL unused Docker volumes (app & service data — irreversible)"))
		fmt.Printf("    • %s\n", ui.Red.Render("CrowdSec / firewall bouncer (if installed)"))
		fmt.Printf("    • %s\n", ui.Red.Render("the Docker engine"))
	} else {
		fmt.Printf("    %s\n", ui.Faint.Render("(data volumes, Docker, and CrowdSec are kept)"))
	}
	fmt.Println()

	// Type-to-confirm gate.
	var typed string
	if err := huh.NewInput().
		Title(fmt.Sprintf("Type %q to confirm", srv.Host)).
		Value(&typed).
		Run(); err != nil {
		return nil
	}
	if strings.TrimSpace(typed) != srv.Host {
		ui.Info("Confirmation did not match — aborted. Nothing was removed.")
		return nil
	}

	docker := remote.NewDocker(exec)

	spin := ui.NewSpinner("Removing neo containers...")
	spin.Start()
	docker.RemoveNeoContainers()               //nolint:errcheck
	docker.RemoveNetwork(config.DockerNetwork) //nolint:errcheck
	spin.Stop()
	ui.Success("Containers and network removed")

	if full {
		spin = ui.NewSpinner("Pruning data volumes...")
		spin.Start()
		docker.PruneVolumes() //nolint:errcheck
		spin.Stop()
		ui.Success("Data volumes pruned")
	}

	// Remove /etc/neo (root-owned — elevate for non-root sessions).
	rm := "rm -rf /etc/neo"
	if exec.User() != "root" {
		rm = "sudo " + rm
	}
	exec.RunQuiet(rm) //nolint:errcheck
	ui.Success("/etc/neo removed")

	if full {
		cs := remote.NewCrowdSec(exec)
		if cs.IsInstalled() {
			spin = ui.NewSpinner("Uninstalling CrowdSec...")
			spin.Start()
			cs.Uninstall() //nolint:errcheck
			spin.Stop()
			ui.Success("CrowdSec removed")
		}

		spin = ui.NewSpinner("Uninstalling Docker...")
		spin.Start()
		osID, _ := exec.Run("grep '^ID=' /etc/os-release | cut -d= -f2 | tr -d '\"'")
		if err := docker.Uninstall(detectPackageManager(strings.TrimSpace(osID))); err != nil {
			spin.Stop()
			ui.Info("Docker uninstall skipped: " + err.Error())
		} else {
			spin.Stop()
			ui.Success("Docker removed")
		}
	}

	// Drop the server from local config.
	delete(cfg.Servers, srv.Name)
	if cfg.Current == srv.Name {
		cfg.Current = ""
		for n := range cfg.Servers {
			cfg.Current = n
			break
		}
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	card := ui.NewCard()
	card.Add(ui.Green.Render("Server destroyed"))
	card.Blank()
	card.AddKV("Server", srv.Host)
	if full {
		card.AddKV("Level", "Full wipe (Docker + data removed)")
	} else {
		card.AddKV("Level", "neo removed (data kept)")
	}
	card.Add(ui.Faint.Render("Removed from local config."))
	card.Render()
	return nil
}
