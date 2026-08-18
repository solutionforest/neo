package app

import (
	"strings"
	"testing"
)

// The manifest tests parse inline fixtures, so nothing verified that the
// templates Neo actually ships still load. A broken manifest would have kept
// `make test` green and only failed at `neo install <name>`.

func TestEmbeddedTemplatesLoad(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	templates := registry.List()
	if len(templates) == 0 {
		t.Fatal("no templates loaded — the go:embed pattern no longer matches any manifest")
	}

	for _, m := range templates {
		t.Run(m.Name, func(t *testing.T) {
			// Every field install depends on to scaffold a working project.
			if m.Name == "" {
				t.Error("name is empty")
			}
			if m.Title == "" {
				t.Error("title is empty — the picker shows it")
			}
			if m.Image == "" {
				t.Error("image is empty — nothing to deploy")
			}
			if m.Port <= 0 || m.Port > 65535 {
				t.Errorf("port %d is not a usable container port", m.Port)
			}
			// A floating tag means a redeploy can silently change versions.
			if strings.HasSuffix(m.Image, ":latest") {
				t.Errorf("image %q is pinned to :latest", m.Image)
			}
			if !strings.Contains(m.Image, ":") {
				t.Errorf("image %q has no tag", m.Image)
			}
		})
	}
}

func TestEmbeddedTemplatesAreAddressable(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Every listed template must be retrievable by the name a user types.
	for _, m := range registry.List() {
		got, ok := registry.Get(m.Name)
		if !ok {
			t.Errorf("%q is listed but Get() cannot find it", m.Name)
			continue
		}
		if got.Image != m.Image {
			t.Errorf("%q resolved to a different manifest", m.Name)
		}
	}

	if _, ok := registry.Get("definitely-not-a-template"); ok {
		t.Error("Get() returned a manifest for an unknown name")
	}
}

func TestEmbeddedTemplateServicesAreUsable(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	for _, m := range registry.List() {
		for _, svc := range m.Services {
			if svc.Name == "" {
				t.Errorf("%s: a bundled service has no name", m.Name)
			}
			if svc.Image == "" {
				t.Errorf("%s/%s: bundled service has no image", m.Name, svc.Name)
			}
		}
		for _, vol := range m.Volumes {
			if vol.Name == "" || vol.Path == "" {
				t.Errorf("%s: volume needs both a name and a container path (got %q → %q)", m.Name, vol.Name, vol.Path)
			}
		}
	}
}
