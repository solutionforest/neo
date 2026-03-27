package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// resolveEnvApp extracts the app name and remaining args from env subcommand args.
// If the first arg looks like KEY=VALUE or is absent, auto-detect app from cwd.
func resolveEnvApp(args []string) (appName string, rest []string) {
	if len(args) > 0 && !strings.Contains(args[0], "=") {
		return args[0], args[1:]
	}
	cwd, _ := os.Getwd()
	return sanitizeName(filepath.Base(cwd)), args
}

func newEnvCmd() *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "env [app]",
		Short: "Manage environment variables for an app",
		Long:  "View, set, remove, or import environment variables. App name is optional — defaults to current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := resolveEnvApp(args)
			if jsonFlag {
				return runEnvListJSON(name)
			}
			return runEnvList(name)
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output env vars as JSON (secrets masked)")

	cmd.AddCommand(
		newEnvSetCmd(),
		newEnvUnsetCmd(),
		newEnvImportCmd(),
	)

	return cmd
}

func newEnvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [app] KEY=VALUE [KEY=VALUE...]",
		Short: "Set environment variables",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest := resolveEnvApp(args)
			if len(rest) == 0 {
				return fmt.Errorf("provide at least one KEY=VALUE pair")
			}
			return runEnvSet(name, rest)
		},
	}
}

func newEnvUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset [app] KEY [KEY...]",
		Short: "Remove environment variables",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest := resolveEnvApp(args)
			if len(rest) == 0 {
				return fmt.Errorf("provide at least one KEY name")
			}
			return runEnvUnset(name, rest)
		},
	}
}

func newEnvImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import [app] <file>",
		Short: "Import environment variables from a .env file",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				return runEnvImport(args[0], args[1])
			}
			name, _ := resolveEnvApp([]string{})
			return runEnvImport(name, args[0])
		},
	}
}

// runEnvList shows all env vars for an app.
func runEnvList(appName string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if len(app.Env) == 0 {
		fmt.Println()
		fmt.Printf("  %s has no environment variables set.\n", ui.Bold.Render(appName))
		fmt.Println()
		ui.Info("Set variables: neo env set " + appName + " KEY=VALUE")
		ui.Info("Import .env:   neo env import " + appName + " .env")
		fmt.Println()
		return nil
	}

	// Sort keys for stable output
	keys := make([]string, 0, len(app.Env))
	for k := range app.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Println()
	fmt.Printf("  %s — %d environment variables\n", ui.Bold.Render(appName), len(app.Env))
	fmt.Println("  " + ui.Faint.Render(strings.Repeat("─", 50)))

	for _, k := range keys {
		v := app.Env[k]
		displayed := v
		if looksLikeSecret(k) && len(v) > 4 {
			displayed = v[:2] + strings.Repeat("*", len(v)-2)
		}
		fmt.Printf("  %-30s %s\n", ui.Green.Render(k), displayed)
	}
	fmt.Println()
	return nil
}

