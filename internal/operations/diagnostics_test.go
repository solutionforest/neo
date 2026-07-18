package operations

import (
	"testing"
	"time"
)

func diagnosticObservation(at time.Time) DiagnosticObservation {
	return DiagnosticObservation{Snapshot: Snapshot{
		Server:    ServerSummary{ID: "production", Name: "production"},
		Reachable: true, ObservedAt: at,
	}}
}

func findRule(findings []Finding, rule string) *Finding {
	for i := range findings {
		if findings[i].Rule == rule {
			return &findings[i]
		}
	}
	return nil
}

func TestDiagnosticsDiskBoundaryIsInclusiveAndImmediate(t *testing.T) {
	at := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		used    uint64
		want    FindingSeverity
		finding bool
	}{
		{name: "below warning", used: 7499, finding: false},
		{name: "warning boundary", used: 7500, want: SeverityWarning, finding: true},
		{name: "critical boundary", used: 9000, want: SeverityCritical, finding: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observation := diagnosticObservation(at)
			total := uint64(10000)
			observation.Snapshot.DiskUsedBytes = &tc.used
			observation.Snapshot.DiskTotalBytes = &total
			_, findings := EvaluateDiagnostics(DiagnosticState{}, observation)
			finding := findRule(findings, "disk_usage")
			if tc.finding && (finding == nil || finding.Severity != tc.want) {
				t.Fatalf("disk finding = %+v, want severity %q", finding, tc.want)
			}
			if !tc.finding && finding != nil {
				t.Fatalf("unexpected disk finding: %+v", finding)
			}
		})
	}
}

func TestDiagnosticsResourcePersistenceAndReset(t *testing.T) {
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	state := DiagnosticState{}
	for sample := 0; sample < 2; sample++ {
		observation := diagnosticObservation(start.Add(time.Duration(sample) * time.Minute))
		cpu := 80.0
		observation.Snapshot.CPUPercent = &cpu
		var findings []Finding
		state, findings = EvaluateDiagnostics(state, observation)
		if findRule(findings, "cpu_usage") != nil {
			t.Fatalf("CPU finding appeared before 3 samples: %+v", findings)
		}
	}
	third := diagnosticObservation(start.Add(2 * time.Minute))
	cpu := 80.0
	third.Snapshot.CPUPercent = &cpu
	state, findings := EvaluateDiagnostics(state, third)
	finding := findRule(findings, "cpu_usage")
	if finding == nil || finding.Severity != SeverityWarning {
		t.Fatalf("third boundary sample = %+v, want warning", finding)
	}
	if !finding.FirstObservedAt.Equal(start) || !finding.LastObservedAt.Equal(third.Snapshot.ObservedAt) {
		t.Fatalf("observation timestamps = %s/%s", finding.FirstObservedAt, finding.LastObservedAt)
	}
	if len(finding.Evidence) < 3 {
		t.Fatalf("finding must expose value, threshold, and persistence evidence: %+v", finding.Evidence)
	}

	below := diagnosticObservation(start.Add(3 * time.Minute))
	cpu = 79.9
	below.Snapshot.CPUPercent = &cpu
	state, findings = EvaluateDiagnostics(state, below)
	if findRule(findings, "cpu_usage") != nil {
		t.Fatalf("below-threshold sample must clear finding")
	}

	for sample := 0; sample < 3; sample++ {
		critical := diagnosticObservation(start.Add(time.Duration(4+sample) * time.Minute))
		cpu = 95.0
		critical.Snapshot.CPUPercent = &cpu
		state, findings = EvaluateDiagnostics(state, critical)
	}
	finding = findRule(findings, "cpu_usage")
	if finding == nil || finding.Severity != SeverityCritical {
		t.Fatalf("three critical-boundary samples = %+v, want critical", finding)
	}
}

