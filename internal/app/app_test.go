package app

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseMinimal(t *testing.T) {
	yaml := `
name: myapp
image: nginx:latest
port: 80
`
	m, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if m.Name != "myapp" {
		t.Errorf("Name = %q, want %q", m.Name, "myapp")
	}
	if m.Image != "nginx:latest" {
		t.Errorf("Image = %q, want %q", m.Image, "nginx:latest")
	}
	if m.Port != 80 {
		t.Errorf("Port = %d, want %d", m.Port, 80)
	}
}

func TestParseFull(t *testing.T) {
	yaml := `
name: ghost
title: Ghost
description: Professional publishing platform
category: cms
version: "5.87"
image: ghost:5-alpine
port: 2368
volumes:
  - name: ghost-content
    path: /var/lib/ghost/content
env:
  - key: url
    from: domain
  - key: SECRET_KEY
    generate: "hex:64"
  - key: MAIL_USER
    ask: true
    label: "Mail username"
services:
  - name: mysql
    image: mysql:8
    port: 3306
    env:
      - key: MYSQL_ROOT_PASSWORD
        generate: "hex:32"
health:
  path: /ghost/api/admin/site/
  retries: 10
`
	m, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if m.Title != "Ghost" {
		t.Errorf("Title = %q, want %q", m.Title, "Ghost")
	}
	if m.Category != "cms" {
		t.Errorf("Category = %q, want %q", m.Category, "cms")
	}
	if len(m.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(m.Volumes))
	}
	if m.Volumes[0].Path != "/var/lib/ghost/content" {
		t.Errorf("Volume path = %q", m.Volumes[0].Path)
	}
	if len(m.Env) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(m.Env))
	}
	if m.Env[0].From != "domain" {
		t.Errorf("Env[0].From = %q, want %q", m.Env[0].From, "domain")
	}
	if m.Env[1].Generate != "hex:64" {
		t.Errorf("Env[1].Generate = %q", m.Env[1].Generate)
	}
	if !m.Env[2].Ask {
		t.Error("Env[2].Ask should be true")
	}
	if len(m.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(m.Services))
	}
	if m.Services[0].Name != "mysql" {
		t.Errorf("Service name = %q", m.Services[0].Name)
	}
	if m.Health == nil {
		t.Fatal("Health should not be nil")
	}
	if m.Health.Retries != 10 {
		t.Errorf("Health.Retries = %d, want 10", m.Health.Retries)
	}
}

func TestParseMissingName(t *testing.T) {
	yaml := `image: nginx:latest`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, should mention 'name'", err.Error())
	}
}

func TestParseMissingImage(t *testing.T) {
	yaml := `name: myapp`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error = %q, should mention 'image'", err.Error())
	}
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte("{{invalid"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestGenerateHex(t *testing.T) {
	val, err := GenerateValue("hex:64")
	if err != nil {
		t.Fatalf("GenerateValue(hex:64) error: %v", err)
	}
	if len(val) != 64 {
		t.Errorf("hex:64 length = %d, want 64", len(val))
	}
	// Must be valid hex
	if _, err := hex.DecodeString(val); err != nil {
		t.Errorf("not valid hex: %v", err)
	}
}

func TestGenerateBase64(t *testing.T) {
	val, err := GenerateValue("base64:32")
	if err != nil {
		t.Fatalf("GenerateValue(base64:32) error: %v", err)
	}
	if len(val) == 0 {
		t.Error("base64 value should not be empty")
	}
}

func TestGenerateUniqueness(t *testing.T) {
	v1, _ := GenerateValue("hex:32")
	v2, _ := GenerateValue("hex:32")
	if v1 == v2 {
		t.Error("two generated values should differ")
	}
}

func TestGenerateInvalidSpec(t *testing.T) {
	_, err := GenerateValue("invalid")
	if err == nil {
		t.Fatal("expected error for spec without colon")
	}
}

func TestGenerateInvalidLength(t *testing.T) {
	_, err := GenerateValue("hex:abc")
	if err == nil {
		t.Fatal("expected error for non-numeric length")
	}
}

func TestGenerateUnknownEncoding(t *testing.T) {
	_, err := GenerateValue("sha256:32")
	if err == nil {
		t.Fatal("expected error for unknown encoding")
	}
}

func TestRegistryLoadsTemplates(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	list := r.List()
	if len(list) == 0 {
		t.Fatal("registry should have at least one template")
	}

	// Every manifest should have required fields
	for _, m := range list {
		if m.Name == "" {
			t.Error("manifest has empty name")
		}
		if m.Image == "" {
			t.Errorf("manifest %q has empty image", m.Name)
		}
		if m.Port == 0 {
			t.Errorf("manifest %q has zero port", m.Name)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	list := r.List()
	if len(list) == 0 {
		t.Skip("no templates available")
	}

	// Should find by name
	first := list[0]
	m, ok := r.Get(first.Name)
	if !ok {
		t.Fatalf("Get(%q) returned false", first.Name)
	}
	if m.Name != first.Name {
		t.Errorf("Get returned %q, want %q", m.Name, first.Name)
	}

	// Case insensitive
	m2, ok := r.Get(strings.ToUpper(first.Name))
	if !ok {
		t.Fatalf("Get(%q) should be case-insensitive", strings.ToUpper(first.Name))
	}
	if m2.Name != first.Name {
		t.Errorf("case-insensitive Get returned %q", m2.Name)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	_, ok := r.Get("nonexistent-app-12345")
	if ok {
		t.Error("Get should return false for nonexistent app")
	}
}

func TestRegistryCategories(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	cats := r.Categories()
	if len(cats) == 0 {
		t.Error("expected at least one category")
	}

	total := 0
	for _, apps := range cats {
		total += len(apps)
	}
	if total != len(r.List()) {
		t.Errorf("categories total (%d) != list total (%d)", total, len(r.List()))
	}
}

func TestRegistryListSorted(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	list := r.List()
	for i := 1; i < len(list); i++ {
		if list[i].Name < list[i-1].Name {
			t.Errorf("List() not sorted: %q before %q", list[i-1].Name, list[i].Name)
		}
	}
}
