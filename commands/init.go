package commands

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newInitCmd() *cobra.Command {
	var name, key string

	cmd := &cobra.Command{
		Use:   "init <user@host>",
		Short: "Initialize a remote server for neo",
		Long:  "Connects via SSH, installs Docker, sets up Caddy reverse proxy, and creates server state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInitWithKey(args[0], name, key)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "server name (default: derived from host)")
	cmd.Flags().StringVarP(&key, "key", "i", "", "path to SSH private key file")
	return cmd
}

func runInit(host, name string) error {
	ui.PrintBanner(cliVersion)
	fmt.Println("  Initializing server...")
	fmt.Println()

	if name == "" {
		name = deriveServerName(host)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Servers[name]; exists {
		var overwrite bool
		huh.NewConfirm().
			Title(fmt.Sprintf("Server %q already exists. Overwrite?", name)).
			Value(&overwrite).
			Run()
		if !overwrite {
			return nil
		}
	}

	// Ask for password if no SSH key auth available
	exec := ssh.New(host, 22)
	if !ssh.HasKeyAuth() {
		var password string
		err := huh.NewInput().
			Title("SSH password").
			EchoMode(huh.EchoModePassword).
			Value(&password).
			Run()
		if err != nil || password == "" {
			return fmt.Errorf("no SSH keys found and no password provided")
		}
		exec.Password = password
	}

	// Connect via SSH — no spinner here because host key verification may need user input
	fmt.Print("  Connecting via SSH...\n")
	if err := connectWithBootRetry(exec); err != nil {
		errStr := err.Error()

		// Network-level failure (refused/timeout/unreachable) — not an auth problem.
		// Skip the password prompt and --key hint; they cannot fix a TCP failure.
		if isNetworkError(errStr) {
			return networkUnreachableError(host, 22, err)
		}

		// Host key mismatch (server rebuilt, IP reused) — show actionable fix
		if strings.Contains(errStr, "HOST KEY HAS CHANGED") {
			ip := extractIP(host)
			if ip == "" {
				ip = host
			}
			fmt.Println()
			return fmt.Errorf("%w\n\n  Run the fix above, then try neo init again", err)
		}

		// User rejected the host fingerprint prompt — just stop
		if strings.Contains(errStr, "connection aborted") {
			return fmt.Errorf("SSH connection failed: %w", err)
		}

		// Auth failure — offer password retry, then hint about --key flag
		if ssh.HasKeyAuth() {
			fmt.Printf("\n  %s\n\n", err)
			var password string
			pErr := huh.NewInput().
				Title("SSH key auth failed — enter password").
				EchoMode(huh.EchoModePassword).
				Value(&password).
				Run()
			if pErr == nil && password != "" {
				exec.Password = password
				spin := ui.NewSpinner("Retrying with password...")
				spin.Start()
				if retryErr := exec.Connect(); retryErr != nil {
					spin.Stop()
					fmt.Printf("\n  Tip: if your cloud key is at a non-standard path, use:\n")
					fmt.Printf("       neo init --key ~/.ssh/your_key %s\n\n", host)
					return fmt.Errorf("SSH connection failed: %w", retryErr)
				}
				spin.Stop()
			} else {
				fmt.Printf("\n  Tip: if your cloud key is at a non-standard path, use:\n")
				fmt.Printf("       neo init --key ~/.ssh/your_key %s\n\n", host)
				return fmt.Errorf("SSH connection failed: %w", err)
			}
		} else {
			// No key auth at all — only password was tried
			fmt.Printf("\n  Tip: specify your SSH key with:\n")
			fmt.Printf("       neo init --key ~/.ssh/your_key %s\n\n", host)
			return fmt.Errorf("SSH connection failed: %w", err)
		}
	}
	defer exec.Close()

	// Deploy neo's managed SSH key immediately, before the heavier setup steps.
	// If init later fails partway (e.g. a package install error), the key is
	// already on the server, so re-running `neo init` uses key auth instead of
	// asking for the password again — init becomes resumable.
	deployNeoKey(exec)

	return setupServer(exec, cfg, name, host, "")
}

// runInitWithKey is like runInit but uses a specific SSH key file (no password prompt).
// If keyPath is empty it falls back to runInit.
func runInitWithKey(host, name, keyPath string) error {
	if keyPath == "" {
		return runInit(host, name)
	}

	ui.PrintBanner(cliVersion)
	fmt.Println("  Initializing server...")
	fmt.Println()

	if name == "" {
		name = deriveServerName(host)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Servers[name]; exists {
		var overwrite bool
		huh.NewConfirm().
			Title(fmt.Sprintf("Server %q already exists. Overwrite?", name)).
			Value(&overwrite).
			Run()
		if !overwrite {
			return nil
		}
	}

	// Load key from disk
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("cannot read SSH key %s: %w", keyPath, err)
	}

	exec := ssh.New(host, 22)
	exec.PrivateKey = keyData

	fmt.Print("  Connecting via SSH...\n")
	if err := connectWithBootRetry(exec); err != nil {
		errStr := err.Error()
		if isNetworkError(errStr) {
			return networkUnreachableError(host, 22, err)
		}
		if strings.Contains(errStr, "HOST KEY HAS CHANGED") {
			ip := extractIP(host)
			if ip == "" {
				ip = host
			}
			fmt.Println()
			return fmt.Errorf("%w\n\n  Run the fix above, then try neo init again", err)
		}
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer exec.Close()

	return setupServer(exec, cfg, name, host, keyPath)
}

// isNetworkError reports whether errStr is a network-level SSH failure (the TCP
// connection could not be established) rather than an auth or host-key problem.
// These fail before authentication, so a password prompt or --key hint is useless.
func isNetworkError(errStr string) bool {
	for _, p := range []string{
		"connection refused",
		"i/o timeout",
		"connection timed out",
		"no route to host",
		"network is unreachable",
		"no such host",
	} {
		if strings.Contains(errStr, p) {
			return true
		}
	}
	return false
}

// isBootingError is the subset of network errors that retrying may resolve —
// typically a freshly provisioned cloud server whose sshd is not up yet. DNS
// failures ("no such host") are excluded because retrying rarely helps.
func isBootingError(errStr string) bool {
	for _, p := range []string{
		"connection refused",
		"i/o timeout",
		"connection timed out",
		"no route to host",
		"network is unreachable",
	} {
		if strings.Contains(errStr, p) {
			return true
		}
	}
	return false
}

// connectWithBootRetry attempts the SSH connection, retrying for up to ~75s when
// the failure looks like a still-booting server (connection refused, etc.). The
// first attempt and every retry run without a spinner active so host-key prompts
// can read stdin cleanly. Non-network errors (auth, host-key mismatch) return
// immediately for the caller's specialized handling.
func connectWithBootRetry(exec *ssh.Executor) error {
	err := exec.Connect()
	if err == nil || !isBootingError(err.Error()) {
		return err
	}

	fmt.Println("  Server refused the connection — it may still be booting. Retrying for up to 75s...")
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		spin := ui.NewSpinner("Waiting for server SSH...")
		spin.Start()
		time.Sleep(5 * time.Second)
		spin.Stop()

		err = exec.Connect()
		if err == nil || !isBootingError(err.Error()) {
			return err
		}
	}
	return err
}

// networkUnreachableError builds an actionable message for a network-level SSH
// failure — pointing at the real causes (boot, firewall, wrong host) instead of
// misleading the user toward SSH keys or passwords.
func networkUnreachableError(host string, port int, err error) error {
	target := extractIP(host)
	if target == "" {
		target = host
	}
	return fmt.Errorf("cannot reach %s:%d over SSH: %w\n\n"+
		"  This is a network error, not an auth problem. Check:\n"+
		"    - the server is running and finished booting\n"+
		"    - port %d is open (no firewall / cloud security-group block)\n"+
		"    - the host/IP is correct\n\n"+
		"  Verify from your machine:  nc -vz %s %d",
		target, port, err, port, target, port)
}

// setupServer performs the shared server initialization after SSH is connected.
func setupServer(exec *ssh.Executor, cfg *config.Config, name, host, keyPath string) error {
	// Detect OS
	osInfo, _ := exec.Run("cat /etc/os-release | grep PRETTY_NAME | cut -d'\"' -f2")
	osID, _ := exec.Run("cat /etc/os-release | grep '^ID=' | cut -d'=' -f2")
	osVersionID, _ := exec.Run("cat /etc/os-release | grep '^VERSION_ID=' | cut -d'=' -f2 | tr -d '\"'")
	memInfo, _ := exec.Run("free -h | awk '/Mem:/{print $2}'")
	totalRAMMBStr, _ := exec.Run("free -m | awk '/Mem:/{print $2}'")
	totalRAMMB, _ := strconv.Atoi(strings.TrimSpace(totalRAMMBStr))
	cpuInfo, _ := exec.Run("nproc")

	// Validate OS — Ubuntu 24.04+, Debian, Fedora 39+, CentOS/RHEL/Alma/Rocky 9+ are supported
	if err := validateOS(osID, osVersionID, osInfo); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Connected (%s, %s RAM, %s CPU)", osInfo, memInfo, cpuInfo))

	// Detect server IP
	serverIP := extractIP(host)
	if serverIP == "" {
		serverIP, _ = exec.Run("curl -sf https://ifconfig.me")
	}

	// System update — use the right package manager for the OS
	spin := ui.NewSpinner("Updating system packages...")
	spin.Start()
	pkgMgr := detectPackageManager(osID)
	switch pkgMgr {
	case "dnf":
		exec.RunQuiet("dnf upgrade -y -q")
	case "yum":
		exec.RunQuiet("yum upgrade -y -q")
	default:
		// Fresh cloud VMs run cloud-init / unattended-upgrades on first boot,
		// which holds the dpkg lock. Make every apt call wait for it (up to 5 min)
		// instead of failing with "Could not get lock /var/lib/dpkg/lock-frontend".
		// Written to apt.conf.d so it also covers the Docker install script's own
		// apt-get calls (curl get.docker.com | sh), which is where init failed.
		exec.RunQuiet(`echo 'DPkg::Lock::Timeout "300";' > /etc/apt/apt.conf.d/99neo-lock-timeout`)
		exec.RunQuiet("DEBIAN_FRONTEND=noninteractive apt-get update -qq")
		exec.RunQuiet("DEBIAN_FRONTEND=noninteractive apt-get upgrade -y -qq")
	}
	spin.Stop()
	ui.Success("System packages updated")

	// Add swap if none exists — size based on total RAM:
	//   ≤ 2 GB RAM  → 2 GB swap
	//   2–8 GB RAM  → 1× RAM (rounded to nearest GB)
	//   > 8 GB RAM  → skip (sufficient RAM, swap not worth the disk cost)
	if swapInfo, _ := exec.Run("swapon --show --noheadings"); swapInfo == "" {
		if swapGB := swapSize(totalRAMMB); swapGB > 0 {
			spin = ui.NewSpinner(fmt.Sprintf("Configuring swap (%d GB)...", swapGB))
			spin.Start()
			exec.RunQuiet(fmt.Sprintf("fallocate -l %dG /swapfile", swapGB))
			exec.RunQuiet("chmod 600 /swapfile")
			exec.RunQuiet("mkswap /swapfile")
			exec.RunQuiet("swapon /swapfile")
			exec.RunQuiet("echo '/swapfile none swap sw 0 0' >> /etc/fstab")
			exec.RunQuiet("sysctl -w vm.swappiness=10")
			exec.RunQuiet("echo 'vm.swappiness=10' >> /etc/sysctl.conf")
			spin.Stop()
			ui.Success(fmt.Sprintf("Swap enabled (%d GB, swappiness=10)", swapGB))
		}
	}

	// Install Docker if needed (or warn about existing containers)
	docker := remote.NewDocker(exec)
	if !docker.IsInstalled() {
		spin = ui.NewSpinner("Installing Docker...")
		spin.Start()
		if err := docker.Install(); err != nil {
			spin.Stop()
			return fmt.Errorf("install docker: %w", err)
		}
		spin.Stop()
	} else {
		// Docker already present — check for running containers
		if running := docker.RunningContainers(); len(running) > 0 {
			fmt.Println()
			fmt.Println("  " + ui.Yellow.Render("⚠  Docker is already installed with running services:"))
			for _, name := range running {
				fmt.Println("     " + ui.Faint.Render("•") + " " + name)
			}
			fmt.Println()
			var confirm bool
			huh.NewConfirm().
				Title("Stop these services and set up neo?").
				Value(&confirm).
				Run() //nolint:errcheck
			if !confirm {
				ui.Info("Cancelled. Run neo init again when ready.")
				return nil
			}
			spin = ui.NewSpinner("Stopping existing containers...")
			spin.Start()
			docker.StopAll(running)
			spin.Stop()
			ui.Success("Existing containers stopped")
		}
	}
	dockerVer, _ := docker.Version()
	ui.Success(fmt.Sprintf("Docker %s ready", dockerVer))

	// Create Docker network
	if err := docker.CreateNetwork(config.DockerNetwork); err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	ui.Success(fmt.Sprintf("Docker network %q created", config.DockerNetwork))

	// Start Caddy
	caddy := remote.NewCaddy(exec)
	if !caddy.IsRunning() {
		// Check for port conflicts before trying to start
		if conflict := caddy.CheckPortConflict(); conflict != "" {
			return fmt.Errorf("port conflict: %s — please free ports 80 and 443, then run neo init again", conflict)
		}

		spin = ui.NewSpinner("Starting Caddy reverse proxy...")
		spin.Start()
		var caddyErr error
		if caddy.Exists() {
			caddyErr = caddy.Start()
		} else {
			caddyErr = caddy.StartContainer()
		}
		spin.Stop()
		if caddyErr != nil {
			return fmt.Errorf("start caddy: %w", caddyErr)
		}
	}
	caddyVer, _ := caddy.Version()
	ui.Success(fmt.Sprintf("Caddy %s running (ports 80, 443)", caddyVer))

	// Add welcome page for direct IP access. Non-fatal, but surface a failure:
	// a silently-swallowed error here used to hide a half-configured Caddy
	// (no apps/http server), which then broke the first real deploy.
	if serverIP != "" {
		if err := caddy.AddWelcomePage(serverIP); err != nil {
			ui.Error(fmt.Sprintf("Welcome page not added: %s", err))
			ui.Info("Not fatal — deploys still work. Re-add later by toggling: neo stealth (twice).")
		}
	}

	// Check port accessibility from outside
	checkPortAccess(serverIP)

	// Initialize remote state
	if err := state.Init(exec, serverIP); err != nil {
		return fmt.Errorf("init state: %w", err)
	}
	ui.Success("State initialized")

	// Save local config
	srv := config.Server{
		Name:          name,
		Host:          host,
		Port:          22,
		InitializedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if keyPath != "" {
		srv.Key = keyPath
	}
	cfg.AddServer(srv)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Success card
	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Server ready!")
	card.Blank()
	card.AddKV("Name", name)
	card.AddKV("Host", host)
	if keyPath != "" {
		card.AddKV("Key", keyPath)
	}
	card.AddKV("Docker", dockerVer)
	card.AddKV("Caddy", caddyVer+" (auto-SSL)")
	card.Blank()
	card.Add("Deploy your first app:")
	card.Add("  neo deploy")
	card.Render()

	promptStar()
	return nil
}

// promptStar invites the user to star the repo on GitHub after a successful
// init. Best-effort — with no TTY it just prints the link.
func promptStar() {
	const repoURL = "https://github.com/solutionforest/neo"
	fmt.Println()
	fmt.Printf("  %s Enjoying neo? A GitHub star helps a lot.\n", ui.Yellow.Render("★"))

	var open bool
	if err := huh.NewConfirm().
		Title("Star neo on GitHub?").
		Description("Opens " + repoURL + " in your browser.").
		Affirmative("Star ★").
		Negative("Maybe later").
		Value(&open).
		Run(); err != nil {
		fmt.Printf("  %s\n\n", ui.Cyan.Render(repoURL))
		return
	}
	if open {
		openBrowser(repoURL)
	} else {
		fmt.Printf("  %s\n\n", ui.Cyan.Render(repoURL))
	}
}

// deriveServerName extracts a short name from the host string.
func deriveServerName(host string) string {
	parts := strings.SplitN(host, "@", 2)
	h := host
	if len(parts) == 2 {
		h = parts[1]
	}

	if strings.Contains(h, ".") && !isIP(h) {
		return strings.SplitN(h, ".", 2)[0]
	}

	// For bare IP addresses, combine a random word with the last octet
	if isIP(h) {
		parts := strings.Split(h, ".")
		words := []string{
			"amber", "arctic", "atlas", "aurora", "azure",
			"blaze", "bold", "breeze", "bright", "brisk",
			"calm", "cedar", "cinder", "cipher", "cobalt",
			"coral", "cosmic", "crimson", "crystal", "cyber",
			"delta", "dune", "dusk", "dynamic", "eager",
			"echo", "ember", "epic", "falcon", "fiery",
			"flint", "flux", "frosty", "gale", "ghost",
			"glacial", "glitch", "glow", "golden", "granite",
			"haze", "hazy", "hollow", "indigo", "inferno",
			"iris", "iron", "ivory", "jade", "jolly",
			"keen", "kindle", "lunar", "lush", "lyric",
			"maple", "marble", "marine", "mellow", "metro",
			"mint", "misty", "mystic", "nebula", "nimble",
			"noble", "north", "nova", "oak", "obsidian",
			"ocean", "olive", "omega", "onyx", "opal",
			"orbit", "peak", "pine", "pixel", "plasma",
			"polar", "prism", "proud", "pulse", "quartz",
			"quick", "quill", "radiant", "rapid", "raven",
			"rebel", "ridge", "ripple", "rosy", "royal",
			"rustic", "sage", "salty", "sapphire", "serene",
			"shadow", "silver", "slate", "sleek", "solar",
			"sonic", "spark", "stellar", "storm", "summit",
			"swift", "teal", "terra", "titan", "topaz",
			"turbo", "twilight", "ultra", "urban", "vale",
			"velvet", "venture", "verdant", "vivid", "void",
			"volt", "vortex", "warm", "wave", "wild",
			"winter", "wolf", "xenon", "yonder", "zenith",
			"zephyr", "zero", "zesty",
		}
		return words[rand.Intn(len(words))] + "-" + parts[len(parts)-1]
	}

	return "production"
}

// extractIP extracts an IP from "user@ip" format.
func extractIP(host string) string {
	parts := strings.SplitN(host, "@", 2)
	h := host
	if len(parts) == 2 {
		h = parts[1]
	}
	if isIP(h) {
		return h
	}
	return ""
}

// supportedOSMsg is the standard message listing supported operating systems.
const supportedOSMsg = "Neo supports Ubuntu 24.04+, Debian, Fedora 39+, CentOS/RHEL/Alma/Rocky 9+."

// validateOS checks that the server runs a supported OS.
func validateOS(osID, versionID, prettyName string) error {
	osID = strings.TrimSpace(strings.ToLower(osID))
	versionID = strings.TrimSpace(versionID)

	switch osID {
	case "debian":
		return nil
	case "ubuntu":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err != nil {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Detected Ubuntu but could not parse version %q.", prettyName, supportedOSMsg, versionID)
		}
		if ver < 24.04 {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Your Ubuntu version (%s) is too old — please upgrade to 24.04 or later.", prettyName, supportedOSMsg, versionID)
		}
		return nil
	case "fedora":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err != nil {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Detected Fedora but could not parse version %q.", prettyName, supportedOSMsg, versionID)
		}
		if ver < 39 {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Your Fedora version (%s) is too old — please upgrade to 39 or later.", prettyName, supportedOSMsg, versionID)
		}
		return nil
	case "centos", "rhel", "almalinux", "rocky":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err != nil {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Could not parse version %q.", prettyName, supportedOSMsg, versionID)
		}
		if ver < 9 {
			return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Version %s is too old — please upgrade to 9 or later.", prettyName, supportedOSMsg, versionID)
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS: %s\n\n  %s\n  Please reinstall your server with a supported OS.", prettyName, supportedOSMsg)
	}
}

// detectPackageManager returns the package manager for the given OS ID.
func detectPackageManager(osID string) string {
	osID = strings.TrimSpace(strings.ToLower(osID))
	switch osID {
	case "fedora":
		return "dnf"
	case "centos", "rhel", "almalinux", "rocky":
		return "dnf" // CentOS 9+ / RHEL 9+ all use dnf
	default:
		return "apt"
	}
}

// isIP returns true if the string looks like an IP address.
func isIP(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// swapSize returns the recommended swap size in GB for a server with the given
// total RAM in MB. Returns 0 when swap is not recommended (>8 GB RAM).
//
//   ≤ 2 048 MB  → 2 GB  (OOM protection on tiny VMs)
//   2 049–8 192 MB → 1× RAM rounded to nearest GB
//   > 8 192 MB  → 0 (skip — sufficient RAM)
func swapSize(ramMB int) int {
	if ramMB <= 0 {
		return 2 // safe default if detection fails
	}
	if ramMB <= 2048 {
		return 2
	}
	if ramMB <= 8192 {
		return (ramMB + 512) / 1024 // round to nearest GB
	}
	return 0
}

// checkPortAccess verifies port 80 and 443 are reachable from outside.
func checkPortAccess(serverIP string) {
	if serverIP == "" {
		return
	}

	fmt.Println()
	fmt.Println("  " + ui.Bold.Render("Firewall check:"))

	ports := []struct {
		port int
		name string
	}{
		{80, "HTTP (80)"},
		{443, "HTTPS (443)"},
	}

	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", serverIP, p.port), 3*time.Second)
		if err != nil {
			fmt.Printf("  %s  %-14s %s\n", ui.Red.Render("✗"), p.name, ui.Faint.Render("not reachable — check your firewall/security group"))
		} else {
			conn.Close()
			fmt.Printf("  %s  %-14s %s\n", ui.Green.Render("✓"), p.name, ui.Faint.Render("open"))
		}
	}
	fmt.Println()
}

// deployNeoKey ensures neo's managed SSH key exists and is deployed to the server.
// Generates ~/.neo/neo_ed25519 on first run. Deploys the public key to the server's
// authorized_keys so all future connections use key auth automatically.
func deployNeoKey(exec *ssh.Executor) {
	pubKey, err := ssh.GenerateNeoKey()
	if err != nil {
		return
	}

	key := strings.TrimSpace(pubKey)
	cmd := fmt.Sprintf(
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF %s ~/.ssh/authorized_keys 2>/dev/null || echo %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		ssh.ShellQuote(key), ssh.ShellQuote(key),
	)
	if err := exec.RunQuiet(cmd); err == nil {
		ui.Success("SSH key deployed — future connections won't need a password")
	}
}
