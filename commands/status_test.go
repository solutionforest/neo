package commands

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vxero/neo/internal/operations"
)

// TestSnapshotToStatusOutputShape characterises the `neo status --json` output:
// the CLI now collects through the shared operation layer, but this adapter must
// keep every legacy field name and JSON type intact so existing consumers of the
// JSON keep working.
func TestSnapshotToStatusOutputShape(t *testing.T) {
	cpu := 42.5
	var ramUsed, ramTotal uint64 = 2 * 1024 * 1024 * 1024, 4 * 1024 * 1024 * 1024
	var diskUsed, diskTotal uint64 = 5 * 1024 * 1024 * 1024, 20 * 1024 * 1024 * 1024
	var uptime uint64 = 90000 // 1 day, 1 hour
	ccpu := 12.5
	var memUsed, memLimit uint64 = 50 * 1024 * 1024, 512 * 1024 * 1024

	snap := operations.Snapshot{
		Server:         operations.ServerSummary{Name: "prod", Host: "root@1.2.3.4"},
		Reachable:      true,
		LatencyMS:      42,
		CPUPercent:     &cpu,
		RAMUsedBytes:   &ramUsed,
		RAMTotalBytes:  &ramTotal,
		DiskUsedBytes:  &diskUsed,
		DiskTotalBytes: &diskTotal,
		UptimeSeconds:  &uptime,
		Apps:           operations.WorkloadCounts{Running: 2, Stopped: 1, Total: 3},
		Services:       operations.WorkloadCounts{Running: 1, Total: 1},
		Containers: []operations.ContainerStat{
			{Name: "app-ghost", CPUPercent: &ccpu, MemUsedBytes: &memUsed, MemLimitBytes: &memLimit},
		},
	}

	out := snapshotToStatusOutput("prod", "root@1.2.3.4", snap)

	if out.Server != "prod" || out.Host != "root@1.2.3.4" || !out.Reachable || out.LatencyMs != 42 {
		t.Errorf("scalar fields wrong: %+v", out)
	}
	if out.CPU != "42.5" {
		t.Errorf("cpu_percent = %q, want 42.5", out.CPU)
	}
	if out.RAMUsedMB != "2048" || out.RAMTotalMB != "4096" {
		t.Errorf("ram MB = %q/%q, want 2048/4096", out.RAMUsedMB, out.RAMTotalMB)
	}
	if out.DiskUsed != "5.0G" || out.DiskTotal != "20G" {
		t.Errorf("disk = %q/%q, want 5.0G/20G", out.DiskUsed, out.DiskTotal)
	}
	if out.Uptime == "" {
		t.Errorf("uptime should be populated")
	}
	if out.Apps.Total != 3 || out.Apps.Running != 2 || out.Apps.Stopped != 1 {
		t.Errorf("apps = %+v", out.Apps)
	}
	if out.Services.Running != 1 || out.Services.Total != 1 {
		t.Errorf("services = %+v", out.Services)
	}
	if len(out.Containers) != 1 || out.Containers[0].Name != "app-ghost" || out.Containers[0].CPU != "12.5%" {
		t.Errorf("containers = %+v", out.Containers)
	}
	if out.Containers[0].Memory == "" {
		t.Errorf("container memory should be populated")
	}

	// The JSON schema (field names) must be exactly the legacy set.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		"server", "host", "reachable", "latency_ms", "cpu_percent",
		"ram_used_mb", "ram_total_mb", "disk_used", "disk_total", "uptime",
		"apps", "services", "containers",
	} {
		if !bytes.Contains(b, []byte(`"`+key+`"`)) {
			t.Errorf("JSON is missing legacy key %q: %s", key, b)
		}
	}
}

// TestSnapshotToStatusOutputUnavailable verifies unavailable metrics render as
// empty strings (matching the pre-refactor behaviour) and that containers is
// always a non-nil array for a stable JSON shape.
func TestSnapshotToStatusOutputUnavailable(t *testing.T) {
	snap := operations.Snapshot{
		Server:    operations.ServerSummary{Name: "s", Host: "h"},
		Reachable: false,
		// all metric pointers nil, no containers
	}
	out := snapshotToStatusOutput("s", "h", snap)
	if out.CPU != "" || out.RAMUsedMB != "" || out.RAMTotalMB != "" ||
		out.DiskUsed != "" || out.DiskTotal != "" || out.Uptime != "" {
		t.Errorf("unavailable metrics should be empty strings: %+v", out)
	}
	if out.Containers == nil {
		t.Errorf("containers must be a non-nil empty slice")
	}
	b, _ := json.Marshal(out)
	if !bytes.Contains(b, []byte(`"containers": []`)) && !bytes.Contains(b, []byte(`"containers":[]`)) {
		t.Errorf("containers should marshal to []: %s", b)
	}
}

func TestHumanizeDiskBytes(t *testing.T) {
	cases := map[uint64]string{
		5 * 1024 * 1024 * 1024:  "5.0G",
		20 * 1024 * 1024 * 1024: "20G",
		512 * 1024 * 1024:       "512M",
		0:                       "0B",
	}
	for in, want := range cases {
		if got := humanizeDiskBytes(in); got != want {
			t.Errorf("humanizeDiskBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
