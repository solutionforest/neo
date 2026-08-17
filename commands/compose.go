package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Entrypoint  interface{} `yaml:"entrypoint"`  // string or list
	Restart     string      `yaml:"restart"`
	DependsOn   interface{} `yaml:"depends_on"` // list or map
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

// parseComposeEnvFile handles every Compose env_file form:
//
//	env_file: .env
//	env_file: [.env, .env.local]
//	env_file:
//	  - path: ./share.env
//	    required: true
//	  - path: ./override.env
//	    required: false
//
// It returns the referenced file paths in order. The long ({path, required})
// form is common in modern Compose files; treating each list item as a plain
// string turned those map entries into garbage like "map[path:./share.env ...]".
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
			switch it := item.(type) {
			case string:
				files = append(files, it)
			case map[string]interface{}:
				// Long form: { path: ..., required: bool }. Callers already
				// skip files that fail to load, which honors required: false.
				if p, ok := it["path"].(string); ok && p != "" {
					files = append(files, p)
				}
			}
		}
		return files
	}

	return nil
}

// composeBuildDockerfile returns the dockerfile path from a Compose build
// directive when it uses the map form ({context, dockerfile}). The plain string
// form (context only) has no custom dockerfile, so it returns "".
func composeBuildDockerfile(build interface{}) string {
	if m, ok := build.(map[string]interface{}); ok {
		if df, ok := m["dockerfile"].(string); ok {
			return df
		}
	}
	return ""
}

// composeBindMounts returns the bind-mount specs (host:container) from a volumes
// list — the ones config generate cannot migrate because they reference host
// paths. Used to warn instead of silently dropping them.
func composeBindMounts(volumes []string) []string {
	var binds []string
	for _, v := range volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.HasPrefix(parts[0], "/") || strings.HasPrefix(parts[0], ".") {
			binds = append(binds, v)
		}
	}
	return binds
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

// composeServiceEnv returns a service's environment as a map, accepting both
// the map and list forms compose allows.
func composeServiceEnv(svc composeService) map[string]string {
	if svc.Environment == nil {
		return nil
	}
	return parseComposeEnvironment(svc.Environment)
}

// backgroundCommandMarkers identify a service whose command is a background
// process rather than a web server. These must never be picked as the public
// app — doing so silently deploys a queue worker where the site should be.
var backgroundCommandMarkers = []string{
	"queue:work", "queue:listen", "horizon", "schedule:work", "schedule:run",
	"worker", "consume", "cron", "supervisord",
}

// looksLikeBackgroundCommand reports whether a compose command runs a worker,
// scheduler or similar rather than serving HTTP.
//
// Flags are stripped before matching. Octane and FrankenPHP servers take
// --workers=N, and matching "worker" inside that flag disqualified the actual
// web service — the one thing this must never do.
func looksLikeBackgroundCommand(cmd string) bool {
	var words []string
	for _, token := range strings.Fields(strings.ToLower(cmd)) {
		if strings.HasPrefix(token, "-") {
			continue
		}
		words = append(words, token)
	}
	joined := strings.Join(words, " ")

	for _, marker := range backgroundCommandMarkers {
		if strings.Contains(joined, marker) {
			return true
		}
	}
	return false
}

// composeFullCommand joins a service's entrypoint and command the way Docker
// runs them. Compose files routinely split them — entrypoint: ["php","artisan"]
// with command: horizon — and reading command alone yields "horizon", which is
// not a program.
func composeFullCommand(svc composeService) string {
	entrypoint := parseComposeCommand(svc.Entrypoint)
	command := parseComposeCommand(svc.Command)

	switch {
	case entrypoint == "":
		return command
	case command == "":
		return entrypoint
	default:
		return entrypoint + " " + command
	}
}

// isOneShotService reports whether a service is an init/build step rather than
// something that stays running: `restart: "no"` alongside a command is the
// compose idiom for "run once and exit" (composer install, migrations, asset
// builds). Deploying one as a worker would re-run it forever.
func isOneShotService(svc composeService) bool {
	restart := strings.ToLower(strings.TrimSpace(svc.Restart))
	return restart == "no" || restart == "on-failure"
}

