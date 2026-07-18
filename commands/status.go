package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/operations"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newStatusCmd() *cobra.Command {
	var liveFlag bool
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show server health, resource usage, and container stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonFlag {
				return runStatusJSON()
			}
			if liveFlag {
				return runStatusLive()
			}
			return runStatus()
		},
	}

	cmd.Flags().BoolVar(&liveFlag, "live", false, "show live-updating metrics (refreshes every 3s)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output server health and container stats as JSON")
	return cmd
}

func runStatus() error {
	cfg, srv, exec, err := mustResolveAndConnect()
	_ = cfg
	if err != nil {
		return err
	}
	defer exec.Close()

	// Ping latency
	start := time.Now()
	latency := time.Duration(0)
	if _, pingErr := exec.Run("true"); pingErr == nil {
		latency = time.Since(start)
	}

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Count apps/services
	runningApps, stoppedApps := 0, 0
	for _, a := range st.Apps {
		if a.Status == "running" {
			runningApps++
		} else {
			stoppedApps++
		}
	}
	runningServices := 0
	for _, svc := range st.Services {
		if svc.Status == "running" {
			runningServices++
		}
	}

	// Server resources (single command to reduce SSH round trips)
	cpuUsage, memUsed, memTotal, diskUsed, diskTotal, uptime := "?", "?", "?", "?", "?", "?"
	var cpuPct, memPct, diskPct int
	out, err := exec.Run(`echo "CPU:$(top -bn1 2>/dev/null | grep '%Cpu' | awk '{print 100-$8}' || echo '?')" && ` +
		`echo "MEM:$(free -m 2>/dev/null | awk '/Mem:/{printf "%d/%d", $3, $2}' || echo '?')" && ` +
		`echo "DISK:$(df -h / 2>/dev/null | awk 'NR==2{printf "%s/%s", $3, $2}' || echo '?')" && ` +
		`echo "DISKPCT:$(df / 2>/dev/null | awk 'NR==2{printf "%d", int($3*100/$2)}' || echo '0')" && ` +
		`echo "UP:$(uptime -p 2>/dev/null | sed 's/^up //' || echo '?')"`)
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "CPU:") {
				cpuUsage = strings.TrimPrefix(line, "CPU:")
				fmt.Sscanf(cpuUsage, "%d", &cpuPct)
			} else if strings.HasPrefix(line, "MEM:") {
				parts := strings.SplitN(strings.TrimPrefix(line, "MEM:"), "/", 2)
				if len(parts) == 2 {
					memUsed, memTotal = parts[0], parts[1]
					var used, total int
					fmt.Sscanf(memUsed, "%d", &used)
					fmt.Sscanf(memTotal, "%d", &total)
					if total > 0 {
						memPct = used * 100 / total
					}
				}
			} else if strings.HasPrefix(line, "DISKPCT:") {
				fmt.Sscanf(strings.TrimPrefix(line, "DISKPCT:"), "%d", &diskPct)
			} else if strings.HasPrefix(line, "DISK:") {
				parts := strings.SplitN(strings.TrimPrefix(line, "DISK:"), "/", 2)
				if len(parts) == 2 {
					diskUsed, diskTotal = parts[0], parts[1]
				}
			} else if strings.HasPrefix(line, "UP:") {
				uptime = strings.TrimPrefix(line, "UP:")
			}
		}
	}

	// Container stats
	containerStats := ""
	if out, err := exec.Run(`docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' 2>/dev/null`); err == nil {
		containerStats = strings.TrimSpace(out)
	}

	// Print output
	fmt.Println()
	fmt.Printf("  Server: %s (%s)\n", ui.Bold.Render(srv.Name), srv.Host)
	fmt.Println()
	fmt.Printf("  %s Reachable (%s)\n", ui.Green.Render("●"), latency.Round(time.Millisecond))
	fmt.Printf("  %-11s %s\n", "CPU:", colorResourcePct(cpuPct, cpuUsage+"%"))
	fmt.Printf("  %-11s %s MB / %s MB\n", "RAM:", colorResourcePct(memPct, memUsed), memTotal)
	fmt.Printf("  %-11s %s / %s\n", "Disk:", colorResourcePct(diskPct, diskUsed), diskTotal)
	fmt.Printf("  %-11s %s\n", "Uptime:", uptime)
	fmt.Println()
	fmt.Printf("  %-11s %s\n", "Apps:", formatAppCounts(runningApps, stoppedApps))
	fmt.Printf("  %-11s %d running\n", "Services:", runningServices)

	// Resource advisories
	printResourceAdvisory(cpuPct, memPct, diskPct)

	// Container table
	if containerStats != "" {
		fmt.Println()
		fmt.Printf("  %-25s %-8s %s\n", "CONTAINER", "CPU", "MEMORY")
		fmt.Printf("  %s\n", ui.Faint.Render("─────────────────────────────────────────────"))
		for _, line := range strings.Split(containerStats, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				name := parts[0]
				cpu := parts[1]
				mem := parts[2]
				fmt.Printf("  %-25s %-8s %s\n", name, cpu, ui.Faint.Render(mem))
			}
		}
	}

	fmt.Println()
	return nil
}

