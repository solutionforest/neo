package remote

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
)

// Docker wraps SSH-based Docker operations on a remote server.
type Docker struct {
	exec *ssh.Executor
}

// NewDocker creates a Docker remote executor.
func NewDocker(exec *ssh.Executor) *Docker {
	return &Docker{exec: exec}
}

// IsInstalled checks if Docker is available on the remote server.
func (d *Docker) IsInstalled() bool {
	_, err := d.exec.Run("docker --version")
	return err == nil
}

// Install installs Docker on the remote server via the convenience script.
func (d *Docker) Install() error {
	return d.exec.RunQuiet(fmt.Sprintf("curl -fsSL %s | sh", config.DefaultDockerInstallURL))
}

// Version returns the Docker version string.
func (d *Docker) Version() (string, error) {
	out, err := d.exec.Run("docker --version")
	if err != nil {
		return "", err
	}
	// "Docker version 27.1.1, build ..." → "27.1.1"
	parts := strings.Fields(out)
	if len(parts) >= 3 {
		return strings.TrimRight(parts[2], ","), nil
	}
	return out, nil
}

// CreateNetwork creates a Docker network if it doesn't exist.
func (d *Docker) CreateNetwork(name string) error {
	q := ssh.ShellQuote(name)
	return d.exec.RunQuiet(fmt.Sprintf(
		"docker network inspect %s >/dev/null 2>&1 || docker network create %s",
		q, q,
	))
}

// Pull pulls a Docker image on the remote server.
func (d *Docker) Pull(image string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker pull %s", ssh.ShellQuote(image)))
}

// PullStream pulls a Docker image and streams output.
func (d *Docker) PullStream(image string, w io.Writer) error {
	return d.exec.Stream(fmt.Sprintf("docker pull %s", ssh.ShellQuote(image)), w)
}

// RunOpts holds options for running a container.
type RunOpts struct {
	Name       string
	Image      string
	Network    string
	Restart    string
	Ports      []string          // "host:container"
	Volumes    []string          // "name:/path" or "/host:/container"
	Env        map[string]string // KEY=VALUE
	Entrypoint string            // override entrypoint
	Cmd        string            // override cmd
	// Docker health check
	HealthCmd         string
	HealthInterval    string // e.g. "30s"
	HealthTimeout     string // e.g. "10s"
	HealthRetries     int    // e.g. 3
	HealthStartPeriod string // e.g. "40s"
}

// Run creates and starts a container.
func (d *Docker) Run(opts RunOpts) (string, error) {
	var args []string
	args = append(args, "docker", "run", "-d")

	if opts.Name != "" {
		args = append(args, "--name", ssh.ShellQuote(opts.Name))
	}
	if opts.Network != "" {
		args = append(args, "--network", ssh.ShellQuote(opts.Network))
	}
	if opts.Restart != "" {
		args = append(args, "--restart", ssh.ShellQuote(opts.Restart))
	}
	for _, p := range opts.Ports {
		args = append(args, "-p", ssh.ShellQuote(p))
	}
	for _, v := range opts.Volumes {
		args = append(args, "-v", ssh.ShellQuote(v))
	}
	for k, v := range opts.Env {
		args = append(args, "-e", ssh.ShellQuote(fmt.Sprintf("%s=%s", k, v)))
	}
	if opts.Entrypoint != "" {
		args = append(args, "--entrypoint", ssh.ShellQuote(opts.Entrypoint))
	}
	if opts.HealthCmd != "" {
		args = append(args, "--health-cmd", ssh.ShellQuote(opts.HealthCmd))
		if opts.HealthInterval != "" {
			args = append(args, "--health-interval", opts.HealthInterval)
		}
		if opts.HealthTimeout != "" {
			args = append(args, "--health-timeout", opts.HealthTimeout)
		}
		if opts.HealthRetries > 0 {
			args = append(args, "--health-retries", fmt.Sprintf("%d", opts.HealthRetries))
		}
		if opts.HealthStartPeriod != "" {
			args = append(args, "--health-start-period", opts.HealthStartPeriod)
		}
	}
	args = append(args, ssh.ShellQuote(opts.Image))
	if opts.Cmd != "" {
		args = append(args, opts.Cmd)
	}

	return d.exec.Run(strings.Join(args, " "))
}

// Stop stops a container.
func (d *Docker) Stop(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker stop %s", ssh.ShellQuote(name)))
}

