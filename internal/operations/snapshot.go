package operations

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/vxero/neo/internal/state"
)

// vmMetricsScript collects host CPU, memory, disk, and uptime in a SINGLE SSH
// round trip to keep snapshot overhead low (plan: "collect VM metrics ... in as
// few remote commands as practical"). Each line is `KEY:value`, where value is
// machine-parseable or the sentinel `NA` when the platform command is missing —
// so a missing tool yields an unavailable field, never a misleading zero.
//
//   - CPU:   busy percent (float), via top's %Cpu idle column.
//   - MEM:   `usedBytes/totalBytes`, via `free -b`.
//   - DISK:  `used1kBlocks/total1kBlocks` for /, via `df -kP`.
//   - UPTIME:integer seconds, via /proc/uptime.
//
// Quoting note: awk programs are single-quoted; each runs inside its own $(...)
// command substitution, whose quoting is parsed independently of the outer
// double-quoted echo, so the single quotes are real awk delimiters.
const vmMetricsScript = `echo "CPU:$(top -bn1 2>/dev/null | awk '/%Cpu/{print 100-$8; f=1} END{if(!f) print "NA"}')"
echo "MEM:$(free -b 2>/dev/null | awk '/^Mem:/{printf "%d/%d", $3, $2; f=1} END{if(!f) print "NA"}')"
echo "DISK:$(df -kP / 2>/dev/null | awk 'NR==2{printf "%d/%d", $3, $2; f=1} END{if(!f) print "NA"}')"
echo "UPTIME:$(awk '{printf "%d", $1; f=1} END{if(!f) print "NA"}' /proc/uptime 2>/dev/null || echo NA)"`

// dockerStatsFormat is a Go-template --format string. The literal \t reaches
// docker (which renders it as a tab), so it must survive as backslash-t — hence
// this is a raw string constant, never a double-quoted one.
const dockerStatsCmd = `stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' 2>/dev/null`

// collectSnapshot gathers reachability, latency, VM metrics, workload counts,
// and container stats over an already-open connection. It is the ONE shared
// collector: Service.Snapshot calls it after dialing, and the CLI's
// `neo status --json` calls it (via CollectSnapshot) with its live connection.
//
// Resilience (plan "Snapshot collection"): every sub-collection is independent.
// A Docker failure never discards valid VM metrics, an unreadable state file
// never discards reachability, and a missing metric stays nil rather than 0.
func collectSnapshot(ctx context.Context, exec Executor, summary ServerSummary, clock Clock) Snapshot {
	snap := Snapshot{
		Server:     summary,
		ObservedAt: clock.Now().UTC(),
		Containers: []ContainerStat{},
	}

	// Reachability + latency: a trivial command times a full round trip.
	start := clock.Now()
	if _, err := exec.Run(ctx, "true"); err == nil {
		snap.Reachable = true
		snap.LatencyMS = clock.Now().Sub(start).Milliseconds()
	}

	if out, err := exec.Run(ctx, vmMetricsScript); err == nil {
		applyVMMetrics(&snap, out)
	}

	snap.Apps, snap.Services = collectWorkloadCounts(ctx, exec)
	snap.Containers = collectContainerStats(ctx, exec)
	return snap
}

// CollectSnapshot collects a snapshot over a caller-owned connection, without
// dialing. The CLI uses it to reuse its interactively-established SSH session so
// the CLI and the bridge share the exact same collection code.
func CollectSnapshot(ctx context.Context, exec Executor, summary ServerSummary, clock Clock) Snapshot {
	if clock == nil {
		clock = SystemClock()
	}
	return collectSnapshot(ctx, exec, summary, clock)
}

