package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ui"
)

func newServersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List all configured servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServers()
		},
	}
	cmd.AddCommand(newServersRemoveCmd())
	return cmd
}

func newServersRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a server from config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServersRemove(args[0])
		},
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUse(args[0])
		},
	}
}

func runServers() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if len(cfg.Servers) == 0 {
		fmt.Println()
		fmt.Println("  No servers configured.")
		fmt.Println()
		ui.Info("Add a server: neo init <user@host>")
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Println("  Servers")
	fmt.Println("  " + ui.Faint.Render("─────────────────────────────────────────────────"))

	for _, srv := range cfg.Servers {
		marker := "  "
		suffix := ""
		if srv.Name == cfg.Current {
			marker = ui.Green.Render("● ")
			suffix = ui.Faint.Render("  (active)")
		}
		fmt.Printf("  %s%-15s %s%s\n", marker, srv.Name, srv.Host, suffix)
	}
	fmt.Println()

	return nil
}

func runServersRemove(name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Servers[name]; !ok {
		ui.Error(fmt.Sprintf("Server %q not found", name))
		return nil
	}

	cfg.RemoveServer(name)
	if err := config.Save(cfg); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Removed server %q", name))
	return nil
}

func runUse(name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Servers[name]; !ok {
		ui.Error(fmt.Sprintf("Server %q not found", name))
		return nil
	}

	cfg.Current = name
	if err := config.Save(cfg); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Switched to server %q", name))
	return nil
}
