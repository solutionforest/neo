package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// Lifecycle actions (plan "Phase 3 > Lifecycle actions" and "Phase 5 > Fix
// safety classes"). The bridge's app.action method starts, stops, or restarts
// ONE application (and its related worker/sidecar/service containers) over the
// shared operation layer, returning a structured OperationResult. This is the
// same behavior the CLI's `neo start|stop|restart` exposes — commands/manage.go
// delegates to ApplyAppAction so there is one implementation, not two.

// DefaultActionTimeout bounds a whole lifecycle action, connection included. It
// stays under the desktop supervisor's per-request timeout so a stuck action
// surfaces as a timeout rather than hanging the UI.
const DefaultActionTimeout = 30 * time.Second

// AppAction is an allowlisted lifecycle verb. The three constants below are the
// ENTIRE allowlist: destructive operations (remove, update, restore, firewall,
// database changes) are deliberately absent and can never be requested through
// this surface — plan "Fix safety classes": destructive actions are not
// available in the first beta.
type AppAction string

const (
	ActionStart   AppAction = "start"
	ActionStop    AppAction = "stop"
	ActionRestart AppAction = "restart"
)

// SafetyClass ranks an action by its availability impact (plan "Fix safety
// classes"). The desktop uses it to choose the confirmation UX; the backend
// exposes it so both surfaces share one source of truth.
type SafetyClass string

const (
	// SafetyReversible: start or restart — one confirmation, remember allowed.
	SafetyReversible SafetyClass = "reversible"
	// SafetyAvailability: stop — confirmation every time.
	SafetyAvailability SafetyClass = "availability"
)

// allowedActions is the explicit allowlist keyed by verb, mapping each to its
// safety class. A verb absent from this map is rejected with action_not_allowed.
var allowedActions = map[AppAction]SafetyClass{
	ActionStart:   SafetyReversible,
	ActionStop:    SafetyAvailability,
	ActionRestart: SafetyReversible,
}

// ActionAllowed reports whether verb is in the lifecycle allowlist.
func ActionAllowed(verb AppAction) bool {
	_, ok := allowedActions[verb]
	return ok
}

// SafetyClassOf returns the safety class of an allowlisted action (empty for a
// disallowed one).
func SafetyClassOf(verb AppAction) SafetyClass { return allowedActions[verb] }