// applyVMMetrics parses the KEY:value lines from vmMetricsScript into the
// snapshot. Unparseable or NA values leave the corresponding pointer nil.
func applyVMMetrics(snap *Snapshot, out string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" || val == "NA" {
			continue
		}
		switch key {
		case "CPU":
			if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 {
				snap.CPUPercent = &f
			}
		case "MEM":
			if used, total, ok := parseUintPair(val); ok {
				snap.RAMUsedBytes = &used
				snap.RAMTotalBytes = &total
			}
		case "DISK":
			// df -kP reports 1024-byte blocks; scale to bytes.
			if usedBlocks, totalBlocks, ok := parseUintPair(val); ok {
				used := usedBlocks * 1024
				total := totalBlocks * 1024
				snap.DiskUsedBytes = &used
				snap.DiskTotalBytes = &total
			}
		case "UPTIME":
			if u, err := strconv.ParseUint(val, 10, 64); err == nil {
				snap.UptimeSeconds = &u
			}
		}
	}
}

// collectWorkloadCounts reads remote state and tallies apps/services. A missing
// or invalid state file (e.g. an uninitialised server) yields zero counts
// rather than failing the snapshot — reachability and metrics are still valid.
func collectWorkloadCounts(ctx context.Context, exec Executor) (apps, services WorkloadCounts) {
	data, err := exec.ReadFileElevated(ctx, state.RemotePath)
	if err != nil {
		return
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	for _, a := range st.Apps {
		apps.Total++
		if a.Status == "running" {
			apps.Running++
		} else {
			apps.Stopped++
		}
	}
	for _, svc := range st.Services {
		services.Total++
		if svc.Status == "running" {
			services.Running++
		} else {
			services.Stopped++
		}
	}
	return
}

// collectContainerStats parses `docker stats` into typed per-container usage.
// Docker being unavailable (or a partial/garbled line) never fails the
// snapshot: the result is simply an empty or shorter list.
func collectContainerStats(ctx context.Context, exec Executor) []ContainerStat {
	bin := "docker"
	if exec.User() != "root" {
		bin = "sudo docker"
	}
	out, err := exec.Run(ctx, bin+" "+dockerStatsCmd)
	stats := []ContainerStat{}
	if err != nil || strings.TrimSpace(out) == "" {
		return stats
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue // never surface an unparsed raw line
		}
		cs := ContainerStat{Name: strings.TrimSpace(parts[0])}
		if v, ok := parsePercent(parts[1]); ok {
			cs.CPUPercent = &v
		}
		if used, limit, ok := parseMemUsage(parts[2]); ok {
			cs.MemUsedBytes = &used
			cs.MemLimitBytes = &limit
		}
		stats = append(stats, cs)
	}
	return stats
}

// parseUintPair parses "a/b" into two uint64s.
func parseUintPair(s string) (a, b uint64, ok bool) {
	left, right, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	av, err1 := strconv.ParseUint(strings.TrimSpace(left), 10, 64)
	bv, err2 := strconv.ParseUint(strings.TrimSpace(right), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return av, bv, true
}

// parsePercent parses a docker percentage like "1.23%" into a float.
func parsePercent(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// parseMemUsage parses a docker MemUsage cell ("50.5MiB / 1.944GiB") into used
// and limit bytes.
func parseMemUsage(s string) (used, limit uint64, ok bool) {
	left, right, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	u, ok1 := parseByteSize(left)
	l, ok2 := parseByteSize(right)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return u, l, true
}

// byteUnits maps a unit suffix to its multiplier. Docker emits IEC units (KiB,
// MiB, GiB, TiB); we also accept SI (kB, MB, GB, TB) and a bare "B" defensively.
var byteUnits = []struct {
	suffix string
	mult   float64
}{
	{"PiB", 1 << 50}, {"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"PB", 1e15}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3},
	{"B", 1},
}

// parseByteSize parses a human byte string like "1.944GiB" into bytes.
func parseByteSize(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, u := range byteUnits {
		if strings.HasSuffix(s, u.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			f, err := strconv.ParseFloat(num, 64)
			if err != nil || f < 0 {
				return 0, false
			}
			return uint64(f * u.mult), true
		}
	}
	// No recognised unit — maybe a bare number of bytes.
	if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0 {
		return uint64(f), true
	}
	return 0, false
}
