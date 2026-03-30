package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// parseEnvFile reads a .env file and returns key-value pairs.
// Supports KEY=VALUE, KEY="VALUE", KEY='VALUE', comments (#), and blank lines.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split on first =
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		if key == "" {
			continue
		}

		// Strip surrounding quotes
		val = unquote(val)

		env[key] = val
	}

	return env, scanner.Err()
}

// parseEnvPairs parses KEY=VALUE strings from CLI flags.
func parseEnvPairs(pairs []string) (map[string]string, error) {
	env := make(map[string]string)
	for _, pair := range pairs {
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid env format %q (expected KEY=VALUE)", pair)
		}
		key := strings.TrimSpace(pair[:idx])
		val := pair[idx+1:]
		if key == "" {
			return nil, fmt.Errorf("empty key in env pair %q", pair)
		}
		env[key] = val
	}
	return env, nil
}

// interpolateEnvValues replaces ${VAR} references in values with values from
// the combined env map or the OS environment. Single-pass: unresolved refs
// are left as-is.
func interpolateEnvValues(env map[string]string) map[string]string {
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[k] = interpolateString(v, env)
	}
	return result
}

// interpolateString replaces all ${VAR} patterns in s.
// Lookup order: env map first, then os.Getenv. Unresolved references are left as-is.
func interpolateString(s string, env map[string]string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end >= 0 {
				varName := s[i+2 : i+2+end]
				if val, ok := env[varName]; ok {
					buf.WriteString(val)
				} else if val := os.Getenv(varName); val != "" {
					buf.WriteString(val)
				} else {
					buf.WriteString(s[i : i+2+end+1]) // leave unresolved
				}
				i = i + 2 + end + 1
				continue
			}
		}
		buf.WriteByte(s[i])
		i++
	}
	return buf.String()
}

// unquote strips matching single or double quotes from a value.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
