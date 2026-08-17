package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ui"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage .neo.yml configuration",
	}
	cmd.AddCommand(newConfigInitCmd())
	cmd.AddCommand(newConfigGenerateCmd())
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new .neo.yml in the current project",
		Long:  "Creates a .neo.yml for the current project. Prompts for name, domain, port, and HTTPS, then writes a commented template with the remaining sections stubbed for easy extension. Use --yes to accept defaults without prompting.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigInit(yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept defaults without prompting")
	return cmd
}

// runConfigInit scaffolds a commented .neo.yml from a few prompted answers.
func runConfigInit(yes bool) error {
	if _, err := os.Stat(".neo.yml"); err == nil {
		ui.Error(".neo.yml already exists — rename or delete it first")
		return nil
	}

	// Defaults: name from directory, port from Dockerfile EXPOSE.
	cwd, _ := os.Getwd()
	name := sanitizeName(filepath.Base(cwd))
	if name == "" {
		name = "app"
	}
	port := detectPort("Dockerfile")
	if port == 0 {
		port = 8080
	}
	domain := ""
	https := true

	if !yes {
		portStr := strconv.Itoa(port)
		_ = huh.NewInput().Title("App name").Value(&name).Run()
		_ = huh.NewInput().Title("Domain (optional)").Placeholder("app.example.com").Value(&domain).Run()
		_ = huh.NewInput().Title("Container port").Value(&portStr).Run()
		if p, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil && p > 0 {
			port = p
		}
		_ = huh.NewConfirm().Title("Enable HTTPS?").Value(&https).Run()
	}

	name = sanitizeName(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("app name is required")
	}
	domain = strings.TrimSpace(domain)

	if err := os.WriteFile(".neo.yml", []byte(neoConfigTemplate(name, domain, port, https)), 0o644); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	card := ui.NewCard()
	card.Add(ui.Bold.Render("✓ .neo.yml created!"))
	card.Blank()
	card.Add("  Next steps:")
	card.Add(fmt.Sprintf("    1. Review %s and uncomment sections you need", ui.Cyan.Render(".neo.yml")))
	card.Add(fmt.Sprintf("    2. %s", ui.Cyan.Render("neo init root@<your-server-ip>")))
	card.Add(fmt.Sprintf("    3. %s", ui.Cyan.Render("neo deploy .")))
	card.Render()
	return nil
}

