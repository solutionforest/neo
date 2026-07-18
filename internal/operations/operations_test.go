package operations

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vxero/neo/internal/config"
)

// --- fakes ----------------------------------------------------------------

type fakeClock struct{ t time.Time }

func (c fakeClock) Now() time.Time { return c.t }

type fakeConfigStore struct {
	cfg *config.Config
	err error
}

func (f fakeConfigStore) Load() (*config.Config, error) { return f.cfg, f.err }

type fakeConnector struct {
	exec      Executor
	err       error
	gotServer config.Server
}

func (c *fakeConnector) Connect(_ context.Context, srv config.Server) (Executor, error) {
	c.gotServer = srv
	if c.err != nil {
		return nil, c.err
	}
	return c.exec, nil
}

// fakeExecutor answers the exact commands the collector issues. Anything else
// returns empty output so unexpected commands never masquerade as data.
type fakeExecutor struct {
	user string

	pingErr error

	vmOut string
	vmErr error

	dockerOut string
	dockerErr error

	stateData []byte
	stateErr  error

	closed bool
}

func (f *fakeExecutor) Run(_ context.Context, cmd string) (string, error) {
	switch {
	case cmd == "true":
		return "", f.pingErr
	case cmd == vmMetricsScript:
		return f.vmOut, f.vmErr
	case strings.Contains(cmd, "stats --no-stream"):
		return f.dockerOut, f.dockerErr
	default:
		return "", nil
	}
}

func (f *fakeExecutor) Stream(_ context.Context, _ string, _ io.Writer) error { return nil }

func (f *fakeExecutor) ReadFileElevated(_ context.Context, _ string) ([]byte, error) {
	return f.stateData, f.stateErr
}

func (f *fakeExecutor) WriteFileElevated(_ context.Context, _ string, _ []byte, _ os.FileMode) error {
	return nil
}

func (f *fakeExecutor) User() string { return f.user }

func (f *fakeExecutor) Close() error { f.closed = true; return nil }

func newTestService(store ConfigStore, conn Connector) *Service {
	return NewService(store, conn, fakeClock{t: time.Unix(1_700_000_000, 0).UTC()}, Options{})
}

func twoServerConfig() *config.Config {
	return &config.Config{
		Current: "production",
		Servers: map[string]config.Server{
			"production": {Name: "production", Host: "root@10.0.0.1", Port: 22},
			"staging":    {Name: "staging", Host: "root@10.0.0.2", Port: 22},
		},
	}
}

// --- ListServers ----------------------------------------------------------

func TestListServersSortedWithCurrentFlag(t *testing.T) {
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{})
	got, err := svc.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 servers, got %d", len(got))
	}
	// Sorted by name: production before staging.
	if got[0].Name != "production" || got[1].Name != "staging" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[0].ID != "production" || got[0].Host != "root@10.0.0.1" {
		t.Errorf("summary fields wrong: %+v", got[0])
	}
	if !got[0].Current {
		t.Errorf("production should be Current")
	}
	if got[1].Current {
		t.Errorf("staging should not be Current")
	}
}

func TestListServersConfigError(t *testing.T) {
	svc := newTestService(fakeConfigStore{err: errors.New("boom")}, &fakeConnector{})
	_, err := svc.ListServers(context.Background())
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrInternal {
		t.Fatalf("want internal_error, got %v", err)
	}
}

// --- Snapshot -------------------------------------------------------------

const validStateJSON = `{"apps":{"ghost":{"status":"running"},"blog":{"status":"stopped"}},"services":{"db":{"status":"running"}}}`

