package commands

import (
	"fmt"
	"os"
	"path/filepath"
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
		Use:   "sync [app]",
		Short: "Sync server state back to .neo.yml",
		Long:  "Reads the current server state for an app and updates .neo.yml to match. Shows a diff of changes before writing.\n\nIf no app name is given, the name is read from .neo.yml or inferred from the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := ""
			if len(args) > 0 {
				appName = args[0]
			}
			return runSync(appName, dryRun)
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

	// Resolve app name: explicit arg → .neo.yml name → directory name
	var nameSource string
	if appName == "" {
		if cfg != nil && cfg.Name != "" {
			appName = sanitizeName(cfg.Name)
			nameSource = ".neo.yml name field"
		} else {
			wd, wdErr := os.Getwd()
			if wdErr != nil {
				return fmt.Errorf("resolve app name: %w", wdErr)
			}
			appName = sanitizeName(filepath.Base(wd))
			nameSource = "directory name"
		}
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
		if nameSource != "" {
			ui.Error(fmt.Sprintf("App %q not found on server (resolved from %s)", appName, nameSource))
		} else {
			ui.Error(fmt.Sprintf("App %q not found on server", appName))
		}
		if len(st.Apps) > 0 {
			names := make([]string, 0, len(st.Apps))
			for name := range st.Apps {
				names = append(names, name)
			}
			sort.Strings(names)
			ui.Info(fmt.Sprintf("Available apps: %s", strings.Join(names, ", ")))
			ui.Info("Run: neo sync <app-name>")
		}
		return nil
	}

	// Collect changes — only deployment metadata that is safe to store in a project file.
	// Env vars, volumes, and workers are NOT synced: they contain secrets and are
	// source-of-truth in .neo.yml, not on the server.
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
	applyChanges(cfg, app)

	// Write
	if err := saveNeoConfig(".", cfg); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	ui.Success(fmt.Sprintf(".neo.yml updated (%d changes)", len(changes)))
	return nil
}

// applyChanges updates only the deployment metadata fields in NeoConfig.
// Env vars, volumes, and workers are intentionally excluded — they contain
// secrets and are managed in .neo.yml directly, not derived from server state.
func applyChanges(cfg *NeoConfig, app state.App) {
	cfg.Domain = app.Domain

	if app.InternalPort != 0 {
		cfg.Port = app.InternalPort
	}

	httpsOn := !app.HTTPOnly
	cfg.HTTPS = &httpsOn
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
