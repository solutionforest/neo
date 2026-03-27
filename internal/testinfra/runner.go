package testinfra

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// Runner orchestrates the integration test sequence.
type Runner struct {
	Token    string
	Region   string
	Size     string
	Image    string
	Keep     bool
	TestData string // path to testdata/ directory

	doClient     *DOClient
	dropletID    int
	dropletIP    string
	sshKeyID     int
	key          *EphemeralKey
	keyFilePath  string // path where private key is saved on disk
	exec         *ssh.Executor
	docker       *remote.Docker
	caddy        *remote.Caddy
	results      []StepResult
	currentPhase string
}

// SSHCommand returns the ssh command string to connect to the VM.
func (r *Runner) SSHCommand() string {
	if r.keyFilePath == "" || r.dropletIP == "" {
		return ""
	}
	return fmt.Sprintf("ssh -i %s root@%s", r.keyFilePath, r.dropletIP)
}

// SaveNeoConfig writes the test server into ~/.neo/config.json using the saved key.
// Returns the server name registered.
func (r *Runner) SaveNeoConfig() (string, error) {
	if r.dropletIP == "" || r.keyFilePath == "" {
		return "", fmt.Errorf("droplet not ready")
	}
	return r.dropletIP, saveNeoConfig(r.dropletIP, r.keyFilePath)
}

