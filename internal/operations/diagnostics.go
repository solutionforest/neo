package operations

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// FindingSeverity is ordered from informational through critical. The string
// values mirror FindingSeverity in the desktop protocol.
type FindingSeverity string

const (
	SeverityInfo     FindingSeverity = "info"
	SeverityWarning  FindingSeverity = "warning"
	SeverityCritical FindingSeverity = "critical"
)

// Evidence is one human-readable fact used by a diagnostic rule. Values are
// formatted by the trusted Go layer; raw remote output is never exposed.
type Evidence struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Finding is a deterministic observation produced from snapshot history.
// Findings deliberately describe what Neo observed and never claim a precise
// cause from a single sample.
type Finding struct {
	ID               string          `json:"id"`
	Rule             string          `json:"rule"`
	Severity         FindingSeverity `json:"severity"`
	Summary          string          `json:"summary"`
	Evidence         []Evidence      `json:"evidence"`
	RecommendedFixID string          `json:"recommendedFixId,omitempty"`
	FirstObservedAt  time.Time       `json:"firstObservedAt"`
	LastObservedAt   time.Time       `json:"lastObservedAt"`
}

// DiagnosticObservation is the complete, parsed input to the pure diagnostic
// reducer. Workloads are included because app/service findings need identities,
// not only the aggregate counts carried by Snapshot.
type DiagnosticObservation struct {
	Snapshot  Snapshot
	Workloads []AppSummary
}

type thresholdState struct {
	WarningCount  int
	CriticalCount int
	WarningSince  time.Time
	CriticalSince time.Time
}

// DiagnosticState is the reducer state returned by EvaluateDiagnostics and
// supplied to its next call. Its internals stay private so all transitions go
// through the pure reducer instead of being mutated by callers.
type DiagnosticState struct {
	samples     int
	thresholds  map[string]thresholdState
	activeSince map[string]time.Time
}

// EvaluateDiagnostics is a pure reducer over parsed observations. It does not
// perform I/O or mutate previous; the returned state can be retained by a
// caller to enforce persistence across samples.
func EvaluateDiagnostics(previous DiagnosticState, observation DiagnosticObservation) (DiagnosticState, []Finding) {
	next := DiagnosticState{
		samples:     previous.samples + 1,
		thresholds:  cloneThresholds(previous.thresholds),
		activeSince: map[string]time.Time{},
	}
	at := observation.Snapshot.ObservedAt.UTC()
	findings := make([]Finding, 0)

	addThresholdFinding(&next, previous, &findings, thresholdRule{
		id: "disk_usage", rule: "disk_usage", label: "Disk usage",
		value:   usagePercentage(observation.Snapshot.DiskUsedBytes, observation.Snapshot.DiskTotalBytes),
		warning: 75, critical: 90, persistence: 1, at: at,
	})
	addThresholdFinding(&next, previous, &findings, thresholdRule{
		id: "ram_usage", rule: "ram_usage", label: "RAM usage",
		value:   usagePercentage(observation.Snapshot.RAMUsedBytes, observation.Snapshot.RAMTotalBytes),
		warning: 80, critical: 95, persistence: 3, at: at,
	})
	addThresholdFinding(&next, previous, &findings, thresholdRule{
		id: "cpu_usage", rule: "cpu_usage", label: "CPU usage",
		value:   observation.Snapshot.CPUPercent,
		warning: 80, critical: 95, persistence: 3, at: at,
	})
	if observation.Snapshot.Reachable {
		latency := float64(observation.Snapshot.LatencyMS)
		addThresholdFinding(&next, previous, &findings, thresholdRule{
			id: "ssh_latency", rule: "ssh_latency", label: "SSH latency",
			value: &latency, warning: 750, critical: 2000, persistence: 3, at: at,
			unit: "ms",
		})
	} else {
		resetThreshold(&next, "ssh_latency")
	}

	addReachabilityFinding(&next, previous, &findings, observation.Snapshot, at)

	// The first observation establishes the workload baseline. A stopped or
	// unhealthy app/service becomes a finding on the first sample AFTER that
	// initial scan, exactly as required by the plan's persistence table.
	if previous.samples > 0 && observation.Snapshot.Reachable {
		for _, workload := range observation.Workloads {
			addWorkloadFinding(&next, previous, &findings, workload, at)
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		return findings[i].ID < findings[j].ID
	})
	return next, findings
}

type thresholdRule struct {
	id, rule, label string
	value           *float64
	warning         float64
	critical        float64
	persistence     int
	at              time.Time
	unit            string
}

func addThresholdFinding(next *DiagnosticState, previous DiagnosticState, findings *[]Finding, r thresholdRule) {
	state := next.thresholds[r.id]
	if r.value == nil || *r.value < r.warning {
		resetThreshold(next, r.id)
		return
	}

	if state.WarningCount == 0 {
		state.WarningSince = r.at
	}
	state.WarningCount++
	if *r.value >= r.critical {
		if state.CriticalCount == 0 {
			state.CriticalSince = r.at
		}
		state.CriticalCount++
	} else {
		state.CriticalCount = 0
		state.CriticalSince = time.Time{}
	}
	next.thresholds[r.id] = state

	severity := SeverityInfo
	since := time.Time{}
	threshold := r.warning
	if state.CriticalCount >= r.persistence {
		severity = SeverityCritical
		since = state.CriticalSince
		threshold = r.critical
	} else if state.WarningCount >= r.persistence {
		severity = SeverityWarning
		since = state.WarningSince
	}
	if severity == SeverityInfo {
		return
	}

	first := activeSince(previous, r.id, since)
	next.activeSince[r.id] = first
	value := fmt.Sprintf("%.1f", *r.value)
	thresholdValue := fmt.Sprintf("%.1f", threshold)
	if r.unit == "ms" {
		value += " ms"
		thresholdValue += " ms"
	} else {
		value += "%"
		thresholdValue += "%"
	}
	*findings = append(*findings, Finding{
		ID: r.id, Rule: r.rule, Severity: severity,
		Summary: fmt.Sprintf("%s was observed at %s", r.label, value),
		Evidence: []Evidence{
			{Label: r.label, Value: value},
			{Label: "Threshold", Value: thresholdValue},
			{Label: "Persistence", Value: fmt.Sprintf("%d consecutive sample(s)", r.persistence)},
		},
		FirstObservedAt: first, LastObservedAt: r.at,
	})
}

