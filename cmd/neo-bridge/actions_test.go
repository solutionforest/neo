package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/operations"
)

// safeBuf is a mutex-guarded output sink: the bridge writes responses AND
// asynchronous action responses to the same stream from different goroutines,
// so a test reading it mid-run needs serialized access.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuf) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// driveWait keeps stdin OPEN (unlike drive, which closes it immediately and thus
// shuts the bridge down) until responses for every wantID have arrived, then
// closes it. This models the real supervisor, whose stdin stays open while an
// asynchronous action runs to completion.
func driveWait(t *testing.T, srv *Server, lines []string, wantIDs ...string) []Response {
	t.Helper()
	pr, pw := io.Pipe()
	out := &safeBuf{}
	done := make(chan struct{})
	go func() {
		_ = srv.Run(context.Background(), pr, out)
		close(done)
	}()

	for _, ln := range lines {
		if _, err := io.WriteString(pw, ln+"\n"); err != nil {
			t.Fatalf("write request: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := decodeResponses(t, out.snapshot())
		if haveAll(resp, wantIDs) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %v; got %+v", wantIDs, resp)
		}
		time.Sleep(2 * time.Millisecond)
	}
	pw.Close()
	<-done
	return decodeResponses(t, out.snapshot())
}

func haveAll(resp []Response, ids []string) bool {
	for _, id := range ids {
		if findByID(resp, id) == nil {
			return false
		}
	}
	return true
}

// actionStubExecutor is a non-blocking fake: every docker command succeeds
// instantly, so an app.action completes as soon as it is dispatched.
type actionStubExecutor struct {
	user  string
	state string
}

func (e actionStubExecutor) Run(context.Context, string) (string, error) { return "", nil }
func (e actionStubExecutor) Stream(context.Context, string, io.Writer) error {
	return nil
}
func (e actionStubExecutor) ReadFileElevated(context.Context, string) ([]byte, error) {
	return []byte(e.state), nil
}
func (e actionStubExecutor) WriteFileElevated(context.Context, string, []byte, os.FileMode) error {
	return nil
}
func (e actionStubExecutor) User() string { return e.user }
func (e actionStubExecutor) Close() error { return nil }

// blockingActionExecutor blocks every docker command until the context is
// cancelled, so a test can prove operation.cancel actually reaches an in-flight
// action.
type blockingActionExecutor struct {
	user  string
	state string
}

func (e blockingActionExecutor) Run(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (e blockingActionExecutor) Stream(context.Context, string, io.Writer) error { return nil }
func (e blockingActionExecutor) ReadFileElevated(context.Context, string) ([]byte, error) {
	return []byte(e.state), nil
}
func (e blockingActionExecutor) WriteFileElevated(context.Context, string, []byte, os.FileMode) error {
	return nil
}
func (e blockingActionExecutor) User() string { return e.user }
func (e blockingActionExecutor) Close() error { return nil }

const actionState = `{"apps":{"web":{"status":"stopped","image":"acme:1"}},"services":{}}`

func actionServer(t *testing.T, exec operations.Executor) *Server {
	t.Helper()
	cfg := &config.Config{
		Current: "production",
		Servers: map[string]config.Server{"production": {Name: "production", Host: "root@10.0.0.1", Port: 22}},
	}
	return serverWithOps(t, stubConfigStore{cfg: cfg}, stubConnector{exec: exec})
}

func findByID(resp []Response, id string) *Response {
	for i := range resp {
		if resp[i].ID == id {
			return &resp[i]
		}
	}
	return nil
}

func TestAppActionSucceeds(t *testing.T) {
	srv := actionServer(t, actionStubExecutor{user: "root", state: actionState})
	resp := driveWait(t, srv,
		[]string{`{"version":1,"id":"a1","method":"app.action","params":{"server":"production","app":"web","action":"start","operationId":"op-1"}}`},
		"a1")

	r := findByID(resp, "a1")
	if r == nil || r.Error != nil {
		t.Fatalf("unexpected app.action response: %+v", resp)
	}
	var res operations.OperationResult
	remarshal(t, r.Result, &res)
	if res.Status != "succeeded" || res.OperationID != "op-1" {
		t.Fatalf("operation result wrong: %+v", res)
	}
}

func TestAppActionDisallowedActionRejected(t *testing.T) {
	srv := actionServer(t, actionStubExecutor{user: "root", state: actionState})
	resp := drive(t, srv,
		`{"version":1,"id":"a2","method":"app.action","params":{"server":"production","app":"web","action":"remove","operationId":"op-2"}}`+"\n")

	r := findByID(resp, "a2")
	if r == nil || r.Error == nil || r.Error.Code != ErrActionNotAllowed {
		t.Fatalf("want action_not_allowed, got %+v", resp)
	}
}

func TestAppActionMissingParams(t *testing.T) {
	srv := actionServer(t, actionStubExecutor{user: "root", state: actionState})
	resp := drive(t, srv, `{"version":1,"id":"a3","method":"app.action","params":{"server":"production"}}`+"\n")
	r := findByID(resp, "a3")
	if r == nil || r.Error == nil || r.Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing app, got %+v", resp)
	}
}

func TestOperationCancelStopsInFlightAction(t *testing.T) {
	srv := actionServer(t, blockingActionExecutor{user: "root", state: actionState})
	// The action blocks until cancelled; operation.cancel with the same id, sent
	// on the next line, releases it. drive() reads all responses after stdin EOF,
	// and cancelAll() waits for the action goroutine to unwind first.
	input := `{"version":1,"id":"act","method":"app.action","params":{"server":"production","app":"web","action":"restart","operationId":"op-cancel"}}` + "\n" +
		`{"version":1,"id":"cancel","method":"operation.cancel","params":{"operationId":"op-cancel"}}` + "\n"
	resp := drive(t, srv, input)

	cancel := findByID(resp, "cancel")
	if cancel == nil || cancel.Error != nil {
		t.Fatalf("operation.cancel response wrong: %+v", resp)
	}
	var cres operationCancelResult
	remarshal(t, cancel.Result, &cres)
	if !cres.Found {
		t.Errorf("operation.cancel should have found the in-flight action")
	}

	act := findByID(resp, "act")
	if act == nil || act.Error == nil || act.Error.Code != ErrOperationCancel {
		t.Fatalf("cancelled action should return operation_cancelled, got %+v", act)
	}
}

func TestOperationCancelUnknownIsNotFound(t *testing.T) {
	srv := actionServer(t, actionStubExecutor{user: "root", state: actionState})
	resp := drive(t, srv, `{"version":1,"id":"c1","method":"operation.cancel","params":{"operationId":"nope"}}`+"\n")
	r := findByID(resp, "c1")
	if r == nil || r.Error != nil {
		t.Fatalf("operation.cancel unknown should succeed idempotently: %+v", resp)
	}
	var cres operationCancelResult
	remarshal(t, r.Result, &cres)
	if cres.Found {
		t.Errorf("cancel of an unknown op should report found=false")
	}
}

func TestDuplicateConcurrentActionRejected(t *testing.T) {
	srv := actionServer(t, blockingActionExecutor{user: "root", state: actionState})
	// First action blocks (registers the app as busy); the second for the same
	// app is refused; then we cancel the first to let the run loop finish.
	input := `{"version":1,"id":"first","method":"app.action","params":{"server":"production","app":"web","action":"stop","operationId":"op-a"}}` + "\n" +
		`{"version":1,"id":"second","method":"app.action","params":{"server":"production","app":"web","action":"stop","operationId":"op-b"}}` + "\n" +
		`{"version":1,"id":"unblock","method":"operation.cancel","params":{"operationId":"op-a"}}` + "\n"
	resp := drive(t, srv, input)

	second := findByID(resp, "second")
	if second == nil || second.Error == nil || second.Error.Code != ErrActionNotAllowed {
		t.Fatalf("duplicate concurrent action should be action_not_allowed, got %+v", second)
	}
}
