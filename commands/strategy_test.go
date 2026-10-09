package commands

import (
	"strings"
	"testing"

	"github.com/vxero/neo/internal/state"
)

func TestResolveStrategy(t *testing.T) {
	cases := []struct {
		name         string
		cfg          *NeoConfig
		scale        int
		wantStrategy string
		wantHostname string
		wantErr      string
	}{
		{name: "no config", cfg: nil},
		{name: "default", cfg: &NeoConfig{}},
		{name: "explicit blue-green is stored as default", cfg: &NeoConfig{Strategy: "blue-green"}},
		{
			// Docker's random hostname made each RabbitMQ recreate a new, empty node.
			name: "recreate pins hostname to app name", cfg: &NeoConfig{Strategy: "recreate"},
			wantStrategy: "recreate", wantHostname: "e-hub-rabbitmq",
		},
		{
			name: "recreate keeps explicit hostname", cfg: &NeoConfig{Strategy: "recreate", Hostname: "rabbitmq"},
			wantStrategy: "recreate", wantHostname: "rabbitmq",
		},
		{name: "hostname alone", cfg: &NeoConfig{Hostname: "mq.internal"}, wantHostname: "mq.internal"},
		{name: "recreate with scale", cfg: &NeoConfig{Strategy: "recreate"}, scale: 3, wantErr: "scale"},
		{name: "hostname with scale", cfg: &NeoConfig{Hostname: "mq"}, scale: 2, wantErr: "replica"},
		{name: "unknown strategy", cfg: &NeoConfig{Strategy: "rolling"}, wantErr: "unknown strategy"},
		{name: "invalid hostname", cfg: &NeoConfig{Hostname: "Bad_Host"}, wantErr: "not a valid hostname"},
		{name: "hostname injection", cfg: &NeoConfig{Hostname: "mq; reboot"}, wantErr: "not a valid hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, hostname, err := resolveStrategy("e-hub-rabbitmq", tc.cfg, tc.scale)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strategy != tc.wantStrategy || hostname != tc.wantHostname {
				t.Errorf("got (%q, %q), want (%q, %q)", strategy, hostname, tc.wantStrategy, tc.wantHostname)
			}
		})
	}
}

func TestAppRunOptsKeepsStateFields(t *testing.T) {
	// env set/unset/import, update and volumes mount rebuild the container
	// from state; the first two used to drop command:.
	app := state.App{
		Name:     "e-hub-rabbitmq",
		Image:    "neo-e-hub-rabbitmq:20261009-abc",
		Env:      map[string]string{"RABBITMQ_DEFAULT_USER": "neo"},
		Command:  "rabbitmq-server",
		Hostname: "e-hub-rabbitmq",
		Restart:  "always",
		Volumes:  map[string]state.VolumeInfo{"e-hub-rabbitmq-data": {ContainerPath: "/var/lib/rabbitmq"}},
		Health:   &state.HealthCheck{Cmd: "rabbitmq-diagnostics ping", Interval: "30s"},
	}
	opts := appRunOpts(app, "app-e-hub-rabbitmq")

	if opts.Cmd != "rabbitmq-server" {
		t.Errorf("Cmd = %q", opts.Cmd)
	}
	if opts.Hostname != "e-hub-rabbitmq" {
		t.Errorf("Hostname = %q", opts.Hostname)
	}
	if opts.Restart != "always" || opts.Image != app.Image || opts.Name != "app-e-hub-rabbitmq" {
		t.Errorf("basic fields wrong: %+v", opts)
	}
	if len(opts.Volumes) != 1 || opts.Volumes[0] != "e-hub-rabbitmq-data:/var/lib/rabbitmq" {
		t.Errorf("Volumes = %v", opts.Volumes)
	}
	if opts.HealthCmd != "rabbitmq-diagnostics ping" || opts.HealthInterval != "30s" {
		t.Errorf("health not applied: %+v", opts)
	}
	if opts.Env["RABBITMQ_DEFAULT_USER"] != "neo" {
		t.Errorf("Env = %v", opts.Env)
	}
}
