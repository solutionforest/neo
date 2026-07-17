package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ui"
)

type remoteVersion struct {
	Version   string            `json:"version"`
	Released  string            `json:"released"`
	Checksums map[string]string `json:"checksums,omitempty"` // "darwin-arm64" → "sha256:..."
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show current version and check for updates",
		Run: func(cmd *cobra.Command, args []string) {
			runVersion()
		},
	}
}

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade neo to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade()
		},
	}
}

func runVersion() {
	fmt.Printf("  neo %s (%s/%s)\n", cliVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	latest, err := fetchLatestVersion()
	if err != nil {
		fmt.Printf("  %s\n", ui.Faint.Render("Could not check for updates"))
		return
	}

	if latest.Version == cliVersion || cliVersion == "dev" {
		fmt.Printf("  %s You're on the latest version.\n", ui.Green.Render("✓"))
	} else {
		fmt.Printf("  %s Update available: %s → %s\n",
			ui.Yellow.Render("!"),
			ui.Faint.Render(cliVersion),
			ui.Green.Render(latest.Version))
		fmt.Printf("  Run %s to upgrade.\n", ui.Bold.Render("neo upgrade"))
	}
	fmt.Println()
}

func runUpgrade() error {
	fmt.Println()
	fmt.Printf("  neo %s (%s/%s)\n", cliVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	// Check latest version
	spin := ui.NewSpinner("Checking for updates...")
	spin.Start()

	latest, err := fetchLatestVersion()
	if err != nil {
		spin.Stop()
		return fmt.Errorf("could not check for updates: %w", err)
	}
	spin.Stop()

	if cliVersion != "dev" && latest.Version == cliVersion {
		ui.Success(fmt.Sprintf("Already on the latest version (%s)", cliVersion))
		return nil
	}

	fmt.Printf("  Upgrading: %s → %s\n\n",
		ui.Faint.Render(cliVersion),
		ui.Green.Render(latest.Version))

	// Determine download URL
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	// Pin the exact version we just checked so the binary always matches the
	// checksums from version.json (the server's "latest" pointer could move).
	downloadURL := fmt.Sprintf("%s/%s/%s?version=%s", config.DownloadBaseURL(), goos, goarch, latest.Version)

	// Find current binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine binary path: %w", err)
	}

	// Download new binary
	spin = ui.NewSpinner("Downloading latest version...")
	spin.Start()

	tmpFile, err := downloadBinary(downloadURL)
	if err != nil {
		spin.Stop()
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpFile)
	spin.Stop()
	ui.Success("Downloaded")

	// Verify checksum (mandatory — abort if server provides no checksum for this platform)
	platform := fmt.Sprintf("%s-%s", goos, goarch)
	if latest.Checksums == nil {
		return fmt.Errorf("upgrade aborted: server did not provide checksums — cannot verify binary integrity")
	}
	expectedHash, ok := latest.Checksums[platform]
	if !ok {
		return fmt.Errorf("upgrade aborted: no checksum available for %s — cannot verify binary integrity", platform)
	}
	{
		data, readErr := os.ReadFile(tmpFile)
		if readErr != nil {
			return fmt.Errorf("failed to read downloaded binary: %w", readErr)
		}
		hash := sha256.Sum256(data)
		actualHash := "sha256:" + hex.EncodeToString(hash[:])
		if actualHash != expectedHash {
			return fmt.Errorf("checksum verification failed — expected %s, got %s", expectedHash, actualHash)
		}
		ui.Success("Checksum verified")
	}

	// Replace current binary
	spin = ui.NewSpinner("Installing...")
	spin.Start()

	if err := replaceBinary(execPath, tmpFile); err != nil {
		spin.Stop()
		fmt.Printf("  You can install manually:\n")
		fmt.Printf("  curl -fsSL %s | sh\n\n", config.InstallURL())
		return fmt.Errorf("install failed: %w", err)
	}
	spin.Stop()

	ui.Success(fmt.Sprintf("Upgraded to %s", latest.Version))
	fmt.Println()
	return nil
}

func fetchLatestVersion() (*remoteVersion, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(config.VersionURL())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("version check returned %d", resp.StatusCode)
	}

	var v remoteVersion
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

func downloadBinary(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "neo-upgrade-*")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()

	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

func replaceBinary(target, source string) error {
	// Fast path: replace in place. Works when we own the install dir (Homebrew,
	// ~/bin, or running as root).
	err := replaceBinaryDirect(target, source)
	if err == nil {
		return nil
	}
	// A curl-installed neo usually lives in a root-owned dir like
	// /usr/local/bin, so a non-root `neo upgrade` hits permission denied.
	// Retry with sudo instead of failing and forcing a re-install.
	if os.IsPermission(err) && runtime.GOOS != "windows" {
		return replaceBinaryWithSudo(target, source)
	}
	return err
}

// replaceBinaryDirect atomically swaps the binary using only the current user's
// permissions. Returns a raw (unwrapped) error so os.IsPermission can detect
// the elevation case.
func replaceBinaryDirect(target, source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}

	tmpPath := target + ".new"
	if err := os.WriteFile(tmpPath, data, info.Mode()); err != nil {
		return err
	}

	backupPath := target + ".old"
	os.Remove(backupPath) // ignore error
	if err := os.Rename(target, backupPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Rename(backupPath, target) // restore
		return err
	}
	os.Remove(backupPath)
	return nil
}

// replaceBinaryWithSudo installs the new binary into a root-owned location,
// prompting for the sudo password if needed. Uses install(1) (present on macOS
// and Linux) to copy + set mode in one step.
func replaceBinaryWithSudo(target, source string) error {
	fmt.Printf("\n  %s is not writable — installing with sudo (you may be prompted for your password)...\n",
		ui.Bold.Render(target))
	cmd := exec.Command("sudo", "install", "-m", "0755", source, target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo install failed: %w", err)
	}
	return nil
}
