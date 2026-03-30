package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ui"
)

func newDevCmd() *cobra.Command {
	var buildFlag bool
	var detachFlag bool

	cmd := &cobra.Command{
		Use:   "dev [down]",
		Short: "Run your app locally for development",
		Long:  "Wraps docker compose with Neo's env loading. If no docker-compose.yml exists, builds and runs from your Dockerfile.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "down" {
				return runDevDown()
			}
			return runDev(buildFlag, detachFlag)
		},
	}

	cmd.Flags().BoolVar(&buildFlag, "build", false, "rebuild images before starting")
	cmd.Flags().BoolVarP(&detachFlag, "detach", "d", false, "run in background")
	return cmd
}

func runDev(build, detach bool) error {
	absPath, _ := filepath.Abs(".")

	// Load .neo.yml for env vars
	neoConfig, _ := loadNeoConfig(".")

	// Check for docker compose
	composePath := findComposeFile(".")
	if composePath != "" {
		return runDevCompose(composePath, neoConfig, build, detach)
	}

	// No compose — build from Dockerfile
	dockerfile := filepath.Join(absPath, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		ui.Error("No docker-compose.yml or Dockerfile found")
		fmt.Println("  Neo dev needs one of these to run your app locally.")
		return nil
	}

	return runDevDockerfile(absPath, neoConfig, build, detach)
}