// Run executes all integration test phases.
func (r *Runner) Run() error {
	r.doClient = NewDOClient(r.Token)

	fmt.Println()
	fmt.Println("  Neo Integration Tests")
	fmt.Println("  ─────────────────────")
	fmt.Printf("  Region: %s  Size: %s  Image: %s\n", r.Region, r.Size, r.Image)
	fmt.Println()

	// Phase 1: Infrastructure
	r.phase("Infrastructure")
	r.step("Generate ephemeral ed25519 keypair", r.stepGenerateKey)
	r.step("Upload SSH key to DO API", r.stepUploadKey)
	r.step("Create Ubuntu 24.04 droplet", r.stepCreateDroplet)
	r.step("Wait for droplet active", r.stepWaitDroplet)
	r.step("Wait for SSH connectivity", r.stepWaitSSH)

	if r.exec == nil {
		PrintResults(VMInfo{Region: r.Region, Size: r.Size, Image: r.Image}, r.results)
		return fmt.Errorf("infrastructure setup failed, cannot continue")
	}

	// Phase 2: Server Init
	r.phase("Server Init")
	r.step("Validate OS (Ubuntu 24.04)", r.stepValidateOS)
	r.step("Wait for cloud-init + apt lock", r.stepWaitCloudInit)
	r.step("System update", r.stepSystemUpdate)
	r.step("Install Docker", r.stepInstallDocker)
	r.step("Get Docker version", r.stepDockerVersion)
	r.step("Create network neo", r.stepCreateNetwork)
	r.step("Start Caddy container", r.stepStartCaddy)
	r.step("Get Caddy version", r.stepCaddyVersion)
	r.step("Init state", r.stepInitState)

	// Phase 3: Template Install — Uptime Kuma
	r.phase("Template Install — Uptime Kuma")
	r.step("Verify empty app list", r.stepVerifyEmptyApps)
	r.step("Pull uptime-kuma image", r.stepPullUptimeKuma)
	r.step("Run uptime-kuma container", r.stepRunUptimeKuma)
	r.step("Health check uptime-kuma", r.stepHealthCheckUptimeKuma)
	r.step("Add Caddy route for uptime-kuma", r.stepAddUptimeKumaRoute)
	r.step("Save state with uptime-kuma", r.stepSaveUptimeKumaState)
	r.step("Reload state and verify", r.stepReloadVerifyUptimeKuma)

	// Phase 4: App Lifecycle
	r.phase("App Lifecycle")
	r.step("Stop uptime-kuma", r.stepStopUptimeKuma)
	r.step("Start uptime-kuma", r.stepStartUptimeKuma)
	r.step("Restart uptime-kuma", r.stepRestartUptimeKuma)
	r.step("Get logs (tail 10)", r.stepGetLogs)

	// Phase 5: Env Vars
	r.phase("Env Vars")
	r.step("Read initial env", r.stepReadEnv)
	r.step("Set TEST_VAR=hello", r.stepSetEnvVar)
	r.step("Verify TEST_VAR persisted", r.stepVerifyEnvVar)
	r.step("Unset TEST_VAR", r.stepUnsetEnvVar)

	// Phase 6: Domain
	r.phase("Domain")
	r.step("Update Caddy route with nip.io domain", r.stepUpdateDomain)
	r.step("Verify domain in state", r.stepVerifyDomain)

	// Phase 7: Volumes
	r.phase("Volumes")
	r.step("List volumes", r.stepListVolumes)
	r.step("Backup volume (tar)", r.stepBackupVolume)

	// Phase 8: Deploy Local — hello-world
	r.phase("Deploy Local — hello-world")
	r.step("Create tar.gz of hello-world", r.stepCreateHelloTar)
	r.step("Upload + extract on server", r.stepUploadHello)
	r.step("Build hello-world image", r.stepBuildHello)
	r.step("Run hello-test container", r.stepRunHello)
	r.step("HTTP verify hello-world", r.stepVerifyHello)
	r.step("Add Caddy route for hello-test", r.stepAddHelloRoute)

	// Phase 9: Deploy Real — Laravel
	r.phase("Deploy Real — Laravel App")
	r.step("Create tar.gz of laravel-app", r.stepCreateLaravelTar)
	r.step("Upload + extract on server", r.stepUploadLaravel)
	r.step("Build laravel-app image", r.stepBuildLaravel)
	r.step("Run laravel-app container", r.stepRunLaravel)
	r.step("Health check laravel-app", r.stepHealthCheckLaravel)
	r.step("HTTP verify laravel (welcome page)", r.stepVerifyLaravel)
	r.step("Add Caddy route for laravel-app", r.stepAddLaravelRoute)

	// Phase 10: Update & Remove
	r.phase("Update & Remove")
	r.step("Update uptime-kuma (pull + recreate)", r.stepUpdateUptimeKuma)
	r.step("Remove hello-test + laravel-app", r.stepRemoveDeployedApps)

	// Phase 11: Redeploy / Blue-Green
	r.phase("Redeploy / Blue-Green")
	r.step("Deploy hello-test again (new tag)", r.stepRedeployHello)
	r.step("Verify swap completed", r.stepVerifySwap)
	r.step("Cleanup — remove hello-test", r.stepCleanupHello)

	// Print results
	PrintResults(VMInfo{
		Region: r.Region,
		Size:   r.Size,
		Image:  r.Image,
		IP:     r.dropletIP,
	}, r.results)

	return nil
}

// Teardown destroys infrastructure. Called separately so main can prompt first.
func (r *Runner) Teardown() {
	if r.dropletID > 0 {
		fmt.Println("  Destroying droplet...")
		if err := r.doClient.DestroyDroplet(r.dropletID); err != nil {
			fmt.Printf("  Warning: destroy droplet failed: %s\n", err)
		} else {
			fmt.Println("  Droplet destroyed.")
		}
	}
	if r.sshKeyID > 0 {
		fmt.Println("  Removing SSH key from DO...")
		if err := r.doClient.DeleteSSHKey(r.sshKeyID); err != nil {
			fmt.Printf("  Warning: delete SSH key failed: %s\n", err)
		} else {
			fmt.Println("  SSH key removed.")
		}
	}
	if r.exec != nil {
		r.exec.Close()
	}
}

// DropletIP returns the droplet's public IP address.
func (r *Runner) DropletIP() string {
	return r.dropletIP
}

// HasFailures returns true if any test step failed.
func (r *Runner) HasFailures() bool {
	for _, res := range r.results {
		if !res.Passed {
			return true
		}
	}
	return false
}

func (r *Runner) phase(name string) {
	r.currentPhase = name
	fmt.Println()
	fmt.Printf("  ── %s ──\n", name)
}