// migrationMarkers identify one-shot work that belongs in release: — it has to
// run against the deployed container, not on the operator's machine.
var migrationMarkers = []string{"migrate", "db:seed", "key:generate", "storage:link"}

// looksLikeMigrationCommand reports whether a one-shot command touches the
// application's own state, which decides whether it maps to release: or hooks:.
func looksLikeMigrationCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, marker := range migrationMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// composeBuildTarget returns a build stage target, when the build directive
// pins one. Neo always builds the Dockerfile's final stage, so a service
// targeting an earlier stage would deploy something else entirely.
func composeBuildTarget(build interface{}) string {
	if m, ok := build.(map[string]interface{}); ok {
		if t, ok := m["target"].(string); ok {
			return t
		}
	}
	return ""
}

// composeBuildArgs returns the build args a build directive declares.
func composeBuildArgs(build interface{}) []string {
	m, ok := build.(map[string]interface{})
	if !ok {
		return nil
	}
	args, ok := m["args"].(map[string]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// composeInfraPrefixes are images/names that are supporting infrastructure, not
// the application being deployed.
var composeInfraPrefixes = []string{
	"mysql", "mariadb", "postgres", "mongo", "redis",
	"memcached", "rabbitmq", "elasticsearch", "meilisearch",
	"minio", "mailhog", "mailpit", "selenium", "phpmyadmin",
	"adminer", "nginx", "traefik", "caddy",
}

func isInfraService(name string, svc composeService) bool {
	nameLower := strings.ToLower(name)
	for _, prefix := range composeInfraPrefixes {
		if strings.Contains(nameLower, prefix) {
			return true
		}
	}
	if svc.Image != "" {
		imageLower := strings.ToLower(svc.Image)
		for _, prefix := range composeInfraPrefixes {
			if strings.HasPrefix(imageLower, prefix) {
				return true
			}
		}
	}
	return false
}

// ComposeAppScore ranks how likely a service is to be the public app.
// Negative means "never pick this".
//
// Ranking beats first-match iteration because compose files routinely contain
// several services built from one image — a web container plus queue workers
// and a scheduler. Picking whichever the map yielded first produced a different
// answer on every run, sometimes naming a queue worker as the site.
func composeAppScore(name string, svc composeService) int {
	if isInfraService(name, svc) {
		return -1
	}

	score := 0
	if svc.Build != nil {
		score += 100 // a service built from source is almost always the app
	}
	if len(svc.Ports) > 0 {
		score += 50
	}

	env := composeServiceEnv(svc)
	// nginx-proxy / jwilder convention: no published ports, the proxy routes by
	// VIRTUAL_HOST instead. Common enough that ignoring it mis-picks the app.
	if env["VIRTUAL_HOST"] != "" || env["VIRTUAL_PORT"] != "" {
		score += 40
	}
	if env["APP_URL"] != "" {
		score += 5
	}

	if isOneShotService(svc) {
		return -1 // an init/build step, not the long-running app
	}

	if cmd := composeFullCommand(svc); cmd != "" {
		if looksLikeBackgroundCommand(cmd) {
			return -1 // a worker or scheduler is never the public app
		}
		score -= 20 // some other custom command: plausible, but less likely
	}

	return score
}

// guessAppService identifies the main application service deterministically:
// highest score wins, ties broken by name so repeated runs agree.
func guessAppService(services map[string]composeService) (string, composeService) {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	bestName, bestScore := "", -1
	for _, name := range names {
		if score := composeAppScore(name, services[name]); score > bestScore {
			bestName, bestScore = name, score
		}
	}
	if bestName == "" {
		return "", composeService{}
	}
	return bestName, services[bestName]
}

// composePublicServices lists every service that looks externally reachable
// (published ports or a VIRTUAL_HOST). Neo deploys one public app, so more than
// one means the compose file needs splitting.
func composePublicServices(services map[string]composeService) []string {
	var public []string
	for name, svc := range services {
		if isInfraService(name, svc) || isOneShotService(svc) {
			continue
		}
		env := composeServiceEnv(svc)
		if len(svc.Ports) > 0 || env["VIRTUAL_HOST"] != "" {
			public = append(public, name)
		}
	}
	sort.Strings(public)
	return public
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