// neoConfigTemplate builds a commented .neo.yml with the answered fields set and
// the remaining sections stubbed as examples.
func neoConfigTemplate(name, domain string, port int, https bool) string {
	domainLine := "# domain: app.example.com          # set a domain (or run: neo domain " + name + " --temp)"
	if domain != "" {
		domainLine = "domain: " + domain
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# .neo.yml — Neo project config. Docs: https://neo.vxero.dev/docs\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "%s\n", domainLine)
	fmt.Fprintf(&b, "port: %d                           # container port (Dockerfile EXPOSE)\n", port)
	fmt.Fprintf(&b, "https: %t\n", https)
	fmt.Fprintf(&b, "# restart: unless-stopped          # Docker restart policy\n")
	b.WriteString(`
# env:                              # non-sensitive env var defaults
#   APP_ENV: production
#   LOG_LEVEL: info

# env_file: .env.production         # load env vars from a file

# volumes:                          # persistent data
#   uploads: /app/uploads           # named volume
#   logs: /var/log/app:/var/log/app # host:container bind mount

# workers:                          # background containers (share app image)
#   queue:
#     command: "node worker.js"
#     restart: always

# sidecars:                         # extra containers on the same network
#   redis:
#     image: redis:7-alpine

# health:                           # container health check
#   cmd: "curl -f http://localhost:PORT/health"
#   interval: 30s
#   retries: 3

# hooks:                            # local lifecycle commands
#   pre_build:
#     - npm run build
#   post_deploy:
#     - echo "deployed"

# environments:                     # per-environment overrides
#   staging:
#     domain: staging.example.com
#     env:
#       APP_ENV: staging
#   production:
#     domain: app.example.com
`)
	return strings.Replace(b.String(), "http://localhost:PORT/health", fmt.Sprintf("http://localhost:%d/health", port), 1)
}

// sidecarFromCompose builds a NeoSidecar from a compose service, carrying over
// the image, env, named volumes, and command/entrypoint override. Command is
// captured because services like Keycloak or WireMock are useless without it.
func sidecarFromCompose(svc composeService) NeoSidecar {
	sc := NeoSidecar{Image: svc.Image}
	if svc.Environment != nil {
		sc.Env = parseComposeEnvironment(svc.Environment)
	}
	if v := parseComposeVolumeMounts(svc.Volumes); len(v) > 0 {
		sc.Volumes = v
	}
	if cmd := parseComposeCommand(svc.Command); cmd != "" {
		sc.Command = cmd
	}
	return sc
}

// sharesAppArtifact reports whether a service runs the same image (or is built
// from the same context) as the app. Those services are workers or sibling
// sites, not independent infrastructure.
func sharesAppArtifact(app, svc composeService) bool {
	if app.Image != "" && svc.Image == app.Image {
		return true
	}
	if app.Build != nil && svc.Build != nil {
		return fmt.Sprintf("%v", app.Build) == fmt.Sprintf("%v", svc.Build)
	}
	return false
}

// conflictingEnvKeys lists variables a service sets to a different value than
// the app. Neo workers inherit the app's environment with no per-worker
// override, so any conflict means the service cannot be modelled as a worker
// without silently changing its behaviour — a queue bound to DB_QUEUE=study
// would start draining the nomination queue instead.
func conflictingEnvKeys(appEnv, svcEnv map[string]string) []string {
	var keys []string
	for k, v := range svcEnv {
		if appVal, ok := appEnv[k]; ok && appVal != v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// warnSidecarBinds prints a warning for any bind mounts a service declares, since
// generate migrates only named volumes — bind mounts reference host paths.
func warnSidecarBinds(name string, svc composeService) {
	if binds := composeBindMounts(svc.Volumes); len(binds) > 0 {
		fmt.Printf("  %s  %-15s bind mounts not migrated: %s\n",
			ui.Yellow.Render("⚠"), name, strings.Join(binds, ", "))
	}
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

	appEnv := composeServiceEnv(appSvc)

	cfg := &NeoConfig{
		Name: appName,
		Port: parseComposePort(appSvc.Ports),
		Env:  make(map[string]string),
	}

	// nginx-proxy style: no published ports, the proxy routes on VIRTUAL_HOST
	// and forwards to VIRTUAL_PORT. Reading those saves the user re-entering
	// values that are sitting right there in the file.
	if cfg.Port == 0 {
		if vp := appEnv["VIRTUAL_PORT"]; vp != "" {
			fmt.Sscanf(vp, "%d", &cfg.Port)
			if cfg.Port > 0 {
				fmt.Printf("  %s  port: %d (from VIRTUAL_PORT)\n", ui.Faint.Render("●"), cfg.Port)
			}
		}
	}
	if vh := appEnv["VIRTUAL_HOST"]; vh != "" {
		cfg.Domain = strings.Split(vh, ",")[0]
		fmt.Printf("  %s  domain: %s (from VIRTUAL_HOST)\n", ui.Faint.Render("●"), cfg.Domain)
	}

	// Record a custom Dockerfile (e.g. build.dockerfile: Dockerfile.local) so
	// deploy doesn't fall back to ./Dockerfile.
	if df := composeBuildDockerfile(appSvc.Build); df != "" {
		cfg.Dockerfile = df
		fmt.Printf("  %s  dockerfile: %s\n", ui.Faint.Render("●"), df)
	}

	// Neo builds the Dockerfile's final stage with no build args, so a service
	// pinning an earlier stage would deploy something other than what compose
	// runs — `target: development` shipped to production being the bad case.
	if target := composeBuildTarget(appSvc.Build); target != "" {
		fmt.Printf("  %s  build target %q not migrated — neo builds the final stage\n", ui.Yellow.Render("⚠"), target)
	}
	if args := composeBuildArgs(appSvc.Build); len(args) > 0 {
		fmt.Printf("  %s  build args not migrated: %s\n", ui.Yellow.Render("⚠"), strings.Join(args, ", "))
	}

	// Carry over the app service's command:. It was previously read for sidecars
	// only, so a compose project whose app overrode its image CMD lost that on
	// migration and deployed the wrong process.
	if cmd := composeFullCommand(appSvc); cmd != "" {
		cfg.Command = CommandString(cmd)
		fmt.Printf("  %s  command: %s\n", ui.Faint.Render("●"), cmd)
	}

	// Extract app env vars
	if appSvc.Environment != nil {
		cfg.Env = parseComposeEnvironment(appSvc.Environment)
	}

	// Extract app env_file — .neo.yml holds a single env_file, so keep the first
	// and warn when the compose service references more.
	envFiles := parseComposeEnvFile(appSvc.EnvFile)
	if len(envFiles) > 0 {
		cfg.EnvFile = envFiles[0]
		if len(envFiles) > 1 {
			fmt.Printf("  %s  %d env_file entries — only %s recorded; add the rest to .neo.yml manually\n",
				ui.Yellow.Render("~"), len(envFiles), envFiles[0])
		}
	}

	// Extract app volumes (named volumes only; bind mounts can't be migrated).
	appVolumes := parseComposeVolumeMounts(appSvc.Volumes)
	if len(appVolumes) > 0 {
		cfg.Volumes = make(map[string]NeoVolume)
		for name, path := range appVolumes {
			cfg.Volumes[name] = NeoVolume{Path: path}
		}
	}
	if binds := composeBindMounts(appSvc.Volumes); len(binds) > 0 {
		fmt.Printf("  %s  %-15s bind mounts not migrated: %s\n",
			ui.Yellow.Render("⚠"), appName, strings.Join(binds, ", "))
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

		if isOneShotService(svc) {
			// restart: "no" with a command is compose's idiom for a step that
			// runs once and exits. As a worker it would loop forever; as a
			// sidecar it would exit and look broken. Point at where the work
			// belongs in Neo instead.
			cmd := composeFullCommand(svc)
			hint := "hooks.pre_build (runs on your machine before the build)"
			if looksLikeMigrationCommand(cmd) {
				hint = "release: (runs in the new container before traffic switches)"
			}
			fmt.Printf("  %s  %-15s → skipped: runs once and exits — move it to %s\n", ui.Yellow.Render("⚠"), name, hint)
			continue
		}

		if isInfra(name, svc) {
			// Infrastructure service → sidecar
			if cfg.Sidecars == nil {
				cfg.Sidecars = make(map[string]NeoSidecar)
			}
			cfg.Sidecars[name] = sidecarFromCompose(svc)
			fmt.Printf("  %s  %-15s → sidecar (%s)\n", ui.Faint.Render("●"), name, svc.Image)
			warnSidecarBinds(name, svc)

		} else if sharesAppArtifact(appSvc, svc) {
			// Same image or build context as the app: a worker, a scheduler, or
			// a second public site sharing one codebase.
			cmd := composeFullCommand(svc)
			svcEnv := composeServiceEnv(svc)
			conflicts := conflictingEnvKeys(appEnv, svcEnv)

			switch {
			case cmd == "":
				// No command means it runs the same web server as the app. With
				// its own VIRTUAL_HOST it is a separate site, which is a separate
				// .neo.yml — emitting it as a sidecar would run a second copy
				// with no route to it.
				if vh := svcEnv["VIRTUAL_HOST"]; vh != "" {
					fmt.Printf("  %s  %-15s → skipped: separate public app (%s)\n", ui.Yellow.Render("⚠"), name, vh)
				} else {
					fmt.Printf("  %s  %-15s → skipped (same image as app, no command)\n", ui.Faint.Render("○"), name)
				}

			case len(conflicts) > 0:
				// Workers inherit the app's env verbatim, so a service that needs
				// different values can't be one. Keep it as a sidecar, which has
				// its own env, and say why.
				if cfg.Sidecars == nil {
					cfg.Sidecars = make(map[string]NeoSidecar)
				}
				cfg.Sidecars[name] = sidecarFromCompose(svc)
				fmt.Printf("  %s  %-15s → sidecar, not worker: needs its own %s\n",
					ui.Yellow.Render("⚠"), name, strings.Join(conflicts, ", "))
				warnSidecarBinds(name, svc)

			default:
				if cfg.Workers == nil {
					cfg.Workers = make(map[string]NeoWorker)
				}
				cfg.Workers[name] = NeoWorker{Command: cmd}
				fmt.Printf("  %s  %-15s → worker (%s)\n", ui.Green.Render("●"), name, cmd)
			}
		} else {
			// Unknown service with image → sidecar
			if svc.Image != "" {
				if cfg.Sidecars == nil {
					cfg.Sidecars = make(map[string]NeoSidecar)
				}
				cfg.Sidecars[name] = sidecarFromCompose(svc)
				fmt.Printf("  %s  %-15s → sidecar (%s)\n", ui.Faint.Render("●"), name, svc.Image)
				warnSidecarBinds(name, svc)
			}
		}
	}

	// A compose file with no build: anywhere describes prebuilt images. Neo
	// deploy builds from a Dockerfile, so the generated config cannot be
	// deployed as-is — better to say so now than to fail at deploy time with
	// "No Dockerfile found".
	anyBuild := false
	for _, svc := range cf.Services {
		if svc.Build != nil {
			anyBuild = true
			break
		}
	}
	if !anyBuild {
		fmt.Println()
		ui.Error("No service in this compose file has a build: — every service uses a prebuilt image.")
		ui.Info("neo deploy builds from a Dockerfile. Add one to this project (and a build: to the app service), or deploy the image another way.")
	}

	// Neo routes one public app per .neo.yml. Several public services means the
	// file describes several sites that each need their own project.
	if public := composePublicServices(cf.Services); len(public) > 1 {
		fmt.Println()
		ui.Error(fmt.Sprintf("%d services look publicly reachable: %s", len(public), strings.Join(public, ", ")))
		ui.Info(fmt.Sprintf("Neo deploys one public app per .neo.yml — %s was chosen. Give each of the others its own project directory and .neo.yml.", appName))
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
