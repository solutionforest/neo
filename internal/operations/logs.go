package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// Log streaming defaults (plan "Log streaming"). The bridge owns cancellation
// and backpressure; these bound how many lines a single subscription may ask
// for so a runaway `--tail` can never pull an unbounded backlog.
const (
	// DefaultLogTail is the recent backlog shown when a subscription does not
	// request a specific tail.
	DefaultLogTail = 200
	// MaxLogTail caps a requested tail so the initial backlog stays bounded.
	MaxLogTail = 5_000
	// maxLogLineBytes guards the line splitter against a single pathological
	// line with no newline growing without bound; such a chunk is flushed as-is.
	maxLogLineBytes = 64 * 1024
)

// LogOptions selects what to stream. Target is an AppSummary ID as returned by
// app.list (a plain app name, "<app>/worker/<w>", "<app>/sidecar/<s>", or
// "service/<name>") — it is validated against the real remote state before any
// container name is built, so a frontend-supplied string is never interpolated
// into a shell command unchecked.
type LogOptions struct {
	Server string
	Target string
	Tail   int
	Follow bool
}

// ClampTail applies the plan's tail policy: a non-positive request means "use
// the default recent backlog"; anything above the cap is clamped down.
func ClampTail(tail int) int {
	switch {
	case tail <= 0:
		return DefaultLogTail
	case tail > MaxLogTail:
		return MaxLogTail
	default:
		return tail
	}
}

// StreamLogs opens a log stream for one workload on one server and delivers each
// complete line to emit. It is the shared implementation behind the bridge's
// logs.subscribe method.
//
// Cancellation and lifetime (plan "Log streaming"): only the CONNECT phase is
// bounded by the connect deadline; the stream itself runs on ctx with no
// timeout, because follow mode is intentionally open-ended. The caller stops it
// by cancelling ctx (an unsubscribe, a closed window, or bridge shutdown), which
// tears down the SSH session and closes the connection here — no session is left
// running. StreamLogs blocks until the stream ends or ctx is cancelled.
func (s *Service) StreamLogs(ctx context.Context, opts LogOptions, emit func(string)) error {
	if emit == nil {
		emit = func(string) {}
	}

	cfg, err := s.cfg.Load()
	if err != nil {
		return newError(ErrInternal, "could not read configuration", false, err, nil)
	}
	srv, ok := lookupServer(cfg, opts.Server)
	if !ok {
		return newError(ErrServerNotFound,
			fmt.Sprintf("server %q is not configured", opts.Server), false, nil,
			map[string]interface{}{"server": opts.Server})
	}

	// Bound ONLY the connection with the connect deadline; the live stream must
	// outlive it (follow mode), so it uses the caller's ctx directly.
	cctx, ccancel := context.WithTimeout(ctx, s.connectTimeout)
	exec, err := s.connector.Connect(cctx, srv)
	ccancel()
	if err != nil {
		return classifyConnectError(err, srv.Name)
	}
	defer exec.Close()

	// Validate the target against real remote Neo state before touching a shell.
	container, err := resolveLogContainer(ctx, exec, opts.Target)
	if err != nil {
		return err
	}

	cmd := buildLogsCommand(exec.User(), container, ClampTail(opts.Tail), opts.Follow)
	lw := &lineWriter{emit: emit, max: maxLogLineBytes}
	streamErr := exec.Stream(ctx, cmd, lw)
	lw.flush() // surface any trailing partial line once the stream has ended

	return classifyStreamError(streamErr)
}

