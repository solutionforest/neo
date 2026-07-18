package operations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vxero/neo/internal/state"
)

// appsFixtureState is a small but representative remote state: two apps (one
// with a worker and a sidecar), plus a shared service. Map insertion order is
// deliberately not alphabetical so the test proves flattenApps sorts.
func appsFixtureState() *state.State {
	return &state.State{
		Apps: map[string]state.App{
			"web": {
				Name:   "web",
				Image:  "acme/web:1",
				Status: "running",
				Workers: map[string]state.AppWorker{
					"queue": {Command: "php artisan queue:work", Status: "running"},
				},
				Sidecars: map[string]state.AppSidecar{
					"cron": {Image: "acme/cron:1", Status: "stopped"},
				},
			},
			"api": {
				Name:   "api",
				Image:  "acme/api:2",
				Status: "stopped",
			},
		},
		Services: map[string]state.SharedService{
			"postgres": {Name: "postgres", Image: "postgres:16", Status: "running"},
		},
	}
}

func stateExecutor(t *testing.T, st *state.State) *fakeExecutor {
	t.Helper()
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return &fakeExecutor{user: "root", stateData: data}
}

func TestListAppsFlattensAndSorts(t *testing.T) {
	exec := stateExecutor(t, appsFixtureState())
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})

	apps, err := svc.ListApps(context.Background(), "production")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}

	// api (app) comes before web (app), each followed by its own workers then
	// sidecars, and the shared service is last.
	want := []AppSummary{
		{ID: "api", Name: "api", Image: "acme/api:2", State: StateStopped, Kind: KindApp},
		{ID: "web", Name: "web", Image: "acme/web:1", State: StateRunning, Kind: KindApp},
		{ID: "web/worker/queue", Name: "queue", Image: "acme/web:1", State: StateRunning, Kind: KindWorker},
		{ID: "web/sidecar/cron", Name: "cron", Image: "acme/cron:1", State: StateStopped, Kind: KindSidecar},
		{ID: "service/postgres", Name: "postgres", Image: "postgres:16", State: StateRunning, Kind: KindService},
	}
	if len(apps) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(apps), len(want), apps)
	}
	for i := range want {
		if apps[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, apps[i], want[i])
		}
	}
}

func TestListAppsUnknownServer(t *testing.T) {
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: &fakeExecutor{}})
	_, err := svc.ListApps(context.Background(), "nope")
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrServerNotFound {
		t.Fatalf("want server_not_found, got %v", err)
	}
}

func TestListAppsConnectErrorClassified(t *testing.T) {
	conn := &fakeConnector{err: errors.New("dial tcp: connection refused")}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, conn)
	_, err := svc.ListApps(context.Background(), "production")
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrSSHUnreachable {
		t.Fatalf("want ssh_unreachable, got %v", err)
	}
}

func TestListAppsUnreadableStateIsTypedError(t *testing.T) {
	exec := &fakeExecutor{user: "root", stateErr: errors.New("permission denied reading state")}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	_, err := svc.ListApps(context.Background(), "production")
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrRemoteStateInvalid {
		t.Fatalf("want remote_state_invalid for unreadable state, got %v", err)
	}
	// An unreadable state must NOT look like an empty (healthy) app list.
	if !exec.closed {
		t.Errorf("executor should be closed after ListApps")
	}
}

func TestListAppsInvalidJSONIsTypedError(t *testing.T) {
	exec := &fakeExecutor{user: "root", stateData: []byte("{not json")}
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	_, err := svc.ListApps(context.Background(), "production")
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != ErrRemoteStateInvalid {
		t.Fatalf("want remote_state_invalid for bad JSON, got %v", err)
	}
}

func TestListAppsEmptyStateYieldsEmptyList(t *testing.T) {
	exec := stateExecutor(t, state.NewState())
	svc := newTestService(fakeConfigStore{cfg: twoServerConfig()}, &fakeConnector{exec: exec})
	apps, err := svc.ListApps(context.Background(), "production")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if apps == nil || len(apps) != 0 {
		t.Fatalf("want non-nil empty slice, got %+v", apps)
	}
}

func TestNormalizeState(t *testing.T) {
	cases := map[string]AppState{
		"running":    StateRunning,
		"stopped":    StateStopped,
		"restarting": StateRestarting,
		"unhealthy":  StateUnhealthy,
		"":           StateStopped,
		"exited":     StateStopped,
	}
	for in, want := range cases {
		if got := normalizeState(in); got != want {
			t.Errorf("normalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}
