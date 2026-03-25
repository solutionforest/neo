package app

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed templates/*/manifest.yml
var templatesFS embed.FS

// Registry holds all available app manifests.
type Registry struct {
	apps map[string]*Manifest
}

// NewRegistry loads all embedded app templates.
func NewRegistry() (*Registry, error) {
	r := &Registry{apps: make(map[string]*Manifest)}

	dirs, err := templatesFS.ReadDir("templates")
	if err != nil {
		return nil, fmt.Errorf("read templates: %w", err)
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		path := "templates/" + d.Name() + "/manifest.yml"
		data, err := templatesFS.ReadFile(path)
		if err != nil {
			continue // skip dirs without manifest.yml
		}
		m, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", d.Name(), err)
		}
		r.apps[m.Name] = m
	}

	return r, nil
}

// Get returns a manifest by app name.
func (r *Registry) Get(name string) (*Manifest, bool) {
	m, ok := r.apps[strings.ToLower(name)]
	return m, ok
}

// List returns all manifests sorted by name.
func (r *Registry) List() []*Manifest {
	var list []*Manifest
	for _, m := range r.apps {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// Categories returns manifests grouped by category.
func (r *Registry) Categories() map[string][]*Manifest {
	cats := make(map[string][]*Manifest)
	for _, m := range r.apps {
		cats[m.Category] = append(cats[m.Category], m)
	}
	return cats
}