func runDevCompose(composePath string, neoConfig *NeoConfig, build, detach bool) error {
	appName := filepath.Base(filepath.Dir(composePath))
	if filepath.Dir(composePath) == "." {
		appName, _ = os.Getwd()
		appName = filepath.Base(appName)
	}
	if neoConfig != nil && neoConfig.Name != "" {
		appName = neoConfig.Name
	}

	fmt.Println()
	fmt.Printf("  %s %s (local)\n", ui.Bold.Render(appName), ui.Faint.Render("via docker compose"))

	// Build docker compose args
	args := []string{"compose", "-f", composePath}

	// Add project name
	args = append(args, "-p", sanitizeName(appName))

	args = append(args, "up")
	if build {
		args = append(args, "--build")
	}
	if detach {
		args = append(args, "-d")
	}

	// Build env using dev priority chain
	projectDir, _ := filepath.Abs(filepath.Dir(composePath))
	devEnv := buildDevEnv(projectDir, neoConfig)

	envVars := os.Environ()
	for k, v := range devEnv {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	// Show info
	if len(devEnv) > 0 {
		fmt.Printf("  Env: %d vars loaded\n", len(devEnv))
	}

	port := resolveDevPort(neoConfig)
	if port > 0 {
		fmt.Printf("  Local: %s\n", ui.Green.Render(fmt.Sprintf("http://localhost:%d", port)))
	}
	fmt.Println()

	// Run docker compose
	cmd := exec.Command("docker", args...)
	cmd.Env = envVars
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func runDevDockerfile(absPath string, neoConfig *NeoConfig, build, detach bool) error {
	appName := filepath.Base(absPath)
	if neoConfig != nil && neoConfig.Name != "" {
		appName = neoConfig.Name
	}

	safeName := sanitizeName(appName)
	containerName := "neo-dev-" + safeName
	imageName := "neo-dev-" + safeName + ":latest"
	networkName := "neo-dev-" + safeName

	hasWorkers := neoConfig != nil && len(neoConfig.Workers) > 0
	hasSidecars := neoConfig != nil && len(neoConfig.Sidecars) > 0

	fmt.Println()
	fmt.Printf("  %s %s (local, Dockerfile)\n", ui.Bold.Render(appName), ui.Faint.Render("standalone"))

	// Build image
	if build || !dockerImageExists(imageName) {
		spin := ui.NewSpinner("Building image...")
		spin.Start()
		buildCmd := exec.Command("docker", "build", "-t", imageName, absPath)
		out, err := buildCmd.CombinedOutput()
		spin.Stop()
		if err != nil {
			fmt.Println(string(out))
			return fmt.Errorf("docker build failed: %w", err)
		}
		ui.Success("Image built")
	}

	// Create Docker network for inter-container communication
	if hasWorkers || hasSidecars {
		exec.Command("docker", "network", "create", networkName).Run()
	}

	// Start sidecars first (services the app depends on)
	if hasSidecars {
		startDevSidecars(appName, absPath, networkName, build, neoConfig.Sidecars)
	}

	// Stop existing app container
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Build run args
	args := []string{"run", "--name", containerName}
	if detach {
		args = append(args, "-d")
	} else {
		args = append(args, "--rm", "-it")
	}

	// Network
	if hasWorkers || hasSidecars {
		args = append(args, "--network", networkName)
	}

	// Port
	port := resolveDevPort(neoConfig)
	args = append(args, "-p", fmt.Sprintf("%d:%d", port, port))

	// Env vars using dev priority chain
	devEnv := buildDevEnv(absPath, neoConfig)
	for k, v := range devEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Volumes
	volumes, err := buildDevVolumes(absPath, neoConfig)
	if err != nil {
		return err
	}
	for _, v := range volumes {
		args = append(args, "-v", v)
	}

	args = append(args, imageName)

	// Show info
	if len(devEnv) > 0 {
		fmt.Printf("  Env: %d vars loaded\n", len(devEnv))
	}
	if len(volumes) > 0 {
		fmt.Printf("  Volumes: %d mounts\n", len(volumes))
	}
	if hasWorkers {
		fmt.Printf("  Workers: %s\n", strings.Join(mapKeys(neoConfig.Workers), ", "))
	}
	if hasSidecars {
		fmt.Printf("  Sidecars: %s\n", strings.Join(mapKeys(neoConfig.Sidecars), ", "))
	}
	fmt.Printf("  Local: %s\n", ui.Green.Render(fmt.Sprintf("http://localhost:%d", port)))
	fmt.Println()

	// Start workers (detached, even when app is foreground)
	if hasWorkers {
		startDevWorkers(appName, imageName, networkName, devEnv, volumes, neoConfig.Workers)
	}

	// Run app
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runDevDown() error {
	absPath, _ := filepath.Abs(".")
	neoConfig, _ := loadNeoConfig(".")

	composePath := findComposeFile(".")
	if composePath != "" {
		appName := filepath.Base(absPath)
		if neoConfig != nil && neoConfig.Name != "" {
			appName = neoConfig.Name
		}

		cmd := exec.Command("docker", "compose", "-f", composePath, "-p", sanitizeName(appName), "down")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Standalone container
	appName := filepath.Base(absPath)
	if neoConfig != nil && neoConfig.Name != "" {
		appName = neoConfig.Name
	}
	safeName := sanitizeName(appName)
	containerName := "neo-dev-" + safeName
	networkName := "neo-dev-" + safeName

	// Stop workers and sidecars first
	if neoConfig != nil {
		for name := range neoConfig.Workers {
			exec.Command("docker", "rm", "-f", "neo-dev-"+safeName+"-worker-"+name).Run()
		}
		for name := range neoConfig.Sidecars {
			exec.Command("docker", "rm", "-f", "neo-dev-"+safeName+"-sidecar-"+name).Run()
		}
	}

	// Stop app container
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Remove network
	exec.Command("docker", "network", "rm", networkName).Run()

	ui.Success("Stopped " + appName)
	return nil
}

func dockerImageExists(image string) bool {
	cmd := exec.Command("docker", "image", "inspect", image)
	return cmd.Run() == nil
}

// startDevWorkers starts worker containers for dev mode.
// Workers share the app image but run with a different command.
func startDevWorkers(appName, imageName, networkName string, env map[string]string, volumes []string, workers map[string]NeoWorker) {
	safeName := sanitizeName(appName)
	for name, cfg := range workers {
		containerName := "neo-dev-" + safeName + "-worker-" + name
		exec.Command("docker", "rm", "-f", containerName).Run()

		args := []string{"run", "-d", "--name", containerName, "--network", networkName}
		for _, v := range volumes {
			args = append(args, "-v", v)
		}
		for k, v := range env {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
		}
		args = append(args, imageName)
		args = append(args, strings.Fields(cfg.Command)...)

		if err := exec.Command("docker", args...).Run(); err != nil {
			ui.Error(fmt.Sprintf("Worker %s failed: %s", name, err))
		} else {
			ui.Success(fmt.Sprintf("Worker %s started", name))
		}
	}
}

// startDevSidecars starts sidecar containers for dev mode.
// Sidecars have their own image (built or pulled) and their own env vars.
func startDevSidecars(appName, projectDir, networkName string, buildFlag bool, sidecars map[string]NeoSidecar) {
	safeName := sanitizeName(appName)
	for name, cfg := range sidecars {
		containerName := "neo-dev-" + safeName + "-sidecar-" + name
		exec.Command("docker", "rm", "-f", containerName).Run()

		var scImage string
		if cfg.Image != "" {
			scImage = cfg.Image
			spin := ui.NewSpinner(fmt.Sprintf("Pulling sidecar %s...", name))
			spin.Start()
			err := exec.Command("docker", "pull", scImage).Run()
			spin.Stop()
			if err != nil {
				ui.Error(fmt.Sprintf("Sidecar %s pull failed: %s", name, err))
				continue
			}
		} else if cfg.Build.Context != "" {
			scImage = "neo-dev-" + safeName + "-sidecar-" + name + ":latest"
			buildCtx := cfg.Build.Context
			if !filepath.IsAbs(buildCtx) {
				buildCtx = filepath.Join(projectDir, buildCtx)
			}

			if buildFlag || !dockerImageExists(scImage) {
				spin := ui.NewSpinner(fmt.Sprintf("Building sidecar %s...", name))
				spin.Start()
				buildArgs := []string{"build", "-t", scImage}
				if cfg.Build.Dockerfile != "" {
					buildArgs = append(buildArgs, "-f", filepath.Join(buildCtx, cfg.Build.Dockerfile))
				}
				buildArgs = append(buildArgs, buildCtx)
				err := exec.Command("docker", buildArgs...).Run()
				spin.Stop()
				if err != nil {
					ui.Error(fmt.Sprintf("Sidecar %s build failed: %s", name, err))
					continue
				}
			}
		} else {
			ui.Error(fmt.Sprintf("Sidecar %s: must have 'image' or 'build'", name))
			continue
		}

		args := []string{"run", "-d", "--name", containerName, "--network", networkName}
		for volName, containerPath := range cfg.Volumes {
			volDockerName := "neo-dev-" + safeName + "-" + volName
			args = append(args, "-v", fmt.Sprintf("%s:%s", volDockerName, containerPath))
		}
		for k, v := range cfg.Env {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
		}
		args = append(args, scImage)
		if cfg.Command != "" {
			args = append(args, strings.Fields(cfg.Command)...)
		}

		if err := exec.Command("docker", args...).Run(); err != nil {
			ui.Error(fmt.Sprintf("Sidecar %s failed: %s", name, err))
		} else {
			ui.Success(fmt.Sprintf("Sidecar %s started", name))
		}
	}
}

// mapKeys returns the keys of a map as a sorted slice (for display).
func mapKeys[K comparable, V any](m map[K]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, fmt.Sprintf("%v", k))
	}
	return keys
}

// buildDevEnv merges env sources for neo dev with proper priority.
// Priority (lowest → highest):
//  1. Auto-loaded .env from project root
//  2. Top-level env_file from .neo.yml
//  3. Top-level env from .neo.yml
//  4. dev.env_file from .neo.yml
//  5. dev.env from .neo.yml
//
// After merging, ${VAR} references are interpolated.
func buildDevEnv(projectDir string, neoConfig *NeoConfig) map[string]string {
	env := make(map[string]string)

	// 1. Auto-load .env if it exists (lowest priority)
	dotEnvPath := filepath.Join(projectDir, ".env")
	if _, err := os.Stat(dotEnvPath); err == nil {
		if autoEnv, err := parseEnvFile(dotEnvPath); err == nil {
			for k, v := range autoEnv {
				env[k] = v
			}
		}
	}

	if neoConfig != nil {
		// 2. Top-level env_file
		if neoConfig.EnvFile != "" {
			envFilePath := neoConfig.EnvFile
			if !filepath.IsAbs(envFilePath) {
				envFilePath = filepath.Join(projectDir, envFilePath)
			}
			if fileEnv, err := parseEnvFile(envFilePath); err == nil {
				for k, v := range fileEnv {
					env[k] = v
				}
			}
		}

		// 3. Top-level env
		for k, v := range neoConfig.Env {
			env[k] = v
		}

		// 4-5. Dev section overrides
		if neoConfig.Dev != nil {
			if neoConfig.Dev.EnvFile != "" {
				envFilePath := neoConfig.Dev.EnvFile
				if !filepath.IsAbs(envFilePath) {
					envFilePath = filepath.Join(projectDir, envFilePath)
				}
				if fileEnv, err := parseEnvFile(envFilePath); err == nil {
					for k, v := range fileEnv {
						env[k] = v
					}
				}
			}
			for k, v := range neoConfig.Dev.Env {
				env[k] = v
			}
		}
	}

	// Apply interpolation
	env = interpolateEnvValues(env)

	return env
}

// buildDevVolumes returns docker -v flags for dev mode.
// Top-level volumes are auto-mounted to ./{volume-name} by default.
// dev.volumes can override local paths (short form) or add standalone mounts (full form with :).
func buildDevVolumes(projectDir string, neoConfig *NeoConfig) ([]string, error) {
	resolved := resolveConfigVolumes(neoConfig)
	if len(resolved) == 0 && (neoConfig == nil || neoConfig.Dev == nil || len(neoConfig.Dev.Volumes) == 0) {
		return nil, nil
	}

	// Start with all top-level volumes → default bind-mount to ./{name}
	localPaths := make(map[string]string)
	containerPaths := make(map[string]string)
	for _, rv := range resolved {
		localPaths[rv.Name] = filepath.Join(projectDir, rv.Name)
		containerPaths[rv.Name] = rv.ContainerPath
	}

	// Extra standalone mounts from dev.volumes (full form with :)
	var extraMounts []string

	if neoConfig != nil && neoConfig.Dev != nil && len(neoConfig.Dev.Volumes) > 0 {
		for name, val := range neoConfig.Dev.Volumes {
			if strings.Contains(val, ":") {
				// Full form: local:container — standalone dev-only mount
				parts := strings.SplitN(val, ":", 2)
				localPath := parts[0]
				if !filepath.IsAbs(localPath) {
					localPath = filepath.Join(projectDir, localPath)
				}
				if err := os.MkdirAll(localPath, 0755); err != nil {
					return nil, fmt.Errorf("create dev volume directory %s: %w", localPath, err)
				}
				extraMounts = append(extraMounts, fmt.Sprintf("%s:%s", localPath, parts[1]))
			} else {
				// Short form: override local path for a top-level volume
				if _, ok := containerPaths[name]; !ok {
					return nil, fmt.Errorf("dev volume %q not found in top-level volumes — add volumes.%s.path to .neo.yml", name, name)
				}
				absLocal := val
				if !filepath.IsAbs(absLocal) {
					absLocal = filepath.Join(projectDir, absLocal)
				}
				localPaths[name] = absLocal
			}
		}
	}

	// Build mount strings from top-level volumes
	var mounts []string
	for name, localPath := range localPaths {
		containerPath := containerPaths[name]
		if err := os.MkdirAll(localPath, 0755); err != nil {
			return nil, fmt.Errorf("create dev volume directory %s: %w", localPath, err)
		}
		mounts = append(mounts, fmt.Sprintf("%s:%s", localPath, containerPath))
	}

	// Append extra standalone mounts
	mounts = append(mounts, extraMounts...)

	return mounts, nil
}

// resolveDevPort returns the port to use for dev mode.
// Priority: dev.port > top-level port > 8080 default.
func resolveDevPort(neoConfig *NeoConfig) int {
	if neoConfig != nil {
		if neoConfig.Dev != nil && neoConfig.Dev.Port > 0 {
			return neoConfig.Dev.Port
		}
		if neoConfig.Port > 0 {
			return neoConfig.Port
		}
	}
	return 8080
}
