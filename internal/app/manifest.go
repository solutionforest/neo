package app

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Manifest defines an installable application template.
type Manifest struct {
	Name        string           `yaml:"name"`
	Title       string           `yaml:"title"`
	Description string           `yaml:"description"`
	Category    string           `yaml:"category"`
	Version     string           `yaml:"version"`
	Image       string           `yaml:"image"`
	Port        int              `yaml:"port"`
	Volumes     []VolumeSpec     `yaml:"volumes,omitempty"`
	Env         []EnvSpec        `yaml:"env,omitempty"`
	Services    []ServiceSpec    `yaml:"services,omitempty"`
	Health      *HealthSpec      `yaml:"health,omitempty"`

	// Community / metadata fields
	Maintainer    string            `yaml:"maintainer,omitempty"`
	Official      bool              `yaml:"official,omitempty"`
	Website       string            `yaml:"website,omitempty"`
	Tags          []string          `yaml:"tags,omitempty"`
	MinNeoVersion string            `yaml:"min_neo_version,omitempty"`
	MinRAM        string            `yaml:"min_ram,omitempty"`
	MinDisk       string            `yaml:"min_disk,omitempty"`
	Notes         string            `yaml:"notes,omitempty"`
	Links         map[string]string `yaml:"links,omitempty"`
}

// VolumeSpec describes a named volume.
type VolumeSpec struct {
	Name  string `yaml:"name"`
	Path  string `yaml:"path"`
	Label string `yaml:"label,omitempty"`
}

// EnvSpec describes an environment variable.
type EnvSpec struct {
	Key         string `yaml:"key"`
	Label       string `yaml:"label,omitempty"`
	Value       string `yaml:"value,omitempty"`
	From        string `yaml:"from,omitempty"`         // "domain" → auto-fill from domain
	FromService string `yaml:"from_service,omitempty"` // auto-wire from service
	Template    string `yaml:"template,omitempty"`      // template string with ${VAR} refs
	Generate    string `yaml:"generate,omitempty"`      // "hex:64" → auto-generate
	Ask         bool   `yaml:"ask,omitempty"`           // prompt user for value
}

// ServiceSpec describes a bundled service (database, cache, etc.).
type ServiceSpec struct {
	Name    string       `yaml:"name"`
	Image   string       `yaml:"image"`
	Port    int          `yaml:"port"`
	Volumes []VolumeSpec `yaml:"volumes,omitempty"`
	Env     []EnvSpec    `yaml:"env,omitempty"`
}

// HealthSpec describes a health check endpoint.
type HealthSpec struct {
	Path     string `yaml:"path"`
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
	Retries  int    `yaml:"retries"`
}

// Parse parses a YAML manifest.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("manifest missing required field: name")
	}
	if m.Image == "" {
		return nil, fmt.Errorf("manifest missing required field: image")
	}
	return &m, nil
}
