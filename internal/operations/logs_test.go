package operations

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logStateJSON has one app with a worker and a sidecar, plus a shared service —
// enough to exercise every target shape containerForTarget resolves.
const logStateJSON = `{
  "apps": {
    "ghost": {
      "status": "running",
      "image": "ghost:5",
      "workers": {"queue": {"status": "running"}},
      "sidecars": {"redis": {"status": "running", "image": "redis:7"}}
    }
  },
  "services": {"mysql": {"status": "running", "image": "mysql:8"}}
}`

// logExecutor is a fake Executor whose Stream drives the caller's line writer
// with scripted lines and then, in follow mode, blocks until ctx is cancelled —
// exactly how a real `docker logs -f` session behaves. It records the command it
// was given and tracks how many streams are currently live so a test can prove a
// cancelled stream leaks nothing.
type logExecutor struct {
	user      string
	stateData []byte
	stateErr  error
	lines     []string

	gotCmd    string
	closed    atomic.Bool
	liveStrms atomic.Int32

	mu       sync.Mutex
	streamed bool
}

func (e *logExecutor) Run(context.Context, string) (string, error) { return "", nil }

func (e *logExecutor) Stream(ctx context.Context, cmd string, w io.Writer) error {
	e.mu.Lock()
	e.gotCmd = cmd
	e.streamed = true
	e.mu.Unlock()

	e.liveStrms.Add(1)
	defer e.liveStrms.Add(-1)

	for _, ln := range e.lines {
		if _, err := io.WriteString(w, ln+"\n"); err != nil {
			return err
		}
	}
	// A follow stream stays open until the context is torn down; a plain tail
	// returns as soon as the backlog has been written.
	if strings.Contains(cmd, " -f ") || strings.HasSuffix(cmd, " -f") {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (e *logExecutor) ReadFileElevated(context.Context, string) ([]byte, error) {
	return e.stateData, e.stateErr
}

func (e *logExecutor) WriteFileElevated(context.Context, string, []byte, os.FileMode) error {
	return nil
}

func (e *logExecutor) User() string { return e.user }
func (e *logExecutor) Close() error { e.closed.Store(true); return nil }
func (e *logExecutor) command() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gotCmd
}

func TestStreamLogsRecentTailEmitsLinesAndCloses(t *testing.T) {
	exec := &logExecutor{
		user:      "root",
		stateData: []byte(logStateJSON),
		lines:     []string{"boot", "ready", "serving"},
	}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	var got []string
	err := svc.StreamLogs(context.Background(),
		LogOptions{Server: "production", Target: "ghost"},
		func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
	if strings.Join(got, ",") != "boot,ready,serving" {
		t.Errorf("lines = %v", got)
	}
	if !exec.closed.Load() {
		t.Errorf("connection must be closed after the stream ends")
	}
	// Default tail and the resolved container name.
	if cmd := exec.command(); !strings.Contains(cmd, "--tail 200") || !strings.Contains(cmd, "app-ghost") {
		t.Errorf("unexpected command: %q", cmd)
	}
}

func TestStreamLogsFollowCancelsWithoutLeakingSession(t *testing.T) {
	exec := &logExecutor{
		user:      "root",
		stateData: []byte(logStateJSON),
		lines:     []string{"line-1"},
	}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- svc.StreamLogs(ctx,
			LogOptions{Server: "production", Target: "ghost", Follow: true},
			func(string) {})
	}()

	// Wait for the follow stream to actually be live, then cancel it.
	waitFor(t, func() bool { return exec.liveStrms.Load() == 1 })
	cancel()

	select {
	case err := <-done:
		var opErr *Error
		if !errors.As(err, &opErr) || opErr.Code != ErrOperationCancelled {
			t.Fatalf("want operation_cancelled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamLogs did not return after cancellation")
	}

	// The SSH session must have been torn down and the connection closed — no leak.
	waitFor(t, func() bool { return exec.liveStrms.Load() == 0 })
	if !exec.closed.Load() {
		t.Errorf("connection must be closed after cancellation")
	}
}

func TestStreamLogsClampsTail(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"zero-defaults", 0, "--tail 200"},
		{"over-cap", 99_999, "--tail 5000"},
		{"passthrough", 500, "--tail 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &logExecutor{user: "root", stateData: []byte(logStateJSON)}
			svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
			if err := svc.StreamLogs(context.Background(),
				LogOptions{Server: "production", Target: "ghost", Tail: tc.in},
				func(string) {}); err != nil {
				t.Fatalf("StreamLogs: %v", err)
			}
			if !strings.Contains(exec.command(), tc.want) {
				t.Errorf("tail %d → command %q, want %q", tc.in, exec.command(), tc.want)
			}
		})
	}
}