func runStatusLive() error {
	ui.SetVersion(cliVersion)
	cfg, srv, exec, err := mustResolveAndConnect()
	_ = cfg
	if err != nil {
		return err
	}
	defer exec.Close()

	return ui.RunLiveView(ui.LiveViewConfig{
		Title:    fmt.Sprintf("  %s  %s", ui.Bold.Render(srv.Name), ui.Faint.Render(srv.Host)),
		Interval: 3 * time.Second,
		Render: func() (string, error) {
			return fetchLiveMetrics(exec)
		},
	})
}

func fetchLiveMetrics(exec *ssh.Executor) (string, error) {
	// VM metrics — single compound SSH command to reduce round trips
	vmOut, vmErr := exec.Run(
		`echo "CPU:$(top -bn1 2>/dev/null | grep '%Cpu' | awk '{print 100-$8}' || echo '?')" && ` +
			`echo "MEM:$(free -m 2>/dev/null | awk '/Mem:/{printf "%d/%d", $3, $2}' || echo '?')" && ` +
			`echo "DISK:$(df -h / 2>/dev/null | awk 'NR==2{printf "%s/%s (%s)", $3, $2, $5}' || echo '?')" && ` +
			`echo "DISKPCT:$(df / 2>/dev/null | awk 'NR==2{printf "%d", int($3*100/$2)}' || echo '0')" && ` +
			`echo "LOAD:$(cat /proc/loadavg 2>/dev/null | awk '{printf "%s %s %s", $1, $2, $3}' || echo '?')" && ` +
			`echo "UP:$(uptime -p 2>/dev/null | sed 's/^up //' || echo '?')" && ` +
			`echo "NET:$(cat /proc/net/dev 2>/dev/null | awk '/eth0:|ens[0-9]/{gsub(/:/,""); printf "%s rx=%s tx=%s", $1, $2, $10}' || echo '?')"`)
	if vmErr != nil {
		return "", vmErr
	}

	// Container stats
	containerOut, _ := exec.Run(
		`docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}' 2>/dev/null`)

	// Parse VM metrics
	vm := map[string]string{}
	for _, line := range strings.Split(vmOut, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, ":"); idx > 0 {
			vm[line[:idx]] = line[idx+1:]
		}
	}

	var sb strings.Builder

	// Parse numeric percentages for coloring and advisories
	var cpuPct, memPct, diskPct int
	fmt.Sscanf(vm["CPU"], "%d", &cpuPct)
	if parts := strings.SplitN(vm["MEM"], "/", 2); len(parts) == 2 {
		var used, total int
		fmt.Sscanf(parts[0], "%d", &used)
		fmt.Sscanf(parts[1], "%d", &total)
		if total > 0 {
			memPct = used * 100 / total
		}
	}
	fmt.Sscanf(vm["DISKPCT"], "%d", &diskPct)

	// Server section
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s\n", ui.Bold.Render("SERVER")))
	sb.WriteString(fmt.Sprintf("  %s\n", ui.Faint.Render("─────────────────────────────────────")))
	sb.WriteString(fmt.Sprintf("  %-11s %s\n", "CPU:", colorResourcePct(cpuPct, vm["CPU"]+"%")))
	if parts := strings.SplitN(vm["MEM"], "/", 2); len(parts) == 2 {
		sb.WriteString(fmt.Sprintf("  %-11s %s MB / %s MB\n", "RAM:", colorResourcePct(memPct, parts[0]), parts[1]))
	} else {
		sb.WriteString(fmt.Sprintf("  %-11s %s\n", "RAM:", vm["MEM"]))
	}
	sb.WriteString(fmt.Sprintf("  %-11s %s\n", "Disk:", vm["DISK"]))
	sb.WriteString(fmt.Sprintf("  %-11s %s\n", "Load:", vm["LOAD"]))
	sb.WriteString(fmt.Sprintf("  %-11s %s\n", "Network:", vm["NET"]))
	sb.WriteString(fmt.Sprintf("  %-11s %s\n", "Uptime:", vm["UP"]))

	// Container section
	containerOut = strings.TrimSpace(containerOut)
	if containerOut != "" {
		const (
			nameWidth  = 34
			cpuWidth   = 8
			memWidth   = 19
			netWidth   = 18
			blockWidth = 18
		)

		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Bold.Render("CONTAINERS")))
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Faint.Render("──────────────────────────────────────────────────────────────────────────")))
		sb.WriteString(fmt.Sprintf("  %s %s %s %s %s\n",
			fitTableCell("NAME", nameWidth),
			fitTableCell("CPU", cpuWidth),
			fitTableCell("MEMORY", memWidth),
			fitTableCell("NET I/O", netWidth),
			fitTableCell("BLOCK I/O", blockWidth),
		))
		for _, line := range strings.Split(containerOut, "\n") {
			parts := strings.SplitN(line, "\t", 5)
			if len(parts) == 5 {
				sb.WriteString(fmt.Sprintf("  %s %s %s %s %s\n",
					fitTableCell(parts[0], nameWidth),
					fitTableCell(parts[1], cpuWidth),
					ui.Faint.Render(fitTableCell(parts[2], memWidth)),
					ui.Faint.Render(fitTableCell(parts[3], netWidth)),
					ui.Faint.Render(fitTableCell(parts[4], blockWidth)),
				))
			}
		}
	}

	// Advisories
	type liveAdvisory struct{ icon, msg, hint string }
	var livAdvisories []liveAdvisory
	if cpuPct >= 90 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("CPU at %d%% — heavy load", cpuPct), "Avoid new deployments. Check `neo logs <app>`."})
	} else if cpuPct >= 75 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("CPU at %d%%", cpuPct), "Defer new deployments until load drops."})
	} else if cpuPct >= 50 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Yellow.Render("!"), fmt.Sprintf("CPU at %d%%", cpuPct), "Monitor for sustained high usage."})
	}
	if memPct >= 90 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("RAM at %d%% — OOM risk", memPct), "Stop unused apps: `neo stop <app>`"})
	} else if memPct >= 75 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("RAM at %d%%", memPct), "Avoid deploying new services."})
	} else if memPct >= 50 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Yellow.Render("!"), fmt.Sprintf("RAM at %d%%", memPct), "New deployments may push memory higher."})
	}
	if diskPct >= 90 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("Disk at %d%% — critical", diskPct), "Run: neo run 'docker system prune -af'"})
	} else if diskPct >= 75 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Red.Render("!"), fmt.Sprintf("Disk at %d%%", diskPct), "Run: neo run 'docker image prune -af'"})
	} else if diskPct >= 50 {
		livAdvisories = append(livAdvisories, liveAdvisory{ui.Yellow.Render("!"), fmt.Sprintf("Disk at %d%%", diskPct), "Consider: neo run 'docker image prune -f'"})
	}
	if len(livAdvisories) > 0 {
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Bold.Render("ADVISORIES")))
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Faint.Render("─────────────────────────────────────")))
		for _, a := range livAdvisories {
			sb.WriteString(fmt.Sprintf("  %s %s\n", a.icon, a.msg))
			sb.WriteString(fmt.Sprintf("    %s\n", ui.Faint.Render(a.hint)))
		}
	}

	sb.WriteString("\n")
	return sb.String(), nil
}

