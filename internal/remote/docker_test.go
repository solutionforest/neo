package remote

import (
	"strings"
	"testing"
)

func TestRunCommandHostname(t *testing.T) {
	cmd := runCommand("docker", RunOpts{Name: "app-mq", Image: "rabbitmq:4", Hostname: "mq"})
	if !strings.Contains(cmd, "--hostname 'mq'") {
		t.Errorf("hostname missing: %s", cmd)
	}
	if cmd := runCommand("docker", RunOpts{Name: "app-web", Image: "web"}); strings.Contains(cmd, "--hostname") {
		t.Errorf("no hostname set, but got: %s", cmd)
	}
}

func TestRunCommandQuotesHealthDurations(t *testing.T) {
	// These come from server state; the env restart path used to validate them
	// itself, and now relies on Run to keep them from reaching the shell raw.
	cmd := runCommand("docker", RunOpts{
		Image:          "web",
		HealthCmd:      "true",
		HealthInterval: "30s; rm -rf /",
	})
	if !strings.Contains(cmd, "--health-interval '30s; rm -rf /'") {
		t.Errorf("health interval not quoted: %s", cmd)
	}
}
