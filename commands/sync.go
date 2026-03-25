package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newSyncCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync <app>",
		Short: "Sync server state back to .neo.yml",
		Long:  "Reads the current server state for an app and updates .neo.yml to match. Shows a diff of changes before writing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(args[0], dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show changes without writing")
	return cmd
}

type syncChange struct {
	kind  string // "~" modified, "+" added, "-" removed
	field string
	old   string
	new   string
}

func runSync(appName string, dryRun bool) error {
	// Load .neo.yml from current directory
	cfg, err := loadNeoConfig(".")
	if err != nil {
		return fmt.Errorf("read .neo.yml: %w", err)
	}
	if cfg == nil {
		ui.Error("No .neo.yml found in current directory")
		return nil
	}

	// Connect and load server state
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	app, ok := st.Apps[appName]
	if !ok {
		ui.Error(fmt.Sprintf("App %q not found on server", appName))
		return nil
	}

	// Collect changes
	var changes []syncChange

	// Domain
	if app.Domain != cfg.Domain {
		changes = append(changes, syncChange{"~", "domain", cfg.Domain, app.Domain})
	}

	// Port
	if app.InternalPort != 0 && app.InternalPort != cfg.Port {
		changes = append(changes, syncChange{"~", "port", fmt.Sprintf("%d", cfg.Port), fmt.Sprintf("%d", app.InternalPort)})
	}

	// HTTPS
	serverHTTPS := !app.HTTPOnly
	configHTTPS := false
	if cfg.HTTPS != nil {
		configHTTPS = *cfg.HTTPS
	}
	if serverHTTPS != configHTTPS {
		changes = append(changes, syncChange{"~", "https", fmt.Sprintf("%v", configHTTPS), fmt.Sprintf("%v", serverHTTPS)})
	}

	// Env vars
	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	// Find added/modified env vars
	envKeys := sortedKeys(app.Env)
	for _, k := range envKeys {
		serverVal := app.Env[k]
		configVal, exists := cfg.Env[k]
		if !exists {
			changes = append(changes, syncChange{"+", "env." + k, "", serverVal})
		} else if configVal != serverVal {
			changes = append(changes, syncChange{"~", "env." + k, configVal, serverVal})
		}
	}
	// Find removed env vars (in config but not on server)
	configKeys := sortedKeys(cfg.Env)
	for _, k := range configKeys {
		if _, exists := app.Env[k]; !exists {
			changes = append(changes, syncChange{"-", "env." + k, cfg.Env[k], ""})
		}
	}

	// Volumes
	for name, vol := range app.Volumes {
		// Strip app name prefix for .neo.yml key
		shortName := strings.TrimPrefix(name, appName+"-")
		if _, exists := cfg.Volumes[shortName]; !exists {
			changes = append(changes, syncChange{"+", "volumes." + shortName, "", vol.ContainerPath})
		}
	}

	// Workers
	for name, worker := range app.Workers {
		if existing, exists := cfg.Workers[name]; !exists {
			changes = append(changes, syncChange{"+", "workers." + name, "", worker.Command})
		} else if existing.Command != worker.Command {
			changes = append(changes, syncChange{"~", "workers." + name, existing.Command, worker.Command})
		}
	}

	if len(changes) == 0 {
		ui.Success(".neo.yml is in sync with server state")
		return nil
	}

	// Print diff
	fmt.Println()
	label := "server → .neo.yml"
	if dryRun {
		label += " (dry run)"
	}
	fmt.Printf("  Syncing %s: %s\n\n", ui.Bold.Render(appName), ui.Faint.Render(label))

	for _, c := range changes {
		switch c.kind {
		case "+":
			fmt.Printf("  %s %-25s %s\n", ui.Green.Render("+"), c.field, ui.Green.Render(c.new))
		case "-":
			fmt.Printf("  %s %-25s %s\n", ui.Red.Render("-"), c.field, ui.Faint.Render(c.old))
		case "~":
			fmt.Printf("  %s %-25s %s → %s\n", ui.Yellow.Render("~"), c.field, ui.Faint.Render(c.old), ui.Bold.Render(c.new))
		}
	}
	fmt.Println()

	if dryRun {
		fmt.Printf("  %d change(s) detected. Run without --dry-run to apply.\n\n", len(changes))
		return nil
	}

	// Confirm
	var confirm bool
	huh.NewConfirm().
		Title(fmt.Sprintf("Write %d change(s) to .neo.yml?", len(changes))).
		Value(&confirm).
		Run() //nolint:errcheck

	if !confirm {
		fmt.Println("  Aborted.")
		return nil
	}

	// Apply changes to config
	applyChanges(cfg, appName, app)

	// Write
	if err := saveNeoConfig(".", cfg); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	ui.Success(fmt.Sprintf(".neo.yml updated (%d changes)", len(changes)))
	return nil
}

// applyChanges updates the NeoConfig with values from server state.
func applyChanges(cfg *NeoConfig, appName string, app state.App) {
	// Domain
	cfg.Domain = app.Domain

	// Port
	if app.InternalPort != 0 {
		cfg.Port = app.InternalPort
	}

	// HTTPS
	httpsOn := !app.HTTPOnly
	cfg.HTTPS = &httpsOn

	// Env vars — replace entirely with server state
	cfg.Env = make(map[string]string)
	for k, v := range app.Env {
		cfg.Env[k] = v
	}

	// Volumes — add missing ones
	if cfg.Volumes == nil {
		cfg.Volumes = make(map[string]NeoVolume)
	}
	for name, vol := range app.Volumes {
		shortName := strings.TrimPrefix(name, appName+"-")
		if _, exists := cfg.Volumes[shortName]; !exists {
			cfg.Volumes[shortName] = NeoVolume{Path: vol.ContainerPath}
		}
	}

	// Workers — sync commands
	if cfg.Workers == nil && len(app.Workers) > 0 {
		cfg.Workers = make(map[string]NeoWorker)
	}
	for name, worker := range app.Workers {
		cfg.Workers[name] = NeoWorker{Command: worker.Command}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
