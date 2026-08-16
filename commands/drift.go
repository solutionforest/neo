package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// stateDrift is the difference between /etc/neo/state.json and what is actually
// running on the server.
//
// The two can disagree: a failed state write, an interrupted deploy, or a
// container removed with plain `docker rm` all leave the file describing a
// server that no longer matches. Without this check `neo list` cheerfully
// reports "No apps installed" while the app is up and serving traffic.
type stateDrift struct {
	Untracked []string // running app containers with no entry in state
	Missing   []string // apps in state whose container does not exist
	Stopped   []string // apps in state whose container exists but is not running
}

// Empty reports whether state and the server agree.
func (d stateDrift) Empty() bool {
	return len(d.Untracked) == 0 && len(d.Missing) == 0 && len(d.Stopped) == 0
}

// appNameFromContainer maps a container name back to its app name, or ""
// when the container is not a primary app container.
//
// Workers (app-<app>-worker-<name>), scaled replicas (app-<app>-0) and the
// blue-green staging container (app-<app>-next) all share the app- prefix but
// are not apps in their own right; counting them would report drift that isn't
// there. knownApps disambiguates replicas, whose suffix is otherwise
// indistinguishable from a normal name ending in a number.
func appNameFromContainer(containerName string, knownApps map[string]state.App) string {
	name, ok := strings.CutPrefix(containerName, config.AppContainerPrefix)
	if !ok || name == "" {
		return ""
	}
	if strings.Contains(name, "-worker-") || strings.HasSuffix(name, "-next") {
		return ""
	}
	// A replica is "<app>-<n>" for an app that exists in state.
	if idx := strings.LastIndexByte(name, '-'); idx > 0 {
		base, suffix := name[:idx], name[idx+1:]
		if _, tracked := knownApps[base]; tracked && isAllDigits(suffix) {
			return ""
		}
	}
	return name
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// detectStateDrift compares server state against the containers on the server.
// A Docker failure is not fatal: callers fall back to showing state alone
// rather than refusing to list anything.
func detectStateDrift(docker *remote.Docker, st *state.State) (stateDrift, error) {
	var drift stateDrift

	containers, err := docker.ListContainers(config.AppContainerPrefix)
	if err != nil {
		return drift, err
	}

	live := make(map[string]remote.ContainerInfo, len(containers))
	for _, c := range containers {
		if appName := appNameFromContainer(c.Name, st.Apps); appName != "" {
			live[appName] = c
		}
	}

	for appName, c := range live {
		if _, tracked := st.Apps[appName]; !tracked && c.State == "running" {
			drift.Untracked = append(drift.Untracked, appName)
		}
	}
	for appName := range st.Apps {
		c, exists := live[appName]
		switch {
		case !exists:
			drift.Missing = append(drift.Missing, appName)
		case c.State != "running":
			drift.Stopped = append(drift.Stopped, appName)
		}
	}

	sort.Strings(drift.Untracked)
	sort.Strings(drift.Missing)
	sort.Strings(drift.Stopped)
	return drift, nil
}

// reportStateDrift prints a warning block when state and the server disagree.
// Silent when they agree, so normal runs stay clean.
func reportStateDrift(drift stateDrift) {
	if drift.Empty() {
		return
	}

	if len(drift.Untracked) > 0 {
		ui.Error(fmt.Sprintf("%d running app(s) missing from server state: %s",
			len(drift.Untracked), strings.Join(drift.Untracked, ", ")))
		ui.Info("These containers are serving traffic but neo has no record of them — 'neo list' and the dashboard undercount.")
		ui.Info("Redeploy each one to restore its record: neo deploy --to <environment>")
	}
	if len(drift.Missing) > 0 {
		ui.Error(fmt.Sprintf("%d app(s) in state have no container: %s",
			len(drift.Missing), strings.Join(drift.Missing, ", ")))
		ui.Info("The record exists but nothing is running. Redeploy, or remove the record with: neo remove <app>")
	}
	if len(drift.Stopped) > 0 {
		ui.Info(fmt.Sprintf("%d app(s) not running: %s — start with: neo start <app>",
			len(drift.Stopped), strings.Join(drift.Stopped, ", ")))
	}
	fmt.Println()
}
