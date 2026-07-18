package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"

	"github.com/vxero/neo/internal/operations"
)

// HelloResult is the payload of a successful bridge.hello. It reports the
// protocol, bridge, and CLI-core versions plus platform and activation state.
// It does NOT carry the desktop app version — that is known only to the Tauri
// shell, which injects it before handing the result to the webview. Field names
// mirror BridgeHello in apps/desktop/src/lib/protocol.ts.
type HelloResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	BridgeVersion   string `json:"bridgeVersion"`
	CoreVersion     string `json:"coreVersion"`
	Platform        string `json:"platform"`
	Arch            string `json:"arch"`
	// Activation is "active", "inactive", "grace", or "unknown". Real license
	// state is wired in a later slice; the walking skeleton reports "unknown".
	Activation string `json:"activation"`
}

// ShutdownResult acknowledges a bridge.shutdown before the process exits.
type ShutdownResult struct {
	OK bool `json:"ok"`
}

// Server implements the newline-delimited JSON protocol over a pair of streams.
// It is transport-agnostic (Run takes an io.Reader/io.Writer) so contract tests
// can drive it with in-memory buffers.
type Server struct {
	bridgeVersion string
	coreVersion   string
	log           *slog.Logger

	seen map[string]struct{} // request ids observed this session (must be unique)

	// ops backs the data methods (server.list, server.snapshot). It is nil in
	// the walking-skeleton tests that only exercise hello/shutdown; data
	// handlers report internal_error until it is wired via WithOperations.
	ops *operations.Service
	// activationFn reports the coarse activation state for bridge.hello without
	// exposing the license key. nil → "unknown".
	activationFn func() string
}

// Option configures a Server at construction time. Existing callers that pass
// no options (the protocol/walking-skeleton tests) keep working unchanged.
type Option func(*Server)

// WithOperations injects the shared operation service that backs the data
// methods.
func WithOperations(ops *operations.Service) Option {
	return func(s *Server) { s.ops = ops }
}

// WithActivation injects the activation-status provider used by bridge.hello.
func WithActivation(fn func() string) Option {
	return func(s *Server) { s.activationFn = fn }
}

// NewServer builds a Server. A nil logger is replaced with a no-op logger so the
// protocol stream on stdout is never contaminated by fallback logging.
func NewServer(bridgeVersion, coreVersion string, logger *slog.Logger, opts ...Option) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{
		bridgeVersion: bridgeVersion,
		coreVersion:   coreVersion,
		log:           logger,
		seen:          map[string]struct{}{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run reads requests from in and writes responses/events to out until in is
// exhausted (stdin closed), a bridge.shutdown request is received, or ctx is
// cancelled. It returns nil on any of those graceful conditions and a non-nil
// error only on an unexpected read/transport failure.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	w := &syncWriter{w: out}

	for {
		if err := ctx.Err(); err != nil {
			s.log.Info("context cancelled, shutting down")
			return nil
		}

		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if stop := s.handleLine(ctx, trimmed, w); stop {
				return nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				s.log.Info("stdin closed, shutting down")
				return nil
			}
			return fmt.Errorf("reading request: %w", readErr)
		}
	}
}

// handleLine parses and dispatches a single request line, writing exactly one
// response. It returns true when the bridge should stop (graceful shutdown).
func (s *Server) handleLine(ctx context.Context, line []byte, w *syncWriter) (stop bool) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.log.Warn("malformed request", "err", err)
		s.writeError(w, salvageID(line), newError(ErrInvalidRequest, "malformed JSON request", false, nil))
		return false
	}

	s.log.Debug("request", "id", req.ID, "method", req.Method, "version", req.Version)

	if req.Version != ProtocolVersion {
		s.writeError(w, req.ID, newError(ErrProtocolMismatch,
			fmt.Sprintf("unsupported protocol version %d (bridge speaks %d)", req.Version, ProtocolVersion),
			false, map[string]interface{}{"expected": ProtocolVersion, "received": req.Version}))
		return false
	}
	if req.ID == "" {
		s.writeError(w, "", newError(ErrInvalidRequest, "missing request id", false, nil))
		return false
	}
	if req.Method == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "missing method", false, nil))
		return false
	}
	if _, dup := s.seen[req.ID]; dup {
		// Request ids correlate responses on the supervisor side; a reused id
		// would break that mapping, so reject it rather than answer ambiguously.
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "duplicate request id", false,
			map[string]interface{}{"id": req.ID}))
		return false
	}
	s.seen[req.ID] = struct{}{}

	switch req.Method {
	case "bridge.hello":
		s.writeResult(w, req.ID, s.hello())
		return false
	case "bridge.shutdown":
		s.writeResult(w, req.ID, ShutdownResult{OK: true})
		s.log.Info("bridge.shutdown received")
		return true
	case "server.list":
		s.handleServerList(ctx, w, req)
		return false
	case "server.snapshot":
		s.handleServerSnapshot(ctx, w, req)
		return false
	default:
		// Methods beyond hello/shutdown arrive in later slices. Until then the
		// bridge answers with a stable code so the UI can react without parsing
		// prose.
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "unknown method: "+req.Method, false,
			map[string]interface{}{"method": req.Method}))
		return false
	}
}

func (s *Server) hello() HelloResult {
	activation := "unknown"
	if s.activationFn != nil {
		if v := s.activationFn(); v != "" {
			activation = v
		}
	}
	return HelloResult{
		ProtocolVersion: ProtocolVersion,
		BridgeVersion:   s.bridgeVersion,
		CoreVersion:     s.coreVersion,
		Platform:        runtime.GOOS,
		Arch:            runtime.GOARCH,
		Activation:      activation,
	}
}

func (s *Server) writeResult(w *syncWriter, id string, result interface{}) {
	if err := w.writeMessage(Response{Version: ProtocolVersion, ID: id, Result: result}); err != nil {
		s.log.Error("writing response", "id", id, "err", err)
	}
}

func (s *Server) writeError(w *syncWriter, id string, rerr *RPCError) {
	if err := w.writeMessage(Response{Version: ProtocolVersion, ID: id, Error: rerr}); err != nil {
		s.log.Error("writing error response", "id", id, "err", err)
	}
}

// salvageID does a best-effort extraction of the request id from a line that
// failed to fully parse, so a malformed request can still be correlated when the
// id field itself is intact. Returns "" when nothing usable is found.
func salvageID(line []byte) string {
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return ""
	}
	return probe.ID
}

// syncWriter serializes access to the output stream. stdout carries both
// responses and (later) streaming events written from multiple goroutines, so
// every message must be written atomically as a single newline-terminated line.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (sw *syncWriter) writeMessage(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	b = append(b, '\n')

	sw.mu.Lock()
	defer sw.mu.Unlock()
	_, err = sw.w.Write(b)
	return err
}
