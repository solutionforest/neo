package commands

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// NeoHealth represents Docker health check configuration in .neo.yml.
type NeoHealth struct {
	Cmd         string `yaml:"cmd"`
	Interval    string `yaml:"interval,omitempty"`     // e.g. "30s"
	Timeout     string `yaml:"timeout,omitempty"`      // e.g. "10s"
	Retries     int    `yaml:"retries,omitempty"`      // e.g. 3
	StartPeriod string `yaml:"start_period,omitempty"` // e.g. "40s"
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
type NeoVolume struct {
	Path string `yaml:"path"` // container path to mount
}

// NeoEnvironment represents a named deployment target in .neo.yml.
type NeoEnvironment struct {
	Name    string               `yaml:"name,omitempty"`    // override app/container name for this env
	Server  string               `yaml:"server,omitempty"`
	Domain  string               `yaml:"domain,omitempty"`
	Port    int                  `yaml:"port,omitempty"`
	HTTPS   *bool                `yaml:"https,omitempty"`   // nil=default, true=HTTPS, false=HTTP-only
	Env     map[string]string    `yaml:"env,omitempty"`
	EnvFile string               `yaml:"env_file,omitempty"`
	SSL      *NeoSSL                `yaml:"ssl,omitempty"`
	Volumes  map[string]NeoVolume   `yaml:"volumes,omitempty"`  // environment-specific persistent volumes
	Workers  map[string]NeoWorker   `yaml:"workers,omitempty"`  // environment-specific workers (override top-level)
	Sidecars map[string]NeoSidecar  `yaml:"sidecars,omitempty"` // environment-specific sidecars (override top-level)
	Restart  string                 `yaml:"restart,omitempty"`  // Docker restart policy override
	Health   *NeoHealth             `yaml:"health,omitempty"`   // Docker health check override
	Hooks    *NeoHooks              `yaml:"hooks,omitempty"`    // deploy lifecycle hooks (override top-level)
}

// NeoConfig represents a .neo.yml project configuration file.
type NeoConfig struct {
	Name           string                    `yaml:"name,omitempty"`
	Server         string                    `yaml:"server,omitempty"`
	Domain         string                    `yaml:"domain,omitempty"`
	Port           int                       `yaml:"port,omitempty"`
	HTTPS          *bool                     `yaml:"https,omitempty"` // nil=default, true=HTTPS, false=HTTP-only
	SSL            *NeoSSL                   `yaml:"ssl,omitempty"`
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

// saveNeoConfig writes the config back to .neo.yml in the given directory.
func saveNeoConfig(projectDir string, cfg *NeoConfig) error {
	path := filepath.Join(projectDir, ".neo.yml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