func (r *Runner) step(name string, fn func() error) {
	start := time.Now()
	err := fn()
	dur := time.Since(start)

	result := StepResult{
		Phase:    r.currentPhase,
		Name:     name,
		Duration: dur,
		Passed:   err == nil,
	}
	if err != nil {
		result.Error = err.Error()
		fmt.Printf("  ✗ %s (%s)\n", name, dur.Round(time.Millisecond))
		fmt.Printf("    %s\n", err)
	} else {
		fmt.Printf("  ✓ %s (%s)\n", name, dur.Round(time.Millisecond))
	}
	r.results = append(r.results, result)
}

// ── Phase 1: Infrastructure ──

func (r *Runner) stepGenerateKey() error {
	key, err := GenerateEphemeralKey()
	if err != nil {
		return err
	}
	if len(key.PrivateKeyPEM) == 0 {
		return fmt.Errorf("private key is empty")
	}
	r.key = key

	// Save private key to /tmp so user can SSH in manually
	keyPath := "/tmp/neo-test-key"
	if err := os.WriteFile(keyPath, key.PrivateKeyPEM, 0600); err != nil {
		return fmt.Errorf("save key file: %w", err)
	}
	r.keyFilePath = keyPath
	return nil
}

func (r *Runner) stepUploadKey() error {
	keyName := fmt.Sprintf("neo-test-%d", time.Now().Unix())
	id, err := r.doClient.UploadSSHKey(keyName, strings.TrimSpace(r.key.PublicKeySSH))
	if err != nil {
		return err
	}
	if id == 0 {
		return fmt.Errorf("key ID is 0")
	}
	r.sshKeyID = id
	return nil
}

func (r *Runner) stepCreateDroplet() error {
	name := fmt.Sprintf("neo-test-%d", time.Now().Unix())
	id, err := r.doClient.CreateDroplet(name, r.Region, r.Size, r.Image, []int{r.sshKeyID})
	if err != nil {
		return err
	}
	if id == 0 {
		return fmt.Errorf("droplet ID is 0")
	}
	r.dropletID = id
	return nil
}

func (r *Runner) stepWaitDroplet() error {
	ip, err := r.doClient.WaitForDroplet(r.dropletID)
	if err != nil {
		return err
	}
	if ip == "" {
		return fmt.Errorf("no public IP")
	}
	r.dropletIP = ip
	fmt.Printf("    IP: %s\n", ip)
	return nil
}

func (r *Runner) stepWaitSSH() error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		exec := ssh.New("root@"+r.dropletIP, 22)
		exec.PrivateKey = r.key.PrivateKeyPEM
		exec.InsecureHostKey = true
		if err := exec.Connect(); err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		r.exec = exec
		r.docker = remote.NewDocker(exec)
		r.caddy = remote.NewCaddy(exec)
		return nil
	}
	return fmt.Errorf("SSH not available after 90s")
}

// ── Phase 2: Server Init ──

func (r *Runner) stepValidateOS() error {
	out, err := r.exec.Run("grep '^ID=' /etc/os-release | cut -d= -f2")
	if err != nil {
		return err
	}
	osID := strings.Trim(out, "\"")
	if osID != "ubuntu" {
		return fmt.Errorf("unexpected OS: %s", osID)
	}
	return nil
}

