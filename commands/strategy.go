package commands

import (
	"fmt"
	"regexp"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

const (
	strategyBlueGreen = "blue-green"
	strategyRecreate  = "recreate"

	// recreateStopTimeout is how long a recreate-strategy app gets to shut down
	// before Docker kills it. Brokers and databases flush to disk on SIGTERM;
	// Docker's 10s default (and the rm -f the blue-green swap uses) can cut
	// that short and leave the data needing recovery on the next start.
	recreateStopTimeout = 60
)

var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// resolveStrategy validates strategy:/hostname: from the merged .neo.yml and
// applies defaults. The strategy comes back normalised: "" for blue-green (so
// state for existing apps is unchanged) or "recreate".
//
// Under recreate the hostname defaults to the app name. Docker otherwise gives
// every new container a random hostname, and stateful images key their data on
// it — RabbitMQ names its node rabbit@<hostname> and keeps data per node name,
// so each recreate came up as a fresh, empty node on the same volume.
func resolveStrategy(appName string, cfg *NeoConfig, scale int) (strategy, hostname string, err error) {
	if cfg != nil {
		strategy, hostname = cfg.Strategy, cfg.Hostname
	}
	switch strategy {
	case "", strategyBlueGreen:
		strategy = ""
	case strategyRecreate:
		if scale > 1 {
			return "", "", fmt.Errorf("strategy: recreate can't be combined with scale: %d — recreate stops the only copy before starting the new one", scale)
		}
		if hostname == "" {
			hostname = appName
		}
	default:
		return "", "", fmt.Errorf("unknown strategy: %q — use %q (default) or %q", strategy, strategyBlueGreen, strategyRecreate)
	}
	if hostname != "" && scale > 1 {
		return "", "", fmt.Errorf("hostname: can't be combined with scale: %d — every replica would get the same hostname", scale)
	}
	if hostname != "" && (len(hostname) > 253 || !hostnamePattern.MatchString(hostname)) {
		return "", "", fmt.Errorf("hostname: %q is not a valid hostname (lowercase letters, digits, '-' and '.')", hostname)
	}
	return strategy, hostname, nil
}

// warnSharedHostname flags the combination that is worse than either setting
// alone: blue-green runs the old and new container side by side on the same
// volumes, and a fixed hostname gives them the same identity while they do. For
// a broker or database that is two processes claiming one data directory.
func warnSharedHostname(strategy, hostname string, hasVolumes bool) {
	if strategy != "" || hostname == "" || !hasVolumes {
		return
	}
	ui.Error(fmt.Sprintf("hostname: %s with the default blue-green strategy briefly runs two containers with that hostname on the same volumes.", hostname))
	ui.Info("For a database or message broker, add strategy: recreate so the old container stops before the new one starts.")
}

// stopForReplace stops and removes the app container ahead of starting its
// replacement. Recreate-strategy apps get time to shut down cleanly.
func stopForReplace(docker *remote.Docker, name, strategy string) {
	if strategy == strategyRecreate {
		docker.StopWait(name, recreateStopTimeout) //nolint:errcheck // absent container is fine
	}
	docker.Remove(name) //nolint:errcheck
}

// appRunOpts rebuilds an app container's options from server state. Every path
// that recreates the container without a deploy (env set/unset/import, update,
// volumes mount) goes through here, so a field persisted in state — command,
// hostname, health — can't be dropped by one of them.
func appRunOpts(app state.App, name string) remote.RunOpts {
	opts := remote.RunOpts{
		Name:     name,
		Image:    app.Image,
		Network:  config.DockerNetwork,
		Restart:  restartPolicy(app.Restart),
		Volumes:  volumesFromState(app.Volumes),
		Env:      app.Env,
		Cmd:      app.Command,
		Hostname: app.Hostname,
	}
	applyHealth(&opts, app.Health)
	return opts
}
