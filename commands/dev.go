package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

	// Build env from .neo.yml
	envVars := os.Environ()
	if neoConfig != nil {
		// Load env_file first
		if neoConfig.EnvFile != "" {
			if fileEnv, err := parseEnvFile(neoConfig.EnvFile); err == nil {
				for k, v := range fileEnv {
					envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
				}
			}
		}
		// Then .neo.yml env vars (higher priority)
		for k, v := range neoConfig.Env {
			envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Show info
	if neoConfig != nil && len(neoConfig.Env) > 0 {
		fmt.Printf("  Env: .neo.yml (%d vars)\n", len(neoConfig.Env))
	}

	port := 0
	if neoConfig != nil && neoConfig.Port > 0 {
		port = neoConfig.Port
	}
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

	containerName := "neo-dev-" + sanitizeName(appName)
	imageName := "neo-dev-" + sanitizeName(appName) + ":latest"

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

	// Stop existing container
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Build run args
	args := []string{"run", "--name", containerName}
	if detach {
		args = append(args, "-d")
	} else {
		args = append(args, "--rm", "-it")
	}

	// Port
	port := 8080
	if neoConfig != nil && neoConfig.Port > 0 {
		port = neoConfig.Port
	}
	args = append(args, "-p", fmt.Sprintf("%d:%d", port, port))

	// Env vars from .neo.yml
	if neoConfig != nil {
		if neoConfig.EnvFile != "" {
			if fileEnv, err := parseEnvFile(neoConfig.EnvFile); err == nil {
				for k, v := range fileEnv {
					args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
				}
			}
		}
		for k, v := range neoConfig.Env {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
		}
	}

	args = append(args, imageName)

	fmt.Printf("  Local: %s\n", ui.Green.Render(fmt.Sprintf("http://localhost:%d", port)))
	fmt.Println()

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
	containerName := "neo-dev-" + sanitizeName(appName)
	exec.Command("docker", "rm", "-f", containerName).Run()
	ui.Success("Stopped " + appName)
	return nil
}

func dockerImageExists(image string) bool {
	cmd := exec.Command("docker", "image", "inspect", image)
	return cmd.Run() == nil
}