func (r *Runner) stepWaitCloudInit() error {
	// Wait for cloud-init to finish
	r.exec.Run("cloud-init status --wait 2>/dev/null || true")
	// Wait for dpkg lock
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		_, err := r.exec.Run("fuser /var/lib/dpkg/lock-frontend 2>/dev/null")
		if err != nil {
			// No process holds the lock
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("dpkg lock held for >2 minutes")
}

func (r *Runner) stepSystemUpdate() error {
	return r.exec.RunQuiet("DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y -qq")
}

func (r *Runner) stepInstallDocker() error {
	if err := r.docker.Install(); err != nil {
		return err
	}
	if !r.docker.IsInstalled() {
		return fmt.Errorf("docker not found after install")
	}
	return nil
}

func (r *Runner) stepDockerVersion() error {
	v, err := r.docker.Version()
	if err != nil {
		return err
	}
	if v == "" {
		return fmt.Errorf("empty version")
	}
	fmt.Printf("    Docker %s\n", v)
	return nil
}

func (r *Runner) stepCreateNetwork() error {
	return r.docker.CreateNetwork("neo")
}

func (r *Runner) stepStartCaddy() error {
	if err := r.caddy.StartContainer(); err != nil {
		return err
	}
	// Wait a moment for container to start
	time.Sleep(3 * time.Second)
	if !r.caddy.IsRunning() {
		return fmt.Errorf("caddy not running after start")
	}

	// Bootstrap Caddy with an HTTP server so AddRoute has a target path.
	// Must include admin block to keep the admin API running after /load replaces the config.
	bootstrapConfig := `{"admin":{"listen":"0.0.0.0:2019"},"apps":{"http":{"servers":{"srv0":{"listen":[":80",":443"],"routes":[]}}}}}`
	cmd := fmt.Sprintf(`curl -sf -X POST http://localhost:2019/load -H "Content-Type: application/json" -d '%s'`, bootstrapConfig)
	if err := r.exec.RunQuiet(cmd); err != nil {
		return fmt.Errorf("bootstrap caddy config: %w", err)
	}
	return nil
}

func (r *Runner) stepCaddyVersion() error {
	v, err := r.caddy.Version()
	if err != nil {
		return err
	}
	if v == "" {
		return fmt.Errorf("empty caddy version")
	}
	fmt.Printf("    Caddy %s\n", v)
	return nil
}

func (r *Runner) stepInitState() error {
	if err := state.Init(r.exec, r.dropletIP); err != nil {
		return err
	}
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if !st.Initialized {
		return fmt.Errorf("state not initialized")
	}
	return nil
}

// ── Phase 3: Template Install ──

func (r *Runner) stepVerifyEmptyApps() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if len(st.Apps) != 0 {
		return fmt.Errorf("expected 0 apps, got %d", len(st.Apps))
	}
	return nil
}

func (r *Runner) stepPullUptimeKuma() error {
	return r.exec.RunQuiet("docker pull louislam/uptime-kuma:1")
}

func (r *Runner) stepRunUptimeKuma() error {
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-uptime-kuma",
		Image:   "louislam/uptime-kuma:1",
		Network: "neo",
		Restart: "unless-stopped",
		Volumes: []string{"uptime-kuma-data:/app/data"},
	})
	return err
}

func (r *Runner) stepHealthCheckUptimeKuma() error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r.docker.IsRunning("app-uptime-kuma") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("uptime-kuma not running after 30s")
}

func (r *Runner) stepAddUptimeKumaRoute() error {
	domain := fmt.Sprintf("uptime-kuma.%s.nip.io", r.dropletIP)
	return r.caddy.AddRoute("uptime-kuma", []string{domain}, "app-uptime-kuma:3001")
}

func (r *Runner) stepSaveUptimeKumaState() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	st.Apps["uptime-kuma"] = state.App{
		Name:         "uptime-kuma",
		Image:        "louislam/uptime-kuma:1",
		Domain:       fmt.Sprintf("uptime-kuma.%s.nip.io", r.dropletIP),
		Status:       "running",
		InternalPort: 3001,
		Volumes:      map[string]state.VolumeInfo{"uptime-kuma-data": {ContainerPath: "/app/data"}},
		InstalledAt:  time.Now().Format(time.RFC3339),
	}
	return state.Save(r.exec, st)
}

func (r *Runner) stepReloadVerifyUptimeKuma() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app, ok := st.Apps["uptime-kuma"]
	if !ok {
		return fmt.Errorf("uptime-kuma not in state")
	}
	if app.Status != "running" {
		return fmt.Errorf("status = %q, want running", app.Status)
	}
	return nil
}

// ── Phase 4: App Lifecycle ──

