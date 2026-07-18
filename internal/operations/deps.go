package operations

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
)

// Executor is the minimal, context-aware remote command surface the operation
// layer needs. It is deliberately narrower than *ssh.Executor so unit tests can
// supply a fake without a live SSH server (plan "Service dependencies"). Every
// method honours ctx's deadline and cancellation.
type Executor interface {
	Run(ctx context.Context, command string) (string, error)
	Stream(ctx context.Context, command string, output io.Writer) error
	ReadFileElevated(ctx context.Context, path string) ([]byte, error)
	// WriteFileElevated installs data at a possibly root-only path (e.g.
	// /etc/neo/state.json) under ctx's deadline. Lifecycle actions need it to
	// persist the new workload status after start/stop/restart.
	WriteFileElevated(ctx context.Context, path string, data []byte, mode os.FileMode) error
	// User is the remote SSH username; the collector needs it to decide whether
	// Docker requires a sudo prefix.
	User() string
	Close() error
}

// Connector opens an Executor for a server. The real implementation dials SSH;
// tests provide a fake that hands back a canned Executor.
type Connector interface {
	Connect(ctx context.Context, server config.Server) (Executor, error)
}

// ConfigStore reads the local Neo configuration (~/.neo/config.json). It is an
// interface so tests can inject fixed servers without touching the filesystem.
type ConfigStore interface {
	Load() (*config.Config, error)
}

// Clock supplies the current time. Injecting it keeps snapshot timestamps and
// latency measurements deterministic under test.
type Clock interface {
	Now() time.Time
}

// --- default implementations ---------------------------------------------

// ConfigLoader adapts a plain loader func (e.g. config.Load) to a ConfigStore.
func ConfigLoader(fn func() (*config.Config, error)) ConfigStore {
	return configLoaderFunc(fn)
}

type configLoaderFunc func() (*config.Config, error)

func (f configLoaderFunc) Load() (*config.Config, error) { return f() }

type systemClock struct{}

// SystemClock returns a Clock backed by time.Now.
func SystemClock() Clock { return systemClock{} }

func (systemClock) Now() time.Time { return time.Now() }

// sshExecutor adapts *ssh.Executor to the context-aware Executor interface by
// delegating to the ...Context methods added in internal/ssh/context.go.
type sshExecutor struct{ e *ssh.Executor }

// NewSSHExecutor wraps an already-connected *ssh.Executor as an operations
// Executor. Used by the CLI, which resolves and connects interactively before
// handing the live connection to the shared collector.
func NewSSHExecutor(e *ssh.Executor) Executor { return &sshExecutor{e: e} }

func (x *sshExecutor) Run(ctx context.Context, cmd string) (string, error) {
	return x.e.RunContext(ctx, cmd)
}

func (x *sshExecutor) Stream(ctx context.Context, cmd string, w io.Writer) error {
	return x.e.StreamContext(ctx, cmd, w)
}

func (x *sshExecutor) ReadFileElevated(ctx context.Context, path string) ([]byte, error) {
	return x.e.ReadFileElevatedContext(ctx, path)
}

func (x *sshExecutor) WriteFileElevated(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	return x.e.WriteFileElevatedContext(ctx, path, data, mode)
}

func (x *sshExecutor) User() string { return x.e.User() }

func (x *sshExecutor) Close() error { return x.e.Close() }

// SSHConnector is the production Connector. It dials SSH in non-interactive mode
// so a background bridge NEVER prompts on stdin for an unknown host key,
// password, or selection (a hard requirement of the sidecar).
type SSHConnector struct{}

// NewSSHConnector returns the production SSH-backed Connector.
func NewSSHConnector() Connector { return &SSHConnector{} }

func (c *SSHConnector) Connect(ctx context.Context, server config.Server) (Executor, error) {
	e := ssh.New(server.Host, server.Port)
	e.NonInteractive = true // never prompt: unknown hosts are rejected, not queried
	if server.Key != "" {
		if data, err := os.ReadFile(server.Key); err == nil {
			e.PrivateKey = data
		}
	}
	if err := e.ConnectContext(ctx); err != nil {
		return nil, err
	}
	return &sshExecutor{e: e}, nil
}