func addReachabilityFinding(next *DiagnosticState, previous DiagnosticState, findings *[]Finding, snapshot Snapshot, at time.Time) {
	const id = "server_reachability"
	state := next.thresholds[id]
	if snapshot.Reachable {
		resetThreshold(next, id)
		return
	}
	if state.CriticalCount == 0 {
		state.CriticalSince = at
	}
	state.CriticalCount++
	next.thresholds[id] = state
	if state.CriticalCount < 2 {
		return
	}
	first := activeSince(previous, id, state.CriticalSince)
	next.activeSince[id] = first
	*findings = append(*findings, Finding{
		ID: id, Rule: id, Severity: SeverityCritical,
		Summary: fmt.Sprintf("Server %q was unreachable", snapshot.Server.Name),
		Evidence: []Evidence{
			{Label: "Reachability", Value: "Unreachable"},
			{Label: "Attempts", Value: fmt.Sprintf("%d consecutive attempts", state.CriticalCount)},
		},
		FirstObservedAt: first, LastObservedAt: at,
	})
}

func addWorkloadFinding(next *DiagnosticState, previous DiagnosticState, findings *[]Finding, workload AppSummary, at time.Time) {
	if workload.State != StateStopped && workload.State != StateRestarting && workload.State != StateUnhealthy {
		return
	}
	rule := "app_state"
	kind := "Application"
	if workload.Kind == KindService {
		rule = "service_state"
		kind = "Service"
	}
	id := rule + ":" + workload.ID
	severity := SeverityWarning
	if workload.State == StateRestarting || workload.State == StateUnhealthy {
		severity = SeverityCritical
	}
	first := activeSince(previous, id, at)
	next.activeSince[id] = first
	fix := ""
	if workload.Kind == KindApp {
		if workload.State == StateStopped {
			fix = "start:" + workload.ID
		} else {
			fix = "restart:" + workload.ID
		}
	}
	*findings = append(*findings, Finding{
		ID: id, Rule: rule, Severity: severity,
		Summary: fmt.Sprintf("%s %q was observed in state %q", kind, workload.Name, workload.State),
		Evidence: []Evidence{
			{Label: "Workload", Value: workload.Name},
			{Label: "Kind", Value: string(workload.Kind)},
			{Label: "State", Value: string(workload.State)},
		},
		RecommendedFixID: fix, FirstObservedAt: first, LastObservedAt: at,
	})
}

func usagePercentage(used, total *uint64) *float64 {
	if used == nil || total == nil || *total == 0 {
		return nil
	}
	value := float64(*used) / float64(*total) * 100
	return &value
}

func resetThreshold(state *DiagnosticState, id string) {
	delete(state.thresholds, id)
}

func activeSince(previous DiagnosticState, id string, fallback time.Time) time.Time {
	if since, ok := previous.activeSince[id]; ok {
		return since
	}
	return fallback
}

func cloneThresholds(in map[string]thresholdState) map[string]thresholdState {
	out := make(map[string]thresholdState, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func severityRank(severity FindingSeverity) int {
	switch severity {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

type diagnosticTracker struct {
	mu      sync.Mutex
	servers map[string]DiagnosticState
}

func newDiagnosticTracker() *diagnosticTracker {
	return &diagnosticTracker{servers: map[string]DiagnosticState{}}
}

func (t *diagnosticTracker) observe(server string, observation DiagnosticObservation) []Finding {
	t.mu.Lock()
	defer t.mu.Unlock()
	next, findings := EvaluateDiagnostics(t.servers[server], observation)
	t.servers[server] = next
	return findings
}

// RunDiagnostics collects one parsed observation and applies the deterministic
// rules. A connection failure is itself valid diagnostic input: the method
// returns a reachability finding after two attempts instead of failing before
// the rule can observe it.
func (s *Service) RunDiagnostics(ctx context.Context, serverName string) ([]Finding, error) {
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
	summary := ServerSummary{ID: srv.Name, Name: srv.Name, Host: srv.Host, Current: srv.Name == cfg.Current}

	sctx, cancel := context.WithTimeout(ctx, s.snapshotTimeout)
	defer cancel()
	cctx, ccancel := context.WithTimeout(sctx, s.connectTimeout)
	exec, connectErr := s.connector.Connect(cctx, srv)
	ccancel()
	if connectErr != nil {
		if ctx.Err() != nil || sctx.Err() == context.Canceled {
			return nil, newError(ErrOperationCancelled, "the operation was cancelled", false, connectErr, nil)
		}
		observation := DiagnosticObservation{Snapshot: Snapshot{
			Server: summary, Reachable: false, ObservedAt: s.clock.Now().UTC(), Containers: []ContainerStat{},
		}}
		return s.diagnostics.observe(serverName, observation), nil
	}
	defer exec.Close()

	snapshot := collectSnapshot(sctx, exec, summary, s.clock)
	workloads, appErr := CollectApps(sctx, exec)
	if appErr != nil {
		workloads = []AppSummary{}
	}
	findings := s.diagnostics.observe(serverName, DiagnosticObservation{
		Snapshot: snapshot, Workloads: workloads,
	})
	return findings, nil
}
