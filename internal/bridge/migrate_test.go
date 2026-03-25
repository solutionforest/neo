package bridge

import (
	"testing"

	"github.com/vxero/neo/internal/state"
)

func TestMapServiceTypeByImage(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{"postgres", "postgres:16-alpine", "postgresql"},
		{"postgres caps", "Postgres:16", "postgresql"},
		{"mysql", "mysql:8", "mysql"},
		{"mariadb", "mariadb:11", "mysql"},
		{"redis", "redis:7-alpine", "redis"},
		{"mongo", "mongo:7", "mongodb"},
		{"elasticsearch", "elasticsearch:8.12.0", "elasticsearch"},
		{"unknown", "rabbitmq:3", ""},
		{"custom", "mycompany/custom-app:latest", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapServiceType("svc", tt.image)
			if got != tt.want {
				t.Errorf("mapServiceType(%q, %q) = %q, want %q", "svc", tt.image, got, tt.want)
			}
		})
	}
}

func TestMapServiceTypeByName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"postgres", "postgresql"},
		{"mysql", "mysql"},
		{"redis", "redis"},
		{"custom", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapServiceType(tt.name, "unknown-image:latest")
			if got != tt.want {
				t.Errorf("mapServiceType(%q, unknown) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestBuildMigrationPlanEmpty(t *testing.T) {
	st := state.NewState()
	st.ServerIP = "1.2.3.4"

	plan := BuildMigrationPlan(st)

	if plan.ServerIP != "1.2.3.4" {
		t.Errorf("ServerIP = %q, want %q", plan.ServerIP, "1.2.3.4")
	}
	if len(plan.Apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(plan.Apps))
	}
	if len(plan.Services) != 0 {
		t.Errorf("expected 0 services, got %d", len(plan.Services))
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(plan.Warnings))
	}
}

func TestBuildMigrationPlanSimpleApp(t *testing.T) {
	st := state.NewState()
	st.ServerIP = "10.0.0.1"
	st.Apps["myapp"] = state.App{
		Name:         "myapp",
		Image:        "myapp:latest",
		Domain:       "app.example.com",
		InternalPort: 3000,
		Env:          map[string]string{"NODE_ENV": "production"},
	}

	plan := BuildMigrationPlan(st)

	if len(plan.Apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(plan.Apps))
	}

	app := plan.Apps[0]
	if app.Name != "myapp" {
		t.Errorf("Name = %q", app.Name)
	}
	if app.Domain != "app.example.com" {
		t.Errorf("Domain = %q", app.Domain)
	}
	if app.Port != 3000 {
		t.Errorf("Port = %d", app.Port)
	}
	if !app.NeedsCluster {
		t.Error("NeedsCluster should be true")
	}
	if app.EnvVars["NODE_ENV"] != "production" {
		t.Errorf("EnvVars[NODE_ENV] = %q", app.EnvVars["NODE_ENV"])
	}
	// No volumes, no warnings
	if len(plan.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(plan.Warnings), plan.Warnings)
	}
}

func TestBuildMigrationPlanWithVolumes(t *testing.T) {
	st := state.NewState()
	st.Apps["ghost"] = state.App{
		Name:  "ghost",
		Image: "ghost:5",
		Volumes: map[string]state.VolumeInfo{
			"ghost-content": {ContainerPath: "/var/lib/ghost/content"},
		},
	}

	plan := BuildMigrationPlan(st)

	if len(plan.Apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(plan.Apps))
	}
	if len(plan.Apps[0].Volumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(plan.Apps[0].Volumes))
	}
	if plan.Apps[0].Notes == "" {
		t.Error("app with volumes should have migration notes")
	}
	if len(plan.Warnings) != 1 {
		t.Errorf("expected 1 warning for volumes, got %d", len(plan.Warnings))
	}
}

func TestBuildMigrationPlanWithServices(t *testing.T) {
	st := state.NewState()
	st.Apps["myapp"] = state.App{
		Name:  "myapp",
		Image: "myapp:latest",
		Services: map[string]state.AppService{
			"postgres": {Image: "postgres:16-alpine"},
			"redis":    {Image: "redis:7-alpine"},
		},
	}

	plan := BuildMigrationPlan(st)

	if len(plan.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(plan.Services))
	}

	typeMap := make(map[string]string)
	for _, svc := range plan.Services {
		typeMap[svc.ServiceName] = svc.VxeroType
	}

	if typeMap["postgres"] != "postgresql" {
		t.Errorf("postgres mapped to %q, want %q", typeMap["postgres"], "postgresql")
	}
	if typeMap["redis"] != "redis" {
		t.Errorf("redis mapped to %q, want %q", typeMap["redis"], "redis")
	}
}

func TestBuildMigrationPlanUnknownService(t *testing.T) {
	st := state.NewState()
	st.Apps["myapp"] = state.App{
		Name:  "myapp",
		Image: "myapp:latest",
		Services: map[string]state.AppService{
			"rabbitmq": {Image: "rabbitmq:3-management"},
		},
	}

	plan := BuildMigrationPlan(st)

	if len(plan.Services) != 0 {
		t.Errorf("unknown service should not be migrated, got %d services", len(plan.Services))
	}
	if len(plan.Warnings) != 1 {
		t.Errorf("expected 1 warning for unknown service, got %d", len(plan.Warnings))
	}
}

func TestBuildMigrationPlanMultipleApps(t *testing.T) {
	st := state.NewState()
	st.ServerIP = "10.0.0.1"
	st.Apps["ghost"] = state.App{
		Name:  "ghost",
		Image: "ghost:5",
		Services: map[string]state.AppService{
			"mysql": {Image: "mysql:8"},
		},
	}
	st.Apps["plausible"] = state.App{
		Name:  "plausible",
		Image: "plausible/analytics:v2",
		Volumes: map[string]state.VolumeInfo{
			"events": {ContainerPath: "/var/lib/plausible"},
		},
		Services: map[string]state.AppService{
			"postgres":   {Image: "postgres:16"},
			"clickhouse": {Image: "clickhouse/clickhouse-server:24"},
		},
	}

	plan := BuildMigrationPlan(st)

	if len(plan.Apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(plan.Apps))
	}

	// ghost-mysql + plausible-postgres = 2 known services
	// plausible-clickhouse = unknown → warning
	knownServices := 0
	for _, svc := range plan.Services {
		if svc.VxeroType != "" {
			knownServices++
		}
	}
	if knownServices != 2 {
		t.Errorf("expected 2 known services, got %d", knownServices)
	}

	// Warnings: 1 for plausible volumes + 1 for clickhouse
	if len(plan.Warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d: %v", len(plan.Warnings), plan.Warnings)
	}
}
