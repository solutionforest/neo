package sandbox

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/testinfra"
)

// Runner orchestrates integration tests against a Docker sandbox container.
type Runner struct {
	Host       string // e.g. "root@localhost"
	Port       int    // e.g. 2222
	PrivateKey []byte // PEM-encoded private key
	Distro     string // e.g. "ubuntu-24.04", "debian-12" (for display)
	Supported  bool   // true = full test, false = expect OS rejection

	exec         *ssh.Executor
	docker       *remote.Docker
	caddy        *remote.Caddy
	results      []testinfra.StepResult
	currentPhase string
	containerIP  string
}

// Run executes test phases appropriate for this distro.
func (r *Runner) Run() error {
	fmt.Println()
	fmt.Printf("  Neo Sandbox Tests — %s\n", r.Distro)
	fmt.Printf("  %s\n", strings.Repeat("─", 20+len(r.Distro)))
	fmt.Printf("  Target: %s:%d  Expected: %s\n", r.Host, r.Port, r.supportLabel())
	fmt.Println()

	// Phase 1: Connect
	r.phase("SSH Connection")
	r.step("Connect to sandbox via SSH", r.stepConnect)

	if r.exec == nil {
		r.printResults()
		return fmt.Errorf("SSH connection failed, cannot continue")
	}

	// Phase 2: OS Detection
	r.phase("OS Detection")
	r.step("Read /etc/os-release", r.stepDetectOS)
	r.step("Validate OS support", r.stepValidateOS)

	if !r.Supported {
		// For unsupported distros, we're done — OS rejection was the test
		r.printResults()
		return nil
	}

	// ── Full test suite for supported distros ──

	r.phase("Server Init")
	r.step("Verify Docker installed", r.stepVerifyDocker)
	r.step("Get Docker version", r.stepDockerVersion)
	r.step("Create network neo", r.stepCreateNetwork)
	r.step("Start Caddy container", r.stepStartCaddy)
	r.step("Get Caddy version", r.stepCaddyVersion)
	r.step("Init state", r.stepInitState)

	r.phase("Template Install — Uptime Kuma")
	r.step("Verify empty app list", r.stepVerifyEmptyApps)
	r.step("Pull uptime-kuma image", r.stepPullUptimeKuma)
	r.step("Run uptime-kuma container", r.stepRunUptimeKuma)
	r.step("Health check uptime-kuma", r.stepHealthCheckUptimeKuma)
	r.step("Add Caddy route for uptime-kuma", r.stepAddUptimeKumaRoute)
	r.step("Save state with uptime-kuma", r.stepSaveUptimeKumaState)
	r.step("Reload state and verify", r.stepReloadVerifyUptimeKuma)

	r.phase("App Lifecycle")
	r.step("Stop uptime-kuma", r.stepStopUptimeKuma)
	r.step("Start uptime-kuma", r.stepStartUptimeKuma)
	r.step("Restart uptime-kuma", r.stepRestartUptimeKuma)
	r.step("Get logs (tail 10)", r.stepGetLogs)

	r.phase("Env Vars")
	r.step("Read initial env", r.stepReadEnv)
	r.step("Set TEST_VAR=hello", r.stepSetEnvVar)
	r.step("Verify TEST_VAR persisted", r.stepVerifyEnvVar)
	r.step("Unset TEST_VAR", r.stepUnsetEnvVar)

	r.phase("Domain")
	r.step("Update Caddy route with sslip.io domain", r.stepUpdateDomain)
	r.step("Verify domain in state", r.stepVerifyDomain)

	r.phase("Volumes")
	r.step("List volumes", r.stepListVolumes)
	r.step("Backup volume (tar)", r.stepBackupVolume)

	r.phase("Update & Remove")
	r.step("Update uptime-kuma (pull + recreate)", r.stepUpdateUptimeKuma)
	r.step("Remove uptime-kuma", r.stepRemoveUptimeKuma)
	r.step("Verify clean state", r.stepVerifyCleanState)

	r.phase("Deploy — Inline Hello")
	r.step("Create Dockerfile on server", r.stepCreateHelloDockerfile)
	r.step("Build hello image", r.stepBuildHello)
	r.step("Run hello container", r.stepRunHello)
	r.step("HTTP verify hello", r.stepVerifyHello)
	r.step("Cleanup hello", r.stepCleanupHello)

	r.printResults()
	return nil
}

