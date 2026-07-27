// neo-bridge is a machine-facing helper for the Neo Desktop app. It exposes
// read-only Neo operations as one-shot commands:
//
//	neo-bridge <method> [paramsJSON]
//
// It prints a single JSON object to stdout and exits. Human-readable diagnostics
// go to stderr so they never corrupt the protocol. The desktop app calls this
// per poll; it reuses the same ~/.neo/config.json, SSH, and remote state as the
// CLI, so it never needs an external `neo` binary or a monitoring agent.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

const protocolVersion = 1

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		fail("invalid_request", "usage: neo-bridge <method> [paramsJSON]")
	}
	method := os.Args[1]

	// `pty` is a raw interactive-shell mode (not the JSON protocol): it bridges
	// stdio to a remote PTY over neo's SSH auth. Used by the desktop terminal.
	if method == "pty" {
		runInteractivePty(os.Args[2:])
		return
	}

	var params map[string]any
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		if err := json.Unmarshal([]byte(os.Args[2]), &params); err != nil {
			fail("invalid_request", "bad params JSON: "+err.Error())
		}
	}

	var (
		result any
		err    error
	)
	switch method {
	case "bridge.hello":
		result = hello()
	case "server.list":
		result, err = listServers()
	case "server.snapshot":
		result, err = snapshot(str(params, "server"))
	case "app.list":
		result, err = listApps(str(params, "server"))
	case "diagnostics.run":
		result, err = diagnostics(str(params, "server"))
	case "app.action":
		result, err = appAction(str(params, "server"), str(params, "app"), str(params, "action"))
	case "app.logs":
		result, err = appLogs(str(params, "server"), str(params, "app"))
	case "app.domain":
		result, err = appDomain(str(params, "server"), str(params, "app"), str(params, "domain"), boolp(params, "https"))
	case "server.sshkey":
		result, err = serverSSHKey(str(params, "server"))
	default:
		fail("invalid_request", "unknown method: "+method)
	}
	if err != nil {
		fail("internal_error", err.Error())
	}
	emit(result)
}

// ---- methods ----

type helloResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	BridgeVersion   string `json:"bridgeVersion"`
	CliCore         string `json:"cliCore"`
	Platform        string `json:"platform"`
	Activated       bool   `json:"activated"`
}

func hello() helloResult {
	cfg, _ := config.Load()
	activated := cfg != nil && cfg.LicenseKey != ""
	return helloResult{
		ProtocolVersion: protocolVersion,
		BridgeVersion:   version,
		CliCore:         version,
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		Activated:       activated,
	}
}

type serverSummary struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	KeyPath string `json:"keyPath"`
	Current bool   `json:"current"`
}

func listServers() ([]serverSummary, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	out := make([]serverSummary, 0, len(cfg.Servers))
	for _, s := range cfg.ServerList() {
		port := s.Port
		if port == 0 {
			port = 22
		}
		out = append(out, serverSummary{
			Name:    s.Name,
			Host:    s.Host,
			Port:    port,
			KeyPath: s.Key,
			Current: s.Name == cfg.Current,
		})
	}
	return out, nil
}

type workloadCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
}

type serverSnapshot struct {
	Server         string         `json:"server"`
	Reachable      bool           `json:"reachable"`
	ObservedAt     string         `json:"observedAt"`
	LatencyMS      int64          `json:"latencyMs"`
	CPUPercent     float64        `json:"cpuPercent"`
	RAMUsedBytes   uint64         `json:"ramUsedBytes"`
	RAMTotalBytes  uint64         `json:"ramTotalBytes"`
	DiskUsedBytes  uint64         `json:"diskUsedBytes"`
	DiskTotalBytes uint64         `json:"diskTotalBytes"`
	UptimeSeconds  uint64         `json:"uptimeSeconds"`
	Apps           workloadCounts `json:"apps"`
	Services       workloadCounts `json:"services"`
}

