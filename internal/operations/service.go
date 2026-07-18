package operations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vxero/neo/internal/config"
)

// Default operation deadlines (plan "Snapshot collection").
const (
	// DefaultConnectTimeout bounds establishing the SSH connection.
	DefaultConnectTimeout = 12 * time.Second
	// DefaultSnapshotTimeout bounds the whole snapshot, connection included.
	DefaultSnapshotTimeout = 15 * time.Second
)

// Service is the shared operation surface. It is constructed once with its
// dependencies and is safe to reuse across requests; it holds no per-request
// state.
type Service struct {
	cfg       ConfigStore
	connector Connector
	clock     Clock

	connectTimeout  time.Duration
	snapshotTimeout time.Duration
}

// Options tunes a Service. Zero values fall back to the Default* deadlines.
type Options struct {
	ConnectTimeout  time.Duration
	SnapshotTimeout time.Duration
}

// NewService wires a Service. A nil clock defaults to the system clock.
func NewService(cfg ConfigStore, connector Connector, clock Clock, opts Options) *Service {
	if clock == nil {
		clock = SystemClock()
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = DefaultConnectTimeout
	}
	if opts.SnapshotTimeout <= 0 {
		opts.SnapshotTimeout = DefaultSnapshotTimeout
	}
	return &Service{
		cfg:             cfg,
		connector:       connector,
		clock:           clock,
		connectTimeout:  opts.ConnectTimeout,
		snapshotTimeout: opts.SnapshotTimeout,
	}
}

// ListServers returns every configured server, sorted by name, with the active
// one flagged Current. It is the backing for the bridge's `server.list`. There
// is no second server registry — this is the same ~/.neo/config.json the CLI
// uses.
func (s *Service) ListServers(ctx context.Context) ([]ServerSummary, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return nil, newError(ErrInternal, "could not read configuration", false, err, nil)
	}

	out := make([]ServerSummary, 0, len(cfg.Servers))
	for _, srv := range cfg.Servers {
		out = append(out, ServerSummary{
			ID:      srv.Name,
			Name:    srv.Name,
			Host:    srv.Host,
			Current: srv.Name == cfg.Current,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Snapshot connects to the named server and collects a typed health snapshot,
// applying the connect and snapshot deadlines. A connection failure is returned
// as a typed *Error with a stable code (server_not_found, ssh_*, or
// operation_timeout); once connected, per-metric failures degrade gracefully
// (see collectSnapshot) rather than failing the whole call.
func (s *Service) Snapshot(ctx context.Context, serverName string) (Snapshot, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return Snapshot{}, newError(ErrInternal, "could not read configuration", false, err, nil)
	}

	srv, ok := lookupServer(cfg, serverName)
	if !ok {
		return Snapshot{}, newError(ErrServerNotFound,
			fmt.Sprintf("server %q is not configured", serverName), false, nil,
			map[string]interface{}{"server": serverName})
	}

	summary := ServerSummary{
		ID:      srv.Name,
		Name:    srv.Name,
		Host:    srv.Host,
		Current: srv.Name == cfg.Current,
	}

	sctx, cancel := context.WithTimeout(ctx, s.snapshotTimeout)
	defer cancel()

	cctx, ccancel := context.WithTimeout(sctx, s.connectTimeout)
	exec, err := s.connector.Connect(cctx, srv)
	ccancel()
	if err != nil {
		// Return the summary so the UI can still label the (offline) server.
		return Snapshot{Server: summary, ObservedAt: s.clock.Now().UTC(), Containers: []ContainerStat{}},
			classifyConnectError(err, srv.Name)
	}
	defer exec.Close()

	return collectSnapshot(sctx, exec, summary, s.clock), nil
}

// lookupServer resolves a server by its config name.
func lookupServer(cfg *config.Config, name string) (config.Server, bool) {
	srv, ok := cfg.Servers[name]
	return srv, ok
}

// classifyConnectError maps an opaque SSH dial/handshake error onto a stable
// operation ErrorCode by inspecting its text. The UI branches on the code, so
// getting this mapping right is what lets the desktop show a precise
// "unknown host" / "auth failed" / "unreachable" message.
func classifyConnectError(err error, serverName string) *Error {
	msg := strings.ToLower(err.Error())
	details := map[string]interface{}{"server": serverName}

	switch {
	case strings.Contains(msg, "knownhosts") ||
		strings.Contains(msg, "unknown host") ||
		strings.Contains(msg, "host key"):
		return newError(ErrSSHUnknownHost, "the server's host key is not trusted", false, err, details)

	case strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "handshake failed"):
		return newError(ErrSSHAuthFailed, "authentication was rejected", false, err, details)

	case strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "deadline exceeded"):
		return newError(ErrOperationTimeout, "connecting to the server timed out", true, err, details)

	case strings.Contains(msg, "context canceled"):
		return newError(ErrOperationCancelled, "the operation was cancelled", false, err, details)

	default:
		// Connection refused, no route to host, DNS failure, etc.
		return newError(ErrSSHUnreachable, "the server could not be reached", true, err, details)
	}
}