func fitTableCell(s string, width int) string {
	s = strings.TrimSpace(s)
	if width <= 0 {
		return s
	}

	r := []rune(s)
	if len(r) > width {
		if width <= 3 {
			return string(r[:width])
		}
		return string(r[:width-3]) + "..."
	}

	if len(r) < width {
		return s + strings.Repeat(" ", width-len(r))
	}

	return s
}

type statusOutput struct {
	Server     string          `json:"server"`
	Host       string          `json:"host"`
	Reachable  bool            `json:"reachable"`
	LatencyMs  int64           `json:"latency_ms"`
	CPU        string          `json:"cpu_percent"`
	RAMUsedMB  string          `json:"ram_used_mb"`
	RAMTotalMB string          `json:"ram_total_mb"`
	DiskUsed   string          `json:"disk_used"`
	DiskTotal  string          `json:"disk_total"`
	Uptime     string          `json:"uptime"`
	Apps       statusAppCount  `json:"apps"`
	Services   statusSvcCount  `json:"services"`
	Containers []containerStat `json:"containers"`
}

type statusAppCount struct {
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	Total   int `json:"total"`
}

type statusSvcCount struct {
	Running int `json:"running"`
	Total   int `json:"total"`
}

type containerStat struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

func runStatusJSON() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	// Collect through the shared operation layer so the CLI and the desktop
	// bridge use ONE snapshot implementation. The typed snapshot is then mapped
	// back to the legacy `neo status --json` shape by the adapter below, keeping
	// the CLI output backward compatible (plan "Phase 3: Domain types").
	summary := operations.ServerSummary{ID: srv.Name, Name: srv.Name, Host: srv.Host, Current: true}
	snap := operations.CollectSnapshot(
		context.Background(),
		operations.NewSSHExecutor(exec),
		summary,
		operations.SystemClock(),
	)

	out := snapshotToStatusOutput(srv.Name, srv.Host, snap)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// snapshotToStatusOutput adapts the shared typed Snapshot onto the legacy