func snapshot(name string) (*serverSnapshot, error) {
	srv, err := resolve(name)
	if err != nil {
		return nil, err
	}
	snap := &serverSnapshot{Server: srv.Name, ObservedAt: now()}

	start := time.Now()
	exec, err := connect(srv)
	if err != nil {
		// Unreachable is a normal state, not an error.
		return snap, nil
	}
	defer exec.Close()
	snap.Reachable = true
	snap.LatencyMS = time.Since(start).Milliseconds()

	metricsCmd := `echo "CPU:$(top -bn1 2>/dev/null | awk '/%Cpu/{print 100-$8; exit}' || echo 0)"; ` +
		`echo "MEM:$(free -b 2>/dev/null | awk '/Mem:/{printf "%d/%d",$3,$2}' || echo 0/0)"; ` +
		`echo "DISK:$(df -B1 / 2>/dev/null | awk 'NR==2{printf "%d/%d",$3,$2}' || echo 0/0)"; ` +
		`echo "UP:$(awk '{print int($1)}' /proc/uptime 2>/dev/null || echo 0)"`
	if out, err := exec.Run(metricsCmd); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "CPU:"):
				snap.CPUPercent = parseFloat(strings.TrimPrefix(line, "CPU:"))
			case strings.HasPrefix(line, "MEM:"):
				snap.RAMUsedBytes, snap.RAMTotalBytes = parsePair(strings.TrimPrefix(line, "MEM:"))
			case strings.HasPrefix(line, "DISK:"):
				snap.DiskUsedBytes, snap.DiskTotalBytes = parsePair(strings.TrimPrefix(line, "DISK:"))
			case strings.HasPrefix(line, "UP:"):
				snap.UptimeSeconds = parseUint(strings.TrimPrefix(line, "UP:"))
			}
		}
	}

	if st, err := state.Load(exec); err == nil {
		for _, a := range st.Apps {
			snap.Apps.Total++
			if a.Status == "running" {
				snap.Apps.Running++
			}
		}
		for _, s := range st.Services {
			snap.Services.Total++
			if s.Status == "running" {
				snap.Services.Running++
			}
		}
	}
	return snap, nil
}

type appSummary struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

func listApps(name string) ([]appSummary, error) {
	srv, err := resolve(name)
	if err != nil {
		return nil, err
	}
	exec, err := connect(srv)
	if err != nil {
		return []appSummary{}, nil
	}
	defer exec.Close()
	st, err := state.Load(exec)
	if err != nil {
		return []appSummary{}, nil
	}
	out := make([]appSummary, 0, len(st.Apps))
	for _, a := range st.Apps {
		status := a.Status
		if status == "" {
			status = "stopped"
		}
		out = append(out, appSummary{Name: a.Name, Domain: a.Domain, Status: status, Image: a.Image})
	}
	return out, nil
}

type finding struct {
	ID             string `json:"id"`
	Rule           string `json:"rule"`
	Severity       string `json:"severity"`
	Summary        string `json:"summary"`
	LastObservedAt string `json:"lastObservedAt"`
}

func diagnostics(name string) ([]finding, error) {
	snap, err := snapshot(name)
	if err != nil {
		return nil, err
	}
	out := []finding{}
	add := func(rule, sev, summary string) {
		out = append(out, finding{ID: rule, Rule: rule, Severity: sev, Summary: summary, LastObservedAt: now()})
	}
	if !snap.Reachable {
		add("reachability", "critical", snap.Server+" is unreachable")
		return out, nil
	}
	if snap.DiskTotalBytes > 0 {
		p := pct(snap.DiskUsedBytes, snap.DiskTotalBytes)
		if p >= 90 {
			add("disk", "critical", fmt.Sprintf("Disk at %d%%", p))
		} else if p >= 75 {
			add("disk", "warning", fmt.Sprintf("Disk at %d%%", p))
		}
	}
	if snap.RAMTotalBytes > 0 {
		p := pct(snap.RAMUsedBytes, snap.RAMTotalBytes)
		if p >= 95 {
			add("ram", "critical", fmt.Sprintf("RAM at %d%%", p))
		} else if p >= 80 {
			add("ram", "warning", fmt.Sprintf("RAM at %d%%", p))
		}
	}
	if snap.CPUPercent >= 95 {
		add("cpu", "critical", fmt.Sprintf("CPU at %.0f%%", snap.CPUPercent))
	} else if snap.CPUPercent >= 80 {
		add("cpu", "warning", fmt.Sprintf("CPU at %.0f%%", snap.CPUPercent))
	}
	if snap.Apps.Total > snap.Apps.Running {
		add("app_state", "warning", fmt.Sprintf("%d app(s) not running", snap.Apps.Total-snap.Apps.Running))
	}
	return out, nil
}