// Change records one workload's state transition performed by an action. Field
// names mirror Change in apps/desktop/src/lib/protocol.ts.
type Change struct {
	Target string `json:"target"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// OperationResult is the structured outcome of a lifecycle action. Field names
// mirror OperationResult in apps/desktop/src/lib/protocol.ts. It carries no
// secrets — only workload identifiers and states (plan "Store a local action
// history without environment values, passwords, private keys, license keys, or
// complete unredacted logs").
type OperationResult struct {
	OperationID string     `json:"operationId"`
	Status      string     `json:"status"` // "succeeded" | "failed" | "cancelled"
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Summary     string     `json:"summary"`
	Changes     []Change   `json:"changes"`
}

// AppActionInput selects the action to run. App is a top-level application name
// as returned by app.list (kind "app"); it is validated against real remote
// state before any container command is built, so a frontend-supplied string is
// never interpolated into a shell command unchecked.
type AppActionInput struct {
	Server      string
	App         string
	Action      AppAction
	OperationID string
}

// RunAppAction executes one allowlisted lifecycle action against one
// application on one server and returns a structured result.
//
// Error vs result: a pre-flight failure (disallowed action, unknown server or
// app, connection failure, cancellation) returns a typed *Error and no result —
// nothing was changed. Once the action runs, a container command that fails
// does NOT return an error; it comes back as a result with Status "failed" and
// the changes that did take effect, so the UI can show a precise outcome and a
// log link. A cancellation mid-action returns operation_cancelled.
func (s *Service) RunAppAction(ctx context.Context, in AppActionInput) (OperationResult, error) {
	startedAt := s.clock.Now().UTC()

	if !ActionAllowed(in.Action) {
		return OperationResult{}, newError(ErrActionNotAllowed,
			fmt.Sprintf("action %q is not allowed", in.Action), false, nil,
			map[string]interface{}{"action": string(in.Action)})
	}

	cfg, err := s.cfg.Load()
	if err != nil {
		return OperationResult{}, newError(ErrInternal, "could not read configuration", false, err, nil)
	}
	srv, ok := lookupServer(cfg, in.Server)
	if !ok {
		return OperationResult{}, newError(ErrServerNotFound,
			fmt.Sprintf("server %q is not configured", in.Server), false, nil,
			map[string]interface{}{"server": in.Server})
	}

	actx, cancel := context.WithTimeout(ctx, s.actionTimeout)
	defer cancel()

	cctx, ccancel := context.WithTimeout(actx, s.connectTimeout)
	exec, err := s.connector.Connect(cctx, srv)
	ccancel()
	if err != nil {
		return OperationResult{}, classifyConnectError(err, srv.Name)
	}
	defer exec.Close()

	data, err := exec.ReadFileElevated(actx, state.RemotePath)
	if err != nil {
		return OperationResult{}, newError(ErrRemoteStateInvalid, "could not read remote state", true, err, nil)
	}
	st := state.NewState()
	if err := json.Unmarshal(data, st); err != nil {
		return OperationResult{}, newError(ErrRemoteStateInvalid, "remote state is not valid JSON", false, err, nil)
	}
	if _, ok := st.Apps[in.App]; !ok {
		return OperationResult{}, newError(ErrAppNotFound,
			fmt.Sprintf("no application %q on this server", in.App), false, nil,
			map[string]interface{}{"app": in.App})
	}

	changes, actionErr := ApplyAppAction(actx, exec, st, in.App, in.Action)

	// A cancelled or timed-out action is reported as such, not as a container
	// failure — the state was left untouched on disk.
	if cerr := actx.Err(); cerr != nil {
		if errors.Is(cerr, context.DeadlineExceeded) {
			return OperationResult{}, newError(ErrOperationTimeout, "the action timed out", true, cerr, nil)
		}
		return OperationResult{}, newError(ErrOperationCancelled, "the action was cancelled", false, cerr, nil)
	}

	operationID := in.OperationID
	if operationID == "" {
		operationID = fmt.Sprintf("op-%d", startedAt.UnixNano())
	}
	finishedAt := s.clock.Now().UTC()
	result := OperationResult{
		OperationID: operationID,
		StartedAt:   startedAt,
		FinishedAt:  &finishedAt,
		Changes:     changes,
	}

	if actionErr != nil {
		// Mirror the CLI: on a primary-container failure the new status is NOT
		// persisted, because the containers are not in the state we would record.
		result.Status = "failed"
		result.Summary = fmt.Sprintf("failed to %s %s", in.Action, in.App)
		return result, nil
	}

	if err := exec.WriteFileElevated(actx, state.RemotePath, marshalState(st), 0o600); err != nil {
		// The action succeeded on the server but the state file could not be
		// updated. Report it as failed so the desktop refreshes and surfaces the
		// discrepancy rather than trusting a stale status.
		result.Status = "failed"
		result.Summary = fmt.Sprintf("%s %s but could not update server state", pastVerb(in.Action), in.App)
		return result, nil
	}

	result.Status = "succeeded"
	result.Summary = fmt.Sprintf("%s %s", pastVerb(in.Action), in.App)
	return result, nil
}

// ApplyAppAction runs an allowlisted lifecycle action against one application
// and its related worker/sidecar/service containers over an already-connected
// executor, mutating st's statuses to match, and returns the state transitions
// it made. It is the shared core reused by both the bridge (RunAppAction) and
// the CLI (commands/manage.go), so start/stop/restart behave identically on
// both surfaces.
//
// Precondition: appName exists in st.Apps (callers validate and report
// app_not_found in their own error style). Only the application container(s)'
// command errors are returned; worker/sidecar/service commands are best-effort,
// matching the CLI's long-standing behavior. The container ordering per action
// is preserved from commands/manage.go so nothing regresses.
func ApplyAppAction(ctx context.Context, exec Executor, st *state.State, appName string, action AppAction) ([]Change, error) {
	app := st.Apps[appName]
	user := exec.User()

	serviceContainers := make([]string, 0, len(app.Services))
	for _, svcName := range sortedKeys(app.Services) {
		serviceContainers = append(serviceContainers, config.SvcContainer(appName, svcName))
	}
	workerContainers := make([]string, 0, len(app.Workers))
	for _, wName := range sortedKeys(app.Workers) {
		workerContainers = append(workerContainers, config.WorkerContainer(appName, wName))
	}
	sidecarContainers := make([]string, 0, len(app.Sidecars))
	for _, scName := range sortedKeys(app.Sidecars) {
		sidecarContainers = append(sidecarContainers, config.SvcContainer(appName, scName))
	}
	var appContainers []string
	if app.Scale > 1 {
		for i := 0; i < app.Scale; i++ {
			appContainers = append(appContainers, config.ReplicaContainer(appName, i))
		}
	} else {
		appContainers = []string{config.AppContainer(appName)}
	}

	verb := string(action)
	var actionErr error
	runPrimary := func(container string) {
		if err := runDockerAction(ctx, exec, user, verb, container); err != nil && actionErr == nil {
			actionErr = err
		}
	}
	best := func(containers []string) {
		for _, c := range containers {
			_ = runDockerAction(ctx, exec, user, verb, c)
		}
	}

	// Ordering mirrors commands/manage.go exactly.
	switch action {
	case ActionStart:
		best(serviceContainers)
		best(sidecarContainers)
		for _, ac := range appContainers {
			runPrimary(ac)
		}
		best(workerContainers)
	case ActionStop:
		best(workerContainers)
		for _, ac := range appContainers {
			runPrimary(ac)
		}
		best(sidecarContainers)
		best(serviceContainers)
	case ActionRestart:
		best(serviceContainers)
		best(sidecarContainers)
		for _, ac := range appContainers {
			runPrimary(ac)
		}
		best(workerContainers)
	}

	// Apply the new statuses to the in-memory state and record the transitions.
	newStatus := "running"
	if action == ActionStop {
		newStatus = "stopped"
	}

	var changes []Change
	if app.Status != newStatus {
		changes = append(changes, Change{Target: appName, From: statusOr(app.Status), To: newStatus})
	}
	app.Status = newStatus
	for _, wName := range sortedKeys(app.Workers) {
		w := app.Workers[wName]
		if w.Status != newStatus {
			changes = append(changes, Change{Target: appName + "/worker/" + wName, From: statusOr(w.Status), To: newStatus})
		}
		w.Status = newStatus
		app.Workers[wName] = w
	}
	for _, scName := range sortedKeys(app.Sidecars) {
		sc := app.Sidecars[scName]
		if sc.Status != newStatus {
			changes = append(changes, Change{Target: appName + "/sidecar/" + scName, From: statusOr(sc.Status), To: newStatus})
		}
		sc.Status = newStatus
		app.Sidecars[scName] = sc
	}
	st.Apps[appName] = app

	return changes, actionErr
}

// runDockerAction issues a single `docker <verb> <container>` over the executor.
// The binary is sudo-prefixed for non-root sessions (matching the snapshot and
// log collectors) and the container name — always a Neo-derived, validated
// name — is shell-quoted, never raw frontend input.
func runDockerAction(ctx context.Context, exec Executor, user, verb, container string) error {
	cmd := fmt.Sprintf("%s %s %s", dockerBin(user), verb, ssh.ShellQuote(container))
	_, err := exec.Run(ctx, cmd)
	return err
}

// dockerBin returns "docker" for root sessions and "sudo docker" otherwise, so
// socket access works for a sudo-capable non-root user.
func dockerBin(user string) string {
	if user == "root" {
		return "docker"
	}
	return "sudo docker"
}

// statusOr normalises an empty persisted status to "stopped" for display, so a
// Change never reports an empty "from".
func statusOr(status string) string {
	if status == "" {
		return "stopped"
	}
	return status
}

// pastVerb renders an action as a past-tense summary fragment.
func pastVerb(action AppAction) string {
	switch action {
	case ActionStart:
		return "started"
	case ActionStop:
		return "stopped"
	case ActionRestart:
		return "restarted"
	default:
		return string(action)
	}
}

// marshalState renders the state exactly as internal/state.Save does (indented
// JSON) so the file the bridge writes is byte-compatible with the CLI's.
func marshalState(st *state.State) []byte {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return data
}