// Start starts a stopped container.
func (d *Docker) Start(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker start %s", ssh.ShellQuote(name)))
}

// Restart restarts a container.
func (d *Docker) Restart(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker restart %s", ssh.ShellQuote(name)))
}

// Remove removes a container (force).
func (d *Docker) Remove(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker rm -f %s", ssh.ShellQuote(name)))
}

// Rename renames a container.
func (d *Docker) Rename(oldName, newName string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker rename %s %s", ssh.ShellQuote(oldName), ssh.ShellQuote(newName)))
}

// Logs streams container logs.
func (d *Docker) Logs(name string, tail int, follow bool, w io.Writer) error {
	cmd := fmt.Sprintf("docker logs --tail %d", tail)
	if follow {
		cmd += " -f"
	}
	cmd += " " + ssh.ShellQuote(name)
	return d.exec.Stream(cmd, w)
}

// IsRunning checks if a container is running.
func (d *Docker) IsRunning(name string) bool {
	out, err := d.exec.Run(fmt.Sprintf(
		"docker inspect -f '{{.State.Running}}' %s 2>/dev/null", ssh.ShellQuote(name),
	))
	return err == nil && strings.TrimSpace(out) == "true"
}

// IsPortOpen checks if the container is accepting TCP connections on the given port.
// It resolves the container's Docker network IP and tests connectivity from the server,
// which mirrors exactly what Caddy does when proxying to the container.
func (d *Docker) IsPortOpen(name string, port int) bool {
	ip, err := d.exec.Run(fmt.Sprintf(
		"docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' %s 2>/dev/null",
		ssh.ShellQuote(name),
	))
	if err != nil || strings.TrimSpace(ip) == "" {
		return false
	}
	return d.exec.RunQuiet(fmt.Sprintf(
		"timeout 2 bash -c '</dev/tcp/%s/%d' 2>/dev/null",
		strings.TrimSpace(ip), port,
	)) == nil
}

// HTTPHealthOpts configures the HTTP health check with Docker-compatible semantics.
type HTTPHealthOpts struct {
	Path        string        // HTTP path — must be non-empty to run the check
	Interval    time.Duration // poll interval (default 10s)
	Timeout     time.Duration // per-request curl timeout (default 5s)
	Retries     int           // consecutive failures before unhealthy (default 3)
	StartPeriod time.Duration // grace period where failures are ignored (default 0)
}

// HTTPHealthCheck polls containerName:port/path until it returns 2xx or becomes unhealthy.
// Mirrors Docker healthcheck semantics: failures during start_period are ignored;
// after start_period, Retries consecutive non-2xx responses trigger failure.
// Returns nil on first 2xx. Returns error after Retries consecutive failures post-start_period.
// Bounded execution: worst case = start_period + (retries × interval).
func (d *Docker) HTTPHealthCheck(containerName string, port int, opts HTTPHealthOpts) error {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Retries <= 0 {
		opts.Retries = 3
	}

	ip, err := d.exec.Run(fmt.Sprintf(
		"docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' %s 2>/dev/null",
		ssh.ShellQuote(containerName),
	))
	if err != nil || strings.TrimSpace(ip) == "" {
		return fmt.Errorf("cannot resolve container IP for %s", containerName)
	}

	url := fmt.Sprintf("http://%s:%d%s", strings.TrimSpace(ip), port, opts.Path)
	curlCmd := fmt.Sprintf(
		"curl -s --max-time %d -o /dev/null -w '%%{http_code}' %s 2>/dev/null",
		int(opts.Timeout.Seconds()), ssh.ShellQuote(url),
	)

	startDeadline := time.Now().Add(opts.StartPeriod)
	consecutive := 0
	var lastCode string

	for {
		out, _ := d.exec.Run(curlCmd)
		code := strings.TrimSpace(out)

		if len(code) == 3 && code[0] == '2' {
			return nil // healthy
		}
		lastCode = code

		if !time.Now().Before(startDeadline) { // past start_period
			consecutive++
			if consecutive >= opts.Retries {
				if lastCode == "" || lastCode == "000" {
					return fmt.Errorf("no HTTP response after %d checks", opts.Retries)
				}
				return fmt.Errorf("HTTP %s for %d consecutive checks", lastCode, opts.Retries)
			}
		}
		time.Sleep(opts.Interval)
	}
}

