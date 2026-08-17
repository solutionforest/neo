package commands

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Surgical YAML editing for .neo.yml.
//
// Marshalling a NeoConfig struct back over a hand-written file destroys
// everything the struct doesn't model: comments, key order, quoting style and
// indentation all get rewritten. Editing the parsed node tree instead changes
// only the values we mean to change and leaves the rest of the file byte-identical.

// loadYAMLDoc parses a YAML file into its document node.
// Returns nil (no error) when the file does not exist.
func loadYAMLDoc(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil // empty file
	}
	return &doc, nil
}

// saveYAMLDoc writes a document node back out with 2-space indentation, which
// is what .neo.yml files are written with by hand and by `neo config init`.
func saveYAMLDoc(path string, doc *yaml.Node) error {
	f, err := os.CreateTemp(fileDir(path), ".neo.yml.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) //nolint:errcheck // no-op once renamed

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		f.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileDir(path string) string {
	if i := lastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return "."
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// docRoot returns the top-level mapping node of a document, or nil if the
// document isn't a mapping.
func docRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// yamlMapGet returns the value node stored under key, or nil.
func yamlMapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlMapSet writes key: val, replacing an existing value in place (so the key
// keeps its position and any comment attached to it) or appending a new pair.
func yamlMapSet(m *yaml.Node, key string, val *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			// Keep the author's quoting style when replacing like with like,
			// so `domain: "x"` doesn't silently become `domain: x`.
			if old.Kind == yaml.ScalarNode && val.Kind == yaml.ScalarNode && old.Tag == val.Tag {
				val.Style = old.Style
			}
			val.HeadComment = old.HeadComment
			val.LineComment = old.LineComment
			val.FootComment = old.FootComment
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val,
	)
}

// yamlMapDelete removes a key and its value. Reports whether it was present.
func yamlMapDelete(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func yamlString(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func yamlInt(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

func yamlBool(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// yamlEnvironmentNode returns the mapping node for one entry under
// environments:, or nil when either the block or that environment is absent.
func yamlEnvironmentNode(root *yaml.Node, envName string) (*yaml.Node, error) {
	envs := yamlMapGet(root, "environments")
	if envs == nil {
		return nil, fmt.Errorf("no environments: block in .neo.yml")
	}
	if envs.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("environments: is not a mapping")
	}
	env := yamlMapGet(envs, envName)
	if env == nil {
		return nil, fmt.Errorf("environment %q not found in .neo.yml", envName)
	}
	if env.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("environment %q is not a mapping", envName)
	}
	return env, nil
}
