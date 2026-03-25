package remote

import (
	"fmt"
	"io"
	"strings"

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

// RemoveVolume removes a Docker volume.
func (d *Docker) RemoveVolume(name string) error {
	return d.exec.RunQuiet(fmt.Sprintf("docker volume rm %s", ssh.ShellQuote(name)))
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
