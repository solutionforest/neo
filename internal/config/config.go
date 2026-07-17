package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultBaseURL is the single production base URL for all neo-cms endpoints.
// It is the ONE place a neo-cms host is hardcoded — every other endpoint is
// derived from it. Point the whole CLI at another environment with the NEO_BASE
// env var, or bake it in at build time:
//
//	-ldflags "-X github.com/vxero/neo/internal/config.DefaultBaseURL=https://neo-staging.vxero.dev"
var DefaultBaseURL = "https://neo.vxero.dev"

// External hosts (not derived from the neo-cms base).
var (
	DefaultAgentInstallURL  = "https://get.vxero.dev/agent"
	DefaultDockerInstallURL = "https://get.docker.com"
)

const (
	DefaultFreeServerLimit = 1

	// Container naming conventions.
	AppContainerPrefix = "app-"
	SvcContainerPrefix = "svc-"
	DockerNetwork      = "neo"
	BackupDir          = "/var/backups/neo"
)

// envOr returns the value of env var key, or fallback when it is unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// BaseURL returns the neo-cms base URL. NEO_BASE > build-time default.
func BaseURL() string { return envOr("NEO_BASE", DefaultBaseURL) }

// APIBaseURL returns the API base (<base>/api), overridable via NEO_API_BASE_URL.
func APIBaseURL() string { return envOr("NEO_API_BASE_URL", BaseURL()+"/api") }

// InstallURL returns the curl|sh install-script URL (<base>/neo), overridable via NEO_INSTALL_URL.
func InstallURL() string { return envOr("NEO_INSTALL_URL", BaseURL()+"/neo") }

// VersionURL returns the version-check URL, overridable via NEO_VERSION_URL.
func VersionURL() string { return envOr("NEO_VERSION_URL", APIBaseURL()+"/neo/version.json") }

// DownloadBaseURL returns the download base URL, overridable via NEO_DOWNLOAD_URL.
func DownloadBaseURL() string { return envOr("NEO_DOWNLOAD_URL", APIBaseURL()+"/download") }

// AgentInstallURL returns the agent install URL, overridable via NEO_AGENT_INSTALL_URL.
func AgentInstallURL() string { return envOr("NEO_AGENT_INSTALL_URL", DefaultAgentInstallURL) }

// DockerInstallURL returns the Docker install-script URL, overridable via NEO_DOCKER_INSTALL_URL.
func DockerInstallURL() string { return envOr("NEO_DOCKER_INSTALL_URL", DefaultDockerInstallURL) }

// AppContainer returns the Docker container name for an app.
func AppContainer(appName string) string {
	return AppContainerPrefix + appName
}

// SvcContainer returns the Docker container name for a bundled service.
func SvcContainer(appName, svcName string) string {
	return SvcContainerPrefix + appName + "-" + svcName
}

// WorkerContainer returns the Docker container name for a worker.
func WorkerContainer(appName, workerName string) string {
	return AppContainerPrefix + appName + "-worker-" + workerName
}

// ReplicaContainer returns the Docker container name for a scaled app replica.
// Replicas are named app-{name}-0, app-{name}-1, etc.
func ReplicaContainer(appName string, index int) string {
	return fmt.Sprintf("%s%s-%d", AppContainerPrefix, appName, index)
}

// SvcContainerShared returns the Docker container name for a shared service.
func SvcContainerShared(svcName string) string {
	return SvcContainerPrefix + svcName
}

// Server represents a configured remote server.
type Server struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Key           string `json:"key,omitempty"`
	InitializedAt string `json:"initialized_at"`
}

// Config is the local CLI configuration stored at ~/.neo/config.json.
type Config struct {
	Current    string            `json:"current"`
	Servers    map[string]Server `json:"servers"`
	LicenseKey string            `json:"license_key,omitempty"`
}

// Dir returns the neo config directory path.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".neo")
}

// Path returns the config file path.
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

// Load reads the config file. Returns empty config if file doesn't exist.
func Load() (*Config, error) {
	cfg := &Config{
		Servers: make(map[string]Server),
	}

	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]Server)
	}
	return cfg, nil
}

// Save writes the config file atomically with file locking.
func Save(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Write to a temp file first, then rename for atomicity
	tmpPath := Path() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Acquire an exclusive lock on the config directory to prevent concurrent writes
	lockPath := filepath.Join(Dir(), ".lock")
	lockF, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		lockFile(lockF)
		defer func() {
			unlockFile(lockF)
			lockF.Close()
		}()
	}

	// Atomic rename
	if err := os.Rename(tmpPath, Path()); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// CurrentServer returns the currently active server, or error if none.
func (c *Config) CurrentServer() (*Server, error) {
	if c.Current == "" {
		return nil, fmt.Errorf("no server selected — run: neo init <user@host>")
	}
	srv, ok := c.Servers[c.Current]
	if !ok {
		return nil, fmt.Errorf("server %q not found in config", c.Current)
	}
	return &srv, nil
}

// AddServer adds or updates a server entry.
// ServerList returns all configured servers as a sorted slice.
func (c *Config) ServerList() []Server {
	list := make([]Server, 0, len(c.Servers))
	for _, s := range c.Servers {
		list = append(list, s)
	}
	return list
}

func (c *Config) AddServer(srv Server) {
	c.Servers[srv.Name] = srv
	if c.Current == "" {
		c.Current = srv.Name
	}
}

// RemoveServer removes a server entry.
func (c *Config) RemoveServer(name string) {
	delete(c.Servers, name)
	if c.Current == name {
		c.Current = ""
		for k := range c.Servers {
			c.Current = k
			break
		}
	}
}