func TestSnapshotCollectsTypedMetrics(t *testing.T) {
	exec := &fakeExecutor{
		user:      "root",
		vmOut:     "CPU:42.5\nMEM:1073741824/2147483648\nDISK:1048576/2097152\nUPTIME:3600",
		dockerOut: "app-ghost\t12.50%\t50MiB / 512MiB",
		stateData: []byte(validStateJSON),
	}
	conn := &fakeConnector{exec: exec}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, conn)

	snap, err := svc.Snapshot(context.Background(), "production")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if conn.gotServer.Name != "production" {
		t.Errorf("connected to wrong server: %+v", conn.gotServer)
	}
	if !snap.Reachable {
		t.Errorf("want reachable")
	}
	if snap.Server.Current != true || snap.Server.ID != "production" {
		t.Errorf("server summary wrong: %+v", snap.Server)
	}
	if snap.CPUPercent == nil || *snap.CPUPercent != 42.5 {
		t.Errorf("cpu = %v, want 42.5", deref(snap.CPUPercent))
	}
	if snap.RAMUsedBytes == nil || *snap.RAMUsedBytes != 1073741824 {
		t.Errorf("ram used = %v", derefU(snap.RAMUsedBytes))
	}
	if snap.RAMTotalBytes == nil || *snap.RAMTotalBytes != 2147483648 {
		t.Errorf("ram total = %v", derefU(snap.RAMTotalBytes))
	}
	// df -kP reports 1024-byte blocks → bytes = blocks * 1024.
	if snap.DiskUsedBytes == nil || *snap.DiskUsedBytes != 1048576*1024 {
		t.Errorf("disk used = %v", derefU(snap.DiskUsedBytes))
	}
	if snap.UptimeSeconds == nil || *snap.UptimeSeconds != 3600 {
		t.Errorf("uptime = %v", derefU(snap.UptimeSeconds))
	}
	if snap.Apps != (WorkloadCounts{Running: 1, Stopped: 1, Total: 2}) {
		t.Errorf("apps = %+v", snap.Apps)
	}
	if snap.Services != (WorkloadCounts{Running: 1, Stopped: 0, Total: 1}) {
		t.Errorf("services = %+v", snap.Services)
	}
	if len(snap.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(snap.Containers))
	}
	c := snap.Containers[0]
	if c.Name != "app-ghost" || c.CPUPercent == nil || *c.CPUPercent != 12.5 {
		t.Errorf("container cpu wrong: %+v", c)
	}
	if c.MemUsedBytes == nil || *c.MemUsedBytes != 50*1024*1024 {
		t.Errorf("container mem used wrong: %v", derefU(c.MemUsedBytes))
	}
	if !exec.closed {
		t.Errorf("executor must be closed after snapshot")
	}
}

func TestSnapshotMissingMetricsAreNilNotZero(t *testing.T) {
	// Platform commands unavailable → NA sentinels; Docker down → error.
	exec := &fakeExecutor{
		user:      "root",
		vmOut:     "CPU:NA\nMEM:NA\nDISK:NA\nUPTIME:NA",
		dockerErr: errors.New("docker: command not found"),
		stateErr:  errors.New("no state file"),
	}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	snap, err := svc.Snapshot(context.Background(), "production")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.Reachable {
		t.Errorf("still reachable even with no metrics")
	}
	for name, p := range map[string]interface{}{
		"cpu": snap.CPUPercent, "ramUsed": snap.RAMUsedBytes, "ramTotal": snap.RAMTotalBytes,
		"diskUsed": snap.DiskUsedBytes, "diskTotal": snap.DiskTotalBytes, "uptime": snap.UptimeSeconds,
	} {
		if !isNilPtr(p) {
			t.Errorf("%s must be nil (unavailable), not zero", name)
		}
	}
	if snap.Containers == nil || len(snap.Containers) != 0 {
		t.Errorf("containers must be empty slice, got %+v", snap.Containers)
	}
	// State unreadable → zero counts, not an error.
	if snap.Apps.Total != 0 {
		t.Errorf("apps should be zero, got %+v", snap.Apps)
	}
}

func TestSnapshotPartialDockerFailureKeepsMetrics(t *testing.T) {
	exec := &fakeExecutor{
		user:      "root",
		vmOut:     "CPU:5\nMEM:100/200\nDISK:1/2\nUPTIME:10",
		dockerErr: errors.New("docker daemon unreachable"),
		stateData: []byte(validStateJSON),
	}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	snap, err := svc.Snapshot(context.Background(), "production")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.CPUPercent == nil || snap.RAMUsedBytes == nil {
		t.Errorf("valid VM metrics were discarded by a docker failure")
	}
	if len(snap.Containers) != 0 {
		t.Errorf("containers should be empty on docker failure")
	}
}