// statusOutput JSON shape. It preserves every field name and JSON type exactly;
// only the collection path changed. An unavailable metric (nil pointer) renders
// as an empty string, matching the pre-refactor behaviour where a missing
// metric left the field blank.
func snapshotToStatusOutput(name, host string, snap operations.Snapshot) statusOutput {
	out := statusOutput{
		Server:     name,
		Host:       host,
		Reachable:  snap.Reachable,
		LatencyMs:  snap.LatencyMS,
		Apps:       statusAppCount{Running: snap.Apps.Running, Stopped: snap.Apps.Stopped, Total: snap.Apps.Total},
		Services:   statusSvcCount{Running: snap.Services.Running, Total: snap.Services.Total},
		Containers: []containerStat{},
	}

	if snap.CPUPercent != nil {
		out.CPU = strconv.FormatFloat(*snap.CPUPercent, 'f', -1, 64)
	}
	if snap.RAMUsedBytes != nil {
		out.RAMUsedMB = strconv.FormatUint(*snap.RAMUsedBytes/(1024*1024), 10)
	}
	if snap.RAMTotalBytes != nil {
		out.RAMTotalMB = strconv.FormatUint(*snap.RAMTotalBytes/(1024*1024), 10)
	}
	if snap.DiskUsedBytes != nil {
		out.DiskUsed = humanizeDiskBytes(*snap.DiskUsedBytes)
	}
	if snap.DiskTotalBytes != nil {
		out.DiskTotal = humanizeDiskBytes(*snap.DiskTotalBytes)
	}
	if snap.UptimeSeconds != nil {
		out.Uptime = humanizeUptime(*snap.UptimeSeconds)
	}

	for _, c := range snap.Containers {
		out.Containers = append(out.Containers, containerStat{
			Name:   c.Name,
			CPU:    formatContainerPercent(c.CPUPercent),
			Memory: formatContainerMem(c.MemUsedBytes, c.MemLimitBytes),
		})
	}
	return out
}

// humanizeDiskBytes renders bytes in `df -h` style: 1024-based, one fractional
// digit under 10, integer otherwise.
func humanizeDiskBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	units := []string{"K", "M", "G", "T", "P"}
	val := float64(b)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	if val < 10 {
		return fmt.Sprintf("%.1f%s", val, units[i])
	}
	return fmt.Sprintf("%.0f%s", val, units[i])
}

// humanizeMemBytes renders bytes in Docker's IEC style (e.g. "1.9GiB").
func humanizeMemBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	val := float64(b)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return fmt.Sprintf("%.1f%s", val, units[i])
}

