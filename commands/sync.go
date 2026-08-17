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
	var target string

	cmd := &cobra.Command{
		Use:   "sync [app]",
		Short: "Sync server state back to .neo.yml",
		Long: `Reads the current server state for an app and updates .neo.yml to match. Shows a diff of changes before writing.

If no app name is given, the name is read from .neo.yml or inferred from the current directory.

When .neo.yml defines environments:, changes are written into the environment
block the app was deployed to — never at the root, where deploy ignores them.
Use --to to pick the environment without being prompted.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := ""
			if len(args) > 0 {
				appName = args[0]
			}
			return runSync(appName, target, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show changes without writing")
	cmd.Flags().StringVar(&target, "to", "", "named environment from .neo.yml (e.g. staging, production)")
	return cmd
}

type syncChange struct {
	kind  string // "~" modified, "+" added, "-" removed
	field string
	old   string
	new   string
}

func runSync(appName, target string, dryRun bool) error {
	// Load .neo.yml from current directory
	cfg, err := loadNeoConfig(".")
	if err != nil {
		return fmt.Errorf("read .neo.yml: %w", err)
	}
	if cfg == nil {
		ui.Error("No .neo.yml found in current directory")
		return nil
	}

	// Resolve which environment we are syncing. Root-level domain/server are
	// ignored by deploy once environments: exist, so writing there would produce
	// a file deploy refuses to use.
	envName, err := resolveSyncEnvironment(cfg, target)
	if err != nil {
		return err
	}
	if envName == "" && target != "" {
		return fmt.Errorf("--to %s given but .neo.yml has no environments: block", target)
	}

	// Resolve app name: explicit arg → environment-derived name → .neo.yml name → directory
	var nameSource string
	if appName == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("resolve app name: %w", wdErr)
		}
		switch {
		case envName != "":
			// Match how deploy names it, including the -<environment> suffix.
			appName = environmentAppName("", envName, cfg.Environments[envName], cfg, wd)
			nameSource = fmt.Sprintf("environment %q", envName)
		case cfg.Name != "":
			appName = sanitizeName(cfg.Name)
			nameSource = ".neo.yml name field"
		default:
			appName = sanitizeName(filepath.Base(wd))
			nameSource = "directory name"
		}
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

	// Compare against the config that actually applies to this deploy target.
	current := effectiveSyncTarget(cfg, envName)
	changes := diffSyncTarget(current, app)

	if len(changes) == 0 {
		ui.Success(".neo.yml is in sync with server state")
		return nil
	}

	// Print diff
	fmt.Println()
	label := "server → .neo.yml"
	if envName != "" {
		label = fmt.Sprintf("server → .neo.yml (environments.%s)", envName)
	}
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

	if err := writeSyncChanges(".neo.yml", envName, changes); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	ui.Success(fmt.Sprintf(".neo.yml updated (%d changes)", len(changes)))
	return nil
}

// resolveSyncEnvironment picks the environment to sync into. Returns "" when
// .neo.yml has no environments: block (root-level sync).
func resolveSyncEnvironment(cfg *NeoConfig, target string) (string, error) {
	if len(cfg.Environments) == 0 {
		return "", nil
	}
	if target != "" {
		if _, ok := cfg.Environments[target]; !ok {
			return "", fmt.Errorf("environment %q not found in .neo.yml", target)
		}
		return target, nil
	}
	if len(cfg.Environments) == 1 {
		for name := range cfg.Environments {
			return name, nil
		}
	}

	names := make([]string, 0, len(cfg.Environments))
	for name := range cfg.Environments {
		names = append(names, name)
	}
	sort.Strings(names)

	opts := make([]ui.SelectOption, 0, len(names))
	for _, name := range names {
		opts = append(opts, ui.SelectOption{Label: name, Value: name})
	}
	chosen := ui.Select("Sync which environment?", opts)
	if chosen == "" {
		return "", fmt.Errorf("no environment selected")
	}
	return chosen, nil
}

// syncTarget is the subset of config that sync compares and writes.
type syncTarget struct {
	domain     string
	hasDomains bool // domains: list is in use — leave it alone
	port       int
	https      *bool
}

// effectiveSyncTarget reads the values that apply to the chosen deploy target:
// the environment block when there is one, otherwise the root.
func effectiveSyncTarget(cfg *NeoConfig, envName string) syncTarget {
	if envName == "" {
		return syncTarget{
			domain:     cfg.Domain,
			hasDomains: len(cfg.Domains) > 0,
			port:       cfg.Port,
			https:      cfg.HTTPS,
		}
	}

	env := cfg.Environments[envName]
	t := syncTarget{
		domain:     env.Domain,
		hasDomains: len(env.Domains) > 0,
		port:       env.Port,
		https:      env.HTTPS,
	}
	// Environments inherit unset fields from the root.
	if t.port == 0 {
		t.port = cfg.Port
	}
	if t.https == nil {
		t.https = cfg.HTTPS
	}
	return t
}

// diffSyncTarget lists what the server has that the config doesn't.
func diffSyncTarget(current syncTarget, app state.App) []syncChange {
	var changes []syncChange

	if current.hasDomains {
		// A domains: list can carry more than sync knows about; rewriting it
		// from a single state value would silently drop the others.
		if app.Domain != "" && !containsString(app.AllDomains(), current.domain) {
			ui.Info("domains: is a list — review it by hand; sync leaves it alone")
		}
	} else if app.Domain != current.domain {
		changes = append(changes, syncChange{"~", "domain", current.domain, app.Domain})
	}

	if app.InternalPort != 0 && app.InternalPort != current.port {
		changes = append(changes, syncChange{"~", "port", fmt.Sprintf("%d", current.port), fmt.Sprintf("%d", app.InternalPort)})
	}

	serverHTTPS := !app.HTTPOnly
	configHTTPS := false
	if current.https != nil {
		configHTTPS = *current.https
	}
	if serverHTTPS != configHTTPS {
		changes = append(changes, syncChange{"~", "https", fmt.Sprintf("%v", configHTTPS), fmt.Sprintf("%v", serverHTTPS)})
	}

	return changes
}

// writeSyncChanges edits .neo.yml in place, touching only the changed keys.
// Editing the node tree (instead of re-marshalling the struct) keeps comments,
// key order, quoting and indentation exactly as the author wrote them.
func writeSyncChanges(path, envName string, changes []syncChange) error {
	doc, err := loadYAMLDoc(path)
	if err != nil {
		return err
	}
	root := docRoot(doc)
	if root == nil {
		return fmt.Errorf("%s is empty or not a YAML mapping", path)
	}

	// Root-level domain/server are ignored by deploy when environments: exist,
	// so writes go into the environment block.
	dest := root
	if envName != "" {
		dest, err = yamlEnvironmentNode(root, envName)
		if err != nil {
			return err
		}
	}

	for _, c := range changes {
		switch c.field {
		case "domain":
			if c.new == "" {
				yamlMapDelete(dest, "domain")
				continue
			}
			yamlMapSet(dest, "domain", yamlString(c.new))
		case "port":
			var port int
			if _, err := fmt.Sscanf(c.new, "%d", &port); err == nil && port > 0 {
				yamlMapSet(dest, "port", yamlInt(port))
			}
		case "https":
			yamlMapSet(dest, "https", yamlBool(c.new == "true"))
		}
	}

	return saveYAMLDoc(path, doc)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
