package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vxero/neo/internal/state"
	"gopkg.in/yaml.v3"
)

// NeoHealth represents Docker health check configuration in .neo.yml.
type NeoHealth struct {
	Cmd         string `yaml:"cmd"`
	Interval    string `yaml:"interval,omitempty"`     // e.g. "30s"
	Timeout     string `yaml:"timeout,omitempty"`      // e.g. "10s"
	Retries     int    `yaml:"retries,omitempty"`      // e.g. 3
	StartPeriod string `yaml:"start_period,omitempty"` // e.g. "40s"
	Path        string `yaml:"path,omitempty"`         // HTTP path for post-deploy check; empty = disabled
}

// NeoWorker represents a background worker container in .neo.yml.
type NeoWorker struct {
	Command     string `yaml:"command"`
	HealthCheck string `yaml:"health_check,omitempty"` // optional health check command
	Restart     string `yaml:"restart,omitempty"`       // Docker restart policy
}

// SidecarBuild supports both string ("../path") and object ({context, dockerfile}) forms.
type SidecarBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

// UnmarshalYAML allows SidecarBuild to parse from a string or an object.
func (b *SidecarBuild) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string first
	var s string
	if err := unmarshal(&s); err == nil {
		b.Context = s
		return nil
	}
	// Try object
	type raw struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	}
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	b.Context = r.Context
	b.Dockerfile = r.Dockerfile
	return nil
}

// NeoSidecar represents a sidecar container in .neo.yml.
// Sidecars run alongside the app on the same Docker network but have their own
// image (built from a Dockerfile or pulled from a registry). They are not
// exposed to the public via Caddy.
type NeoSidecar struct {
	Build   SidecarBuild      `yaml:"build,omitempty"`   // path or {context, dockerfile}
	Image   string            `yaml:"image,omitempty"`   // pre-built image (mutually exclusive with build)
	Volumes map[string]string `yaml:"volumes,omitempty"` // name: containerPath
	Env     map[string]string `yaml:"env,omitempty"`     // sidecar-specific env vars
	Command string            `yaml:"command,omitempty"` // override entrypoint/cmd
	Restart string            `yaml:"restart,omitempty"` // Docker restart policy
	Health  *NeoHealth        `yaml:"health,omitempty"`  // Docker health check
}

// NeoSSL represents custom SSL certificate configuration in .neo.yml.
type NeoSSL struct {
	Certificate string `yaml:"certificate"` // path to PEM certificate file (relative to .neo.yml)
	PrivateKey  string `yaml:"private_key"`  // path to PEM private key file (relative to .neo.yml)
}

// HookCommands holds one or more shell commands for a deploy hook.
// Accepts both a single string and a list of strings in YAML.
type HookCommands []string

// UnmarshalYAML allows HookCommands to parse from a string or a list.
func (h *HookCommands) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var single string
	if err := unmarshal(&single); err == nil {
		*h = HookCommands{single}
		return nil
	}
	var list []string
	if err := unmarshal(&list); err != nil {
		return err
	}
	*h = HookCommands(list)
	return nil
}

// NeoHooks represents deploy lifecycle hooks in .neo.yml.
type NeoHooks struct {
	PreBuild   HookCommands `yaml:"pre_build,omitempty"`
	PostDeploy HookCommands `yaml:"post_deploy,omitempty"`
}

// NeoVolume represents a persistent volume declaration in .neo.yml.
// Supports flat string and structured object forms:
//
//	volumes:
//	  database: /var/www/html/database              # container path only (named volume)
//	  logs: /var/log/myapp:/var/log/app              # host:container (bind mount on server)
//	  storage:
//	    path: /var/www/html/storage                   # structured object
//	    mount: /mnt/data/storage                      # optional host mount path
type NeoVolume struct {
	Path  string `yaml:"path"`            // container path to mount
	Mount string `yaml:"mount,omitempty"` // optional host path on server (bind mount instead of named volume)
}

// UnmarshalYAML allows NeoVolume to parse from a string or an object.
// Flat strings with ":" are parsed as host:container bind mounts.
func (v *NeoVolume) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		if strings.Contains(s, ":") {
			parts := strings.SplitN(s, ":", 2)
			v.Mount = parts[0]
			v.Path = parts[1]
		} else {
			v.Path = s
		}
		return nil
	}
	type raw struct {
		Path  string `yaml:"path"`
		Mount string `yaml:"mount,omitempty"`
	}
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	v.Path = r.Path
	v.Mount = r.Mount
	return nil
}