func TestStreamLogsResolvesEveryTargetShape(t *testing.T) {
	cases := map[string]string{
		"ghost":               "app-ghost",
		"ghost/worker/queue":  "app-ghost-worker-queue",
		"ghost/sidecar/redis": "svc-ghost-redis",
		"service/mysql":       "svc-mysql",
	}
	for target, container := range cases {
		t.Run(target, func(t *testing.T) {
			exec := &logExecutor{user: "root", stateData: []byte(logStateJSON)}
			svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
			if err := svc.StreamLogs(context.Background(),
				LogOptions{Server: "production", Target: target}, func(string) {}); err != nil {
				t.Fatalf("StreamLogs(%q): %v", target, err)
			}
			// The container is shell-quoted, so match the bare token.
			if !strings.Contains(exec.command(), container) {
				t.Errorf("target %q → command %q, want container %q", target, exec.command(), container)
			}
		})
	}
}

func TestStreamLogsUnknownTargetIsAppNotFound(t *testing.T) {
	exec := &logExecutor{user: "root", stateData: []byte(logStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	err := svc.StreamLogs(context.Background(),
		LogOptions{Server: "production", Target: "does-not-exist"}, func(string) {})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrAppNotFound {
		t.Fatalf("want app_not_found, got %v", err)
	}
	if exec.streamed {
		t.Errorf("must not open a stream for an unresolved target")
	}
	if !exec.closed.Load() {
		t.Errorf("connection must still be closed on a resolution failure")
	}
}

func TestStreamLogsServerNotFound(t *testing.T) {
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{})
	err := svc.StreamLogs(context.Background(),
		LogOptions{Server: "nope", Target: "ghost"}, func(string) {})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %v", err)
	}
}

func TestStreamLogsNonRootUsesSudo(t *testing.T) {
	exec := &logExecutor{user: "deploy", stateData: []byte(logStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	if err := svc.StreamLogs(context.Background(),
		LogOptions{Server: "production", Target: "ghost"}, func(string) {}); err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
	if !strings.HasPrefix(exec.command(), "sudo docker logs") {
		t.Errorf("non-root command must use sudo: %q", exec.command())
	}
}

func TestLineWriterSplitsAndTrimsCR(t *testing.T) {
	var got []string
	w := &lineWriter{emit: func(s string) { got = append(got, s) }, max: maxLogLineBytes}
	// Arrives in arbitrary chunks, including a CRLF and a split line.
	_, _ = w.Write([]byte("alpha\r\nbra"))
	_, _ = w.Write([]byte("vo\ncharlie"))
	if strings.Join(got, "|") != "alpha|bravo" {
		t.Fatalf("mid-stream lines = %v", got)
	}
	w.flush() // trailing partial with no newline
	if strings.Join(got, "|") != "alpha|bravo|charlie" {
		t.Errorf("after flush = %v", got)
	}
}

func TestClampTail(t *testing.T) {
	for in, want := range map[int]int{0: DefaultLogTail, -5: DefaultLogTail, 10: 10, MaxLogTail + 1: MaxLogTail} {
		if got := ClampTail(in); got != want {
			t.Errorf("ClampTail(%d) = %d, want %d", in, got, want)
		}
	}
}

// waitFor spins until cond is true or the deadline elapses. Used to synchronise
// on the background stream goroutine without a fixed sleep.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
