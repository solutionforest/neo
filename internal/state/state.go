package state

import (
	"encoding/json"
	"fmt"

	"github.com/vxero/neo/internal/ssh"
)

const RemotePath = "/etc/neo/state.json"

// AppService represents a bundled service for an app (legacy, kept for backwards compat).
type AppService struct {
	Image       string `json:"image"`
	ContainerID string `json:"container_id,omitempty"`
}

// SharedService represents a server-level shared service (e.g., one MySQL for many apps).
type SharedService struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Status      string            `json:"status"` // "running", "stopped"
	ContainerID string            `json:"container_id,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Volumes     map[string]string `json:"volumes,omitempty"` // volumeName: containerPath
	Port        int               `json:"port,omitempty"`
	LinkedApps  map[string]Link   `json:"linked_apps,omitempty"` // appName → link details
	CreatedAt   string            `json:"created_at"`
}

// Link describes how a shared service is connected to an app.
type Link struct {
	Database string            `json:"database,omitempty"` // database created for this app
	User     string            `json:"user,omitempty"`     // user created for this app
	EnvVars  map[string]string `json:"env_vars,omitempty"` // injected into the app
}

// VolumeInfo describes a volume and its optional host mount.
type VolumeInfo struct {
	ContainerPath string  `json:"container_path"`
	Mount         *string `json:"mount"`
}

// AppWorker represents a background worker container for an app.
type AppWorker struct {
	Command     string `json:"command"`
	ContainerID string `json:"container_id,omitempty"`
	Status      string `json:"status"`
}

// AppSidecar represents a sidecar container for an app.
type AppSidecar struct {
	Image   string            `json:"image"`
	Volumes map[string]string `json:"volumes,omitempty"` // volumeName: containerPath
	Env     map[string]string `json:"env,omitempty"`
	Command string            `json:"command,omitempty"`
	Status  string            `json:"status"`
}

// App represents an installed application on the server.
type App struct {
	Name         string                `json:"name"`
	Image        string                `json:"image"`
	Domain       string                `json:"domain"`                    // primary domain (backward compat)
	ExtraDomains []string              `json:"extra_domains,omitempty"`   // additional domains
	HTTPOnly     bool                  `json:"http_only,omitempty"` // true = HTTP, false = HTTPS (default)
	Status       string                `json:"status"`
	ContainerID  string                `json:"container_id,omitempty"`
	InternalPort int                   `json:"internal_port"`
	Volumes      map[string]VolumeInfo `json:"volumes,omitempty"`
	Env          map[string]string     `json:"env,omitempty"`
	Services     map[string]AppService `json:"services,omitempty"`
	Workers      map[string]AppWorker  `json:"workers,omitempty"`
	Sidecars     map[string]AppSidecar `json:"sidecars,omitempty"`
	InstalledAt  string                `json:"installed_at"`
}

// AllDomains returns all domains for this app: primary first, then extras.
// Returns nil if no domain is configured.
func (a App) AllDomains() []string {
	if a.Domain == "" {
		return nil
	}
	return append([]string{a.Domain}, a.ExtraDomains...)
}

// AddDomain adds a domain to the app without removing existing ones.
// If no primary domain is set, the new domain becomes primary.
func (a *App) AddDomain(domain string) {
	if a.Domain == "" {
		a.Domain = domain
		return
	}
	// Avoid duplicates
	for _, d := range a.AllDomains() {
		if d == domain {
			return
		}
	}
	a.ExtraDomains = append(a.ExtraDomains, domain)
}

// RemoveDomain removes a domain from the app.
// If the primary domain is removed, the first extra domain is promoted.
func (a *App) RemoveDomain(domain string) {
	if a.Domain == domain {
		if len(a.ExtraDomains) > 0 {
			a.Domain = a.ExtraDomains[0]
			a.ExtraDomains = a.ExtraDomains[1:]
		} else {
			a.Domain = ""
		}
		return
	}
	filtered := a.ExtraDomains[:0]
	for _, d := range a.ExtraDomains {
		if d != domain {
			filtered = append(filtered, d)
		}
	}
	a.ExtraDomains = filtered
}

// State is the remote server state stored at /etc/neo/state.json.
type State struct {
	Initialized bool                     `json:"initialized"`
	ServerIP    string                   `json:"server_ip"`
	ServerArch  string                   `json:"server_arch,omitempty"`
	Apps        map[string]App           `json:"apps"`
	Services    map[string]SharedService `json:"services,omitempty"`
	Connected   bool                     `json:"connected"`
	VxeroURL    string                   `json:"vxero_url,omitempty"`
	VxeroToken  string                   `json:"vxero_token,omitempty"`
}

// NewState returns an empty state.
func NewState() *State {
	return &State{
		Apps:     make(map[string]App),
		Services: make(map[string]SharedService),
	}
}

// Load reads the state from the remote server over SSH.
func Load(exec *ssh.Executor) (*State, error) {
	data, err := exec.ReadFile(RemotePath)
	if err != nil {
		return nil, fmt.Errorf("read remote state: %w", err)
	}

	st := NewState()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse remote state: %w", err)
	}
	if st.Apps == nil {
		st.Apps = make(map[string]App)
	}
	if st.Services == nil {
		st.Services = make(map[string]SharedService)
	}
	return st, nil
}

// Save writes the state to the remote server over SSH.
func Save(exec *ssh.Executor, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	// Ensure directory exists with restrictive permissions
	exec.RunQuiet("mkdir -p /etc/neo && chmod 700 /etc/neo")

	return exec.WriteFile(RemotePath, data, 0600)
}

// Init creates a new initialized state on the remote server.
func Init(exec *ssh.Executor, serverIP string) error {
	st := NewState()
	st.Initialized = true
	st.ServerIP = serverIP
	return Save(exec, st)
}