// ContainerStatus returns the container status string.
func (d *Docker) ContainerStatus(name string) string {
	out, err := d.exec.Run(fmt.Sprintf(
		"docker inspect -f '{{.State.Status}}' %s 2>/dev/null", ssh.ShellQuote(name),
	))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// VolumeList lists Docker volumes.
func (d *Docker) VolumeList() (string, error) {
	return d.exec.Run("docker volume ls --format '{{.Name}}\t{{.Driver}}'")
}

// VolumeSize returns the disk usage of a volume.
func (d *Docker) VolumeSize(name string) (string, error) {
	return d.exec.Run(fmt.Sprintf(
		"docker system df -v 2>/dev/null | grep -F %s | awk '{print $NF}' || echo 'N/A'",
		ssh.ShellQuote(name),
	))
}

// PruneImages removes dangling images and old versioned images for the given
// app prefix (e.g. "neo-myapp"), keeping only the current image tag.
// Errors are silently ignored — pruning is best-effort and must not block deploys.
func (d *Docker) PruneImages(appPrefix, keepTag string) {
	// 1. Remove dangling images (untagged layers left over from builds/loads)
	d.exec.RunQuiet("docker image prune -f")

	// 2. Remove old versioned images for this app, keeping the current one
	// List all image IDs+tags for this app prefix, then delete anything that isn't keepTag
	out, err := d.exec.Run(fmt.Sprintf(
		"docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | grep %s",
		ssh.ShellQuote("^"+appPrefix+":"),
	))
	if err != nil || out == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		tag, id := parts[0], parts[1]
		if tag == keepTag {
			continue // keep current image
		}
		d.exec.RunQuiet(fmt.Sprintf("docker rmi %s 2>/dev/null || true", ssh.ShellQuote(id)))
	}
}

// RemoveVolume removes a Docker volume.
func (d *Docker) RemoveVolume(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker volume rm %s", ssh.ShellQuote(name)))
}

// Stats returns a one-shot snapshot of container resource usage.
func (d *Docker) Stats(format string) (string, error) {
	cmd := fmt.Sprintf("docker stats --no-stream --format %s 2>/dev/null", ssh.ShellQuote(format))
	return d.exec.Run(cmd)
}

// Exec runs a command inside a running container.
func (d *Docker) Exec(container, cmd string) (string, error) {
	return d.exec.Run(fmt.Sprintf("docker exec %s sh -c %s", ssh.ShellQuote(container), ssh.ShellQuote(cmd)))
}

// Build builds an image from a build context directory on the remote server.
func (d *Docker) Build(contextDir, dockerfile, tag string, w io.Writer) error {
	cmd := fmt.Sprintf("DOCKER_BUILDKIT=1 docker build -t %s -f %s %s", ssh.ShellQuote(tag), ssh.ShellQuote(dockerfile), ssh.ShellQuote(contextDir))
	return d.exec.Stream(cmd, w)
}

// LoadImage loads a Docker image by streaming a tar archive into docker load.
func (d *Docker) LoadImage(r io.Reader) (string, error) {
	return d.exec.StreamInput("docker load", r)
}

// LoadImageGzipped loads a gzip-compressed Docker image from a reader.
// The stream is decompressed server-side before being piped into docker load.
func (d *Docker) LoadImageGzipped(r io.Reader) (string, error) {
	return d.exec.StreamInput("gunzip | docker load", r)
}

// Tag tags a Docker image.
func (d *Docker) Tag(src, dst string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker tag %s %s", ssh.ShellQuote(src), ssh.ShellQuote(dst)))
}

// CopyVolume copies data between a Docker volume and a host path.
func (d *Docker) CopyVolume(volumeName, hostPath string) error {
	return d.exec.RunQuiet(fmt.Sprintf(
		"docker run --rm -v %s:/src -v %s:/dst alpine cp -a /src/. /dst/",
		ssh.ShellQuote(volumeName), ssh.ShellQuote(hostPath),
	))
}

// RunningContainers returns a list of running container names (excluding neo-managed ones).
func (d *Docker) RunningContainers() []string {
	out, err := d.exec.Run(`docker ps --format '{{.Names}}' 2>/dev/null`)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var result []string
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		name = strings.TrimSpace(name)
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

// StopAll stops all currently running containers.
func (d *Docker) StopAll(names []string) error {
	if len(names) == 0 {
		return nil
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = ssh.ShellQuote(n)
	}
	_, err := d.exec.Run("docker stop " + strings.Join(quoted, " "))
	return err
}
