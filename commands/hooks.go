package commands

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/vxero/neo/internal/ui"
)

// runHook executes a list of hook commands locally, aborting on the first failure.
// Commands run via "sh -c" for shell expansion support.
// Returns nil if commands is nil or empty.
func runHook(hookName string, commands HookCommands, projectDir string, hookEnv map[string]string) error {
	if len(commands) == 0 {
		return nil
	}

	ui.Info(fmt.Sprintf("Running %s hook...", hookName))

	for _, cmdStr := range commands {
		fmt.Printf("    $ %s\n", ui.Faint.Render(cmdStr))
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = buildHookEnv(hookEnv)

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s hook failed: %q: %w", hookName, cmdStr, err)
		}
	}
	return nil
}

// buildHookEnv returns the current process environment augmented with NEO_* context variables.
func buildHookEnv(hookVars map[string]string) []string {
	env := os.Environ()
	for k, v := range hookVars {
		env = append(env, k+"="+v)
	}
	return env
}

// resolveHooks returns the effective hooks for a deploy, applying environment override.
// If the environment defines hooks, they fully replace the top-level hooks.
func resolveHooks(topLevel, envLevel *NeoHooks) *NeoHooks {
	if envLevel != nil {
		return envLevel
	}
	return topLevel
}

// hookEnvVars builds the map of NEO_* environment variables passed to hook scripts.
func hookEnvVars(appName, envName, domain, server string) map[string]string {
	return map[string]string{
		"NEO_APP":    appName,
		"NEO_ENV":    envName,
		"NEO_DOMAIN": domain,
		"NEO_SERVER": server,
	}
}
