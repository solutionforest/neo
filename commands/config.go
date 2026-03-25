package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ui"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage .neo.yml configuration",
	}
	cmd.AddCommand(newConfigGenerateCmd())
	return cmd
}

func newConfigGenerateCmd() *cobra.Command {
	var composePath string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate .neo.yml from docker-compose.yml",
		Long:  "Scans your docker-compose.yml and generates a .neo.yml config file for Neo deployments. Auto-detects the app service, infrastructure sidecars, workers, volumes, and env vars.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGenerate(composePath)
		},
	}

	cmd.Flags().StringVar(&composePath, "compose", "", "path to docker-compose.yml (auto-detected if not set)")
	return cmd
}

func runConfigGenerate(composePath string) error {
	// Find compose file
	if composePath == "" {
		composePath = findComposeFile(".")
	}
	if composePath == "" {
		ui.Error("No docker-compose.yml found in current directory")
		fmt.Println("  Create one, or use --compose to specify a path.")
		return nil
	}

	// Check if .neo.yml already exists
	if _, err := os.Stat(".neo.yml"); err == nil {
		ui.Error(".neo.yml already exists — rename or delete it first")
		return nil
	}

	// Parse compose file
	cf, err := parseFullComposeFile(composePath)
	if err != nil {
		return err
	}

	if len(cf.Services) == 0 {
		ui.Error("No services found in " + composePath)
		return nil
	}

	fmt.Println()
	fmt.Printf("  Scanning %s (%d services)\n\n", ui.Bold.Render(filepath.Base(composePath)), len(cf.Services))

	// Classify services
	appName, appSvc := guessAppService(cf.Services)
	if appName == "" {
		ui.Error("Could not identify the main app service — add compose_service to .neo.yml manually")
		return nil
	}

	cfg := &NeoConfig{
		Name: appName,
		Port: parseComposePort(appSvc.Ports),
		Env:  make(map[string]string),
	}

	// Extract app env vars
	if appSvc.Environment != nil {
		cfg.Env = parseComposeEnvironment(appSvc.Environment)
	}

	// Extract app env_file
	envFiles := parseComposeEnvFile(appSvc.EnvFile)
	if len(envFiles) > 0 {
		cfg.EnvFile = envFiles[0]
	}

	// Extract app volumes
	appVolumes := parseComposeVolumeMounts(appSvc.Volumes)
	if len(appVolumes) > 0 {
		cfg.Volumes = make(map[string]NeoVolume)
		for name, path := range appVolumes {
			cfg.Volumes[name] = NeoVolume{Path: path}
		}
	}

	// Classify other services
	infraPrefixes := []string{
		"mysql", "mariadb", "postgres", "mongo", "redis",
		"memcached", "rabbitmq", "elasticsearch", "meilisearch",
		"minio", "mailhog", "mailpit", "selenium", "phpmyadmin",
		"adminer", "nginx", "traefik", "caddy", "clickhouse",
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

	hasBuild := func(svc composeService) bool {
		return svc.Build != nil
	}

	// Sort service names for deterministic output
	svcNames := make([]string, 0, len(cf.Services))
	for name := range cf.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	for _, name := range svcNames {
		svc := cf.Services[name]
		if name == appName {
			continue // skip the main app
		}

		if isInfra(name, svc) {
			// Infrastructure service → sidecar
			if cfg.Sidecars == nil {
				cfg.Sidecars = make(map[string]NeoSidecar)
			}
			sc := NeoSidecar{
				Image: svc.Image,
			}
			if svc.Environment != nil {
				sc.Env = parseComposeEnvironment(svc.Environment)
			}
			scVolumes := parseComposeVolumeMounts(svc.Volumes)
			if len(scVolumes) > 0 {
				sc.Volumes = scVolumes
			}
			cfg.Sidecars[name] = sc

			fmt.Printf("  %s  %-15s → sidecar (%s)\n", ui.Faint.Render("●"), name, svc.Image)

		} else if hasBuild(svc) {
			// Same build context + custom command → worker
			cmd := parseComposeCommand(svc.Command)
			if cmd != "" {
				if cfg.Workers == nil {
					cfg.Workers = make(map[string]NeoWorker)
				}
				cfg.Workers[name] = NeoWorker{Command: cmd}
				fmt.Printf("  %s  %-15s → worker (%s)\n", ui.Green.Render("●"), name, cmd)
			} else {
				fmt.Printf("  %s  %-15s → skipped (no command, same build as app)\n", ui.Faint.Render("○"), name)
			}
		} else {
			// Unknown service with image → sidecar
			if svc.Image != "" {
				if cfg.Sidecars == nil {
					cfg.Sidecars = make(map[string]NeoSidecar)
				}
				sc := NeoSidecar{Image: svc.Image}
				if svc.Environment != nil {
					sc.Env = parseComposeEnvironment(svc.Environment)
				}
				scVolumes := parseComposeVolumeMounts(svc.Volumes)
				if len(scVolumes) > 0 {
					sc.Volumes = scVolumes
				}
				cfg.Sidecars[name] = sc
				fmt.Printf("  %s  %-15s → sidecar (%s)\n", ui.Faint.Render("●"), name, svc.Image)
			}
		}
	}

	// Rewrite DB_HOST-style env vars to use Neo's container naming
	for k, v := range cfg.Env {
		if strings.HasSuffix(strings.ToUpper(k), "_HOST") || k == "REDIS_HOST" {
			// Check if value matches a sidecar name
			if cfg.Sidecars != nil {
				if _, isSidecar := cfg.Sidecars[v]; isSidecar {
					newVal := fmt.Sprintf("svc-%s-%s", appName, v)
					cfg.Env[k] = newVal
					fmt.Printf("  %s  env.%s: %s → %s\n", ui.Yellow.Render("~"), k, v, newVal)
				}
			}
		}
	}

	fmt.Println()

	// Print summary
	fmt.Printf("  App:      %s (port %d)\n", ui.Bold.Render(appName), cfg.Port)
	if len(cfg.Env) > 0 {
		fmt.Printf("  Env vars: %d\n", len(cfg.Env))
	}
	if len(cfg.Volumes) > 0 {
		fmt.Printf("  Volumes:  %d\n", len(cfg.Volumes))
	}
	if len(cfg.Workers) > 0 {
		fmt.Printf("  Workers:  %d\n", len(cfg.Workers))
	}
	if len(cfg.Sidecars) > 0 {
		fmt.Printf("  Sidecars: %d\n", len(cfg.Sidecars))
	}
	fmt.Println()

	// Write .neo.yml
	if err := saveNeoConfig(".", cfg); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	card := ui.NewCard()
	card.Add(ui.Bold.Render("✓ .neo.yml generated!"))
	card.Blank()
	card.Add("  Next steps:")
	card.Add(fmt.Sprintf("    1. Review %s", ui.Cyan.Render(".neo.yml")))
	card.Add(fmt.Sprintf("    2. Add secrets to %s", ui.Cyan.Render(".env.production")))
	card.Add(fmt.Sprintf("    3. %s", ui.Cyan.Render("neo init root@<your-server-ip>")))
	card.Add(fmt.Sprintf("    4. %s", ui.Cyan.Render("neo deploy .")))
	card.Render()

	return nil
}
