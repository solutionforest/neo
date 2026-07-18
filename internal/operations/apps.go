package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/vxero/neo/internal/state"
)

// AppKind classifies a workload row returned by app.list. The four kinds mirror
// the "app" | "worker" | "sidecar" | "service" union in AppSummary in
// apps/desktop/src/lib/protocol.ts.
type AppKind string

const (
	KindApp     AppKind = "app"
	KindWorker  AppKind = "worker"
	KindSidecar AppKind = "sidecar"
	KindService AppKind = "service"
)

// AppState is the normalized lifecycle state of a workload. Neo's remote state
// only ever persists "running" or "stopped", but the desktop's AppState union
// also carries "restarting" and "unhealthy" so a later diagnostics slice can
// surface those without a protocol change; normalizeState maps any recognised
// value through and treats everything else as stopped (never a false "running").
type AppState string

const (
	StateRunning    AppState = "running"
	StateStopped    AppState = "stopped"
	StateRestarting AppState = "restarting"
	StateUnhealthy  AppState = "unhealthy"
)

// AppSummary is one workload row (an application, one of its workers or
// sidecars, or a shared service). It is intentionally flat — the desktop renders
// a single list keyed by ID — so ID is namespaced to stay unique across apps
// that happen to share a worker/sidecar name. Field names mirror AppSummary in
// apps/desktop/src/lib/protocol.ts.
type AppSummary struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Image string   `json:"image"`
	State AppState `json:"state"`
	Kind  AppKind  `json:"kind"`
}

// ListApps connects to the named server, reads its remote Neo state, and flattens
// it into the workload rows the desktop app.list method returns. Connection
// failures come back as the same typed *Error codes as Snapshot; an unreachable
// or uninitialised server therefore yields a precise code the UI can branch on
// rather than an empty list that looks healthy.
func (s *Service) ListApps(ctx context.Context, serverName string) ([]AppSummary, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return nil, newError(ErrInternal, "could not read configuration", false, err, nil)
	}

	srv, ok := lookupServer(cfg, serverName)
	if !ok {
		return nil, newError(ErrServerNotFound,
			fmt.Sprintf("server %q is not configured", serverName), false, nil,
			map[string]interface{}{"server": serverName})
	}

	sctx, cancel := context.WithTimeout(ctx, s.snapshotTimeout)
	defer cancel()

	cctx, ccancel := context.WithTimeout(sctx, s.connectTimeout)
	exec, err := s.connector.Connect(cctx, srv)
	ccancel()
	if err != nil {
		return nil, classifyConnectError(err, srv.Name)
	}
	defer exec.Close()

	return CollectApps(sctx, exec)
}

// CollectApps reads and flattens the remote Neo state over a caller-owned
// connection, without dialing. It is the ONE shared implementation: ListApps
// calls it after connecting, and the CLI can reuse it with its interactive
// session so both surfaces produce identical rows. A missing or unparseable
// state file is reported as remote_state_invalid rather than a misleading empty
// list — the caller cannot tell "no apps" from "could not read" otherwise.
func CollectApps(ctx context.Context, exec Executor) ([]AppSummary, error) {
	data, err := exec.ReadFileElevated(ctx, state.RemotePath)
	if err != nil {
		return nil, newError(ErrRemoteStateInvalid, "could not read remote state", true, err, nil)
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, newError(ErrRemoteStateInvalid, "remote state is not valid JSON", false, err, nil)
	}
	return flattenApps(&st), nil
}

// flattenApps turns the remote state into a stable, deterministically ordered
// list: each application (sorted by name) is followed immediately by its workers
// then its sidecars (each sorted by name), and shared services (sorted by name)
// come last. Deterministic ordering keeps the desktop list from reshuffling on
// every poll and keeps the Go tests exact.
func flattenApps(st *state.State) []AppSummary {
	out := []AppSummary{}

	for _, name := range sortedKeys(st.Apps) {
		app := st.Apps[name]
		out = append(out, AppSummary{
			ID:    name,
			Name:  name,
			Image: app.Image,
			State: normalizeState(app.Status),
			Kind:  KindApp,
		})
		for _, wname := range sortedKeys(app.Workers) {
			w := app.Workers[wname]
			out = append(out, AppSummary{
				ID:   name + "/worker/" + wname,
				Name: wname,
				// A worker runs the parent application's image with a different
				// command, so the app image is the meaningful thing to show.
				Image: app.Image,
				State: normalizeState(w.Status),
				Kind:  KindWorker,
			})
		}
		for _, sname := range sortedKeys(app.Sidecars) {
			sc := app.Sidecars[sname]
			out = append(out, AppSummary{
				ID:    name + "/sidecar/" + sname,
				Name:  sname,
				Image: sc.Image,
				State: normalizeState(sc.Status),
				Kind:  KindSidecar,
			})
		}
	}

	for _, name := range sortedKeys(st.Services) {
		svc := st.Services[name]
		out = append(out, AppSummary{
			ID:    "service/" + name,
			Name:  name,
			Image: svc.Image,
			State: normalizeState(svc.Status),
			Kind:  KindService,
		})
	}

	return out
}

// normalizeState maps a persisted status string onto the desktop AppState union.
// Unknown or empty statuses become "stopped" so a workload is never shown as
// running on the strength of an unrecognised value.
func normalizeState(status string) AppState {
	switch status {
	case "running":
		return StateRunning
	case "restarting":
		return StateRestarting
	case "unhealthy":
		return StateUnhealthy
	default:
		return StateStopped
	}
}

// sortedKeys returns the keys of any string-keyed map in ascending order. It
// keeps the flattened list stable across polls regardless of Go's random map
// iteration order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
