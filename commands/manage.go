package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/operations"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <app>",
		Short: "Start a stopped app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManage(args[0], "start")
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <app>",
		Short: "Stop a running app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManage(args[0], "stop")
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <app>",
		Short: "Restart an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManage(args[0], "restart")
		},
	}
}

func newRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <app>",
		Short: "Remove an app (keeps data volumes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update <app>",
		Short: "Update an app to the latest image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(args[0])
		},
	}
}

func runManage(appName, action string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	if _, ok := st.Apps[appName]; !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	spin := ui.NewSpinner(fmt.Sprintf("%sing %s...", action, appName))
	spin.Start()

	// Reuse the shared lifecycle logic so the CLI and the neo-bridge sidecar run
	// start/stop/restart through ONE implementation (internal/operations). The
	// CLI keeps its own presentation (spinner + success card) and state save.
	_, actionErr := operations.ApplyAppAction(
		context.Background(),
		operations.NewSSHExecutor(exec),
		st,
		appName,
		operations.AppAction(action),
	)

	spin.Stop()

	if actionErr != nil {
		return fmt.Errorf("failed to %s %s: %w", action, appName, actionErr)
	}

	state.Save(exec, st)

	ui.Success(fmt.Sprintf("%s %sed", appName, action))
	return nil
}

func runRemove(appName string, force bool) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if !force {
		var confirm bool
		huh.NewConfirm().
			Title(fmt.Sprintf("Remove %s? Data volumes will be kept.", appName)).
			Affirmative("Yes, remove").
			Negative("Cancel").
			Value(&confirm).
			Run()
		if !confirm {
			return nil
		}
	}

	docker := remote.NewDocker(exec)
	caddy := remote.NewCaddy(exec)

	spin := ui.NewSpinner(fmt.Sprintf("Removing %s...", appName))
	spin.Start()

	// Stop and remove worker containers first
	for wName := range app.Workers {
		docker.Remove(config.WorkerContainer(appName, wName))
	}

	// Stop and remove app container(s)
	containerName := config.AppContainer(appName)
	if app.Scale > 1 {
		for i := 0; i < app.Scale; i++ {
			docker.Remove(config.ReplicaContainer(appName, i))
		}
	} else {
		docker.Remove(containerName)
	}

	// Remove sidecar containers
	for scName := range app.Sidecars {
		docker.Remove(config.SvcContainer(appName, scName))
	}

	// Remove service containers
	for svcName := range app.Services {
		docker.Remove(config.SvcContainer(appName, svcName))
	}

	// Remove Caddy route
	caddy.RemoveRoute(containerName)

	spin.Stop()

	// Update state
	delete(st.Apps, appName)
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("%s removed. Data volumes preserved on server.", appName))
	return nil
}

func runUpdate(appName string) error {
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
	containerName := config.AppContainer(appName)

	// Pull latest image
	spin := ui.NewSpinner(fmt.Sprintf("Pulling latest %s...", app.Image))
	spin.Start()
	if err := docker.Pull(app.Image); err != nil {
		spin.Stop()
		return fmt.Errorf("pull: %w", err)
	}
	spin.Stop()
	ui.Success("Image pulled")

	// Stop workers and sidecars first
	for wName := range app.Workers {
		docker.Stop(config.WorkerContainer(appName, wName))
		docker.Remove(config.WorkerContainer(appName, wName))
	}
	for scName := range app.Sidecars {
		docker.Stop(config.SvcContainer(appName, scName))
		docker.Remove(config.SvcContainer(appName, scName))
	}

	// Stop old container
	spin = ui.NewSpinner("Replacing container...")
	spin.Start()
	docker.Stop(containerName)
	docker.Remove(containerName)

	// Rebuild volumes list
	volumes := volumesFromState(app.Volumes)

	// Start new container with same config
	updateOpts := remote.RunOpts{
		Name:    containerName,
		Image:   app.Image,
		Network: config.DockerNetwork,
		Restart: restartPolicy(app.Restart),
		Volumes: volumes,
		Env:     app.Env,
	}
	applyHealth(&updateOpts, app.Health)
	_, err = docker.Run(updateOpts)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("failed to start updated container: %w", err)
	}

	// Restart workers
	for wName, w := range app.Workers {
		wContainer := config.WorkerContainer(appName, wName)
		_, wErr := docker.Run(remote.RunOpts{
			Name:    wContainer,
			Image:   app.Image,
			Network: config.DockerNetwork,
			Restart: restartPolicy(w.Restart),
			Volumes: volumes,
			Env:     app.Env,
			Cmd:     w.Command,
		})
		if wErr != nil {
			ui.Error(fmt.Sprintf("Failed to restart worker %s: %s", wName, wErr))
		}
	}

	// Restart sidecars (use their own image, not the app image)
	for scName, sc := range app.Sidecars {
		scContainer := config.SvcContainer(appName, scName)
		var scVolumes []string
		for volName, containerPath := range sc.Volumes {
			appVolName := appName + "-" + volName
			scVolumes = append(scVolumes, fmt.Sprintf("%s:%s", appVolName, containerPath))
		}
		scOpts := remote.RunOpts{
			Name:    scContainer,
			Image:   sc.Image,
			Network: config.DockerNetwork,
			Restart: restartPolicy(sc.Restart),
			Volumes: scVolumes,
			Env:     sc.Env,
			Cmd:     sc.Command,
		}
		applyHealth(&scOpts, sc.Health)
		_, scErr := docker.Run(scOpts)
		if scErr != nil {
			ui.Error(fmt.Sprintf("Failed to restart sidecar %s: %s", scName, scErr))
		}
	}

	// Wait briefly for health
	time.Sleep(2 * time.Second)
	if docker.IsRunning(containerName) {
		ui.Success(fmt.Sprintf("%s updated and running", appName))
	} else {
		ui.Error(fmt.Sprintf("%s updated but may not be healthy — check: neo logs %s", appName, appName))
	}

	return nil
}