func TestSnapshotServerNotFound(t *testing.T) {
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{})
	_, err := svc.Snapshot(context.Background(), "does-not-exist")
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %v", err)
	}
	if opErr.Details["server"] != "does-not-exist" {
		t.Errorf("details.server missing: %+v", opErr.Details)
	}
}

func TestSnapshotConnectErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want ErrorCode
	}{
		{"auth", "ssh: handshake failed: unable to authenticate", ErrSSHAuthFailed},
		{"unknownhost", "knownhosts: key is unknown", ErrSSHUnknownHost},
		{"timeout", "dial tcp 10.0.0.1:22: i/o timeout", ErrOperationTimeout},
		{"refused", "dial tcp 10.0.0.1:22: connect: connection refused", ErrSSHUnreachable},

		// SSH edge cases from the plan's manual matrix. Every one must land on a
		// stable code the UI can branch on — never leak the raw SSH text.
		// An encrypted key the non-interactive bridge cannot unlock, or a missing
		// key, both surface as "no supported methods" / auth rejection.
		{"encrypted_key", "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain", ErrSSHAuthFailed},
		{"missing_key", "ssh: handshake failed: unable to authenticate, no supported methods remain", ErrSSHAuthFailed},
		{"permission_denied", "ssh: handshake failed: ssh: permission denied", ErrSSHAuthFailed},
		// An unknown host must be rejected (strict known_hosts), not accepted.
		{"unknown_host_prompt", "unknown host key for 10.0.0.1 — run a command manually first to accept the key", ErrSSHUnknownHost},
		{"changed_host_key", "WARNING: HOST KEY HAS CHANGED for 10.0.0.1", ErrSSHUnknownHost},
		{"cancelled", "context canceled", ErrOperationCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConnector{err: errors.New(tc.err)}
			svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, conn)
			snap, err := svc.Snapshot(context.Background(), "production")
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.want {
				t.Fatalf("err = %v, want code %s", err, tc.want)
			}
			// The summary is still returned so the UI can label the offline server.
			if snap.Server.Name != "production" {
				t.Errorf("summary missing on connect failure: %+v", snap.Server)
			}
		})
	}
}

// --- parser units ---------------------------------------------------------

func TestParseByteSize(t *testing.T) {
	cases := map[string]uint64{
		"1GiB":   1 << 30,
		"512MiB": 512 << 20,
		"1.5GiB": uint64(1.5 * (1 << 30)),
		"1000":   1000,
		"2kB":    2000,
		"0B":     0,
	}
	for in, want := range cases {
		got, ok := parseByteSize(in)
		if !ok || got != want {
			t.Errorf("parseByteSize(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	if _, ok := parseByteSize("garbage"); ok {
		t.Errorf("garbage should not parse")
	}
}

func TestParseMemUsage(t *testing.T) {
	used, limit, ok := parseMemUsage("50MiB / 1.944GiB")
	var one uint64 = 1
	wantLimit := uint64(1.944 * float64(one<<30))
	if !ok || used != 50<<20 || limit != wantLimit {
		t.Errorf("parseMemUsage = %d/%d,%v want limit %d", used, limit, ok, wantLimit)
	}
	if _, _, ok := parseMemUsage("nope"); ok {
		t.Errorf("malformed mem usage should not parse")
	}
}

func TestApplyVMMetricsIgnoresGarbageLines(t *testing.T) {
	var snap Snapshot
	applyVMMetrics(&snap, "CPU:notanumber\nMEM:1/\ngibberish\nUPTIME:99")
	if snap.CPUPercent != nil {
		t.Errorf("bad cpu should stay nil")
	}
	if snap.RAMUsedBytes != nil {
		t.Errorf("half mem pair should stay nil")
	}
	if snap.UptimeSeconds == nil || *snap.UptimeSeconds != 99 {
		t.Errorf("valid uptime should still parse alongside garbage")
	}
}

// --- helpers --------------------------------------------------------------

func deref(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}
func derefU(p *uint64) int64 {
	if p == nil {
		return -1
	}
	return int64(*p)
}
func isNilPtr(v interface{}) bool {
	switch p := v.(type) {
	case *float64:
		return p == nil
	case *uint64:
		return p == nil
	default:
		return false
	}
}
