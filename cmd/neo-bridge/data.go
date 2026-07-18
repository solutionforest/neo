package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vxero/neo/internal/operations"
)

// Read-only data methods are backed by the shared internal/operations service
// over the real
// ~/.neo/config.json — there is no second server registry (plan "Phase 3").

// handleServerList answers server.list with every configured server.
func (s *Server) handleServerList(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}
	servers, err := s.ops.ListServers(ctx)
	if err != nil {
		s.writeError(w, req.ID, rpcFromOpError(err))
		return
	}
	if servers == nil {
		servers = []operations.ServerSummary{}
	}
	s.writeResult(w, req.ID, servers)
}

// snapshotParams is the params payload of a server.snapshot request.
type snapshotParams struct {
	Server string `json:"server"`
}

// handleServerSnapshot answers server.snapshot for one named server.
func (s *Server) handleServerSnapshot(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}

	var p snapshotParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for server.snapshot", false, nil))
			return
		}
	}
	if p.Server == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "server.snapshot requires a 'server' param", false, nil))
		return
	}

	snap, err := s.ops.Snapshot(ctx, p.Server)
	if err != nil {
		s.writeError(w, req.ID, rpcFromOpError(err))
		return
	}
	s.writeResult(w, req.ID, snap)
}

// appListParams is the params payload of an app.list request.
type appListParams struct {
	Server string `json:"server"`
}

// handleAppList answers app.list for one named server with the flattened set of
// applications, workers, sidecars, and shared services.
func (s *Server) handleAppList(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}

	var p appListParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for app.list", false, nil))
			return
		}
	}
	if p.Server == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "app.list requires a 'server' param", false, nil))
		return
	}

	apps, err := s.ops.ListApps(ctx, p.Server)
	if err != nil {
		s.writeError(w, req.ID, rpcFromOpError(err))
		return
	}
	if apps == nil {
		apps = []operations.AppSummary{}
	}
	s.writeResult(w, req.ID, apps)
}

// handleDiagnosticsRun collects one server observation and evaluates the pure
// diagnostic rules, including their per-server persistence state.
func (s *Server) handleDiagnosticsRun(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}

	var p snapshotParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for diagnostics.run", false, nil))
			return
		}
	}
	if p.Server == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "diagnostics.run requires a 'server' param", false, nil))
		return
	}

	findings, err := s.ops.RunDiagnostics(ctx, p.Server)
	if err != nil {
		s.writeError(w, req.ID, rpcFromOpError(err))
		return
	}
	if findings == nil {
		findings = []operations.Finding{}
	}
	s.writeResult(w, req.ID, findings)
}

// rpcFromOpError converts an operation-layer error into a protocol RPCError.
// A typed *operations.Error carries a stable code that maps 1:1 onto the wire
// codes (same string values); anything else is sanitised to internal_error so
// no unexpected detail (or the license key) can leak to the webview.
func rpcFromOpError(err error) *RPCError {
	var opErr *operations.Error
	if errors.As(err, &opErr) {
		return newError(ErrorCode(opErr.Code), opErr.Message, opErr.Retryable, opErr.Details)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newError(ErrOperationTimeout, "the operation timed out", true, nil)
	}
	if errors.Is(err, context.Canceled) {
		return newError(ErrOperationCancel, "the operation was cancelled", false, nil)
	}
	return newError(ErrInternal, "an internal error occurred", false, nil)
}
