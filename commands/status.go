package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newStatusCmd() *cobra.Command {
	var liveFlag bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show server health, resource usage, and container stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			if liveFlag {
				return runStatusLive()
			}
			return runStatus()
		},
	}

	cmd.Flags().BoolVar(&liveFlag, "live", false, "show live-updating metrics (refreshes every 3s)")
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

	// Server section
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s\n", ui.Bold.Render("SERVER")))
	sb.WriteString(fmt.Sprintf("  %s\n", ui.Faint.Render("─────────────────────────────────────")))
	sb.WriteString(fmt.Sprintf("  %-11s %s%%\n", "CPU:", vm["CPU"]))
	if parts := strings.SplitN(vm["MEM"], "/", 2); len(parts) == 2 {
		sb.WriteString(fmt.Sprintf("  %-11s %s MB / %s MB\n", "RAM:", parts[0], parts[1]))
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
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Bold.Render("CONTAINERS")))
		sb.WriteString(fmt.Sprintf("  %s\n", ui.Faint.Render("──────────────────────────────────────────────────────────────────────────")))
		sb.WriteString(fmt.Sprintf("  %-22s %-8s %-22s %-16s %s\n", "NAME", "CPU", "MEMORY", "NET I/O", "BLOCK I/O"))
		for _, line := range strings.Split(containerOut, "\n") {
			parts := strings.SplitN(line, "\t", 5)
			if len(parts) == 5 {
				sb.WriteString(fmt.Sprintf("  %-22s %-8s %-22s %-16s %s\n",
					parts[0], parts[1], ui.Faint.Render(parts[2]), ui.Faint.Render(parts[3]), ui.Faint.Render(parts[4])))
			}
		}
	}

	sb.WriteString("\n")
	return sb.String(), nil
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
