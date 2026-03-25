//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest represents the fields we read from each template manifest.yml.
type Manifest struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Category    string   `yaml:"category"`
	Version     string   `yaml:"version"`
	Maintainer  string   `yaml:"maintainer"`
	Official    bool     `yaml:"official"`
	Tags        []string `yaml:"tags"`
}

// TemplateEntry is a single entry in the generated index.
type TemplateEntry struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Version     string   `json:"version"`
	Maintainer  string   `json:"maintainer"`
	Official    bool     `json:"official"`
	Tags        []string `json:"tags"`
}

// TemplateIndex is the top-level structure written to templates.json.
type TemplateIndex struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	Templates   []TemplateEntry `json:"templates"`
}

func main() {
	templatesDir := filepath.Join("internal", "app", "templates")
	outputPath := filepath.Join(templatesDir, "templates.json")

	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading templates directory: %v\n", err)
		os.Exit(1)
	}

	var templates []TemplateEntry

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(templatesDir, entry.Name(), "manifest.yml")

		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // skip directories without a manifest
		}

		var m Manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s (invalid YAML: %v)\n", entry.Name(), err)
			continue
		}

		templates = append(templates, TemplateEntry{
			Name:        m.Name,
			Title:       m.Title,
			Description: m.Description,
			Category:    m.Category,
			Version:     m.Version,
			Maintainer:  m.Maintainer,
			Official:    m.Official,
			Tags:        m.Tags,
		})
	}

	// Sort alphabetically by name
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})

	index := TemplateIndex{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Templates:   templates,
	}

	output, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
		os.Exit(1)
	}

	// Append trailing newline
	output = append(output, '\n')

	if err := os.WriteFile(outputPath, output, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputPath, err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s with %d templates\n", outputPath, len(templates))
}