// HasFailures returns true if any step failed.
func (r *Runner) HasFailures() bool {
	for _, res := range r.results {
		if !res.Passed {
			return true
		}
	}
	return false
}

func (r *Runner) supportLabel() string {
	if r.Supported {
		return "supported"
	}
	return "unsupported (should reject)"
}

func (r *Runner) printResults() {
	testinfra.PrintResults(testinfra.VMInfo{
		Region: "local",
		Size:   "docker",
		Image:  r.Distro,
		IP:     fmt.Sprintf("%s:%d", r.Host, r.Port),
	}, r.results)
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

	result := testinfra.StepResult{
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

// ── Phase 1: SSH Connection ──

func (r *Runner) stepConnect() error {
	exec := ssh.New(r.Host, r.Port)
	exec.PrivateKey = r.PrivateKey
	exec.InsecureHostKey = true
	if err := exec.Connect(); err != nil {
		return fmt.Errorf("SSH connect: %w", err)
	}
	r.exec = exec
	r.docker = remote.NewDocker(exec)
	r.caddy = remote.NewCaddy(exec)

	ip, _ := r.exec.Run("hostname -I | awk '{print $1}'")
	r.containerIP = strings.TrimSpace(ip)
	if r.containerIP == "" {
		r.containerIP = "127.0.0.1"
	}
	return nil
}

// ── Phase 2: OS Detection & Validation ──

func (r *Runner) stepDetectOS() error {
	pretty, _ := r.exec.Run("grep PRETTY_NAME /etc/os-release | cut -d'\"' -f2")
	pretty = strings.TrimSpace(pretty)
	if pretty == "" {
		return fmt.Errorf("could not read PRETTY_NAME from /etc/os-release")
	}
	fmt.Printf("    %s\n", pretty)
	return nil
}

func (r *Runner) stepValidateOS() error {
	osID, err := r.exec.Run("grep '^ID=' /etc/os-release | cut -d= -f2")
	if err != nil {
		return err
	}
	osID = strings.TrimSpace(strings.Trim(osID, "\""))

	versionID, _ := r.exec.Run("grep '^VERSION_ID=' /etc/os-release | cut -d= -f2 | tr -d '\"'")
	versionID = strings.TrimSpace(versionID)

	// Run the same validation logic as neo init (commands/init.go:validateOS)
	supported := false
	switch osID {
	case "debian":
		supported = true
	case "ubuntu":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err == nil && ver >= 24.04 {
			supported = true
		}
	case "fedora":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err == nil && ver >= 39 {
			supported = true
		}
	case "centos", "rhel", "almalinux", "rocky":
		ver, err := strconv.ParseFloat(versionID, 64)
		if err == nil && ver >= 9 {
			supported = true
		}
	}

	if r.Supported && !supported {
		return fmt.Errorf("expected %s to be supported, but validateOS would reject it (ID=%s, VERSION_ID=%s)", r.Distro, osID, versionID)
	}
	if !r.Supported && supported {
		return fmt.Errorf("expected %s to be unsupported, but validateOS would accept it (ID=%s, VERSION_ID=%s)", r.Distro, osID, versionID)
	}

	if r.Supported {
		fmt.Printf("    OS validated: %s %s (supported)\n", osID, versionID)
	} else {
		fmt.Printf("    OS validated: %s %s (correctly rejected)\n", osID, versionID)
	}
	return nil
}

// ── Server Init ──

func (r *Runner) stepVerifyDocker() error {
	if !r.docker.IsInstalled() {
		return fmt.Errorf("docker not installed in sandbox")
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
	time.Sleep(3 * time.Second)
	if !r.caddy.IsRunning() {
		return fmt.Errorf("caddy not running after start")
	}

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
	fmt.Printf("    Caddy %s\n", v)
	return nil
}

func (r *Runner) stepInitState() error {
	if err := state.Init(r.exec, r.containerIP); err != nil {
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

// ── Template Install ──

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
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if r.docker.IsRunning("app-uptime-kuma") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("uptime-kuma not running after 60s")
}

func (r *Runner) stepAddUptimeKumaRoute() error {
	domain := fmt.Sprintf("uptime-kuma.%s.sslip.io", r.containerIP)
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
		Domain:       fmt.Sprintf("uptime-kuma.%s.sslip.io", r.containerIP),
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

// ── App Lifecycle ──

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

// ── Env Vars ──

func (r *Runner) stepReadEnv() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	if app.Env == nil {
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
	st2, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if _, exists := st2.Apps["uptime-kuma"].Env["TEST_VAR"]; exists {
		return fmt.Errorf("TEST_VAR still exists after unset")
	}
	return nil
}

// ── Domain ──

func (r *Runner) stepUpdateDomain() error {
	domain := fmt.Sprintf("uptime-kuma.%s.sslip.io", r.containerIP)
	return r.caddy.UpdateRoute("uptime-kuma", []string{domain}, "app-uptime-kuma:3001")
}

func (r *Runner) stepVerifyDomain() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	app := st.Apps["uptime-kuma"]
	expected := fmt.Sprintf("uptime-kuma.%s.sslip.io", r.containerIP)
	if app.Domain != expected {
		return fmt.Errorf("domain = %q, want %q", app.Domain, expected)
	}
	return nil
}

// ── Volumes ──

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
	_, err := r.exec.Run("docker run --rm -v uptime-kuma-data:/data -v /tmp:/backup alpine tar czf /backup/uptime-kuma-backup.tar.gz -C /data .")
	if err != nil {
		return err
	}
	if !r.exec.FileExists("/tmp/uptime-kuma-backup.tar.gz") {
		return fmt.Errorf("backup file not found")
	}
	return nil
}

// ── Update & Remove ──

func (r *Runner) stepUpdateUptimeKuma() error {
	if err := r.exec.RunQuiet("docker pull louislam/uptime-kuma:1"); err != nil {
		return err
	}
	r.docker.Stop("app-uptime-kuma")
	r.docker.Remove("app-uptime-kuma")
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

func (r *Runner) stepRemoveUptimeKuma() error {
	r.docker.Stop("app-uptime-kuma")
	r.docker.Remove("app-uptime-kuma")
	r.caddy.RemoveRoute("uptime-kuma")

	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	delete(st.Apps, "uptime-kuma")
	return state.Save(r.exec, st)
}

func (r *Runner) stepVerifyCleanState() error {
	st, err := state.Load(r.exec)
	if err != nil {
		return err
	}
	if len(st.Apps) != 0 {
		return fmt.Errorf("expected 0 apps after cleanup, got %d", len(st.Apps))
	}
	return nil
}

// ── Deploy inline hello-world ──

func (r *Runner) stepCreateHelloDockerfile() error {
	r.exec.RunQuiet("mkdir -p /tmp/neo-build/hello")

	dockerfile := `FROM golang:1.24-alpine AS build
WORKDIR /app
COPY main.go .
RUN go build -o /hello main.go
FROM alpine:3.20
COPY --from=build /hello /hello
EXPOSE 3000
CMD ["/hello"]`

	mainGo := `package main
import (
	"fmt"
	"net/http"
)
func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Hello from neo sandbox")
	})
	http.ListenAndServe(":3000", nil)
}`

	if err := r.exec.WriteFile("/tmp/neo-build/hello/Dockerfile", []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}
	if err := r.exec.WriteFile("/tmp/neo-build/hello/main.go", []byte(mainGo), 0644); err != nil {
		return fmt.Errorf("write main.go: %w", err)
	}
	return nil
}

func (r *Runner) stepBuildHello() error {
	var buf bytes.Buffer
	err := r.docker.Build("/tmp/neo-build/hello", "/tmp/neo-build/hello/Dockerfile", "hello-sandbox:latest", &buf)
	if err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	return nil
}

func (r *Runner) stepRunHello() error {
	_, err := r.docker.Run(remote.RunOpts{
		Name:    "app-hello-sandbox",
		Image:   "hello-sandbox:latest",
		Network: "neo",
		Restart: "unless-stopped",
	})
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	if !r.docker.IsRunning("app-hello-sandbox") {
		return fmt.Errorf("hello-sandbox not running")
	}
	return nil
}

func (r *Runner) stepVerifyHello() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := r.exec.Run("docker run --rm --network neo alpine/curl -sf http://app-hello-sandbox:3000 2>/dev/null || true")
		if err == nil && strings.Contains(out, "Hello from neo sandbox") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("hello-sandbox did not respond with expected content")
}

func (r *Runner) stepCleanupHello() error {
	r.docker.Stop("app-hello-sandbox")
	r.docker.Remove("app-hello-sandbox")
	r.exec.RunQuiet("rm -rf /tmp/neo-build/hello")
	r.exec.RunQuiet("docker rmi hello-sandbox:latest 2>/dev/null || true")
	return nil
}
