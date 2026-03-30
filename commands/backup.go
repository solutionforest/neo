package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	plan := license.CurrentPlan(cfg.LicenseKey)
	if !license.Allowed(license.FeatureBackup, plan, 0) {
		fmt.Println()
		ui.Error("Backups require a Neo+ license")
		fmt.Println()
		fmt.Printf("  Upgrade to %s to unlock backups:\n", ui.Bold.Render("Neo+"))
		fmt.Printf("    %s\n", ui.Cyan.Render("https://neo.vxero.dev/"))
		fmt.Printf("  Or activate a license: %s\n", ui.Faint.Render("neo plus activate <key>"))
		fmt.Println()
		return fmt.Errorf("backups require Neo+")
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
		volumeArgs += fmt.Sprintf(" -v %s:%s:ro", neossh.ShellQuote(src), neossh.ShellQuote("/backup/"+volName))
	}

	// Create backup
	spin = ui.NewSpinner("Creating backup...")
	spin.Start()
	tarCmd := fmt.Sprintf(
		"docker run --rm%s -v %s:/out alpine tar czf /out/%s-%s.tar.gz -C /backup .",
		volumeArgs, neossh.ShellQuote(config.BackupDir), sanitizeName(appName), timestamp,
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
	size, _ := exec.Run(fmt.Sprintf("du -h %s | cut -f1", neossh.ShellQuote(backupFile)))

	ui.Success(fmt.Sprintf("Backup created: %s (%s)", backupFile, size))

	// Offer to download
	var download bool
	huh.NewConfirm().
		Title("Download backup to local machine?").
		Value(&download).
		Run() //nolint:errcheck

	if !download {
		return nil
	}

	localFile := filepath.Base(backupFile)
	f, err := os.Create(localFile)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	defer f.Close()

	spin = ui.NewSpinner(fmt.Sprintf("Downloading %s...", localFile))
	spin.Start()
	err = exec.Stream(fmt.Sprintf("cat %s", neossh.ShellQuote(backupFile)), f)
	spin.Stop()

	if err != nil {
		os.Remove(localFile)
		return fmt.Errorf("download backup: %w", err)
	}

	ui.Success(fmt.Sprintf("Downloaded %s", localFile))
	return nil
}

func runRestore(appName, backupFile string) error {
	cleaned := filepath.Clean(backupFile)
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("backup file path must not contain '..'")
	}
	// Reject paths with shell metacharacters
	for _, c := range backupFile {
		if c == ';' || c == '|' || c == '&' || c == '$' || c == '`' || c == '(' || c == ')' || c == '\'' || c == '"' || c == '\n' {
			return fmt.Errorf("backup file path contains invalid characters")
		}
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
		volumeArgs += fmt.Sprintf(" -v %s:%s", neossh.ShellQuote(dst), neossh.ShellQuote("/restore/"+volName))
	}

	// Extract backup
	spin = ui.NewSpinner("Restoring from backup...")
	spin.Start()

	backupPath := backupFile
	if !exec.FileExists(backupFile) {
		remotePath := fmt.Sprintf("/tmp/neo-restore-%s.tar.gz", sanitizeName(appName))
		if err := exec.Upload(backupFile, remotePath); err != nil {
			spin.Stop()
			return fmt.Errorf("upload backup: %w", err)
		}
		backupPath = remotePath
	}

	restoreCmd := fmt.Sprintf(
		"docker run --rm -v %s:/backup.tar.gz:ro%s alpine sh -c 'cd /restore && tar xzf /backup.tar.gz --no-same-owner --no-same-permissions 2>/dev/null; true'",
		neossh.ShellQuote(backupPath), volumeArgs,
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
