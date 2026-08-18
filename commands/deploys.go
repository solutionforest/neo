package commands

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// Deployment history lives beside state, one append-only file per app.
//
// It is deliberately NOT part of state.json. That file is a whole-document
// read-modify-write, the same structure that lost app entries when two deploys
// raced (fixed in 0.24.1). An append has no read step, so concurrent deploys
// cannot overwrite each other's history — and history is precisely what you
// want to survive a race.
//
// It is also server-side rather than in the project folder: two laptops and a
// CI runner deploying the same app must share one record, it has to survive a
// fresh clone, and rollback can only offer builds whose image still exists on
// that particular server.
const (
	deploysDir = "/etc/neo/deploys"

	// deployHistoryLimit caps the log so it can't grow without bound on a small
	// VPS. Well beyond the two or three images prune retains, so the log still
	// shows history that is no longer restorable.
	deployHistoryLimit = 50
)

func deployHistoryPath(appName string) string {
	return deploysDir + "/" + appName + ".jsonl"
}

// recordDeployment appends one entry to an app's history. Failure is reported
// but never fails a deploy: the app is already live by this point, and losing a
// log line is not a reason to tell the user their deploy failed.
func recordDeployment(exec *neossh.Executor, appName string, dep *state.Deployment) {
	if dep == nil {
		return
	}

	line, err := json.Marshal(dep)
	if err != nil {
		ui.Error(fmt.Sprintf("could not record deployment history: %s", err))
		return
	}

	path := deployHistoryPath(appName)
	sudo := ""
	if exec.User() != "root" {
		sudo = "sudo "
	}

	// Append, then trim to the newest deployHistoryLimit lines. Done in one
	// command so a second deploy can't interleave between the two steps.
	cmd := fmt.Sprintf(
		`%smkdir -p %s && printf '%%s\n' %s | %stee -a %s >/dev/null && %ssh -c %s`,
		sudo, neossh.ShellQuote(deploysDir),
		neossh.ShellQuote(string(line)),
		sudo, neossh.ShellQuote(path),
		sudo,
		neossh.ShellQuote(fmt.Sprintf("tail -n %d %s > %s.tmp && mv %s.tmp %s",
			deployHistoryLimit, path, path, path, path)),
	)
	if err := exec.RunQuiet(cmd); err != nil {
		ui.Error(fmt.Sprintf("could not record deployment history: %s", err))
	}
}

// readDeployHistory returns an app's deployments, newest first.
func readDeployHistory(exec *neossh.Executor, appName string) ([]state.Deployment, error) {
	data, err := exec.ReadFileElevated(deployHistoryPath(appName))
	if err != nil {
		// No history yet is not an error: apps deployed before this existed,
		// and a brand new app, both legitimately have none.
		return nil, nil
	}

	var out []state.Deployment
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var dep state.Deployment
		if err := json.Unmarshal([]byte(line), &dep); err != nil {
			continue // a truncated write shouldn't hide the rest of the log
		}
		out = append(out, dep)
	}

	// Newest first — the order people read a deploy log in.
	sort.SliceStable(out, func(i, j int) bool { return out[i].DeployedAt > out[j].DeployedAt })
	return out, nil
}

func newDeploysCmd() *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "deploys [app]",
		Short: "Show an app's deployment history",
		Long: `Lists what has been deployed for an app: build identifier, commit, who deployed it and when.

History is recorded on the server, so it is shared by everyone who deploys — including CI.
Entries whose image has since been pruned are marked, because those can no longer be redeployed
from the image alone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := resolveEnvApp(args)
			return runDeploys(name, jsonFlag)
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	return cmd
}

func runDeploys(appName string, asJSON bool) error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	history, err := readDeployHistory(exec, appName)
	if err != nil {
		return err
	}

	st, stErr := state.Load(exec)
	var current *state.Deployment
	if stErr == nil {
		if app, ok := st.Apps[appName]; ok {
			current = app.Deployment
		}
	}

	// An entry is only redeployable while its image is still on the server —
	// prune keeps just the last few, so most history is informational.
	images := map[string]bool{}
	if list, imgErr := remote.NewDocker(exec).ListImages("neo-" + appName); imgErr == nil {
		for _, img := range list {
			images[img] = true
		}
	}

	if asJSON {
		type entry struct {
			state.Deployment
			Current   bool `json:"current"`
			Available bool `json:"image_available"`
		}
		out := make([]entry, 0, len(history))
		for _, dep := range history {
			out = append(out, entry{
				Deployment: dep,
				Current:    current != nil && current.ID == dep.ID,
				Available:  images[dep.Image],
			})
		}
		data, mErr := json.MarshalIndent(out, "", "  ")
		if mErr != nil {
			return mErr
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Println()
	fmt.Printf("  %s on %s\n\n", ui.Bold.Render(appName), srv.Name)

	if len(history) == 0 {
		ui.Info("No deployment history yet.")
		ui.Info("History is recorded from the next deploy onwards — apps deployed by older versions of neo have none.")
		fmt.Println()
		return nil
	}

	fmt.Printf("  %-20s %-18s %-22s %s\n", "DEPLOYED", "VERSION", "BY", "")
	fmt.Println("  " + ui.Faint.Render(strings.Repeat("─", 78)))

	for _, dep := range history {
		marker := " "
		if current != nil && current.ID == dep.ID {
			marker = ui.Green.Render("●")
		}

		note := ""
		if !images[dep.Image] {
			note = ui.Faint.Render("image pruned")
		}
		if dep.Dirty {
			note = strings.TrimSpace(ui.Yellow.Render("dirty") + " " + note)
		}

		version := dep.Describe()
		if version == "" {
			version = ui.Faint.Render("no git info")
		}

		fmt.Printf("  %s %-18s %-18s %-22s %s\n",
			marker, formatDeployAge(dep.DeployedAt), version, dep.DeployedBy, note)
	}

	fmt.Println()
	ui.Info(fmt.Sprintf("%d deployment(s) recorded · ● = currently serving", len(history)))
	fmt.Println()
	return nil
}

// formatDeployAge renders an RFC3339 timestamp as a relative age, falling back
// to the raw value when it can't be parsed.
func formatDeployAge(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
