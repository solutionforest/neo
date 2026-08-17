package commands

import (
	"fmt"
	"os"

	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ui"
)

// runReleaseCommands executes each release command inside a container on the
// server and stops at the first failure.
//
// These run in the NEW container while the old one is still serving traffic, so
// a failed migration aborts the deploy and leaves the previous version up.
// That is the difference from hooks:, which run locally on the operator's
// machine and cannot see the container at all.
func runReleaseCommands(docker *remote.Docker, containerName string, commands HookCommands) error {
	if len(commands) == 0 {
		return nil
	}

	ui.Info(fmt.Sprintf("Running %d release command(s) in %s...", len(commands), containerName))

	for _, cmdStr := range commands {
		fmt.Printf("    $ %s\n", ui.Faint.Render(cmdStr))
		if err := docker.ExecStream(containerName, cmdStr, os.Stdout); err != nil {
			return fmt.Errorf("release command failed: %s: %w", cmdStr, err)
		}
	}

	ui.Success("Release commands completed")
	return nil
}

// resolveRelease returns the release commands for a deploy: an environment's
// list fully replaces the top-level one, matching how hooks: behave.
func resolveRelease(top, env HookCommands) HookCommands {
	if len(env) > 0 {
		return env
	}
	return top
}
