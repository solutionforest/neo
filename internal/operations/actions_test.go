package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vxero/neo/internal/state"
)

// actionExecutor records every docker command issued and can be scripted to
// fail a specific container, so the lifecycle tests assert ordering, the sudo
// prefix, and the failure/persist behavior without a live server.
type actionExecutor struct {
	user     string
	stateIn  []byte
	failOn   string // substring: any Run whose command contains it errors
	writeErr error

	mu       sync.Mutex
	ran      []string
	wrote    []byte
	didWrite bool
}

func (e *actionExecutor) Run(_ context.Context, cmd string) (string, error) {
	e.mu.Lock()
	e.ran = append(e.ran, cmd)
	e.mu.Unlock()
	if e.failOn != "" && strings.Contains(cmd, e.failOn) {
		return "", errors.New("boom")
	}
	return "", nil
}

func (e *actionExecutor) Stream(context.Context, string, io.Writer) error { return nil }

func (e *actionExecutor) ReadFileElevated(context.Context, string) ([]byte, error) {
	return e.stateIn, nil
}

func (e *actionExecutor) WriteFileElevated(_ context.Context, _ string, data []byte, _ os.FileMode) error {
	e.mu.Lock()
	e.didWrite = true
	e.wrote = append([]byte(nil), data...)
	e.mu.Unlock()
	return e.writeErr
}

func (e *actionExecutor) User() string { return e.user }
func (e *actionExecutor) Close() error { return nil }

func (e *actionExecutor) commands() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ran...)
}

const actionStateJSON = `{
  "apps": {
    "web": {
      "status": "stopped",
      "image": "acme/web:1",
      "workers": {"queue": {"status": "stopped"}},
      "sidecars": {"cache": {"status": "stopped", "image": "redis:7"}}
    }
  },
  "services": {}
}`

func TestActionAllowlist(t *testing.T) {
	for _, a := range []AppAction{ActionStart, ActionStop, ActionRestart} {
		if !ActionAllowed(a) {
			t.Errorf("%q should be allowed", a)
		}
	}
	for _, a := range []AppAction{"remove", "update", "restore", "firewall", "destroy", ""} {
		if ActionAllowed(AppAction(a)) {
			t.Errorf("%q must NOT be allowed", a)
		}
	}
	if SafetyClassOf(ActionStop) != SafetyAvailability {
		t.Errorf("stop should be availability-affecting")
	}
	if SafetyClassOf(ActionStart) != SafetyReversible || SafetyClassOf(ActionRestart) != SafetyReversible {
		t.Errorf("start/restart should be reversible")
	}
}

func TestRunAppActionStartSucceedsAndPersists(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	res, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "web", Action: ActionStart, OperationID: "op-1",
	})
	if err != nil {
		t.Fatalf("RunAppAction: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", res.Status)
	}
	if res.OperationID != "op-1" {
		t.Errorf("operationId = %q", res.OperationID)
	}
	if !strings.Contains(res.Summary, "started web") {
		t.Errorf("summary = %q", res.Summary)
	}
	if res.FinishedAt == nil {
		t.Errorf("finishedAt should be set")
	}

	// Three workloads transitioned stopped→running.
	wantChanges := map[string]bool{
		"web": false, "web/worker/queue": false, "web/sidecar/cache": false,
	}
	for _, c := range res.Changes {
		if _, ok := wantChanges[c.Target]; !ok {
			t.Errorf("unexpected change target %q", c.Target)
			continue
		}
		if c.From != "stopped" || c.To != "running" {
			t.Errorf("change %+v", c)
		}
		wantChanges[c.Target] = true
	}
	for target, seen := range wantChanges {
		if !seen {
			t.Errorf("missing change for %q", target)
		}
	}

	// The primary app container was started (root → no sudo).
	if !ranContains(exec.commands(), "docker start 'app-web'") {
		t.Errorf("app container not started: %v", exec.commands())
	}

	// State was persisted with the new running statuses.
	if !exec.didWrite {
		t.Fatalf("state was not persisted")
	}
	var persisted state.State
	if err := json.Unmarshal(exec.wrote, &persisted); err != nil {
		t.Fatalf("persisted state invalid: %v", err)
	}
	if persisted.Apps["web"].Status != "running" {
		t.Errorf("persisted app status = %q", persisted.Apps["web"].Status)
	}
	if persisted.Apps["web"].Workers["queue"].Status != "running" {
		t.Errorf("worker status not persisted")
	}
}

func TestRunAppActionSudoForNonRoot(t *testing.T) {
	exec := &actionExecutor{user: "deploy", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	if _, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "web", Action: ActionRestart,
	}); err != nil {
		t.Fatalf("RunAppAction: %v", err)
	}
	if !ranContains(exec.commands(), "sudo docker restart 'app-web'") {
		t.Errorf("non-root should use sudo docker: %v", exec.commands())
	}
}

func TestRunAppActionDisallowedAction(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	_, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "web", Action: AppAction("remove"),
	})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrActionNotAllowed {
		t.Fatalf("want action_not_allowed, got %v", err)
	}
	// A disallowed action must never touch the server.
	if len(exec.commands()) != 0 {
		t.Errorf("disallowed action ran commands: %v", exec.commands())
	}
}

func TestRunAppActionUnknownApp(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	_, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "ghost", Action: ActionStart,
	})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrAppNotFound {
		t.Fatalf("want app_not_found, got %v", err)
	}
}

func TestRunAppActionUnknownServer(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	_, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "nope", App: "web", Action: ActionStart,
	})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %v", err)
	}
}

func TestRunAppActionContainerFailureDoesNotPersist(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON), failOn: "app-web"}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	res, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "web", Action: ActionStart, OperationID: "op-x",
	})
	if err != nil {
		t.Fatalf("a container failure should be a failed result, not an error: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("status = %q, want failed", res.Status)
	}
	if exec.didWrite {
		t.Errorf("a failed action must not persist state")
	}
}

func TestRunAppActionCancelled(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the action runs

	_, err := svc.RunAppAction(ctx, AppActionInput{
		Server: "production", App: "web", Action: ActionStart,
	})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrOperationCancelled {
		t.Fatalf("want operation_cancelled, got %v", err)
	}
}

func TestRunAppActionGeneratesOperationIDWhenEmpty(t *testing.T) {
	exec := &actionExecutor{user: "root", stateIn: []byte(actionStateJSON)}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	res, err := svc.RunAppAction(context.Background(), AppActionInput{
		Server: "production", App: "web", Action: ActionStop,
	})
	if err != nil {
		t.Fatalf("RunAppAction: %v", err)
	}
	if res.OperationID == "" {
		t.Errorf("operationId should be generated when not supplied")
	}
	if res.Status != "succeeded" {
		t.Errorf("status = %q", res.Status)
	}
}

func ranContains(cmds []string, want string) bool {
	for _, c := range cmds {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}
