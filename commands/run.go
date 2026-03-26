package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
	cryptossh "golang.org/x/crypto/ssh"
)

func newRunCmd() *cobra.Command {
	var (
		workerFlag      string
		sidecarFlag     string
		interactiveFlag bool
	)

	cmd := &cobra.Command{
		Use:   "run <app> [flags] -- <command> [args...]",
		Short: "Run a one-off command in an app container",
		Long: `Execute a command inside a running app container.

Examples:
  neo run myapp -- php artisan migrate
  neo run myapp -w queue -- php artisan queue:restart
  neo run myapp -i -- bash`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			cmdArgs := findArgsAfterDash()
			if len(cmdArgs) == 0 {
				return fmt.Errorf("no command specified — usage: neo run %s -- <command>", appName)
			}
			return runExec(appName, workerFlag, sidecarFlag, cmdArgs, interactiveFlag)
		},
	}

	cmd.Flags().StringVarP(&workerFlag, "worker", "w", "", "run in a specific worker container")
	cmd.Flags().StringVarP(&sidecarFlag, "sidecar", "c", "", "run in a specific sidecar container")
	cmd.Flags().BoolVarP(&interactiveFlag, "interactive", "i", false, "run interactively with a PTY")
	return cmd
}

// findArgsAfterDash returns all arguments after the "--" separator in os.Args.
func findArgsAfterDash() []string {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			return os.Args[i+1:]
		}
	}
	return nil
}

func runExec(appName, worker, sidecar string, cmdArgs []string, interactive bool) error {
	cfg, srv, sshExec, err := mustResolveAndConnect()
	_ = cfg
	if err != nil {
		return err
	}
	defer sshExec.Close()

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	// Resolve container name
	var containerName string
	switch {
	case worker != "":
		if _, wOk := app.Workers[worker]; !wOk {
			return fmt.Errorf("worker %q not found in app %q", worker, appName)
		}
		containerName = config.WorkerContainer(appName, worker)
	case sidecar != "":
		if _, sOk := app.Sidecars[sidecar]; !sOk {
			return fmt.Errorf("sidecar %q not found in app %q", sidecar, appName)
		}
		containerName = config.SvcContainer(appName, sidecar)
	default:
		containerName = config.AppContainer(appName)
	}

	// Verify container is running
	docker := remote.NewDocker(sshExec)
	if !docker.IsRunning(containerName) {
		return fmt.Errorf("container %s is not running", containerName)
	}

	if interactive {
		return runExecInteractive(srv, containerName, cmdArgs)
	}
	return runExecStream(sshExec, containerName, cmdArgs)
}

// runExecStream runs a non-interactive command and streams output.
func runExecStream(sshExec *ssh.Executor, containerName string, cmdArgs []string) error {
	quoted := make([]string, len(cmdArgs))
	for i, a := range cmdArgs {
		quoted[i] = ssh.ShellQuote(a)
	}

	cmd := fmt.Sprintf("docker exec %s %s",
		ssh.ShellQuote(containerName),
		strings.Join(quoted, " "))

	err := sshExec.Stream(cmd, os.Stdout)
	if err != nil {
		// Propagate the remote exit code
		var exitErr *cryptossh.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitStatus())
		}
		return err
	}
	return nil
}

// runExecInteractive runs an interactive command with PTY via system ssh.
func runExecInteractive(srv *config.Server, containerName string, cmdArgs []string) error {
	ui.ShowCursor()

	// Build remote command with proper quoting
	parts := []string{"docker", "exec", "-it", containerName}
	for _, a := range cmdArgs {
		parts = append(parts, ssh.ShellQuote(a))
	}
	remoteCmd := strings.Join(parts, " ")

	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs, "-t") // force PTY
	sshArgs = append(sshArgs, srv.Host, remoteCmd)

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	c := exec.Command(sshPath, sshArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