func TestDiagnosticsRAMAndLatencyPersistenceBoundaries(t *testing.T) {
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		ramUsed  uint64
		latency  int64
		severity FindingSeverity
	}{
		{name: "warning boundaries", ramUsed: 80, latency: 750, severity: SeverityWarning},
		{name: "critical boundaries", ramUsed: 95, latency: 2000, severity: SeverityCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := DiagnosticState{}
			var findings []Finding
			for sample := 0; sample < 3; sample++ {
				observation := diagnosticObservation(start.Add(time.Duration(sample) * time.Minute))
				used, total := tc.ramUsed, uint64(100)
				observation.Snapshot.RAMUsedBytes = &used
				observation.Snapshot.RAMTotalBytes = &total
				observation.Snapshot.LatencyMS = tc.latency
				state, findings = EvaluateDiagnostics(state, observation)
			}
			if finding := findRule(findings, "ram_usage"); finding == nil || finding.Severity != tc.severity {
				t.Fatalf("RAM boundary did not persist to %s: %+v", tc.severity, finding)
			}
			if finding := findRule(findings, "ssh_latency"); finding == nil || finding.Severity != tc.severity {
				t.Fatalf("latency boundary did not persist to %s: %+v", tc.severity, finding)
			}
		})
	}
}

func TestDiagnosticsReachabilityRequiresTwoAttempts(t *testing.T) {
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	state := DiagnosticState{}
	first := diagnosticObservation(start)
	first.Snapshot.Reachable = false
	state, findings := EvaluateDiagnostics(state, first)
	if findRule(findings, "server_reachability") != nil {
		t.Fatalf("reachability finding appeared after one attempt")
	}
	second := diagnosticObservation(start.Add(time.Minute))
	second.Snapshot.Reachable = false
	state, findings = EvaluateDiagnostics(state, second)
	finding := findRule(findings, "server_reachability")
	if finding == nil || finding.Severity != SeverityCritical {
		t.Fatalf("second unreachable attempt = %+v, want critical", finding)
	}
	if !finding.FirstObservedAt.Equal(start) || !finding.LastObservedAt.Equal(second.Snapshot.ObservedAt) {
		t.Fatalf("reachability timestamps = %s/%s", finding.FirstObservedAt, finding.LastObservedAt)
	}

	recovered := diagnosticObservation(start.Add(2 * time.Minute))
	_, findings = EvaluateDiagnostics(state, recovered)
	if findRule(findings, "server_reachability") != nil {
		t.Fatalf("reachable observation must clear reachability finding")
	}
}

func TestDiagnosticsWorkloadsStartAfterInitialScan(t *testing.T) {
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	workloads := []AppSummary{
		{ID: "web", Name: "web", Kind: KindApp, State: StateStopped},
		{ID: "service/redis", Name: "redis", Kind: KindService, State: StateUnhealthy},
	}
	first := diagnosticObservation(start)
	first.Workloads = workloads
	state, findings := EvaluateDiagnostics(DiagnosticState{}, first)
	if len(findings) != 0 {
		t.Fatalf("initial scan must only establish workload baseline: %+v", findings)
	}

	second := diagnosticObservation(start.Add(time.Minute))
	second.Workloads = workloads
	_, findings = EvaluateDiagnostics(state, second)
	appFinding := findRule(findings, "app_state")
	if appFinding == nil || appFinding.Severity != SeverityWarning || appFinding.RecommendedFixID != "start:web" {
		t.Fatalf("stopped app finding wrong: %+v", appFinding)
	}
	serviceFinding := findRule(findings, "service_state")
	if serviceFinding == nil || serviceFinding.Severity != SeverityCritical {
		t.Fatalf("unhealthy service finding wrong: %+v", serviceFinding)
	}
	if !appFinding.FirstObservedAt.Equal(second.Snapshot.ObservedAt) {
		t.Fatalf("workload firstObservedAt must be after initial scan, got %s", appFinding.FirstObservedAt)
	}
}

func TestEvaluateDiagnosticsDoesNotMutatePreviousState(t *testing.T) {
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	observation := diagnosticObservation(start)
	cpu := 80.0
	observation.Snapshot.CPUPercent = &cpu
	previous := DiagnosticState{thresholds: map[string]thresholdState{"sentinel": {WarningCount: 7}}}
	_, _ = EvaluateDiagnostics(previous, observation)
	if len(previous.thresholds) != 1 || previous.thresholds["sentinel"].WarningCount != 7 {
		t.Fatalf("pure reducer mutated previous state: %+v", previous.thresholds)
	}
}