// NeoBasicAuth configures HTTP basic authentication for a deployment.
// Caddy handles auth at the proxy layer — the app container is unaffected.
type NeoBasicAuth struct {
	User     string   `yaml:"user"`
	Password string   `yaml:"password"`
	Bypass   []string `yaml:"bypass,omitempty"` // paths excluded from auth (e.g. /api/*)
}

// NeoDevConfig represents dev-only settings in .neo.yml.
// Used exclusively by `neo dev` and ignored during deploy.
type NeoDevConfig struct {
	EnvFile string            `yaml:"env_file,omitempty"` // dev-only env file (e.g. .env)
	Port    int               `yaml:"port,omitempty"`     // local port override
	Env     map[string]string `yaml:"env,omitempty"`      // dev-only env vars
	Volumes map[string]string `yaml:"volumes,omitempty"`  // volume-name → local-path or local:container
}

// NeoEnvironment represents a named deployment target in .neo.yml.
type NeoEnvironment struct {
	Name      string               `yaml:"name,omitempty"`      // override app/container name for this env
	Server    string               `yaml:"server,omitempty"`
	Domain    string               `yaml:"domain,omitempty"`    // single domain (backward compat)
	Domains   []string             `yaml:"domains,omitempty"`   // multiple domains (takes precedence)
	Port      int                  `yaml:"port,omitempty"`
	HTTPS     *bool                `yaml:"https,omitempty"`     // nil=default, true=HTTPS, false=HTTP-only
	Env       map[string]string    `yaml:"env,omitempty"`
	EnvFile   string               `yaml:"env_file,omitempty"`
	SSL       *NeoSSL              `yaml:"ssl,omitempty"`
	BasicAuth *NeoBasicAuth        `yaml:"basic_auth,omitempty"` // HTTP basic auth at proxy layer
	Volumes   map[string]NeoVolume `yaml:"volumes,omitempty"`   // environment-specific persistent volumes
	Workers   map[string]NeoWorker `yaml:"workers,omitempty"`   // environment-specific workers (override top-level)
	Sidecars  map[string]NeoSidecar `yaml:"sidecars,omitempty"` // environment-specific sidecars (override top-level)
	Restart   string               `yaml:"restart,omitempty"`   // Docker restart policy override
	Health    *NeoHealth           `yaml:"health,omitempty"`    // Docker health check override
	Hooks     *NeoHooks            `yaml:"hooks,omitempty"`     // deploy lifecycle hooks (override top-level)
}

// NeoConfig represents a .neo.yml project configuration file.
type NeoConfig struct {
	Name           string                    `yaml:"name,omitempty"`
	Server         string                    `yaml:"server,omitempty"`
	Domain         string                    `yaml:"domain,omitempty"`          // single domain (backward compat)
	Domains        []string                  `yaml:"domains,omitempty"`         // multiple domains (takes precedence)
	Port           int                       `yaml:"port,omitempty"`
	HTTPS          *bool                     `yaml:"https,omitempty"` // nil=default, true=HTTPS, false=HTTP-only
	SSL            *NeoSSL                   `yaml:"ssl,omitempty"`
	BasicAuth      *NeoBasicAuth             `yaml:"basic_auth,omitempty"` // HTTP basic auth at proxy layer
	Env            map[string]string         `yaml:"env,omitempty"`
	EnvFile        string                    `yaml:"env_file,omitempty"`
	ComposeService string                    `yaml:"compose_service,omitempty"`
	Restart        string                    `yaml:"restart,omitempty"` // Docker restart policy (default: unless-stopped)
	Health         *NeoHealth                `yaml:"health,omitempty"` // Docker health check
	Hooks          *NeoHooks                 `yaml:"hooks,omitempty"`
	Environments   map[string]NeoEnvironment `yaml:"environments,omitempty"`
	Workers        map[string]NeoWorker      `yaml:"workers,omitempty"`
	Sidecars       map[string]NeoSidecar     `yaml:"sidecars,omitempty"`
	Volumes        map[string]NeoVolume      `yaml:"volumes,omitempty"`
	Dev            *NeoDevConfig             `yaml:"dev,omitempty"` // dev-only settings for `neo dev`
}

// PrimaryDomain returns the first configured domain: domains[0] > domain > "".
func (c *NeoConfig) PrimaryDomain() string {
	if len(c.Domains) > 0 {
		return c.Domains[0]
	}
	return c.Domain
}

