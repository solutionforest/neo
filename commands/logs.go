package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
)

func newLogsCmd() *cobra.Command {
	var (
		tail        int
		follow      bool
		workerFlag  string
		serviceFlag bool
		grepPattern string
		sidecarFlag string
	)

	cmd := &cobra.Command{
		Use:   "logs <app|service>",
		Short: "Stream app or service container logs",
		Long:  "Stream logs from the app container. Use --worker for a worker, --sidecar for a sidecar, or --service to target a shared service.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceFlag {
				return runServiceLogs(args[0], tail, follow)
			}
			return runLogs(args[0], tail, follow, workerFlag, sidecarFlag, grepPattern)
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 100, "number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().StringVarP(&workerFlag, "worker", "w", "", "show logs for a specific worker")
	cmd.Flags().BoolVarP(&serviceFlag, "service", "s", false, "target a shared service instead of an app")
	cmd.Flags().StringVarP(&grepPattern, "grep", "g", "", "filter log lines by pattern (grep)")
	cmd.Flags().StringVarP(&sidecarFlag, "sidecar", "c", "", "show logs for a specific sidecar container")
	return cmd
}

func runLogs(appName string, tail int, follow bool, worker, sidecar, grepPattern string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	docker := remote.NewDocker(exec)

	var containerName string
	switch {
	case sidecar != "":
		containerName = config.SvcContainer(appName, sidecar)
	case worker != "":
		if _, wOk := app.Workers[worker]; !wOk {
			msg := fmt.Sprintf("worker %q not found for app %s", worker, appName)
			if len(app.Workers) > 0 {
				msg += "\n  Available workers:"
				for wName := range app.Workers {
					msg += fmt.Sprintf("\n    - %s", wName)
				}
			}
			return fmt.Errorf("%s", msg)
		}
		containerName = config.WorkerContainer(appName, worker)
	default:
		containerName = config.AppContainer(appName)
	}

	if grepPattern != "" {
		return streamLogsGrep(exec, containerName, tail, follow, grepPattern)
	}

	return docker.Logs(containerName, tail, follow, os.Stdout)
}

// streamLogsGrep streams container logs piped through grep on the server side.
func streamLogsGrep(exec *neossh.Executor, containerName string, tail int, follow bool, pattern string) error {
	followFlag := ""
	if follow {
		followFlag = " -f"
	}
	cmd := fmt.Sprintf(
		"docker logs --tail %d%s %s 2>&1 | grep --color=always %s",
		tail,
		followFlag,
		neossh.ShellQuote(containerName),
		neossh.ShellQuote(pattern),
	)
	return exec.Stream(cmd, os.Stdout)
}
