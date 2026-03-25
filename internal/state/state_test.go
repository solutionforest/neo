package state

import (
	"encoding/json"
	"testing"
)

func TestNewState(t *testing.T) {
	st := NewState()
	if st.Initialized {
		t.Error("new state should not be initialized")
	}
	if st.Apps == nil {
		t.Fatal("Apps map should not be nil")
	}
	if len(st.Apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(st.Apps))
	}
}

func TestStateJSONRoundTrip(t *testing.T) {
	st := &State{
		Initialized: true,
		ServerIP:    "10.0.0.1",
		Apps: map[string]App{
			"ghost": {
				Name:         "ghost",
				Image:        "ghost:5",
				Domain:       "blog.example.com",
				Status:       "running",
				InternalPort: 2368,
				Env: map[string]string{
					"url": "https://blog.example.com",
				},
				Volumes: map[string]VolumeInfo{
					"ghost-content": {ContainerPath: "/var/lib/ghost/content"},
				},
				Services: map[string]AppService{
					"mysql": {Image: "mysql:8"},
				},
				InstalledAt: "2026-03-18T10:00:00Z",
			},
		},
		Connected:  true,
		VxeroURL:   "https://app.vxero.dev",
		VxeroToken: "vxero_abc",
	}

	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !loaded.Initialized {
		t.Error("Initialized should be true")
	}
	if loaded.ServerIP != "10.0.0.1" {
		t.Errorf("ServerIP = %q", loaded.ServerIP)
	}
	if !loaded.Connected {
		t.Error("Connected should be true")
	}
	if loaded.VxeroURL != "https://app.vxero.dev" {
		t.Errorf("VxeroURL = %q", loaded.VxeroURL)
	}

	app, ok := loaded.Apps["ghost"]
	if !ok {
		t.Fatal("ghost app not found")
	}
	if app.Domain != "blog.example.com" {
		t.Errorf("Domain = %q", app.Domain)
	}
	if app.InternalPort != 2368 {
		t.Errorf("InternalPort = %d", app.InternalPort)
	}
	if app.Env["url"] != "https://blog.example.com" {
		t.Errorf("Env[url] = %q", app.Env["url"])
	}
	if vol, ok := app.Volumes["ghost-content"]; !ok {
		t.Error("volume ghost-content not found")
	} else if vol.ContainerPath != "/var/lib/ghost/content" {
		t.Errorf("ContainerPath = %q", vol.ContainerPath)
	}
	if svc, ok := app.Services["mysql"]; !ok {
		t.Error("service mysql not found")
	} else if svc.Image != "mysql:8" {
		t.Errorf("service image = %q", svc.Image)
	}
}

func TestStateOmitEmpty(t *testing.T) {
	st := &State{
		Initialized: true,
		ServerIP:    "1.2.3.4",
		Apps:        map[string]App{},
	}

	data, _ := json.Marshal(st)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	// vxero_url and vxero_token should be omitted when empty
	if _, ok := raw["vxero_url"]; ok {
		t.Error("vxero_url should be omitted when empty")
	}
	if _, ok := raw["vxero_token"]; ok {
		t.Error("vxero_token should be omitted when empty")
	}
}

func TestAppOmitEmpty(t *testing.T) {
	app := App{
		Name:   "test",
		Image:  "test:1",
		Status: "running",
	}

	data, _ := json.Marshal(app)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	// Volumes, env, services should be omitted when nil
	if _, ok := raw["volumes"]; ok {
		t.Error("volumes should be omitted when nil")
	}
	if _, ok := raw["env"]; ok {
		t.Error("env should be omitted when nil")
	}
	if _, ok := raw["services"]; ok {
		t.Error("services should be omitted when nil")
	}
	if _, ok := raw["container_id"]; ok {
		t.Error("container_id should be omitted when empty")
	}
}

func TestNilAppsMapHandling(t *testing.T) {
	// Simulate loading JSON with null apps
	raw := `{"initialized": true, "server_ip": "1.1.1.1", "apps": null}`
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	// Apps should be nil from JSON, but Load() would fix it
	// Verify we can safely check length
	if st.Apps != nil && len(st.Apps) != 0 {
		t.Errorf("expected nil or empty apps")
	}
}
