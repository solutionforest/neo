// Package operations holds Neo's server-facing business logic — reading
// configured servers, collecting health snapshots, and (in later slices) apps,
// logs, diagnostics, and lifecycle actions — behind dependency-injected
// interfaces so both the CLI and the neo-bridge sidecar share ONE
// implementation.
//
// Design rules (plan "Phase 3: extract shared Go operations"):
//   - No Cobra, Huh, Lipgloss, or terminal-UI import may appear in this
//     package. It returns typed data; presentation lives in commands/ and the
//     desktop webview.
//   - Every operation takes a context.Context and honours its deadline and
//     cancellation, because the bridge must never hang.
//   - Metrics are typed numeric fields. A metric that could not be collected is
//     represented as nil (a null on the wire), never a misleading zero.
package operations

import "time"

// ServerSummary identifies a configured server for the UI. ID is a stable key
// (the server name — Neo's config is keyed by name, there is no separate id).
// Field names mirror ServerSummary in apps/desktop/src/lib/protocol.ts.
type ServerSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Current bool   `json:"current"`
}

// WorkloadCounts is a running/stopped/total rollup for apps or services.
type WorkloadCounts struct {
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	Total   int `json:"total"`
}

// ContainerStat is a single container's live resource usage. Each numeric field
// is a pointer so "not reported" (nil → null) is distinguishable from a genuine
// zero reading.
type ContainerStat struct {
	Name          string   `json:"name"`
	CPUPercent    *float64 `json:"cpuPercent"`
	MemUsedBytes  *uint64  `json:"memUsedBytes"`
	MemLimitBytes *uint64  `json:"memLimitBytes"`
}

// Snapshot is the typed health snapshot of one server at a point in time. It is
// the shared source of truth: the CLI's `neo status --json` maps it to the
// legacy JSON shape via an adapter, and the bridge returns it verbatim as the
// result of `server.snapshot`.
//
// The pointer metric fields (CPUPercent, RAM*, Disk*, UptimeSeconds) are nil
// when the corresponding remote command was missing or unparseable, so a server
// that cannot report memory is never shown as "0 bytes used". Field names mirror
// ServerSnapshot in apps/desktop/src/lib/protocol.ts.
type Snapshot struct {
	Server         ServerSummary   `json:"server"`
	Reachable      bool            `json:"reachable"`
	ObservedAt     time.Time       `json:"observedAt"`
	LatencyMS      int64           `json:"latencyMs"`
	CPUPercent     *float64        `json:"cpuPercent"`
	RAMUsedBytes   *uint64         `json:"ramUsedBytes"`
	RAMTotalBytes  *uint64         `json:"ramTotalBytes"`
	DiskUsedBytes  *uint64         `json:"diskUsedBytes"`
	DiskTotalBytes *uint64         `json:"diskTotalBytes"`
	UptimeSeconds  *uint64         `json:"uptimeSeconds"`
	Apps           WorkloadCounts  `json:"apps"`
	Services       WorkloadCounts  `json:"services"`
	Containers     []ContainerStat `json:"containers"`
}