// resolveLogContainer reads remote Neo state and maps a validated app.list
// target ID onto a Docker container name. An unknown target is reported as
// app_not_found rather than being blindly turned into a command, and an
// unreadable/invalid state file is remote_state_invalid.
func resolveLogContainer(ctx context.Context, exec Executor, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", newError(ErrInvalidRequest, "logs require a target", false, nil, nil)
	}
	data, err := exec.ReadFileElevated(ctx, state.RemotePath)
	if err != nil {
		return "", newError(ErrRemoteStateInvalid, "could not read remote state", true, err, nil)
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return "", newError(ErrRemoteStateInvalid, "remote state is not valid JSON", false, err, nil)
	}
	container, ok := containerForTarget(&st, target)
	if !ok {
		return "", newError(ErrAppNotFound,
			fmt.Sprintf("no workload %q on this server", target), false, nil,
			map[string]interface{}{"target": target})
	}
	return container, nil
}

// containerForTarget resolves an app.list ID to its Docker container name,
// validating each segment against remote state. The ID shapes mirror
// flattenApps (apps.go): "<app>", "<app>/worker/<w>", "<app>/sidecar/<s>", and
// "service/<name>". A target that does not correspond to a real workload returns
// ok=false so the caller can reject it as app_not_found.
func containerForTarget(st *state.State, target string) (string, bool) {
	if name, ok := strings.CutPrefix(target, "service/"); ok {
		if _, exists := st.Services[name]; exists {
			return config.SvcContainerShared(name), true
		}
		return "", false
	}

	if appName, rest, ok := strings.Cut(target, "/"); ok {
		app, exists := st.Apps[appName]
		if !exists {
			return "", false
		}
		if wName, ok := strings.CutPrefix(rest, "worker/"); ok {
			if _, ok := app.Workers[wName]; ok {
				return config.WorkerContainer(appName, wName), true
			}
			return "", false
		}
		if scName, ok := strings.CutPrefix(rest, "sidecar/"); ok {
			if _, ok := app.Sidecars[scName]; ok {
				return config.SvcContainer(appName, scName), true
			}
			return "", false
		}
		return "", false
	}

	if _, exists := st.Apps[target]; exists {
		return config.AppContainer(target), true
	}
	return "", false
}

// buildLogsCommand assembles the remote `docker logs` invocation. The container
// name is shell-quoted; it is a validated Neo container name, never raw frontend
// input. Non-root sessions get a sudo prefix, matching the snapshot collector.
func buildLogsCommand(user, container string, tail int, follow bool) string {
	bin := "docker"
	if user != "root" {
		bin = "sudo docker"
	}
	cmd := fmt.Sprintf("%s logs --tail %d", bin, tail)
	if follow {
		cmd += " -f"
	}
	return cmd + " " + ssh.ShellQuote(container)
}

// classifyStreamError maps a stream's terminating error onto a stable code. A
// cancelled or timed-out context is the expected way a follow stream ends and is
// reported as such; anything else is an internal failure the UI can retry.
func classifyStreamError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return newError(ErrOperationCancelled, "the log stream was cancelled", false, err, nil)
	case errors.Is(err, context.DeadlineExceeded):
		return newError(ErrOperationTimeout, "the log stream timed out", true, err, nil)
	default:
		return newError(ErrInternal, "the log stream ended unexpectedly", true, err, nil)
	}
}

// lineWriter is an io.Writer that reassembles a byte stream into complete lines
// and hands each one to emit. It strips a trailing CR so Windows-style CRLF
// output does not leak carriage returns into the UI. A single line with no
// newline that grows past max is flushed defensively so memory stays bounded.
//
// It is written to by exactly one goroutine (the SSH session's stdout copier),
// so it needs no internal locking; emit itself must be safe for that goroutine.
type lineWriter struct {
	emit func(string)
	buf  []byte
	max  int
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(string(bytes.TrimSuffix(w.buf[:i], []byte("\r"))))
		w.buf = w.buf[i+1:]
	}
	if w.max > 0 && len(w.buf) > w.max {
		w.emit(string(w.buf))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// flush emits any buffered bytes that were not newline-terminated. It must only
// be called once the stream has fully stopped writing.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(string(bytes.TrimSuffix(w.buf, []byte("\r"))))
		w.buf = w.buf[:0]
	}
}
