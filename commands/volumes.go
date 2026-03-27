package commands

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newVolumesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volumes",
		Short: "List Docker volumes on the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVolumes()
		},
	}

	cmd.AddCommand(newVolumesMountCmd())
	return cmd
}

func newVolumesMountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mount <volume> <host-path>",
		Short: "Mount a Docker volume to a host path on the server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVolumesMount(args[0], args[1])
		},
	}
}

func runVolumes() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Volumes on %s (%s)\n", ui.Bold.Render(srv.Name), srv.Host)
	fmt.Println("  " + ui.Faint.Render("──────────────────────────────────────────────────────"))

	for appName, app := range st.Apps {
		for volName, vol := range app.Volumes {
			mount := "docker volume"
			if vol.Mount != nil {
				mount = "→ " + *vol.Mount
			}
			fmt.Printf("  %-25s %-15s %s\n", volName, ui.Faint.Render(appName), mount)
		}
	}
	fmt.Println()

	return nil
}

func runVolumesMount(volumeName, hostPath string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	// Find which app owns this volume
	var ownerApp string
	var volInfo state.VolumeInfo
	for appName, app := range st.Apps {
		if v, ok := app.Volumes[volumeName]; ok {
			ownerApp = appName
			volInfo = v
			break
		}
	}

	if ownerApp == "" {
		ui.Error(fmt.Sprintf("Volume %q not found in any app", volumeName))
		return nil
	}

	var confirm bool
	huh.NewConfirm().
		Title(fmt.Sprintf("Mount %s to %s? This will briefly stop %s.", volumeName, hostPath, ownerApp)).
		Affirmative("Yes, proceed").
		Negative("Cancel").
		Value(&confirm).
		Run()
	if !confirm {
		return nil
	}

	docker := remote.NewDocker(exec)
	containerName := config.AppContainer(ownerApp)

	// Stop app
	spin := ui.NewSpinner(fmt.Sprintf("Stopping %s...", ownerApp))
	spin.Start()
	docker.Stop(containerName)
	spin.Stop()

	// Create target directory and copy data
	spin = ui.NewSpinner("Copying data to host path...")
	spin.Start()
	exec.RunQuiet(fmt.Sprintf("mkdir -p %s", hostPath))
	if err := docker.CopyVolume(volumeName, hostPath); err != nil {
		spin.Stop()
		// Restart app even on failure
		docker.Start(containerName)
		return fmt.Errorf("copy volume data: %w", err)
	}
	spin.Stop()
	ui.Success("Data copied")

	// Recreate container with bind mount
	spin = ui.NewSpinner("Recreating container with bind mount...")
	spin.Start()
	docker.Remove(containerName)

	app := st.Apps[ownerApp]
	var volumes []string
	for name, vol := range app.Volumes {
		if name == volumeName {
			volumes = append(volumes, fmt.Sprintf("%s:%s", hostPath, vol.ContainerPath))
		} else if vol.Mount != nil {
			volumes = append(volumes, fmt.Sprintf("%s:%s", *vol.Mount, vol.ContainerPath))
		} else {
			volumes = append(volumes, fmt.Sprintf("%s:%s", name, vol.ContainerPath))
		}
	}

	volOpts := remote.RunOpts{
		Name:    containerName,
		Image:   app.Image,
		Network: config.DockerNetwork,
		Restart: restartPolicy(app.Restart),
		Volumes: volumes,
		Env:     app.Env,
	}
	applyHealth(&volOpts, app.Health)
	_, err = docker.Run(volOpts)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("recreate container: %w", err)
	}

	// Update state
	volInfo.Mount = &hostPath
	app.Volumes[volumeName] = volInfo
	st.Apps[ownerApp] = app
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("%s mounted to %s", volumeName, hostPath))
	return nil
}
