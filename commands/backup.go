package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ui"
)

func newBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <app>",
		Short: "Backup an app's data volumes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackup(args[0])
		},
	}
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <app> <backup-file>",
		Short: "Restore an app from a backup file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(args[0], args[1])
		},
	}
}

func runBackup(appName string) error {
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
	timestamp := time.Now().UTC().Format("20060102-150405")
	backupFile := fmt.Sprintf("%s/%s-%s.tar.gz", config.BackupDir, appName, timestamp)

	// Ensure backup directory exists
	exec.RunQuiet(fmt.Sprintf("mkdir -p %s", config.BackupDir))

	// Pause app
	spin := ui.NewSpinner(fmt.Sprintf("Pausing %s...", appName))
	spin.Start()
	docker.Stop(containerName)
	spin.Stop()

	// Build tar command for all volumes
	var volumeArgs string
	for volName, vol := range app.Volumes {
		src := volName
		if vol.Mount != nil {
			src = *vol.Mount
		}
		volumeArgs += fmt.Sprintf(" -v %s:/backup/%s:ro", src, volName)
	}

	// Create backup
	spin = ui.NewSpinner("Creating backup...")
	spin.Start()
	tarCmd := fmt.Sprintf(
		"docker run --rm%s -v %s:/out alpine tar czf /out/%s-%s.tar.gz -C /backup .",
		volumeArgs, config.BackupDir, appName, timestamp,
	)
	err = exec.RunQuiet(tarCmd)
	spin.Stop()

	if err != nil {
		// Restart app even on failure
		docker.Start(containerName)
		return fmt.Errorf("create backup: %w", err)
	}

	// Resume app
	spin = ui.NewSpinner(fmt.Sprintf("Resuming %s...", appName))
	spin.Start()
	docker.Start(containerName)
	spin.Stop()

	// Get backup size
	size, _ := exec.Run(fmt.Sprintf("du -h %s | cut -f1", backupFile))

	ui.Success(fmt.Sprintf("Backup created: %s (%s)", backupFile, size))

	// Offer to download
	var download bool
	huh.NewConfirm().
		Title("Download backup to local machine?").
		Value(&download).
		Run()

	if download {
		ui.Info("Download not yet implemented — copy manually:")
		ui.Info(fmt.Sprintf("  scp %s:%s .", exec.Host, backupFile))
	}

	return nil
}

func runRestore(appName, backupFile string) error {
	if strings.Contains(backupFile, "..") {
		return fmt.Errorf("backup file path must not contain '..'")
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

	var confirm bool
	huh.NewConfirm().
		Title(fmt.Sprintf("Restore %s from %s? This will overwrite current data.", appName, backupFile)).
		Affirmative("Yes, restore").
		Negative("Cancel").
		Value(&confirm).
		Run()
	if !confirm {
		return nil
	}

	docker := remote.NewDocker(exec)
	containerName := config.AppContainer(appName)

	// Stop app
	spin := ui.NewSpinner(fmt.Sprintf("Stopping %s...", appName))
	spin.Start()
	docker.Stop(containerName)
	for svcName := range app.Services {
		docker.Stop(config.SvcContainer(appName, svcName))
	}
	spin.Stop()

	// Build volume mount args for restore
	var volumeArgs string
	for volName, vol := range app.Volumes {
		dst := volName
		if vol.Mount != nil {
			dst = *vol.Mount
		}
		volumeArgs += fmt.Sprintf(" -v %s:/restore/%s", dst, volName)
	}

	// Extract backup
	spin = ui.NewSpinner("Restoring from backup...")
	spin.Start()

	backupPath := backupFile
	if !exec.FileExists(backupFile) {
		remotePath := fmt.Sprintf("/tmp/neo-restore-%s.tar.gz", appName)
		if err := exec.Upload(backupFile, remotePath); err != nil {
			spin.Stop()
			return fmt.Errorf("upload backup: %w", err)
		}
		backupPath = remotePath
	}

	restoreCmd := fmt.Sprintf(
		"docker run --rm -v %s:/backup.tar.gz:ro%s alpine sh -c 'cd /restore && tar xzf /backup.tar.gz --no-same-owner --no-same-permissions 2>/dev/null; true'",
		backupPath, volumeArgs,
	)
	err = exec.RunQuiet(restoreCmd)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}

	// Restart everything
	spin = ui.NewSpinner(fmt.Sprintf("Starting %s...", appName))
	spin.Start()
	for svcName := range app.Services {
		docker.Start(config.SvcContainer(appName, svcName))
	}
	docker.Start(containerName)
	spin.Stop()

	ui.Success(fmt.Sprintf("%s restored successfully", appName))
	return nil
}