type actionResult struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
}

// appAction performs a lifecycle op (restart/stop/start) directly via Docker
// over SSH — no terminal, no external neo binary — and updates remote state.
func appAction(server, app, action string) (*actionResult, error) {
	if app == "" {
		return nil, fmt.Errorf("app is required")
	}
	srv, err := resolve(server)
	if err != nil {
		return nil, err
	}
	exec, err := connect(srv)
	if err != nil {
		return nil, err
	}
	defer exec.Close()

	d := remote.NewDocker(exec)
	container := config.AppContainer(app)
	switch action {
	case "restart":
		err = d.Restart(container)
	case "stop":
		err = d.Stop(container)
	case "start":
		err = d.Start(container)
	case "remove":
		_ = d.Stop(container)
		err = d.Remove(container)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	if err != nil {
		return nil, err
	}

	// Remove: also drop the proxy route and state entry (volumes are kept).
	if action == "remove" {
		_ = remote.NewCaddy(exec).RemoveRoute(app)
		if st, e := state.Load(exec); e == nil {
			delete(st.Apps, app)
			_ = state.Save(exec, st)
		}
		return &actionResult{OK: true, Status: "removed"}, nil
	}

	status := "running"
	if action == "stop" {
		status = "stopped"
	}
	if st, e := state.Load(exec); e == nil {
		if a, ok := st.Apps[app]; ok {
			a.Status = status
			st.Apps[app] = a
			_ = state.Save(exec, st)
		}
	}
	return &actionResult{OK: true, Status: status}, nil
}

type logsResult struct {
	Logs string `json:"logs"`
}

// appLogs returns the last chunk of a container's logs (non-following, so it
// never hangs). Powers the GUI log viewer.
func appLogs(server, app string) (*logsResult, error) {
	if app == "" {
		return nil, fmt.Errorf("app is required")
	}
	srv, err := resolve(server)
	if err != nil {
		return nil, err
	}
	exec, err := connect(srv)
	if err != nil {
		return nil, err
	}
	defer exec.Close()
	container := config.AppContainer(app)
	cmd := fmt.Sprintf("docker logs --tail 500 %s 2>&1 | tail -c 80000", container)
	out, err := exec.Run(cmd)
	if err != nil && out == "" {
		return nil, err
	}
	return &logsResult{Logs: out}, nil
}

// appDomain assigns a domain to an app via Caddy (HTTPS auto-provisioned unless
// https=false) and records it in remote state. No terminal, no neo binary.
func appDomain(server, app, domain string, https bool) (*actionResult, error) {
	if app == "" || domain == "" {
		return nil, fmt.Errorf("app and domain are required")
	}
	srv, err := resolve(server)
	if err != nil {
		return nil, err
	}
	exec, err := connect(srv)
	if err != nil {
		return nil, err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return nil, err
	}
	a, ok := st.Apps[app]
	if !ok {
		return nil, fmt.Errorf("app not found: %s", app)
	}
	port := a.InternalPort
	if port == 0 {
		port = 80
	}
	upstream := fmt.Sprintf("%s:%d", config.AppContainer(app), port)

	caddy := remote.NewCaddy(exec)
	domains := []string{domain}
	if https {
		err = caddy.UpdateRoute(app, domains, upstream)
	} else {
		err = caddy.UpdateRouteHTTP(app, domains, upstream)
	}
	if err != nil {
		return nil, err
	}

	a.Domain = domain
	a.HTTPOnly = !https
	st.Apps[app] = a
	_ = state.Save(exec, st)
	return &actionResult{OK: true, Status: domain}, nil
}

type sshKeyResult struct {
	KeyPath string `json:"keyPath"`
}

// serverSSHKey finds the private key that actually authenticates to a server.
// The user may have dozens of keys; system `ssh` only tries id_* by default,
// so the desktop terminal needs the exact one. Each key is probed in isolation
// (single auth method) so the result is the real match, not a false positive.
func serverSSHKey(name string) (*sshKeyResult, error) {
	srv, err := resolve(name)
	if err != nil {
		return nil, err
	}
	if srv.Key != "" {
		return &sshKeyResult{KeyPath: srv.Key}, nil
	}

	user, host := splitHost(srv.Host)
	port := srv.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	home, err := os.UserHomeDir()
	if err != nil {
		return &sshKeyResult{}, nil
	}
	dir := filepath.Join(home, ".ssh")
	ents, _ := os.ReadDir(dir)

	skip := map[string]bool{"known_hosts": true, "config": true, "authorized_keys": true, "agent": true}
	// Probe neo's managed key first (this is what `neo init` installs), then
	// id_ed25519 / id_rsa, then everything else.
	files := []string{
		ssh.NeoKeyPath(),
		filepath.Join(dir, "id_ed25519"),
		filepath.Join(dir, "id_rsa"),
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if n == "id_ed25519" || n == "id_rsa" || skip[n] {
			continue
		}
		if strings.HasSuffix(n, ".pub") || strings.HasSuffix(n, ".ppk") || strings.HasSuffix(n, ".gz") {
			continue
		}
		files = append(files, filepath.Join(dir, n))
	}

	for _, kp := range files {
		data, err := os.ReadFile(kp)
		if err != nil {
			continue
		}
		signer, err := cryptossh.ParsePrivateKey(data)
		if err != nil {
			continue // encrypted or not a key
		}
		cfg := &cryptossh.ClientConfig{
			User:            user,
			Auth:            []cryptossh.AuthMethod{cryptossh.PublicKeys(signer)},
			HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
			Timeout:         6 * time.Second,
		}
		conn, err := cryptossh.Dial("tcp", addr, cfg)
		if err == nil {
			_ = conn.Close()
			return &sshKeyResult{KeyPath: kp}, nil
		}
	}
	return &sshKeyResult{}, nil
}

func splitHost(h string) (string, string) {
	if i := strings.IndexByte(h, '@'); i >= 0 {
		return h[:i], h[i+1:]
	}
	return "root", h
}

// runInteractivePty connects to a server (neo auth) and bridges stdio to a
// remote login shell, or to `docker exec -it <container> sh` when a container
// is given. Args: <server> <container|-> [cols] [rows].
func runInteractivePty(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: neo-bridge pty <server> <container|-> [cols] [rows]")
		os.Exit(2)
	}
	server := args[0]
	container := ""
	if len(args) > 1 && args[1] != "-" && args[1] != "" {
		container = args[1]
	}
	cols := argInt(args, 2, 80)
	rows := argInt(args, 3, 24)

	srv, err := resolve(server)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exec, err := connect(srv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to %s: %v\r\n", srv.Host, err)
		os.Exit(1)
	}
	defer exec.Close()

	cmd := ""
	if container != "" {
		cmd = fmt.Sprintf("docker exec -it %s sh", config.AppContainer(container))
	}
	if err := exec.InteractiveShell(cols, rows, cmd); err != nil {
		fmt.Fprintf(os.Stderr, "\r\nsession ended: %v\r\n", err)
		os.Exit(1)
	}
}

