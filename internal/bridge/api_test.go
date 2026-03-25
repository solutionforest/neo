package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewVxeroClient(t *testing.T) {
	c := NewVxeroClient("https://app.vxero.dev/", "vxero_abc123")
	if c.BaseURL != "https://app.vxero.dev" {
		t.Errorf("BaseURL = %q, want trailing slash stripped", c.BaseURL)
	}
	if c.Token != "vxero_abc123" {
		t.Errorf("Token = %q", c.Token)
	}
}

func TestWhoami(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":    1,
			"name":  "John",
			"email": "john@example.com",
			"current_team": map[string]any{
				"id":   5,
				"name": "Acme",
			},
		})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok123")
	user, team, err := c.Whoami()
	if err != nil {
		t.Fatalf("Whoami() error: %v", err)
	}
	if user.Name != "John" {
		t.Errorf("user.Name = %q", user.Name)
	}
	if user.Email != "john@example.com" {
		t.Errorf("user.Email = %q", user.Email)
	}
	if team == nil || team.Name != "Acme" {
		t.Errorf("team = %v", team)
	}
}

func TestWhoamiUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"message": "Unauthenticated."})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "bad_token")
	_, _, err := c.Whoami()
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestCreateServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/servers" {
			t.Errorf("path = %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["name"] != "prod" {
			t.Errorf("name = %v", body["name"])
		}
		if body["ip_address"] != "1.2.3.4" {
			t.Errorf("ip_address = %v", body["ip_address"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"server": map[string]any{
				"id":         42,
				"name":       "prod",
				"ip_address": "1.2.3.4",
				"status":     "pending",
			},
			"install_command": "curl -fsSL https://get.vxero.dev/agent | sh",
		})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok")
	resp, err := c.CreateServer("prod", "1.2.3.4", 22)
	if err != nil {
		t.Fatalf("CreateServer() error: %v", err)
	}
	if resp.Server.ID != 42 {
		t.Errorf("server.ID = %d", resp.Server.ID)
	}
	if resp.Server.Name != "prod" {
		t.Errorf("server.Name = %q", resp.Server.Name)
	}
	if resp.InstallCommand == "" {
		t.Error("install_command should not be empty")
	}
}

func TestListClusters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": 1, "name": "k3s-prod", "status": "active"},
				{"id": 2, "name": "k3s-staging", "status": "active"},
			},
		})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok")
	clusters, err := c.ListClusters()
	if err != nil {
		t.Fatalf("ListClusters() error: %v", err)
	}
	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}
	if clusters[0].Name != "k3s-prod" {
		t.Errorf("clusters[0].Name = %q", clusters[0].Name)
	}
}

func TestCreateService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/1/services" {
			t.Errorf("path = %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":     10,
			"name":   "ghost-mysql",
			"type":   "mysql",
			"status": "provisioning",
		})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok")
	svc, err := c.CreateService(1, "ghost-mysql", "mysql")
	if err != nil {
		t.Fatalf("CreateService() error: %v", err)
	}
	if svc.ID != 10 {
		t.Errorf("service.ID = %d", svc.ID)
	}
	if svc.Type != "mysql" {
		t.Errorf("service.Type = %q", svc.Type)
	}
}

func TestAPIErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Server quota exceeded. Maximum: 5",
		})
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok")
	_, err := c.CreateServer("test", "1.1.1.1", 22)
	if err == nil {
		t.Fatal("expected error for 422")
	}
	if got := err.Error(); got != "API error: Server quota exceeded. Maximum: 5" {
		t.Errorf("error = %q", got)
	}
}

func TestUpdateEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/environments/99" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewVxeroClient(server.URL, "tok")
	err := c.UpdateEnvironment(99, map[string]any{"domain": "new.example.com"})
	if err != nil {
		t.Fatalf("UpdateEnvironment() error: %v", err)
	}
}
