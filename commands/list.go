package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newListCmd() *cobra.Command {
	var formatFlag string
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List all apps on the current server",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonFlag {
				formatFlag = "json"
			}
			return runList(formatFlag)
		},
	}

	cmd.Flags().StringVar(&formatFlag, "format", "table", "output format: table or json")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON (shorthand for --format json)")

	return cmd
}

func runList(format string) error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Cross-check state against the containers actually on the server. A Docker
	// failure here is not fatal — fall back to reporting state alone.
	drift, driftErr := detectStateDrift(remote.NewDocker(exec), st)

	if format == "json" {
		return runListJSON(srv.Name, st, drift)
	}

	fmt.Println()
	fmt.Printf("  Server: %s (%s)\n", ui.Bold.Render(srv.Name), srv.Host)
	fmt.Println()

	if len(st.Apps) == 0 {
		fmt.Println("  No apps installed.")
		fmt.Println()
		if driftErr == nil && len(drift.Untracked) > 0 {
			// "No apps installed" while containers are serving traffic is the
			// confusing case this check exists for — say so instead of lying.
			reportStateDrift(drift)
		} else {
			ui.Info("Install an app: neo install")
			fmt.Println()
		}
		return nil
	}

	fmt.Println("  " + fmt.Sprintf("%-18s %-32s %-6s %-12s %s", "NAME", "DOMAIN", "PORT", "STATUS", "IMAGE"))
	fmt.Println("  " + ui.Faint.Render("──────────────────────────────────────────────────────────────────────────────"))

	running, stopped := 0, 0
	for _, a := range st.Apps {
		domain := a.Domain
		if domain == "" {
			domain = "—"
		}
		bullet := ui.StatusBullet(a.Status)
		fmt.Printf("  %s %-17s %-32s %-6d %-12s %s\n", bullet, a.Name, domain, a.InternalPort, a.Status, ui.Faint.Render(a.Image))
		if a.Status == "running" {
			running++
		} else {
			stopped++
		}
		// Show workers indented under the app
		for wName, w := range a.Workers {
			wBullet := ui.StatusBullet(w.Status)
			fmt.Printf("    %s %-15s %s\n", wBullet, wName, ui.Faint.Render(w.Command))
		}
	}

	fmt.Println()
	fmt.Printf("  %d apps · %d running · %d stopped\n", len(st.Apps), running, stopped)
	fmt.Println()

	if driftErr == nil {
		reportStateDrift(drift)
	}

	// Show shared services
	if len(st.Services) > 0 {
		fmt.Println()
		fmt.Println("  " + ui.Bold.Render("Shared Services"))
		fmt.Println("  " + fmt.Sprintf("%-18s %-25s %s", "NAME", "IMAGE", "STATUS"))
		fmt.Println("  " + ui.Faint.Render("────────────────────────────────────────────────────────"))

		for _, svc := range st.Services {
			bullet := ui.StatusBullet(svc.Status)
			fmt.Printf("  %s %-17s %-25s %s\n", bullet, svc.Name, ui.Faint.Render(svc.Image), svc.Status)
		}
	}

	fmt.Println()

	return nil
}

// listOutput is the JSON structure for --format json.
type listOutput struct {
	Server   string                         `json:"server"`
	Apps     map[string]state.App           `json:"apps"`
	Services map[string]state.SharedService `json:"services"`
	Drift    *driftOutput                   `json:"drift,omitempty"`
}

// driftOutput exposes state-vs-server disagreement to scripts, which otherwise
// have no way to tell an empty server from a server whose state was lost.
type driftOutput struct {
	Untracked []string `json:"untracked,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	Stopped   []string `json:"stopped,omitempty"`
}

func runListJSON(serverName string, st *state.State, drift stateDrift) error {
	out := listOutput{
		Server:   serverName,
		Apps:     st.Apps,
		Services: st.Services,
	}
	if !drift.Empty() {
		out.Drift = &driftOutput{
			Untracked: drift.Untracked,
			Missing:   drift.Missing,
			Stopped:   drift.Stopped,
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