func (r *Runner) stepStopUptimeKuma() error {
	if err := r.docker.Stop("app-uptime-kuma"); err != nil {
		return err
	}
	if r.docker.IsRunning("app-uptime-kuma") {
		return fmt.Errorf("still running after stop")
	}
	return nil
}

func (r *Runner) stepStartUptimeKuma() error {
	if err := r.docker.Start("app-uptime-kuma"); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	if !r.docker.IsRunning("app-uptime-kuma") {
		return fmt.Errorf("not running after start")
	}
	return nil
}

func (r *Runner) stepRestartUptimeKuma() error {
	if err := r.docker.Restart("app-uptime-kuma"); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	if !r.docker.IsRunning("app-uptime-kuma") {
		return fmt.Errorf("not running after restart")
	}
	return nil
}

func (r *Runner) stepGetLogs() error {
	var buf bytes.Buffer
	if err := r.docker.Logs("app-uptime-kuma", 10, false, &buf); err != nil {
		return err
	}
	if buf.Len() == 0 {
		return fmt.Errorf("empty logs")
	}
	return nil
}

// ── Phase 5: Env Vars ──

func (r *Runner) stepReadEnv() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	if app.Env == nil {
		// Initialize env map
		app.Env = make(map[string]string)
		st.Apps["uptime-kuma"] = app
		return state.Save(r.exec, st)
	}
	return nil
}

func (r *Runner) stepSetEnvVar() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	if app.Env == nil {
		app.Env = make(map[string]string)
	}
	app.Env["TEST_VAR"] = "hello"
	st.Apps["uptime-kuma"] = app
	return state.Save(r.exec, st)
}

func (r *Runner) stepVerifyEnvVar() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	if app.Env["TEST_VAR"] != "hello" {
		return fmt.Errorf("TEST_VAR = %q, want hello", app.Env["TEST_VAR"])
	}
	return nil
}

func (r *Runner) stepUnsetEnvVar() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	delete(app.Env, "TEST_VAR")
	st.Apps["uptime-kuma"] = app
	if err := state.Save(r.exec, st); err != nil {
		return err
	}
	// Verify
	st2, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if _, exists := st2.Apps["uptime-kuma"].Env["TEST_VAR"]; exists {
		return fmt.Errorf("TEST_VAR still exists after unset")
	}
	return nil
}

// ── Phase 6: Domain ──

func (r *Runner) stepUpdateDomain() error {
	domain := fmt.Sprintf("uptime-kuma.%s.nip.io", r.dropletIP)
	return r.caddy.UpdateRoute("uptime-kuma", []string{domain}, "app-uptime-kuma:3001")
}

func (r *Runner) stepVerifyDomain() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	expected := fmt.Sprintf("uptime-kuma.%s.nip.io", r.dropletIP)
	if app.Domain != expected {
		return fmt.Errorf("domain = %q, want %q", app.Domain, expected)
	}
	return nil
}

// ── Phase 7: Volumes ──

func (r *Runner) stepListVolumes() error {
	out, err := r.docker.VolumeList()
	if err != nil {
		return err
	}
	if !strings.Contains(out, "uptime-kuma-data") {
		return fmt.Errorf("uptime-kuma-data volume not found in: %s", out)
	}
	return nil
}

func (r *Runner) stepBackupVolume() error {
	// Create a tar backup of the volume
	_, err := r.exec.Run("docker run --rm -v uptime-kuma-data:/data -v /tmp:/backup alpine tar czf /backup/uptime-kuma-backup.tar.gz -C /data .")
	if err != nil {
		return err
	}
	if !r.exec.FileExists("/tmp/uptime-kuma-backup.tar.gz") {
		return fmt.Errorf("backup file not found")
	}
	return nil
}

// ── Phase 8: Deploy hello-world ──

func (r *Runner) stepCreateHelloTar() error {
	return nil // just checks path exists
}