// ExtraConfigDomains returns additional domains from the domains: list (all except the first).
func (c *NeoConfig) ExtraConfigDomains() []string {
	if len(c.Domains) > 1 {
		return c.Domains[1:]
	}
	return nil
}

// loadNeoConfig reads .neo.yml from the given project directory.
// Returns nil if the file doesn't exist (not an error).
func loadNeoConfig(projectDir string) (*NeoConfig, error) {
	path := filepath.Join(projectDir, ".neo.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg NeoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ResolvedVolume represents a volume from .neo.yml after config resolution,
// before mode-specific formatting (dev bind-mount vs deploy named volume).
type ResolvedVolume struct {
	Name          string // short name from .neo.yml key (e.g. "database")
	ContainerPath string // container mount point (e.g. "/var/www/html/database")
	Mount         string // optional host mount path (bind mount instead of named volume)
}

// resolveConfigVolumes extracts the canonical volume list from neoConfig.Volumes.
// Returns nil if neoConfig is nil or has no volumes.
func resolveConfigVolumes(neoConfig *NeoConfig) []ResolvedVolume {
	if neoConfig == nil || len(neoConfig.Volumes) == 0 {
		return nil
	}
	vols := make([]ResolvedVolume, 0, len(neoConfig.Volumes))
	for name, vol := range neoConfig.Volumes {
		vols = append(vols, ResolvedVolume{
			Name:          name,
			ContainerPath: vol.Path,
			Mount:         vol.Mount,
		})
	}
	return vols
}

// volumesFromState reconstructs Docker volume mount strings from stored state.
// Used by deploy (redeploy/env-only), manage, and other commands that restart containers.
func volumesFromState(stateVolumes map[string]state.VolumeInfo) []string {
	var vols []string
	for name, vol := range stateVolumes {
		if vol.Mount != nil {
			vols = append(vols, fmt.Sprintf("%s:%s", *vol.Mount, vol.ContainerPath))
		} else {
			vols = append(vols, fmt.Sprintf("%s:%s", name, vol.ContainerPath))
		}
	}
	return vols
}

// buildDeployVolumes resolves volumes for deploy mode.
// On redeploy (existing != nil), starts from existing state volumes and adds new ones from config.
// On first deploy (existing == nil), creates named Docker volumes from config.
// Returns volume mount strings and a VolumeInfo map for state persistence.
func buildDeployVolumes(appName string, neoConfig *NeoConfig, existing *state.App) ([]string, map[string]state.VolumeInfo) {
	declaredVolumes := make(map[string]state.VolumeInfo)
	var volumes []string

	if existing != nil {
		// Redeploy: start from state
		volumes = volumesFromState(existing.Volumes)
		for name, vol := range existing.Volumes {
			declaredVolumes[name] = vol
		}
		// Add any new volumes from config that weren't in previous state
		for _, rv := range resolveConfigVolumes(neoConfig) {
			volName := appName + "-" + rv.Name
			if _, exists := existing.Volumes[volName]; !exists {
				if rv.Mount != "" {
					// Bind mount on server
					volumes = append(volumes, fmt.Sprintf("%s:%s", rv.Mount, rv.ContainerPath))
					mountStr := rv.Mount
					declaredVolumes[volName] = state.VolumeInfo{ContainerPath: rv.ContainerPath, Mount: &mountStr}
				} else {
					volumes = append(volumes, fmt.Sprintf("%s:%s", volName, rv.ContainerPath))
					declaredVolumes[volName] = state.VolumeInfo{ContainerPath: rv.ContainerPath}
				}
			}
		}
	} else {
		// First deploy
		for _, rv := range resolveConfigVolumes(neoConfig) {
			volName := appName + "-" + rv.Name
			if rv.Mount != "" {
				// Bind mount on server
				volumes = append(volumes, fmt.Sprintf("%s:%s", rv.Mount, rv.ContainerPath))
				mountStr := rv.Mount
				declaredVolumes[volName] = state.VolumeInfo{ContainerPath: rv.ContainerPath, Mount: &mountStr}
			} else {
				// Named Docker volume
				volumes = append(volumes, fmt.Sprintf("%s:%s", volName, rv.ContainerPath))
				declaredVolumes[volName] = state.VolumeInfo{ContainerPath: rv.ContainerPath}
			}
		}
	}

	return volumes, declaredVolumes
}

// saveNeoConfig writes the config back to .neo.yml in the given directory.
func saveNeoConfig(projectDir string, cfg *NeoConfig) error {
	path := filepath.Join(projectDir, ".neo.yml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
