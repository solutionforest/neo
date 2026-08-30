package commands

import (
	"testing"

	"github.com/vxero/neo/internal/state"
)

func TestCachedAppsFromState(t *testing.T) {
	st := &state.State{
		Apps: map[string]state.App{
			"web":    {Name: "web", Status: "running", Domain: "web.example.com"},
			"api":    {Name: "api", Status: "stopped"},
			"worker": {Name: "worker", Status: "running", Domain: "w.example.com"},
		},
	}

	apps := cachedAppsFromState(st)
	if len(apps) != 3 {
		t.Fatalf("got %d apps, want 3", len(apps))
	}
	// Name-sorted for a stable render order (map iteration is not).
	if apps[0].Name != "api" || apps[1].Name != "web" || apps[2].Name != "worker" {
		t.Fatalf("not name-sorted: %v", []string{apps[0].Name, apps[1].Name, apps[2].Name})
	}
	if apps[1].Status != "running" || apps[1].Domain != "web.example.com" {
		t.Errorf("web mapped wrong: %+v", apps[1])
	}
}

func TestServerCacheFromState(t *testing.T) {
	st := &state.State{
		Apps: map[string]state.App{
			"web": {Name: "web", Status: "running"},
			"api": {Name: "api", Status: "stopped"},
		},
		Services: map[string]state.SharedService{
			"pg": {Status: "running"},
		},
	}
	sc := serverCacheFromState(st)
	if sc.AppCount != 2 || sc.RunningApps != 1 {
		t.Errorf("app counts: got %d/%d, want 2/1", sc.AppCount, sc.RunningApps)
	}
	if sc.ServiceCount != 1 || sc.RunningServices != 1 {
		t.Errorf("service counts: got %d/%d, want 1/1", sc.ServiceCount, sc.RunningServices)
	}
	if !sc.Reachable || len(sc.Apps) != 2 {
		t.Errorf("expected reachable with 2 cached apps, got reachable=%v apps=%d", sc.Reachable, len(sc.Apps))
	}
}