// runWorkerManage starts, stops, or restarts a single worker container.
func runWorkerManage(appName, workerName, action string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	w, ok := app.Workers[workerName]
	if !ok {
		return fmt.Errorf("worker %q not found on %s", workerName, appName)
	}

	containerName := config.WorkerContainer(appName, workerName)
	docker := remote.NewDocker(exec)

	spin := ui.NewSpinner(fmt.Sprintf("%sing worker %s...", action, workerName))
	spin.Start()

	switch action {
	case "start":
		docker.Start(containerName) //nolint:errcheck
		w.Status = "running"
	case "stop":
		docker.Stop(containerName) //nolint:errcheck
		w.Status = "stopped"
	case "restart":
		docker.Restart(containerName) //nolint:errcheck
		w.Status = "running"
	}

	spin.Stop()
	app.Workers[workerName] = w
	st.Apps[appName] = app
	state.Save(exec, st) //nolint:errcheck

	ui.Success(fmt.Sprintf("Worker %s %sed", workerName, action))
	return nil
}

// runWorkerRedeploy stops, removes, and re-creates a single worker container using the
// current app image and the worker's configured command.
func runWorkerRedeploy(appName, workerName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	w, ok := app.Workers[workerName]
	if !ok {
		return fmt.Errorf("worker %q not found on %s", workerName, appName)
	}

	containerName := config.WorkerContainer(appName, workerName)
	docker := remote.NewDocker(exec)

	// Rebuild volumes list from app config
	volumes := volumesFromState(app.Volumes)

	spin := ui.NewSpinner(fmt.Sprintf("Redeploying worker %s...", workerName))
	spin.Start()

	docker.Stop(containerName)   //nolint:errcheck
	docker.Remove(containerName) //nolint:errcheck

	_, runErr := docker.Run(remote.RunOpts{
		Name:    containerName,
		Image:   app.Image,
		Network: config.DockerNetwork,
		Restart: restartPolicy(w.Restart),
		Volumes: volumes,
		Env:     app.Env,
		Cmd:     w.Command,
	})

	spin.Stop()

	if runErr != nil {
		return fmt.Errorf("failed to start worker %s: %w", workerName, runErr)
	}

	w.Status = "running"
	app.Workers[workerName] = w
	st.Apps[appName] = app
	state.Save(exec, st) //nolint:errcheck

	ui.Success(fmt.Sprintf("Worker %s redeployed and running", workerName))
	return nil
}

// runSidecarManage starts, stops, or restarts a single sidecar container.
func runSidecarManage(appName, sidecarName, action string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	sc, ok := app.Sidecars[sidecarName]
	if !ok {
		return fmt.Errorf("sidecar %q not found on %s", sidecarName, appName)
	}

	containerName := config.SvcContainer(appName, sidecarName)
	docker := remote.NewDocker(exec)

	spin := ui.NewSpinner(fmt.Sprintf("%sing sidecar %s...", action, sidecarName))
	spin.Start()

	switch action {
	case "start":
		docker.Start(containerName) //nolint:errcheck
		sc.Status = "running"
	case "stop":
		docker.Stop(containerName) //nolint:errcheck
		sc.Status = "stopped"
	case "restart":
		docker.Restart(containerName) //nolint:errcheck
		sc.Status = "running"
	}

	spin.Stop()
	app.Sidecars[sidecarName] = sc
	st.Apps[appName] = app
	state.Save(exec, st) //nolint:errcheck

	ui.Success(fmt.Sprintf("Sidecar %s %sed", sidecarName, action))
	return nil
}

// runSidecarRedeploy stops, removes, and re-creates a single sidecar container using
// the sidecar's own image and configured command.
func runSidecarRedeploy(appName, sidecarName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	sc, ok := app.Sidecars[sidecarName]
	if !ok {
		return fmt.Errorf("sidecar %q not found on %s", sidecarName, appName)
	}

	containerName := config.SvcContainer(appName, sidecarName)
	docker := remote.NewDocker(exec)

	var scVolumes []string
	for volName, containerPath := range sc.Volumes {
		appVolName := appName + "-" + volName
		scVolumes = append(scVolumes, fmt.Sprintf("%s:%s", appVolName, containerPath))
	}

	spin := ui.NewSpinner(fmt.Sprintf("Redeploying sidecar %s...", sidecarName))
	spin.Start()

	docker.Stop(containerName)   //nolint:errcheck
	docker.Remove(containerName) //nolint:errcheck

	scRedeployOpts := remote.RunOpts{
		Name:    containerName,
		Image:   sc.Image,
		Network: config.DockerNetwork,
		Restart: restartPolicy(sc.Restart),
		Volumes: scVolumes,
		Env:     sc.Env,
		Cmd:     sc.Command,
	}
	applyHealth(&scRedeployOpts, sc.Health)
	_, runErr := docker.Run(scRedeployOpts)

	spin.Stop()

	if runErr != nil {
		return fmt.Errorf("failed to start sidecar %s: %w", sidecarName, runErr)
	}

	sc.Status = "running"
	app.Sidecars[sidecarName] = sc
	st.Apps[appName] = app
	state.Save(exec, st) //nolint:errcheck

	ui.Success(fmt.Sprintf("Sidecar %s redeployed and running", sidecarName))
	return nil
}
