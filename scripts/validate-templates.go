//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest represents a template manifest.yml with only the fields we validate.
type Manifest struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Category    string   `yaml:"category"`
	Version     string   `yaml:"version"`
	Image       string   `yaml:"image"`
	Port        int      `yaml:"port"`
	Env         []EnvVar `yaml:"env"`
	Services    []struct {
		Env []EnvVar `yaml:"env"`
	} `yaml:"services"`
}

// EnvVar represents an environment variable entry in a manifest.
type EnvVar struct {
	Key      string `yaml:"key"`
	Generate string `yaml:"generate"`
}

var allowedCategories = map[string]bool{
	"analytics":       true,
	"automation":      true,
	"blogging":        true,
	"cms":             true,
	"communication":   true,
	"databases":       true,
	"developer-tools": true,
	"e-commerce":      true,
	"file-sharing":    true,
	"monitoring":      true,
	"productivity":    true,
	"reading":         true,
	"search":          true,
	"security":        true,
	"social":          true,
	"support":         true,
}

var allowedEncodings = map[string]bool{
	"hex":    true,
	"base64": true,
}

func main() {
	templatesDir := filepath.Join("internal", "app", "templates")

	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading templates directory: %v\n", err)
		os.Exit(1)
	}

	totalErrors := 0
	totalTemplates := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		manifestPath := filepath.Join(templatesDir, dirName, "manifest.yml")

		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		}

		totalTemplates++
		errors := validateTemplate(templatesDir, dirName, manifestPath)

		if len(errors) == 0 {
			fmt.Printf("  PASS  %s\n", dirName)
		} else {
			fmt.Printf("  FAIL  %s\n", dirName)
			for _, e := range errors {
				fmt.Printf("        - %s\n", e)
			}
			totalErrors += len(errors)
		}
	}

	fmt.Println()
	fmt.Printf("Validated %d templates\n", totalTemplates)

	if totalErrors > 0 {
		fmt.Printf("%d error(s) found\n", totalErrors)
		os.Exit(1)
	}

	fmt.Println("All templates valid")
}

func validateTemplate(templatesDir, dirName, manifestPath string) []string {
	var errors []string

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return append(errors, fmt.Sprintf("cannot read manifest: %v", err))
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return append(errors, fmt.Sprintf("invalid YAML: %v", err))
	}

	// Required fields
	if m.Name == "" {
		errors = append(errors, "missing required field: name")
	}
	if m.Title == "" {
		errors = append(errors, "missing required field: title")
	}
	if m.Description == "" {
		errors = append(errors, "missing required field: description")
	}
	if m.Category == "" {
		errors = append(errors, "missing required field: category")
	}
	if m.Version == "" {
		errors = append(errors, "missing required field: version")
	}
	if m.Image == "" {
		errors = append(errors, "missing required field: image")
	}
	if m.Port == 0 {
		errors = append(errors, "missing required field: port")
	}

	// Name must match directory name
	if m.Name != "" && m.Name != dirName {
		errors = append(errors, fmt.Sprintf("name %q does not match directory %q", m.Name, dirName))
	}

	// Category must be from allowed list
	if m.Category != "" && !allowedCategories[m.Category] {
		allowed := make([]string, 0, len(allowedCategories))
		for k := range allowedCategories {
			allowed = append(allowed, k)
		}
		errors = append(errors, fmt.Sprintf("invalid category %q (allowed: %s)", m.Category, strings.Join(sorted(allowed), ", ")))
	}

	// Docker image must have a tag separator
	if m.Image != "" && !strings.Contains(m.Image, ":") {
		errors = append(errors, fmt.Sprintf("image %q missing tag (expected format: image:tag)", m.Image))
	}

	// Validate generate specs in env vars
	errors = append(errors, validateGenerateSpecs(m.Env, "env")...)
	for i, svc := range m.Services {
		errors = append(errors, validateGenerateSpecs(svc.Env, fmt.Sprintf("services[%d].env", i))...)
	}

	// Check README.md exists
	readmePath := filepath.Join(templatesDir, dirName, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		errors = append(errors, "missing README.md")
	}

	return errors
}

func validateGenerateSpecs(envVars []EnvVar, context string) []string {
	var errors []string
	for _, ev := range envVars {
		if ev.Generate == "" {
			continue
		}
		parts := strings.SplitN(ev.Generate, ":", 2)
		if len(parts) != 2 {
			errors = append(errors, fmt.Sprintf("%s: %s has invalid generate spec %q (expected encoding:length)", context, ev.Key, ev.Generate))
			continue
		}
		encoding := parts[0]
		if !allowedEncodings[encoding] {
			errors = append(errors, fmt.Sprintf("%s: %s has unknown encoding %q in generate spec (allowed: hex, base64)", context, ev.Key, encoding))
		}
	}
	return errors
}

func sorted(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
