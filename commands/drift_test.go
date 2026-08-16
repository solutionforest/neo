package commands

import (
	"testing"

	"github.com/vxero/neo/internal/state"
)

func TestAppNameFromContainer(t *testing.T) {
	known := map[string]state.App{
		"shop":    {Name: "shop"},
		"api-v2":  {Name: "api-v2"},
		"backend": {Name: "backend"},
	}

	cases := []struct {
		name      string
		container string
		want      string
	}{
		{"plain app", "app-shop", "shop"},
		{"app with dashes", "app-api-v2", "api-v2"},
		{"unknown app still resolves", "app-newthing", "newthing"},
		{"worker is not an app", "app-shop-worker-queue", ""},
		{"blue-green staging container", "app-shop-next", ""},
		{"replica of a known app", "app-shop-0", ""},
		{"replica index 12", "app-shop-12", ""},
		// "app-metrics2" is a real app name, not a replica: the digits are part
		// of the name and "metrics" is not a tracked app.
		{"name ending in a digit", "app-metrics2", "metrics2"},
		{"service container", "svc-mysql", ""},
		{"caddy", "neo-caddy", ""},
		{"bare prefix", "app-", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appNameFromContainer(tc.container, known); got != tc.want {
				t.Errorf("appNameFromContainer(%q) = %q, want %q", tc.container, got, tc.want)
			}
		})
	}
}

func TestAppNameFromContainerReplicaOfUnknownApp(t *testing.T) {
	// With no state to compare against, "app-shop-0" cannot be told apart from
	// an app legitimately named "shop-0". Resolving it keeps the container
	// visible as untracked rather than hiding it.
	if got := appNameFromContainer("app-shop-0", map[string]state.App{}); got != "shop-0" {
		t.Errorf("got %q, want shop-0", got)
	}
}

func TestStateDriftEmpty(t *testing.T) {
	if !(stateDrift{}).Empty() {
		t.Error("zero drift should be empty")
	}
	if (stateDrift{Untracked: []string{"shop"}}).Empty() {
		t.Error("drift with an untracked app is not empty")
	}
	if (stateDrift{Missing: []string{"shop"}}).Empty() {
		t.Error("drift with a missing app is not empty")
	}
	if (stateDrift{Stopped: []string{"shop"}}).Empty() {
		t.Error("drift with a stopped app is not empty")
	}
}

func TestIsAllDigits(t *testing.T) {
	for _, s := range []string{"0", "12", "9999"} {
		if !isAllDigits(s) {
			t.Errorf("isAllDigits(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a", "1a", "-1", "1 "} {
		if isAllDigits(s) {
			t.Errorf("isAllDigits(%q) = true", s)
		}
	}
}
