package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server health, resource usage, and container stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
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
	out, err := exec.Run(`echo "CPU:$(top -bn1 2>/dev/null | grep '%Cpu' | awk '{print 100-$8}' || echo '?')" && ` +
		`echo "MEM:$(free -m 2>/dev/null | awk '/Mem:/{printf "%d/%d", $3, $2}' || echo '?')" && ` +
		`echo "DISK:$(df -h / 2>/dev/null | awk 'NR==2{printf "%s/%s", $3, $2}' || echo '?')" && ` +
		`echo "UP:$(uptime -p 2>/dev/null | sed 's/^up //' || echo '?')"`)
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "CPU:") {
				cpuUsage = strings.TrimPrefix(line, "CPU:")
			} else if strings.HasPrefix(line, "MEM:") {
				parts := strings.SplitN(strings.TrimPrefix(line, "MEM:"), "/", 2)
				if len(parts) == 2 {
					memUsed, memTotal = parts[0], parts[1]
				}
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
	fmt.Printf("  %-11s %s\n", "CPU:", cpuUsage+"%")
	fmt.Printf("  %-11s %s MB / %s MB\n", "RAM:", memUsed, memTotal)
	fmt.Printf("  %-11s %s / %s\n", "Disk:", diskUsed, diskTotal)
	fmt.Printf("  %-11s %s\n", "Uptime:", uptime)
	fmt.Println()
	fmt.Printf("  %-11s %s\n", "Apps:", formatAppCounts(runningApps, stoppedApps))
	fmt.Printf("  %-11s %d running\n", "Services:", runningServices)

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

func formatAppCounts(running, stopped int) string {
	parts := []string{
		fmt.Sprintf("%d running", running),
	}
	if stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	return strings.Join(parts, ", ")
}