// humanizeUptime renders a duration in seconds as a compact human string.
func humanizeUptime(sec uint64) string {
	days := sec / 86400
	hours := (sec % 86400) / 3600
	mins := (sec % 3600) / 60
	var parts []string
	if days > 0 {
		parts = append(parts, pluralUnit(days, "day"))
	}
	if hours > 0 {
		parts = append(parts, pluralUnit(hours, "hour"))
	}
	if mins > 0 {
		parts = append(parts, pluralUnit(mins, "minute"))
	}
	if len(parts) == 0 {
		return "less than a minute"
	}
	return strings.Join(parts, ", ")
}

func pluralUnit(n uint64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func formatContainerPercent(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64) + "%"
}

func formatContainerMem(used, limit *uint64) string {
	if used == nil || limit == nil {
		return ""
	}
	return humanizeMemBytes(*used) + " / " + humanizeMemBytes(*limit)
}

// colorResourcePct colorizes a resource value string based on its usage percentage.
// Green < 50%, Yellow 50–75%, Red > 75%.
func colorResourcePct(pct int, label string) string {
	switch {
	case pct >= 75:
		return ui.Red.Render(label)
	case pct >= 50:
		return ui.Yellow.Render(label)
	default:
		return label
	}
}

// printResourceAdvisory prints actionable warnings below `neo status` when CPU, RAM,
// or disk usage crosses the 50% or 75% threshold.
func printResourceAdvisory(cpuPct, memPct, diskPct int) {
	type advisory struct {
		icon string
		msg  string
		hint string
	}
	var advisories []advisory

	// CPU
	switch {
	case cpuPct >= 90:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("CPU is at %d%% — server is under heavy load", cpuPct),
			hint: "Avoid deploying new services. Run `neo logs <app>` to find the culprit.",
		})
	case cpuPct >= 75:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("CPU is at %d%%", cpuPct),
			hint: "Consider deferring new deployments until load drops.",
		})
	case cpuPct >= 50:
		advisories = append(advisories, advisory{
			icon: ui.Yellow.Render("!"),
			msg:  fmt.Sprintf("CPU is at %d%%", cpuPct),
			hint: "Monitor for sustained high usage.",
		})
	}

	// RAM
	switch {
	case memPct >= 90:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("RAM is at %d%% — risk of OOM crashes", memPct),
			hint: "Do not deploy new services. Stop unused apps with `neo stop <app>`.",
		})
	case memPct >= 75:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("RAM is at %d%%", memPct),
			hint: "Avoid deploying new services. Consider a larger server plan.",
		})
	case memPct >= 50:
		advisories = append(advisories, advisory{
			icon: ui.Yellow.Render("!"),
			msg:  fmt.Sprintf("RAM is at %d%%", memPct),
			hint: "Keep an eye on memory — new deployments may push it higher.",
		})
	}

	// Disk
	switch {
	case diskPct >= 90:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("Disk is at %d%% — server may stop working soon", diskPct),
			hint: "Run `neo run 'docker system prune -af'` immediately to free space.",
		})
	case diskPct >= 75:
		advisories = append(advisories, advisory{
			icon: ui.Red.Render("!"),
			msg:  fmt.Sprintf("Disk is at %d%%", diskPct),
			hint: "Prune unused Docker images: `neo run 'docker image prune -af'`",
		})
	case diskPct >= 50:
		advisories = append(advisories, advisory{
			icon: ui.Yellow.Render("!"),
			msg:  fmt.Sprintf("Disk is at %d%%", diskPct),
			hint: "Consider pruning old images: `neo run 'docker image prune -f'`",
		})
	}

	if len(advisories) == 0 {
		return
	}

	fmt.Println()
	fmt.Printf("  %s\n", ui.Bold.Render("ADVISORIES"))
	fmt.Printf("  %s\n", ui.Faint.Render("─────────────────────────────────────"))
	for _, a := range advisories {
		fmt.Printf("  %s %s\n", a.icon, a.msg)
		fmt.Printf("    %s\n", ui.Faint.Render(a.hint))
	}
}

func formatAppCounts(running, stopped int) string {
	parts := []string{
		fmt.Sprintf("%d running", running),
	}
	if stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	return strings.Join(parts, ", ")
}