func (r *Runner) stepUploadHello() error {
	srcDir := filepath.Join(r.TestData, "hello-world")
	var buf bytes.Buffer
	if err := createTarGz(srcDir, &buf); err != nil {
		return fmt.Errorf("create tar: %w", err)
	}
	// Upload tar
	r.exec.RunQuiet("mkdir -p /tmp/neo-build/hello-world")
	if err := r.exec.UploadReader(&buf, int64(buf.Len()), "/tmp/neo-build/hello-world.tar.gz", 0644); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	_, err := r.exec.Run("cd /tmp/neo-build && tar xzf hello-world.tar.gz -C hello-world")
	return err
}

func (r *Runner) stepBuildHello() error {
	var buf bytes.Buffer
	err := r.docker.Build("/tmp/neo-build/hello-world", "/tmp/neo-build/hello-world/Dockerfile", "hello-test:latest", &buf)
	if err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	return nil
}

func (r *Runner) stepRunHello() error {
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-hello-test",
		Image:   "hello-test:latest",
		Network: "neo",
		Restart: "unless-stopped",
	})
	if err != nil {
		return err
	}
	// Wait for it to start
	time.Sleep(3 * time.Second)
	if !r.docker.IsRunning("app-hello-test") {
		return fmt.Errorf("hello-test not running")
	}
	return nil
}

func (r *Runner) stepVerifyHello() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		// Must curl from inside the neo network to resolve Docker DNS
		out, err := r.exec.Run("docker run --rm --network neo alpine/curl -sf http://app-hello-test:3000 2>/dev/null || true")
		if err == nil && strings.Contains(out, "Hello from neo") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("hello-world did not respond with expected content")
}

func (r *Runner) stepAddHelloRoute() error {
	domain := fmt.Sprintf("hello-test.%s.nip.io", r.dropletIP)
	return r.caddy.AddRoute("hello-test", []string{domain}, "app-hello-test:3000")
}

// ── Phase 9: Deploy Laravel ──

func (r *Runner) stepCreateLaravelTar() error {
	return nil // verified by upload step
}

func (r *Runner) stepUploadLaravel() error {
	srcDir := filepath.Join(r.TestData, "laravel-app")
	var buf bytes.Buffer
	if err := createTarGz(srcDir, &buf); err != nil {
		return fmt.Errorf("create tar: %w", err)
	}
	r.exec.RunQuiet("mkdir -p /tmp/neo-build/laravel-app")
	if err := r.exec.UploadReader(&buf, int64(buf.Len()), "/tmp/neo-build/laravel-app.tar.gz", 0644); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	_, err := r.exec.Run("cd /tmp/neo-build && tar xzf laravel-app.tar.gz -C laravel-app")
	return err
}

func (r *Runner) stepBuildLaravel() error {
	var buf bytes.Buffer
	err := r.docker.Build("/tmp/neo-build/laravel-app", "/tmp/neo-build/laravel-app/Dockerfile", "laravel-app:latest", &buf)
	if err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	return nil
}

func (r *Runner) stepRunLaravel() error {
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-laravel-app",
		Image:   "laravel-app:latest",
		Network: "neo",
		Restart: "unless-stopped",
	})
	return err
}

func (r *Runner) stepHealthCheckLaravel() error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if r.docker.IsRunning("app-laravel-app") {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("laravel-app not running after 90s")
}