func argInt(args []string, i, def int) int {
	if i >= len(args) {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(args[i])); err == nil && n > 0 {
		return n
	}
	return def
}

// ---- helpers ----

func resolve(name string) (*config.Server, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = cfg.Current
	}
	s, ok := cfg.Servers[name]
	if !ok {
		fail("server_not_found", "server not found: "+name)
	}
	return &s, nil
}

func connect(srv *config.Server) (*ssh.Executor, error) {
	exec := ssh.New(srv.Host, srv.Port)
	exec.NonInteractive = true // never prompt for password / host trust
	if srv.Key != "" {
		if data, err := os.ReadFile(srv.Key); err == nil {
			exec.PrivateKey = data
		}
	}
	if err := exec.Connect(); err != nil {
		return nil, err
	}
	return exec, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func pct(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int(used * 100 / total)
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if f < 0 {
		f = 0
	}
	return f
}

func parseUint(s string) uint64 {
	u, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return u
}

func parsePair(s string) (uint64, uint64) {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseUint(parts[0]), parseUint(parts[1])
}

func boolp(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func emit(result any) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(map[string]any{"version": protocolVersion, "result": result})
}

func fail(code, message string) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(map[string]any{
		"version": protocolVersion,
		"error":   map[string]any{"code": code, "message": message},
	})
	os.Exit(0) // protocol-level error still exits 0; the payload carries the error
}
