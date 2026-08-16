package commands

import (
	"bufio"
	"fmt"
	"io"
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

	return parseEnvReader(f)
}

// parseEnvContent parses .env text that is already in memory — used for
// decrypted .env.encrypted contents, which never touch disk.
func parseEnvContent(content string) map[string]string {
	env, _ := parseEnvReader(strings.NewReader(content))
	return env
}

// parseEnvReader is the shared .env parser behind parseEnvFile/parseEnvContent.
func parseEnvReader(r io.Reader) (map[string]string, error) {
	env := make(map[string]string)
	scanner := bufio.NewScanner(r)
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

// interpolateString replaces all ${VAR} and ${VAR:-default} patterns in s.
// Lookup order: env map first, then os.Getenv. An unset or empty value falls back
// to the :-default (bash semantics) when one is given; otherwise the reference is
// left as-is.
func interpolateString(s string, env map[string]string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end >= 0 {
				expr := s[i+2 : i+2+end] // "VAR" or "VAR:-default"
				varName, defaultVal, hasDefault := expr, "", false
				if idx := strings.Index(expr, ":-"); idx >= 0 {
					varName = expr[:idx]
					defaultVal = expr[idx+2:]
					hasDefault = true
				}

				resolved, found := "", false
				if v, ok := env[varName]; ok && v != "" {
					resolved, found = v, true
				} else if v := os.Getenv(varName); v != "" {
					resolved, found = v, true
				}

				if found {
					buf.WriteString(resolved)
				} else if hasDefault {
					buf.WriteString(defaultVal)
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