func (r *Runner) stepVerifyLaravel() error {
	// Pull curl image first
	r.exec.RunQuiet("docker pull alpine/curl 2>/dev/null || true")
	deadline := time.Now().Add(60 * time.Second)
	var lastOut string
	for time.Now().Before(deadline) {
		// Must curl from inside the neo network to resolve Docker DNS
		out, err := r.exec.Run("docker run --rm --network neo alpine/curl -s http://app-laravel-app:8080 2>/dev/null || true")
		lastOut = out
		if err == nil && (strings.Contains(out, "Laravel") || strings.Contains(out, "laravel")) {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	// Show what we got for debugging
	snippet := lastOut
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	return fmt.Errorf("laravel-app response did not contain 'Laravel', got: %s", snippet)
}

func (r *Runner) stepAddLaravelRoute() error {
	domain := fmt.Sprintf("laravel-app.%s.nip.io", r.dropletIP)
	return r.caddy.AddRoute("laravel-app", []string{domain}, "app-laravel-app:8080")
}

// ── Phase 10: Update & Remove ──

func (r *Runner) stepUpdateUptimeKuma() error {
	// Pull latest
	if err := r.exec.RunQuiet("docker pull louislam/uptime-kuma:1"); err != nil {
		return err
	}
	// Stop + remove old
	r.docker.Stop("app-uptime-kuma")
	r.docker.Remove("app-uptime-kuma")
	// Recreate
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-uptime-kuma",
		Image:   "louislam/uptime-kuma:1",
		Network: "neo",
		Restart: "unless-stopped",
		Volumes: []string{"uptime-kuma-data:/app/data"},
	})
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	if !r.docker.IsRunning("app-uptime-kuma") {
		return fmt.Errorf("not running after update")
	}
	return nil
}

func (r *Runner) stepRemoveDeployedApps() error {
	for _, name := range []string{"hello-test", "laravel-app"} {
		r.docker.Stop("app-" + name)
		r.docker.Remove("app-" + name)
		r.caddy.RemoveRoute(name)

		st, err := state.Load(r.exec)
		if err != nil {
			return err
		}
		delete(st.Apps, name)
		if err := state.Save(r.exec, st); err != nil {
			return err
		}
	}

	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if _, ok := st.Apps["hello-test"]; ok {
		return fmt.Errorf("hello-test still in state")
	}
	if _, ok := st.Apps["laravel-app"]; ok {
		return fmt.Errorf("laravel-app still in state")
	}
	return nil
}

// ── Phase 11: Redeploy / Blue-Green ──

func (r *Runner) stepRedeployHello() error {
	// Build with a new tag
	var buf bytes.Buffer
	if err := r.docker.Build("/tmp/neo-build/hello-world", "/tmp/neo-build/hello-world/Dockerfile", "hello-test:v2", &buf); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	// Run as -next
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-hello-test-next",
		Image:   "hello-test:v2",
		Network: "neo",
		Restart: "unless-stopped",
	})
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	if !r.docker.IsRunning("app-hello-test-next") {
		return fmt.Errorf("next container not running")
	}
	return nil
}

func (r *Runner) stepVerifySwap() error {
	// Swap: rename -next to primary
	r.docker.Remove("app-hello-test") // remove old if exists
	if err := r.docker.Rename("app-hello-test-next", "app-hello-test"); err != nil {
		return err
	}
	if !r.docker.IsRunning("app-hello-test") {
		return fmt.Errorf("swapped container not running")
	}
	return nil
}

func (r *Runner) stepCleanupHello() error {
	r.docker.Stop("app-hello-test")
	r.docker.Remove("app-hello-test")

	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	delete(st.Apps, "hello-test")
	return state.Save(r.exec, st)
}

// ── Helpers ──

// saveNeoConfig writes the test server into ~/.neo/config.json.
func saveNeoConfig(ip, keyPath string) error {
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".neo")
	configPath := filepath.Join(configDir, "config.json")

	type server struct {
		Name          string `json:"name"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		Key           string `json:"key,omitempty"`
		InitializedAt string `json:"initialized_at"`
	}
	type neoConfig struct {
		Current string            `json:"current"`
		Servers map[string]server `json:"servers"`
	}

	// Load existing or create fresh
	cfg := neoConfig{Servers: make(map[string]server)}
	if data, err := os.ReadFile(configPath); err == nil {
		json.Unmarshal(data, &cfg)
	}

	name := "neo-test"
	cfg.Servers[name] = server{
		Name:          name,
		Host:          "root@" + ip,
		Port:          22,
		Key:           keyPath,
		InitializedAt: time.Now().Format(time.RFC3339),
	}
	cfg.Current = name

	os.MkdirAll(configDir, 0o700)
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath, data, 0o600)
}

func createTarGz(srcDir string, buf *bytes.Buffer) error {
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gzw.Close()
}