// runEnvListJSON outputs env vars for an app as JSON, with secrets masked.
func runEnvListJSON(appName string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	// Sort keys for stable output and mask secrets
	masked := make(map[string]string, len(app.Env))
	for k, v := range app.Env {
		if looksLikeSecret(k) && len(v) > 4 {
			masked[k] = v[:2] + strings.Repeat("*", len(v)-2)
		} else {
			masked[k] = v
		}
	}

	out := struct {
		App  string            `json:"app"`
		Vars map[string]string `json:"vars"`
	}{
		App:  appName,
		Vars: masked,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// runEnvSet sets one or more env vars and restarts the app.
func runEnvSet(appName string, pairs []string) error {
	newVars, err := parseEnvPairs(pairs)
	if err != nil {
		return err
	}

	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if app.Env == nil {
		app.Env = make(map[string]string)
	}

	changed := 0
	for k, v := range newVars {
		if app.Env[k] != v {
			changed++
		}
		app.Env[k] = v
	}

	if changed == 0 {
		ui.Info("No changes — all values already match")
		return nil
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	for k := range newVars {
		ui.Success(fmt.Sprintf("Set %s", k))
	}

	return restartWithNewEnv(appName, app, exec)
}

// runEnvUnset removes env vars and restarts the app.
func runEnvUnset(appName string, keys []string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	removed := 0
	for _, k := range keys {
		if _, exists := app.Env[k]; exists {
			delete(app.Env, k)
			removed++
			ui.Success(fmt.Sprintf("Removed %s", k))
		} else {
			ui.Info(fmt.Sprintf("%s was not set", k))
		}
	}

	if removed == 0 {
		return nil
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	return restartWithNewEnv(appName, app, exec)
}

// runEnvImport imports vars from a .env file and restarts.
func runEnvImport(appName, filePath string) error {
	fileEnv, err := parseEnvFile(filePath)
	if err != nil {
		return err
	}

	if len(fileEnv) == 0 {
		ui.Info("No variables found in " + filePath)
		return nil
	}

	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if app.Env == nil {
		app.Env = make(map[string]string)
	}

	added, updated := 0, 0
	for k, v := range fileEnv {
		if old, exists := app.Env[k]; exists {
			if old != v {
				updated++
			}
		} else {
			added++
		}
		app.Env[k] = v
	}

	if added == 0 && updated == 0 {
		ui.Info("No changes — all values already match")
		return nil
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("Imported %d variables from %s (%d new, %d updated)", len(fileEnv), filePath, added, updated))

	return restartWithNewEnv(appName, app, exec)
}

// restartWithNewEnv recreates the app container with updated env vars.
func restartWithNewEnv(appName string, app state.App, exec *neossh.Executor) error {
	fmt.Println()
	spin := ui.NewSpinner("Restarting " + appName + " with new config...")
	spin.Start()

	containerName := config.AppContainer(appName)

	// Stop and remove old container
	exec.Run(fmt.Sprintf("docker stop %s 2>/dev/null; docker rm %s 2>/dev/null", containerName, containerName))

	// Build docker run command with all env vars
	var envArgs []string
	for k, v := range app.Env {
		envArgs = append(envArgs, fmt.Sprintf("-e %s", neossh.ShellQuote(fmt.Sprintf("%s=%s", k, v))))
	}

	// Build volume args
	var volArgs []string
	for name, vol := range app.Volumes {
		src := name
		if vol.Mount != nil {
			src = *vol.Mount
		}
		volArgs = append(volArgs, fmt.Sprintf("-v %s:%s", src, vol.ContainerPath))
	}

	restart := "unless-stopped"
	if app.Restart != "" {
		restart = app.Restart
	}

	cmd := fmt.Sprintf("docker run -d --name %s --network %s --restart %s %s %s",
		containerName,
		config.DockerNetwork,
		restart,
		strings.Join(envArgs, " "),
		strings.Join(volArgs, " "),
	)

	// Add health check flags if configured
	if app.Health != nil && app.Health.Cmd != "" {
		cmd += fmt.Sprintf(" --health-cmd %s", neossh.ShellQuote(app.Health.Cmd))
		if app.Health.Interval != "" {
			cmd += fmt.Sprintf(" --health-interval %s", app.Health.Interval)
		}
		if app.Health.Timeout != "" {
			cmd += fmt.Sprintf(" --health-timeout %s", app.Health.Timeout)
		}
		if app.Health.Retries > 0 {
			cmd += fmt.Sprintf(" --health-retries %d", app.Health.Retries)
		}
		if app.Health.StartPeriod != "" {
			cmd += fmt.Sprintf(" --health-start-period %s", app.Health.StartPeriod)
		}
	}

	cmd += " " + app.Image

	_, err := exec.Run(cmd)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("failed to restart %s: %w", appName, err)
	}

	ui.Success(fmt.Sprintf("%s restarted with updated environment", appName))
	return nil
}

// looksLikeSecret checks if an env var key looks like it contains sensitive data.
func looksLikeSecret(key string) bool {
	key = strings.ToUpper(key)
	secrets := []string{"KEY", "SECRET", "PASSWORD", "TOKEN", "PASS", "PRIVATE", "CREDENTIAL"}
	for _, s := range secrets {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}
