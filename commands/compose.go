package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile is a minimal representation of docker-compose.yml.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// composeService is a single service in a compose file.
type composeService struct {
	Image       string      `yaml:"image"`
	Build       interface{} `yaml:"build"` // string or map
	Ports       []string    `yaml:"ports"`
	Environment interface{} `yaml:"environment"` // map or list
	EnvFile     interface{} `yaml:"env_file"`    // string or list
	Volumes     []string    `yaml:"volumes"`     // "name:/path" or "/host:/path"
	Command     interface{} `yaml:"command"`     // string or list
	DependsOn   interface{} `yaml:"depends_on"`  // list or map
}

// parseComposeCommand extracts command as a single string.
func parseComposeCommand(cmd interface{}) string {
	if cmd == nil {
		return ""
	}
	switch v := cmd.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// parseComposeVolumeMounts extracts named volume mounts (skipping bind mounts).
// Returns map of volume-name → container-path.
func parseComposeVolumeMounts(volumes []string) map[string]string {
	result := make(map[string]string)
	for _, v := range volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		path := parts[1]
		// Strip :ro, :rw suffixes
		if idx := strings.IndexByte(path, ':'); idx > 0 {
			path = path[:idx]
		}
		// Skip bind mounts (start with / or .)
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, ".") {
			continue
		}
		result[name] = path
	}
	return result
}

// parseFullComposeFile reads a compose file and returns all services with full details.
func parseFullComposeFile(path string) (*composeFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}
	return &cf, nil
}

// parseComposeFile reads a docker-compose.yml and extracts config for a service.
// If service is empty and there's only one service, it uses that one.
// If there are multiple services, it tries to find the main app service
// (one with build: context, not a database/cache image).
func parseComposeFile(path, service string) (*composeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}

	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	if len(cf.Services) == 0 {
		return nil, fmt.Errorf("no services found in %s", path)
	}

	// Resolve which service to use
	var svc composeService
	var svcName string

	if service != "" {
		s, ok := cf.Services[service]
		if !ok {
			return nil, fmt.Errorf("service %q not found in %s", service, path)
		}
		svc = s
		svcName = service
	} else if len(cf.Services) == 1 {
		for name, s := range cf.Services {
			svc = s
			svcName = name
		}
	} else {
		// Try to find the main app service (has build context, not a known infra image)
		svcName, svc = guessAppService(cf.Services)
		if svcName == "" {
			// Fall back to first service with a build directive
			for name, s := range cf.Services {
				if s.Build != nil {
					svc = s
					svcName = name
					break
				}
			}
		}
		if svcName == "" {
			return nil, fmt.Errorf("multiple services in %s — specify with .neo.yml compose_service or --compose-service", path)
		}
	}

	result := &composeResult{
		ServiceName: svcName,
		Env:         make(map[string]string),
	}

	// Extract environment variables
	if svc.Environment != nil {
		result.Env = parseComposeEnvironment(svc.Environment)
	}

	// Extract env_file references
	envFiles := parseComposeEnvFile(svc.EnvFile)
	dir := filepath.Dir(path)
	for _, ef := range envFiles {
		efPath := ef
		if !filepath.IsAbs(efPath) {
			efPath = filepath.Join(dir, efPath)
		}
		if fileEnv, err := parseEnvFile(efPath); err == nil {
			for k, v := range fileEnv {
				// Don't override explicit environment values
				if _, exists := result.Env[k]; !exists {
					result.Env[k] = v
				}
			}
		}
	}

	// Extract port
	result.Port = parseComposePort(svc.Ports)

	return result, nil
}

// composeResult holds extracted config from a compose service.
type composeResult struct {
	ServiceName string
	Env         map[string]string
	Port        int
}

// parseComposeEnvironment handles both map and list formats.
// Map: environment: { KEY: value }
// List: environment: [ "KEY=value" ]
func parseComposeEnvironment(env interface{}) map[string]string {
	result := make(map[string]string)

	switch v := env.(type) {
	case map[string]interface{}:
		for key, val := range v {
			result[key] = fmt.Sprintf("%v", val)
		}
	case []interface{}:
		for _, item := range v {
			s := fmt.Sprintf("%v", item)
			if idx := strings.IndexByte(s, '='); idx > 0 {
				result[s[:idx]] = s[idx+1:]
			}
		}
	}

	return result
}

// parseComposeEnvFile handles both string and list formats.
func parseComposeEnvFile(envFile interface{}) []string {
	if envFile == nil {
		return nil
	}

	switch v := envFile.(type) {
	case string:
		return []string{v}
	case []interface{}:
		var files []string
		for _, item := range v {
			files = append(files, fmt.Sprintf("%v", item))
		}
		return files
	}

	return nil
}

// parseComposePort extracts the container port from a ports list.
// Handles "8080:3000" → 3000, "3000" → 3000, "8080:3000/tcp" → 3000
func parseComposePort(ports []string) int {
	if len(ports) == 0 {
		return 0
	}

	// Use first port entry
	p := ports[0]

	// Strip protocol suffix
	if idx := strings.IndexByte(p, '/'); idx > 0 {
		p = p[:idx]
	}

	var port int
	if idx := strings.LastIndexByte(p, ':'); idx >= 0 {
		// "8080:3000" → container port is 3000
		fmt.Sscanf(p[idx+1:], "%d", &port)
	} else {
		fmt.Sscanf(p, "%d", &port)
	}

	return port
}

// guessAppService tries to identify the main application service.
// It prefers services with a build directive and skips known infrastructure images.
func guessAppService(services map[string]composeService) (string, composeService) {
	infraPrefixes := []string{
		"mysql", "mariadb", "postgres", "mongo", "redis",
		"memcached", "rabbitmq", "elasticsearch", "meilisearch",
		"minio", "mailhog", "mailpit", "selenium", "phpmyadmin",
		"adminer", "nginx", "traefik", "caddy",
	}

	isInfra := func(name string, svc composeService) bool {
		nameLower := strings.ToLower(name)
		for _, prefix := range infraPrefixes {
			if strings.Contains(nameLower, prefix) {
				return true
			}
		}
		if svc.Image != "" {
			imageLower := strings.ToLower(svc.Image)
			for _, prefix := range infraPrefixes {
				if strings.HasPrefix(imageLower, prefix) {
					return true
				}
			}
		}
		return false
	}

	// First pass: find services with build + ports (most likely the main app)
	for name, svc := range services {
		if svc.Build != nil && len(svc.Ports) > 0 && !isInfra(name, svc) {
			return name, svc
		}
	}

	// Second pass: find services with build that aren't infra
	for name, svc := range services {
		if svc.Build != nil && !isInfra(name, svc) {
			return name, svc
		}
	}

	// Third pass: any service that isn't infra
	for name, svc := range services {
		if !isInfra(name, svc) {
			return name, svc
		}
	}

	return "", composeService{}
}

// findComposeFile looks for docker-compose files in a directory.
func findComposeFile(dir string) string {
	names := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
