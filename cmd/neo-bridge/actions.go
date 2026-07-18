package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/vxero/neo/internal/operations"
)

// Lifecycle action methods: app.action (start/stop/restart one application) and
// operation.cancel. Unlike the read-only data methods, an action runs on its own
// goroutine so the read loop stays responsive — a follow-up operation.cancel can
// then reach the bridge and cancel the in-flight action's context (the same
// pattern the log streams use).
//
// Duplicate-click protection (plan "Phase 5 acceptance criteria": duplicate
// clicks cannot start the same action twice concurrently) is enforced here in
// the bridge, independent of the UI: a second action for the same (server, app)
// while one is in flight is rejected with action_not_allowed.

// opManager tracks in-flight lifecycle operations so operation.cancel can reach
// them by id and so a duplicate concurrent action on the same application is
// refused.
type opManager struct {
	mu       sync.Mutex
	inflight map[string]context.CancelFunc // by operationId
	busy     map[string]string             // "server\x00app" -> operationId
	counter  uint64
	wg       sync.WaitGroup
}

func newOpManager() *opManager {
	return &opManager{
		inflight: map[string]context.CancelFunc{},
		busy:     map[string]string{},
	}
}

// register reserves an operation. It derives a cancellable context from parent
// (the bridge's run context, so shutdown cancels every action) and returns the
// resolved operation id plus a done func the caller MUST defer to release the
// registration. It fails if operationID is already running or another action is
// in flight for busyKey.
func (m *opManager) register(parent context.Context, operationID, busyKey string) (context.Context, string, func(), *RPCError) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if operationID != "" {
		if _, dup := m.inflight[operationID]; dup {
			return nil, "", nil, newError(ErrInvalidRequest, "operation already running", false,
				map[string]interface{}{"operationId": operationID})
		}
	} else {
		m.counter++
		operationID = fmt.Sprintf("op-%d", m.counter)
	}

	if existing, busy := m.busy[busyKey]; busy {
		return nil, "", nil, newError(ErrActionNotAllowed,
			"an action is already running for this application", false,
			map[string]interface{}{"operationId": existing})
	}

	ctx, cancel := context.WithCancel(parent)
	m.inflight[operationID] = cancel
	m.busy[busyKey] = operationID
	m.wg.Add(1)

	id := operationID
	done := func() {
		m.mu.Lock()
		delete(m.inflight, id)
		if m.busy[busyKey] == id {
			delete(m.busy, busyKey)
		}
		m.mu.Unlock()
		cancel()
		m.wg.Done()
	}
	return ctx, id, done, nil
}

// cancel cancels an in-flight operation by id. Returns false if no such
// operation is live (idempotent: cancelling an already-finished op is harmless).
func (m *opManager) cancel(operationID string) bool {
	m.mu.Lock()
	cancel, ok := m.inflight[operationID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// cancelAll cancels every in-flight operation and waits for each goroutine to
// unwind, so no action outlives the bridge (called as Run returns).
func (m *opManager) cancelAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.inflight))
	for _, c := range m.inflight {
		cancels = append(cancels, c)
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	m.wg.Wait()
}

// activeCount reports how many operations are live (used by tests).
func (m *opManager) activeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inflight)
}

// --- request handlers -----------------------------------------------------

// appActionParams is the params payload of an app.action request. OperationID is
// optional and client-supplied: the desktop generates it so it can issue a
// matching operation.cancel while the action is still in flight.
type appActionParams struct {
	Server      string `json:"server"`
	App         string `json:"app"`
	Action      string `json:"action"`
	OperationID string `json:"operationId"`
}

func (s *Server) handleAppAction(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}

	var p appActionParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for app.action", false, nil))
			return
		}
	}
	if p.Server == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "app.action requires a 'server' param", false, nil))
		return
	}
	if p.App == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "app.action requires an 'app' param", false, nil))
		return
	}

	action := operations.AppAction(p.Action)
	// Enforce the allowlist here too (defense in depth): a destructive or unknown
	// verb never reaches the operation layer.
	if !operations.ActionAllowed(action) {
		s.writeError(w, req.ID, newError(ErrActionNotAllowed,
			"action is not allowed", false, map[string]interface{}{"action": p.Action}))
		return
	}

	busyKey := p.Server + "\x00" + p.App
	cctx, opID, done, rerr := s.opsMgr.register(ctx, p.OperationID, busyKey)
	if rerr != nil {
		s.writeError(w, req.ID, rerr)
		return
	}

	// Run the action off the read loop so operation.cancel can still be serviced.
	go func() {
		defer done()
		result, err := s.ops.RunAppAction(cctx, operations.AppActionInput{
			Server:      p.Server,
			App:         p.App,
			Action:      action,
			OperationID: opID,
		})
		if err != nil {
			s.writeError(w, req.ID, rpcFromOpError(err))
			return
		}
		s.writeResult(w, req.ID, result)
	}()
}

// operationCancelParams is the params payload of an operation.cancel request.
type operationCancelParams struct {
	OperationID string `json:"operationId"`
}

// operationCancelResult reports whether a live operation was found and
// cancelled. Cancelling an unknown id is not an error — it is idempotent.
type operationCancelResult struct {
	OK    bool `json:"ok"`
	Found bool `json:"found"`
}

func (s *Server) handleOperationCancel(w *syncWriter, req Request) {
	var p operationCancelParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for operation.cancel", false, nil))
			return
		}
	}
	if p.OperationID == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "operation.cancel requires an 'operationId' param", false, nil))
		return
	}
	found := s.opsMgr.cancel(p.OperationID)
	s.writeResult(w, req.ID, operationCancelResult{OK: true, Found: found})
}
