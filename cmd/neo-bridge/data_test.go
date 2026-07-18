package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/operations"
)

// These tests drive the bridge's data methods end-to-end through a real
// operations.Service wired with fakes (the operation interfaces are exported),
// so the wire encoding, error mapping, and the operations plumbing are all
// exercised together.

type stubConfigStore struct {
	cfg *config.Config
	err error
}

func (s stubConfigStore) Load() (*config.Config, error) { return s.cfg, s.err }

type stubConnector struct {
	exec operations.Executor
	err  error
}

func (s stubConnector) Connect(_ context.Context, _ config.Server) (operations.Executor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.exec, nil
}

type stubExecutor struct{ user string }

func (e stubExecutor) Run(_ context.Context, cmd string) (string, error) {
	switch {
	case cmd == "true":
		return "", nil // reachable
	case strings.Contains(cmd, "stats --no-stream"):
		return "app-ghost\t3.20%\t20MiB / 512MiB", nil
	default: // the VM metrics script
		return "CPU:12\nMEM:100/200\nDISK:1/2\nUPTIME:60", nil
	}
}

func (e stubExecutor) Stream(_ context.Context, _ string, _ io.Writer) error { return nil }
func (e stubExecutor) ReadFileElevated(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{"apps":{"ghost":{"status":"running"}},"services":{}}`), nil
}
func (e stubExecutor) User() string { return e.user }
func (e stubExecutor) Close() error { return nil }

func serverWithOps(t *testing.T, store operations.ConfigStore, conn operations.Connector) *Server {
	t.Helper()
	ops := operations.NewService(store, conn, operations.SystemClock(), operations.Options{})
	return NewServer("1", "1", slog.New(slog.NewTextHandler(io.Discard, nil)), WithOperations(ops))
}

func drive(t *testing.T, srv *Server, input string) []Response {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return decodeResponses(t, out.Bytes())
}

func TestServerListMethod(t *testing.T) {
	cfg := &config.Config{
		Current: "production",
		Servers: map[string]config.Server{
			"production": {Name: "production", Host: "root@10.0.0.1", Port: 22},
			"staging":    {Name: "staging", Host: "root@10.0.0.2", Port: 22},
		},
	}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{})
	resp := drive(t, srv, `{"version":1,"id":"l1","method":"server.list"}`+"\n")

	if len(resp) != 1 || resp[0].Error != nil {
		t.Fatalf("unexpected server.list response: %+v", resp)
	}
	var servers []operations.ServerSummary
	remarshal(t, resp[0].Result, &servers)
	if len(servers) != 2 || servers[0].Name != "production" || !servers[0].Current {
		t.Fatalf("server.list result wrong: %+v", servers)
	}
}

func TestServerSnapshotMethod(t *testing.T) {
	cfg := &config.Config{
		Current: "production",
		Servers: map[string]config.Server{"production": {Name: "production", Host: "root@10.0.0.1", Port: 22}},
	}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{exec: stubExecutor{user: "root"}})
	resp := drive(t, srv, `{"version":1,"id":"s1","method":"server.snapshot","params":{"server":"production"}}`+"\n")

	if len(resp) != 1 || resp[0].Error != nil {
		t.Fatalf("unexpected snapshot response: %+v", resp)
	}
	var snap operations.Snapshot
	remarshal(t, resp[0].Result, &snap)
	if !snap.Reachable || snap.Server.Name != "production" {
		t.Fatalf("snapshot wrong: %+v", snap)
	}
	if snap.CPUPercent == nil || snap.Apps.Running != 1 {
		t.Fatalf("snapshot metrics wrong: cpu=%v apps=%+v", snap.CPUPercent, snap.Apps)
	}
	if len(snap.Containers) != 1 {
		t.Fatalf("want 1 container, got %+v", snap.Containers)
	}
}

func TestServerSnapshotMissingParam(t *testing.T) {
	srv := serverWithOps(t, stubConfigStore{cfg: &config.Config{Servers: map[string]config.Server{}}}, stubConnector{})
	resp := drive(t, srv, `{"version":1,"id":"s2","method":"server.snapshot"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing server, got %+v", resp)
	}
}

func TestServerSnapshotUnknownServerMapsToCode(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.Server{}}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{})
	resp := drive(t, srv, `{"version":1,"id":"s3","method":"server.snapshot","params":{"server":"ghost"}}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %+v", resp)
	}
}

func TestServerSnapshotConnectErrorMapsToSSHCode(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.Server{"production": {Name: "production", Host: "root@10.0.0.1", Port: 22}}}
	conn := stubConnector{err: errors.New("dial tcp 10.0.0.1:22: connect: connection refused")}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, conn)
	resp := drive(t, srv, `{"version":1,"id":"s4","method":"server.snapshot","params":{"server":"production"}}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrSSHUnreachable {
		t.Fatalf("want ssh_unreachable, got %+v", resp)
	}
	if !resp[0].Error.Retryable {
		t.Errorf("ssh_unreachable should be retryable")
	}
}

func TestAppListMethod(t *testing.T) {
	cfg := &config.Config{
		Current: "production",
		Servers: map[string]config.Server{"production": {Name: "production", Host: "root@10.0.0.1", Port: 22}},
	}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{exec: stubExecutor{user: "root"}})
	resp := drive(t, srv, `{"version":1,"id":"a1","method":"app.list","params":{"server":"production"}}`+"\n")

	if len(resp) != 1 || resp[0].Error != nil {
		t.Fatalf("unexpected app.list response: %+v", resp)
	}
	var apps []operations.AppSummary
	remarshal(t, resp[0].Result, &apps)
	if len(apps) != 1 {
		t.Fatalf("want 1 app row, got %+v", apps)
	}
	if apps[0].Name != "ghost" || apps[0].Kind != operations.KindApp || apps[0].State != operations.StateRunning {
		t.Fatalf("app.list row wrong: %+v", apps[0])
	}
}

func TestAppListMissingParam(t *testing.T) {
	srv := serverWithOps(t, stubConfigStore{cfg: &config.Config{Servers: map[string]config.Server{}}}, stubConnector{})
	resp := drive(t, srv, `{"version":1,"id":"a2","method":"app.list"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing server, got %+v", resp)
	}
}

func TestAppListUnknownServerMapsToCode(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.Server{}}
	srv := serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{})
	resp := drive(t, srv, `{"version":1,"id":"a3","method":"app.list","params":{"server":"ghost"}}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %+v", resp)
	}
}

func TestDataMethodsUnconfigured(t *testing.T) {
	// A server with no operations service must fail cleanly, not panic.
	srv := NewServer("1", "1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp := drive(t, srv, `{"version":1,"id":"n1","method":"server.list"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInternal {
		t.Fatalf("want internal_error when unconfigured, got %+v", resp)
	}
}
