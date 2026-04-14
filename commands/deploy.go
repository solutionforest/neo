package commands

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// deployFlags holds the CLI flag values for the deploy command.
type deployFlags struct {
	domain     string
	tempDomain bool // assign a temporary {app}.{ip}.sslip.io domain with auto-SSL
	noDomain   bool // skip domain assignment entirely (internal services)
	port       int
	appName    string
	dockerfile string
	envPairs   []string
	envFile    string
	target     string
	envOnly    bool // skip rebuild, just restart with updated env
	all        bool // build once, deploy to all .neo.yml environments in parallel
	parallel   int  // max concurrent deploys for --all (default 3)
}

func newDeployCmd() *cobra.Command {
	var flags deployFlags

	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy a local project to the server",
		Long:  "Builds and deploys a Dockerfile-based project. Auto-detects whether to build locally or on the server.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return runDeploy(path, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.domain, "domain", "d", "", "domain name for the app")
	cmd.Flags().BoolVar(&flags.tempDomain, "temp", false, "assign a temporary {app}.{ip}.sslip.io domain with auto-SSL")
	cmd.Flags().BoolVar(&flags.noDomain, "no-domain", false, "skip domain assignment (for internal services)")
	cmd.Flags().IntVarP(&flags.port, "port", "p", 0, "container port to expose (auto-detected from Dockerfile)")
	cmd.Flags().StringVarP(&flags.appName, "name", "n", "", "app name (defaults to directory name)")
	cmd.Flags().StringVarP(&flags.dockerfile, "dockerfile", "f", "", "path to Dockerfile (default: Dockerfile)")
	cmd.Flags().StringArrayVarP(&flags.envPairs, "env", "e", nil, "environment variable (KEY=VALUE, repeatable)")
	cmd.Flags().StringVar(&flags.envFile, "env-file", "", "path to .env file to load")
	cmd.Flags().StringVar(&flags.target, "to", "", "named environment from .neo.yml (e.g. staging, production)")
	cmd.Flags().BoolVar(&flags.envOnly, "env-only", false, "restart with updated env/config only — skip rebuild and image transfer")
	cmd.Flags().BoolVar(&flags.all, "all", false, "build once and deploy to all environments in .neo.yml simultaneously")
	cmd.Flags().IntVar(&flags.parallel, "parallel", 3, "max concurrent deploys for --all (lower for small servers)")

	return cmd
}

func runDeploy(projectPath string, flags deployFlags) error {
	domain := flags.domain
	port := flags.port
	appName := flags.appName
	dockerfile := flags.dockerfile
	envFile := flags.envFile
	target := flags.target

	// Resolve absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Validate project directory exists
	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		ui.Error(fmt.Sprintf("Directory not found: %s", absPath))
		return nil
	}

	// Find Dockerfile
	if dockerfile == "" {
		dockerfile = filepath.Join(absPath, "Dockerfile")
	} else if !filepath.IsAbs(dockerfile) {
		dockerfile = filepath.Join(absPath, dockerfile)
	}
	if _, err := os.Stat(dockerfile); err != nil {
		ui.Error("No Dockerfile found. Create one or specify with --dockerfile.")
		return nil
	}

	// Load .neo.yml for defaults (parsed early for name/port/domain)
	neoConfig, _ := loadNeoConfig(absPath)

	// When environments: is defined, root-level server:/domains: are ignored.
	// Warn the user so they know to move them into each environment.
	if neoConfig != nil && len(neoConfig.Environments) > 0 {
		if neoConfig.Server != "" {
			fmt.Println()
			ui.Error("root-level server: is ignored when environments: are defined")
			ui.Info("Move server: into each environment block:")
			fmt.Printf("\n    environments:\n      production:\n        server: %s\n\n", neoConfig.Server)
		}
		if neoConfig.PrimaryDomain() != "" {
			ui.Error("root-level domain:/domains: is ignored when environments: are defined")
			ui.Info("Move domains: into each environment block:")
			fmt.Printf("\n    environments:\n      production:\n        domains:\n          - %s\n\n", neoConfig.PrimaryDomain())
		}
		if neoConfig.Server != "" || neoConfig.PrimaryDomain() != "" {
			return fmt.Errorf("update .neo.yml to move server/domains into each environment")
		}
	}

	// Validate that every environment has an explicit server.
	if neoConfig != nil && len(neoConfig.Environments) > 0 {
		var missing []string
		for envName, envCfg := range neoConfig.Environments {
			if len(envCfg.EffectiveServers()) == 0 {
				missing = append(missing, envName)
			}
		}
		if len(missing) > 0 {
			for _, envName := range missing {
				ui.Error(fmt.Sprintf("environment %q has no server: defined in .neo.yml", envName))
				ui.Info(fmt.Sprintf("Add to the %q environment:", envName))
				fmt.Printf("\n    environments:\n      %s:\n        server: your-server-name\n\n", envName)
			}
			return fmt.Errorf("every environment must specify a server:")
		}
	}

	// --all: build image once, then transfer to every environment in parallel
	if flags.all {
		if neoConfig == nil || len(neoConfig.Environments) == 0 {
			return fmt.Errorf("--all requires environments defined in .neo.yml")
		}
		if !isLocalDockerAvailable() {
			return fmt.Errorf("--all requires local Docker (not found on this machine)")
		}
		return runDeployAll(absPath, dockerfile, flags, neoConfig)
	}

	// Resolve named environment from .neo.yml (--to flag or prompt if multiple exist)
	var resolvedEnv string // hoisted so app name derivation can read it
	if neoConfig != nil && len(neoConfig.Environments) > 0 {
		resolvedEnv = target
		if resolvedEnv == "" {
			if len(neoConfig.Environments) == 1 {
				// Only one env — use it automatically
				for k := range neoConfig.Environments {
					resolvedEnv = k
				}
			} else {
				// Multiple envs — prompt user to select
				var opts []ui.SelectOption
				for k := range neoConfig.Environments {
					opts = append(opts, ui.SelectOption{k, k})
				}
				resolvedEnv = ui.Select("Deploy to which environment?", opts)
				if resolvedEnv == "" {
					return nil
				}
			}
		}
		envName := resolvedEnv
		// Merge environment config (overrides top-level neoConfig)
		if env, ok := neoConfig.Environments[envName]; ok {
			fmt.Printf("  Environment: %s\n", ui.Bold.Render(envName))
			if env.Name != "" && flags.appName == "" {
				flags.appName = sanitizeName(env.Name) // env name: overrides top-level name + suffix logic
			}
			if env.Server != "" && serverFlag == "" {
				serverFlag = env.Server
			}
			if env.Domain != "" && domain == "" {
				domain = env.Domain
			}
			if env.Port > 0 && port == 0 {
				port = env.Port
			}
			// Merge env vars (environment overrides top-level neoConfig.Env)
			if neoConfig.Env == nil {
				neoConfig.Env = make(map[string]string)
			}
			for k, v := range env.Env {
				neoConfig.Env[k] = v
			}
			if env.EnvFile != "" && neoConfig.EnvFile == "" {
				neoConfig.EnvFile = env.EnvFile
			}
			// Environment SSL overrides top-level
			if env.SSL != nil {
				neoConfig.SSL = env.SSL
			}
			// Merge environment volumes into top-level (environment-specific volumes
			// override top-level volumes with the same key)
			if len(env.Volumes) > 0 {
				if neoConfig.Volumes == nil {
					neoConfig.Volumes = make(map[string]NeoVolume)
				}
				for k, v := range env.Volumes {
					neoConfig.Volumes[k] = v
				}
			}
			// Merge environment workers (full replace if any defined)
			if len(env.Workers) > 0 {
				neoConfig.Workers = env.Workers
			}
			// Merge environment sidecars (full replace if any defined)
			if len(env.Sidecars) > 0 {
				neoConfig.Sidecars = env.Sidecars
			}
			// Environment restart/health override top-level
			if env.Restart != "" {
				neoConfig.Restart = env.Restart
			}
			if env.Health != nil {
				neoConfig.Health = env.Health
			}
			// Environment hooks fully replace top-level hooks
			if env.Hooks != nil {
				neoConfig.Hooks = env.Hooks
			}
			// Environment basic_auth overrides top-level
			if env.BasicAuth != nil {
				neoConfig.BasicAuth = env.BasicAuth
			}
			// Environment scale overrides top-level
			if env.Scale > 0 {
				neoConfig.Scale = env.Scale
			}
		}
	}

	// Derive app name: flag > .neo.yml > directory name
	if appName == "" {
		if neoConfig != nil && neoConfig.Name != "" {
			appName = sanitizeName(neoConfig.Name)
		} else {
			appName = sanitizeName(filepath.Base(absPath))
		}
		// Append environment suffix for non-production environments so that
		// staging/preview deployments don't collide with the production container.
		// "production", "prod", "main", and "default" are treated as primary — no suffix.
		if resolvedEnv != "" && !isProductionEnv(resolvedEnv) {
			appName = appName + "-" + sanitizeName(resolvedEnv)
		}
	}

	// Auto-detect port: flag > .neo.yml > Dockerfile EXPOSE
	if port == 0 {
		if neoConfig != nil && neoConfig.Port > 0 {
			port = neoConfig.Port
		} else {
			port = detectPort(dockerfile)
		}
	}

	// Connect to server — environment server > .neo.yml server > --server flag > global current
	if serverFlag == "" && neoConfig != nil && neoConfig.Server != "" {
		serverFlag = neoConfig.Server
	}
	cfg, srv, sshExec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer sshExec.Close()
	licenseKey := cfg.LicenseKey

	// Load state
	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Pre-flight: block deploy if server is critically low on memory.
	// Deploying to an OOM server crashes running containers and can make
	// the server unreachable over SSH.
	if err := checkServerMemory(sshExec, 150); err != nil {
		return err
	}

	// Check if this is a redeploy
	existing, isRedeploy := st.Apps[appName]

	// --temp: auto-assign {app}.{ip}.sslip.io (overrides --domain and skips prompt)
	if flags.tempDomain {
		if st.ServerIP == "" {
			return fmt.Errorf("server IP not found in state — run 'neo init' to re-initialize")
		}
		domain = appName + "." + st.ServerIP + ".sslip.io"
		ui.Info(fmt.Sprintf("Using temporary domain: %s", domain))
	}

	// .neo.yml domain: none → treat as --no-domain
	if neoConfig != nil && neoConfig.Domain == "none" {
		flags.noDomain = true
	}

	// Resolve domain: --no-domain > flag > redeploy state > .neo.yml > prompt
	if flags.noDomain {
		domain = ""
	} else if domain == "" {
		if isRedeploy && existing.Domain != "" {
			domain = existing.Domain
		} else if neoConfig != nil && neoConfig.PrimaryDomain() != "" {
			domain = neoConfig.PrimaryDomain()
		} else {
			err := huh.NewInput().
				Title("Domain for " + appName).
				Placeholder("app.example.com (leave empty to skip)").
				Value(&domain).
				Run()
			if err != nil {
				return err
			}
		}
	}

	// Validate domain before proceeding
	if domain != "" {
		if err := validateDomain(domain); err != nil {
			return err
		}
	}

	// Prompt for port if still unknown
	if port == 0 {
		portStr := "8080"
		err := huh.NewInput().
			Title("Which port does your app listen on?").
			Placeholder("8080").
			Value(&portStr).
			Run()
		if err != nil {
			return err
		}
		fmt.Sscanf(portStr, "%d", &port)
		if port == 0 {
			port = 8080
		}
	}

	// Build env vars with priority: CLI --env > --env-file > .neo.yml > docker-compose.yml > server state
	env := make(map[string]string)

	// 1. Start with server state on redeploy
	if isRedeploy {
		for k, v := range existing.Env {
			env[k] = v
		}
	}

	// 2. Load from docker-compose.yml (auto-detected or via .neo.yml compose_service)
	composeService := ""
	if neoConfig != nil {
		composeService = neoConfig.ComposeService
	}
	if composePath := findComposeFile(absPath); composePath != "" {
		if result, err := parseComposeFile(composePath, composeService); err == nil {
			for k, v := range result.Env {
				env[k] = v
			}
			// Use compose port if not set elsewhere
			if port == 0 && result.Port > 0 {
				port = result.Port
			}
			ui.Info(fmt.Sprintf("Loaded %d env vars from %s (service: %s)", len(result.Env), filepath.Base(composePath), result.ServiceName))
		}
	}

	// 3. Load .neo.yml env_file and env if present (overrides compose)
	if neoConfig != nil {
		if neoConfig.EnvFile != "" {
			envFilePath := neoConfig.EnvFile
			if !filepath.IsAbs(envFilePath) {
				envFilePath = filepath.Join(absPath, envFilePath)
			}
			if fileEnv, err := parseEnvFile(envFilePath); err == nil {
				for k, v := range fileEnv {
					env[k] = v
				}
			}
		}
		for k, v := range neoConfig.Env {
			env[k] = v
		}
	}

	// 5. Load --env-file flag (overrides .neo.yml)
	if envFile != "" {
		envFilePath := envFile
		if !filepath.IsAbs(envFilePath) {
			envFilePath = filepath.Join(absPath, envFilePath)
		}
		fileEnv, err := parseEnvFile(envFilePath)
		if err != nil {
			return fmt.Errorf("load env file: %w", err)
		}
		for k, v := range fileEnv {
			env[k] = v
		}
	}

	// 6. Apply --env flags (highest priority)
	if len(flags.envPairs) > 0 {
		flagEnv, err := parseEnvPairs(flags.envPairs)
		if err != nil {
			return err
		}
		for k, v := range flagEnv {
			env[k] = v
		}
	}

	// Auto-assign a temporary sslip.io domain when no domain set (first deploy only).
	// sslip.io resolves the IP from the hostname and supports Let's Encrypt auto-SSL.
	if domain == "" && !isRedeploy && !flags.noDomain {
		if st.ServerIP != "" {
			domain = appName + "." + st.ServerIP + ".sslip.io"
			ui.Info(fmt.Sprintf("No domain set — using temporary domain: %s", domain))
			ui.Info("Set a real domain when ready: neo domain " + appName + " yourdomain.com")
		}
	}

	// Auto-set APP_URL from domain if not already explicitly set
	if domain != "" {
		if _, ok := env["APP_URL"]; !ok {
			// Scheme: explicit .neo.yml https: flag wins; else redeploy inherits state; first deploy defaults to http
			httpsEnabled := false
			if neoConfig != nil && neoConfig.HTTPS != nil {
				httpsEnabled = *neoConfig.HTTPS
			} else if isRedeploy && !existing.HTTPOnly {
				httpsEnabled = true
			}
			scheme := "http"
			if httpsEnabled {
				scheme = "https"
			}
			env["APP_URL"] = scheme + "://" + domain
		}
	}

	fmt.Println()
	if isRedeploy {
		fmt.Printf("  Redeploying %s\n", ui.Bold.Render(appName))
	} else {
		fmt.Printf("  Deploying %s\n", ui.Bold.Render(appName))
	}
	fmt.Printf("  Server: %s (%s)\n", srv.Name, srv.Host)
	if domain != "" {
		fmt.Printf("  Domain: %s\n", domain)
	}
	fmt.Printf("  Port:   %d\n", port)
	if len(env) > 0 {
		fmt.Printf("  Env:    %d variables\n", len(env))
	}
	fmt.Println()

	docker := remote.NewDocker(sshExec)
	caddy := remote.NewCaddy(sshExec)
	containerName := config.AppContainer(appName)

	// -- env-only: restart existing container with updated env, no rebuild --
	if flags.envOnly {
		if !isRedeploy {
			return fmt.Errorf("--env-only requires an existing deployed app; %q has not been deployed yet", appName)
		}

		fmt.Println()
		fmt.Printf("  Restarting %s with updated env (no rebuild)\n", ui.Bold.Render(appName))
		fmt.Printf("  Image: %s\n", ui.Faint.Render(existing.Image))
		fmt.Println()

		// Rebuild volumes list from state
		volumes := volumesFromState(existing.Volumes)

		spin := ui.NewSpinner("Stopping old container...")
		spin.Start()
		docker.Remove(containerName)
		spin.Stop()

		spin = ui.NewSpinner("Starting container with new env...")
		spin.Start()
		envOnlyOpts := remote.RunOpts{
			Name:    containerName,
			Image:   existing.Image,
			Network: config.DockerNetwork,
			Restart: restartPolicy(existing.Restart),
			Volumes: volumes,
			Env:     env,
		}
		applyHealth(&envOnlyOpts, existing.Health)
		_, startErr := docker.Run(envOnlyOpts)
		spin.Stop()
		if startErr != nil {
			return fmt.Errorf("restart container: %w", startErr)
		}

		spin = ui.NewSpinner("Waiting for health check...")
		spin.Start()
		healthy := waitForHealthy(docker, containerName, 0, 30*time.Second)
		spin.Stop()
		if !healthy {
			ui.Error("Container failed health check after env update")
			return nil
		}

		// Persist updated env in state
		existing.Env = env
		st.Apps[appName] = existing
		state.Save(sshExec, st)

		ui.Success(fmt.Sprintf("%s restarted with %d env variable(s)", appName, len(env)))
		if domain != "" {
			scheme := "https"
			if existing.HTTPOnly {
				scheme = "http"
			}
			fmt.Printf("  %s\n", scheme+"://"+domain)
		}
		return nil
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	imageTag := fmt.Sprintf("neo-%s:%s", appName, timestamp)

	// Detect build strategy: local Docker or remote build
	localDocker := isLocalDockerAvailable()

	// Detect server arch for cross-platform local builds (cached in state to skip repeated SSH round trip)
	serverPlatform := "linux/amd64"
	if localDocker {
		arch := st.ServerArch
		if arch == "" {
			if detected, err2 := sshExec.Run("uname -m"); err2 == nil {
				arch = strings.TrimSpace(detected)
				st.ServerArch = arch
			}
		}
		if arch == "aarch64" || arch == "arm64" {
			serverPlatform = "linux/arm64"
		}
	}

	// Run pre-build hook locally before Docker build
	if neoConfig != nil && neoConfig.Hooks != nil {
		hEnv := hookEnvVars(appName, resolvedEnv, domain, srv.Host)
		if err := runHook("pre_build", neoConfig.Hooks.PreBuild, absPath, hEnv); err != nil {
			return err
		}
	}

	if localDocker {
		err = deployViaLocalBuild(absPath, dockerfile, imageTag, serverPlatform, sshExec, licenseKey)
	} else {
		err = deployViaRemoteBuild(absPath, dockerfile, imageTag, sshExec, docker)
	}
	if err != nil {
		return err
	}

	// Build volumes list: .neo.yml declarations on first deploy, state on redeploy
	var existingApp *state.App
	if isRedeploy {
		existingApp = &existing
	}
	volumes, declaredVolumes := buildDeployVolumes(appName, neoConfig, existingApp)

	// Resolve restart policy and health check from .neo.yml
	appRestart := ""
	var appHealth *state.HealthCheck
	if neoConfig != nil {
		appRestart = neoConfig.Restart
		appHealth = neoHealthToState(neoConfig.Health)
	}

	// spin is declared here so it remains in scope for the workers/sidecars sections below.
	var spin *ui.Spinner

	// Resolve desired replica count from .neo.yml (default: 1 = single-container mode)
	scale := 1
	if neoConfig != nil && neoConfig.Scale > 1 {
		scale = neoConfig.Scale
	}

	// Determine HTTP mode (independent of health check result; used by both scale paths
	// and by the state save below).
	httpOnly := true
	if isRedeploy {
		httpOnly = existing.HTTPOnly
	}
	if neoConfig != nil && neoConfig.HTTPS != nil {
		httpOnly = !*neoConfig.HTTPS
	}

	// Build the full domain list for Caddy: primary + config extras + state extras (redeploy).
	deployDomains := func() []string {
		if domain == "" {
			return nil
		}
		seen := map[string]bool{domain: true}
		result := []string{domain}
		if neoConfig != nil {
			for _, d := range neoConfig.ExtraConfigDomains() {
				if !seen[d] {
					seen[d] = true
					result = append(result, d)
				}
			}
		}
		if isRedeploy {
			for _, d := range existing.ExtraDomains {
				if !seen[d] {
					seen[d] = true
					result = append(result, d)
				}
			}
		}
		return result
	}()

	// addCaddyRoute replaces (or creates) the single-upstream Caddy route for this app.
	addCaddyRoute := func(cName string, domains []string, p int) {
		if len(domains) == 0 {
			return
		}
		upstream := fmt.Sprintf("%s:%d", cName, p)
		authOpts := neoBasicAuthToRouteOpts(neoConfig)
		if httpOnly {
			caddy.UpdateRouteHTTP(cName, domains, upstream, authOpts...)
		} else {
			caddy.UpdateRoute(cName, domains, upstream, authOpts...)
		}
	}

	if scale > 1 {
		// ── Multi-replica (scaled) deploy ─────────────────────────────────────────
		replicaNames := make([]string, scale)
		nextNames := make([]string, scale)
		for i := 0; i < scale; i++ {
			replicaNames[i] = config.ReplicaContainer(appName, i)
			nextNames[i] = replicaNames[i] + "-next"
		}

		// Clean up any leftover -next containers from a previous failed deploy
		for _, n := range nextNames {
			docker.Remove(n)
		}

		// Start all replicas as staging (-next) containers
		var startedNext []string
		for i := range replicaNames {
			nextN := nextNames[i]
			spin := ui.NewSpinner(fmt.Sprintf("Starting replica %d/%d...", i+1, scale))
			spin.Start()
			rOpts := remote.RunOpts{
				Name:    nextN,
				Image:   imageTag,
				Network: config.DockerNetwork,
				Restart: restartPolicy(appRestart),
				Volumes: volumes,
				Env:     env,
			}
			applyHealth(&rOpts, appHealth)
			_, rErr := docker.Run(rOpts)
			spin.Stop()
			if rErr != nil {
				ui.Error(fmt.Sprintf("Replica %d failed to start: %s — rolling back", i+1, rErr))
				for _, n := range startedNext {
					docker.Remove(n)
				}
				return fmt.Errorf("start replica %d: %w", i, rErr)
			}
			startedNext = append(startedNext, nextN)
		}

		// Health-check all replicas
		for i, nextN := range nextNames {
			spin := ui.NewSpinner(fmt.Sprintf("Waiting for replica %d/%d to be ready...", i+1, scale))
			spin.Start()
			ok := waitForHealthy(docker, nextN, port, 120*time.Second)
			spin.Stop()
			if !ok {
				ui.Error(fmt.Sprintf("Replica %d failed health check — rolling back all", i+1))
				for _, n := range nextNames {
					docker.Remove(n)
				}
				if isRedeploy {
					ui.Info(fmt.Sprintf("Previous version still running. Debug with: neo logs %s", appName))
				}
				return nil
			}
		}
		ui.Success(fmt.Sprintf("All %d replicas healthy", scale))

		authOpts := neoBasicAuthToRouteOpts(neoConfig)

		// For redeploy: switch Caddy to -next replicas before removing old containers
		nextUpstreams := make([]string, scale)
		for i, n := range nextNames {
			nextUpstreams[i] = fmt.Sprintf("%s:%d", n, port)
		}
		if isRedeploy && len(deployDomains) > 0 {
			if httpOnly {
				caddy.UpdateRouteMultiHTTP(containerName, deployDomains, nextUpstreams, authOpts...)
			} else {
				caddy.UpdateRouteMulti(containerName, deployDomains, nextUpstreams, authOpts...)
			}
		}

		// Remove old containers (single-container or previous replica set)
		if isRedeploy {
			spin := ui.NewSpinner("Removing old containers...")
			spin.Start()
			oldScale := existing.Scale
			if oldScale <= 1 {
				docker.Remove(containerName)
			} else {
				for i := 0; i < oldScale; i++ {
					docker.Remove(config.ReplicaContainer(appName, i))
				}
			}
			spin.Stop()
		}

		// Rename -next to canonical replica names
		for i := range replicaNames {
			docker.Rename(nextNames[i], replicaNames[i])
		}

		// Point Caddy at canonical replica names
		canonicalUpstreams := make([]string, scale)
		for i, r := range replicaNames {
			canonicalUpstreams[i] = fmt.Sprintf("%s:%d", r, port)
		}
		if len(deployDomains) > 0 {
			if isRedeploy {
				caddy.PatchUpstreams(containerName, canonicalUpstreams, deployDomains, httpOnly, authOpts...)
			} else {
				// First deploy: add the Caddy route
				if neoConfig != nil && neoConfig.SSL != nil && neoConfig.SSL.Certificate != "" && neoConfig.SSL.PrivateKey != "" {
					certPath := neoConfig.SSL.Certificate
					keyPath := neoConfig.SSL.PrivateKey
					if !filepath.IsAbs(certPath) {
						certPath = filepath.Join(absPath, certPath)
					}
					if !filepath.IsAbs(keyPath) {
						keyPath = filepath.Join(absPath, keyPath)
					}
					if err := runDomainCustomCert(appName, domain, certPath, keyPath); err != nil {
						ui.Error(fmt.Sprintf("Custom SSL setup failed: %s — falling back to HTTP", err))
						caddy.AddRouteMultiHTTP(containerName, deployDomains, canonicalUpstreams, authOpts...)
					}
					httpOnly = false
				} else if neoConfig != nil && neoConfig.HTTPS != nil && *neoConfig.HTTPS {
					if err := caddy.AddRouteMulti(containerName, deployDomains, canonicalUpstreams, authOpts...); err != nil {
						ui.Error(fmt.Sprintf("Failed to add Caddy HTTPS route: %s", err))
					}
					httpOnly = false
				} else {
					if err := caddy.AddRouteMultiHTTP(containerName, deployDomains, canonicalUpstreams, authOpts...); err != nil {
						ui.Error(fmt.Sprintf("Failed to add Caddy route: %s", err))
					}
				}
			}
			if isRedeploy {
				ui.Success(fmt.Sprintf("Traffic balanced across %d replicas (%s)", scale, domain))
			} else {
				ui.Success(fmt.Sprintf("%d replicas serving (%s)", scale, domain))
			}
		} else {
			if isRedeploy {
				ui.Success(fmt.Sprintf("Swapped to %d replicas (zero downtime)", scale))
			} else {
				ui.Success(fmt.Sprintf("%d replicas started", scale))
			}
		}
	} else {
		// ── Single-container blue-green deploy (original behavior) ─────────────────
		nextName := containerName + "-next"

		// Clean up any leftover -next container from a failed previous deploy
		docker.Remove(nextName)

		// Start new container with staging name
		spin = ui.NewSpinner("Starting new container...")
		spin.Start()
		appOpts := remote.RunOpts{
			Name:    nextName,
			Image:   imageTag,
			Network: config.DockerNetwork,
			Restart: restartPolicy(appRestart),
			Volumes: volumes,
			Env:     env,
		}
		applyHealth(&appOpts, appHealth)
		_, err = docker.Run(appOpts)
		spin.Stop()
		if err != nil {
			return fmt.Errorf("start container: %w", err)
		}
		ui.Success("New container started")

		// Health check the new container — verify TCP connectivity, not just running state
		spin = ui.NewSpinner("Waiting for app to be ready...")
		spin.Start()
		healthy := waitForHealthy(docker, nextName, port, 120*time.Second)
		spin.Stop()

		if !healthy {
			// Rollback: remove the failed new container, keep old one running
			docker.Remove(nextName)
			ui.Error("New container failed health check — rolled back")
			ui.Info(fmt.Sprintf("Old version still running. Debug with: neo logs %s", appName))
			return nil
		}
		ui.Success("Health check passed")

		// HTTP health check — opt-in, only runs when health.path is configured.
		// port==0 or no health.path → skipped (no behavior change for existing deployments).
		// Runs BEFORE Caddy traffic switch: if unhealthy, old container keeps serving (true zero-downtime rollback).
		if port > 0 {
			var stateHealth *state.HealthCheck
			if isRedeploy {
				stateHealth = existing.Health
			}
			var neoHealth *NeoHealth
			if neoConfig != nil {
				neoHealth = neoConfig.Health
			}
			hOpts := httpHealthOpts(neoHealth, stateHealth)
			if hOpts.Path != "" {
				spin = ui.NewSpinner(fmt.Sprintf("Waiting for HTTP health (%s)...", hOpts.Path))
				spin.Start()
				httpErr := docker.HTTPHealthCheck(nextName, port, hOpts)
				spin.Stop()
				if httpErr != nil {
					docker.Remove(nextName)
					if isRedeploy {
						ui.Error(fmt.Sprintf("HTTP health check failed — rolled back: %s", httpErr))
						ui.Info(fmt.Sprintf("Old version still running. Debug with: neo logs %s", appName))
					} else {
						ui.Error(fmt.Sprintf("HTTP health check failed: %s", httpErr))
						ui.Info("Fix the issue and re-deploy.")
					}
					return nil
				}
				ui.Success(fmt.Sprintf("HTTP health OK (%s)", hOpts.Path))
			}
		}

		if isRedeploy {
			authOpts := neoBasicAuthToRouteOpts(neoConfig)
			hasAuth := len(authOpts) > 0 && authOpts[0].BasicAuth != nil

			// Zero-downtime swap:
			// 1. Point Caddy at the new (next) container.
			// If basic_auth is configured we must do a full UpdateRoute so the route
			// structure (subroute + authentication handler) is rebuilt — PatchUpstream
			// only changes the dial address and cannot add or remove auth.
			if len(deployDomains) > 0 {
				dial := fmt.Sprintf("%s:%d", nextName, port)
				if hasAuth {
					if httpOnly {
						caddy.UpdateRouteHTTP(containerName, deployDomains, dial, authOpts...)
					} else {
						caddy.UpdateRoute(containerName, deployDomains, dial, authOpts...)
					}
				} else if err := caddy.PatchUpstream(containerName, dial); err != nil {
					// Fallback: route may not exist yet (e.g. app deployed before this version)
					if httpOnly {
						caddy.UpdateRouteHTTP(containerName, deployDomains, dial, authOpts...)
					} else {
						caddy.UpdateRoute(containerName, deployDomains, dial, authOpts...)
					}
				}
			}

			// 2. Stop and remove old container (no longer receiving traffic)
			spin = ui.NewSpinner("Removing old container...")
			spin.Start()
			docker.Remove(containerName)
			spin.Stop()

			// 3. Rename new container to canonical name
			docker.Rename(nextName, containerName)

			// 4. Atomically patch Caddy back to canonical container name
			if len(deployDomains) > 0 {
				dial := fmt.Sprintf("%s:%d", containerName, port)
				if hasAuth {
					addCaddyRoute(containerName, deployDomains, port)
				} else if err := caddy.PatchUpstream(containerName, dial); err != nil {
					addCaddyRoute(containerName, deployDomains, port)
				}
				ui.Success(fmt.Sprintf("Traffic switched to new version (%s)", domain))
			} else {
				ui.Success("Swapped to new version (zero downtime)")
			}
		} else {
			// First deploy
			docker.Rename(nextName, containerName)

			if domain != "" {
				upstream := fmt.Sprintf("%s:%d", containerName, port)
				domains := []string{domain}
				authOpts := neoBasicAuthToRouteOpts(neoConfig)

				// Check for custom SSL certificate from .neo.yml
				if neoConfig != nil && neoConfig.SSL != nil && neoConfig.SSL.Certificate != "" && neoConfig.SSL.PrivateKey != "" {
					certPath := neoConfig.SSL.Certificate
					keyPath := neoConfig.SSL.PrivateKey
					if !filepath.IsAbs(certPath) {
						certPath = filepath.Join(absPath, certPath)
					}
					if !filepath.IsAbs(keyPath) {
						keyPath = filepath.Join(absPath, keyPath)
					}
					if err := runDomainCustomCert(appName, domain, certPath, keyPath); err != nil {
						ui.Error(fmt.Sprintf("Custom SSL setup failed: %s — falling back to HTTP", err))
						caddy.AddRouteHTTP(containerName, domains, upstream, authOpts...)
					}
					httpOnly = false
				} else if neoConfig != nil && neoConfig.HTTPS != nil && *neoConfig.HTTPS {
					// HTTPS enabled via .neo.yml https: true
					if err := caddy.AddRoute(containerName, domains, upstream, authOpts...); err != nil {
						ui.Error(fmt.Sprintf("Failed to add Caddy HTTPS route: %s", err))
					}
					httpOnly = false
				} else {
					// HTTP-only by default (user can enable HTTPS via neo domain --temp or neo domain <app> <domain>)
					if err := caddy.AddRouteHTTP(containerName, domains, upstream, authOpts...); err != nil {
						ui.Error(fmt.Sprintf("Failed to add Caddy route: %s", err))
					}
				}
			}
		}
	}

	// Deploy worker containers in parallel (same image, different command, shared volumes + env)
	workerStates := make(map[string]state.AppWorker)
	if neoConfig != nil && len(neoConfig.Workers) > 0 {
		if len(neoConfig.Workers) == 1 {
			// Single worker: use spinner for nicer output
			for wName, wCfg := range neoConfig.Workers {
				workerContainer := fmt.Sprintf("app-%s-worker-%s", appName, wName)
				workerNext := workerContainer + "-next"
				docker.Remove(workerNext)

				spin = ui.NewSpinner(fmt.Sprintf("Starting worker: %s...", wName))
				spin.Start()
				wRestart := wCfg.Restart
				if wRestart == "" {
					wRestart = appRestart
				}
				_, wErr := docker.Run(remote.RunOpts{
					Name:    workerNext,
					Image:   imageTag,
					Network: config.DockerNetwork,
					Restart: restartPolicy(wRestart),
					Volumes: volumes,
					Env:     env,
					Cmd:     wCfg.Command,
				})
				spin.Stop()
				if wErr != nil {
					ui.Error(fmt.Sprintf("Failed to start worker %s: %s", wName, wErr))
					continue
				}
				if !waitForHealthy(docker, workerNext, 0, 15*time.Second) {
					docker.Remove(workerNext)
					ui.Error(fmt.Sprintf("Worker %s failed health check — skipped", wName))
					continue
				}
				if isRedeploy {
					docker.Remove(workerContainer)
				}
				docker.Rename(workerNext, workerContainer)
				workerStates[wName] = state.AppWorker{Command: wCfg.Command, Status: "running", Restart: wRestart}
				ui.Success(fmt.Sprintf("Worker %s started", wName))
			}
		} else {
			// Multiple workers: launch all in parallel, print results after
			fmt.Printf("  Starting %d workers in parallel...\n", len(neoConfig.Workers))
			type workerResult struct {
				name string
				st   state.AppWorker
				err  string
			}
			results := make(chan workerResult, len(neoConfig.Workers))
			var wg sync.WaitGroup
			for wName, wCfg := range neoConfig.Workers {
				wg.Add(1)
				go func(name string, cfg NeoWorker) {
					defer wg.Done()
					workerContainer := fmt.Sprintf("app-%s-worker-%s", appName, name)
					workerNext := workerContainer + "-next"
					docker.Remove(workerNext)
					wRst := cfg.Restart
					if wRst == "" {
						wRst = appRestart
					}
					_, wErr := docker.Run(remote.RunOpts{
						Name:    workerNext,
						Image:   imageTag,
						Network: config.DockerNetwork,
						Restart: restartPolicy(wRst),
						Volumes: volumes,
						Env:     env,
						Cmd:     cfg.Command,
					})
					if wErr != nil {
						results <- workerResult{name: name, err: wErr.Error()}
						return
					}
					if !waitForHealthy(docker, workerNext, 0, 15*time.Second) {
						docker.Remove(workerNext)
						results <- workerResult{name: name, err: "health check failed"}
						return
					}
					if isRedeploy {
						docker.Remove(workerContainer)
					}
					docker.Rename(workerNext, workerContainer)
					results <- workerResult{name: name, st: state.AppWorker{Command: cfg.Command, Status: "running", Restart: wRst}}
				}(wName, wCfg)
			}
			wg.Wait()
			close(results)
			for r := range results {
				if r.err != "" {
					ui.Error(fmt.Sprintf("Worker %s: %s", r.name, r.err))
				} else {
					workerStates[r.name] = r.st
					ui.Success(fmt.Sprintf("Worker %s started", r.name))
				}
			}
		}

		// Remove workers that are no longer in .neo.yml
		if isRedeploy && existing.Workers != nil {
			for oldWorker := range existing.Workers {
				if _, stillExists := neoConfig.Workers[oldWorker]; !stillExists {
					oldContainer := fmt.Sprintf("app-%s-worker-%s", appName, oldWorker)
					docker.Remove(oldContainer)
					ui.Info(fmt.Sprintf("Removed old worker: %s", oldWorker))
				}
			}
		}
	} else if isRedeploy && existing.Workers != nil {
		// No workers in config but had workers before — remove them
		for oldWorker := range existing.Workers {
			oldContainer := fmt.Sprintf("app-%s-worker-%s", appName, oldWorker)
			docker.Remove(oldContainer)
		}
	}

	// Deploy sidecar containers (own image, shared network, no public route)
	sidecarStates := make(map[string]state.AppSidecar)
	if neoConfig != nil && len(neoConfig.Sidecars) > 0 {
		for scName, scCfg := range neoConfig.Sidecars {
			scContainer := fmt.Sprintf("svc-%s-%s", appName, scName)
			scImageTag := ""

			if scCfg.Build.Context != "" {
				// Build sidecar image from Dockerfile
				buildCtx := scCfg.Build.Context
				if !filepath.IsAbs(buildCtx) {
					buildCtx = filepath.Join(absPath, buildCtx)
				}
				scImageTag = fmt.Sprintf("neo-%s-sidecar-%s:%s", appName, scName, timestamp)

				scDockerfile := filepath.Join(buildCtx, "Dockerfile")
				if scCfg.Build.Dockerfile != "" {
					df := scCfg.Build.Dockerfile
					if !filepath.IsAbs(df) {
						df = filepath.Join(buildCtx, df)
					}
					scDockerfile = df
				}

				if localDocker {
					err = buildSidecarLocal(buildCtx, scDockerfile, scImageTag, serverPlatform, docker)
				} else {
					err = deployViaRemoteBuild(buildCtx, scDockerfile, scImageTag, sshExec, docker)
				}
				if err != nil {
					ui.Error(fmt.Sprintf("Failed to build sidecar %s: %s", scName, err))
					continue
				}
			} else if scCfg.Image != "" {
				// Pull pre-built image
				scImageTag = scCfg.Image
				spin = ui.NewSpinner(fmt.Sprintf("Pulling sidecar %s...", scName))
				spin.Start()
				pullErr := docker.Pull(scImageTag)
				spin.Stop()
				if pullErr != nil {
					ui.Error(fmt.Sprintf("Failed to pull sidecar %s: %s", scName, pullErr))
					continue
				}
			} else {
				ui.Error(fmt.Sprintf("Sidecar %s must have either 'build' or 'image'", scName))
				continue
			}

			// Build sidecar volumes: shared app volumes + sidecar-specific volumes
			var scVolumes []string
			for volName, containerPath := range scCfg.Volumes {
				// Check if this refers to a shared app volume
				appVolName := appName + "-" + volName
				if _, exists := declaredVolumes[appVolName]; exists {
					scVolumes = append(scVolumes, fmt.Sprintf("%s:%s", appVolName, containerPath))
				} else {
					// Standalone sidecar volume
					sidecarVolName := fmt.Sprintf("%s-%s-%s", appName, scName, volName)
					scVolumes = append(scVolumes, fmt.Sprintf("%s:%s", sidecarVolName, containerPath))
				}
			}

			scNext := scContainer + "-next"
			docker.Remove(scNext)

			spin = ui.NewSpinner(fmt.Sprintf("Starting sidecar: %s...", scName))
			spin.Start()
			scRst := scCfg.Restart
			if scRst == "" {
				scRst = appRestart
			}
			scOpts := remote.RunOpts{
				Name:    scNext,
				Image:   scImageTag,
				Network: config.DockerNetwork,
				Restart: restartPolicy(scRst),
				Volumes: scVolumes,
				Env:     scCfg.Env,
				Cmd:     scCfg.Command,
			}
			applyHealth(&scOpts, neoHealthToState(scCfg.Health))
			_, scErr := docker.Run(scOpts)
			spin.Stop()

			if scErr != nil {
				ui.Error(fmt.Sprintf("Failed to start sidecar %s: %s", scName, scErr))
				continue
			}

			// Health check sidecar
			spin = ui.NewSpinner(fmt.Sprintf("Checking sidecar %s...", scName))
			spin.Start()
			scHealthy := waitForHealthy(docker, scNext, 0, 30*time.Second)
			spin.Stop()

			if !scHealthy {
				docker.Remove(scNext)
				ui.Error(fmt.Sprintf("Sidecar %s failed health check — skipped", scName))
				continue
			}

			// Swap old sidecar for new
			if isRedeploy {
				docker.Remove(scContainer)
			}
			docker.Rename(scNext, scContainer)

			scRstState := scCfg.Restart
			if scRstState == "" {
				scRstState = appRestart
			}
			sidecarStates[scName] = state.AppSidecar{
				Image:   scImageTag,
				Volumes: scCfg.Volumes,
				Env:     scCfg.Env,
				Command: scCfg.Command,
				Status:  "running",
				Restart: scRstState,
				Health:  neoHealthToState(scCfg.Health),
			}
			ui.Success(fmt.Sprintf("Sidecar %s started", scName))
		}

		// Remove sidecars that are no longer in .neo.yml
		if isRedeploy && existing.Sidecars != nil {
			for oldSc := range existing.Sidecars {
				if _, stillExists := neoConfig.Sidecars[oldSc]; !stillExists {
					docker.Remove(fmt.Sprintf("svc-%s-%s", appName, oldSc))
					ui.Info(fmt.Sprintf("Removed old sidecar: %s", oldSc))
				}
			}
		}
	} else if isRedeploy && existing.Sidecars != nil {
		// No sidecars in config but had sidecars before — remove them
		for oldSc := range existing.Sidecars {
			docker.Remove(fmt.Sprintf("svc-%s-%s", appName, oldSc))
		}
	}

	// Update remote state
	stateApp := state.App{
		Name:         appName,
		Image:        imageTag,
		Domain:       domain,
		HTTPOnly:     httpOnly,
		Status:       "running",
		InternalPort: port,
		Env:          env,
		Volumes:      declaredVolumes,
		Services:     make(map[string]state.AppService),
		Workers:      workerStates,
		Sidecars:     sidecarStates,
		Restart:      appRestart,
		Health:       appHealth,
		Scale:        scale,
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if isRedeploy {
		stateApp.Services = existing.Services
		stateApp.InstalledAt = existing.InstalledAt
		stateApp.ExtraDomains = existing.ExtraDomains // preserve additional domains across redeploys
	}
	st.Apps[appName] = stateApp
	state.Save(sshExec, st)

	// Run post-deploy hook locally (failure is logged but does not roll back)
	if neoConfig != nil && neoConfig.Hooks != nil {
		hEnv := hookEnvVars(appName, resolvedEnv, domain, srv.Host)
		if err := runHook("post_deploy", neoConfig.Hooks.PostDeploy, absPath, hEnv); err != nil {
			ui.Error(fmt.Sprintf("post_deploy hook failed: %s", err))
		}
	}

	// Prune old images in the background (best-effort, never blocks or fails the deploy)
	go docker.PruneImages(fmt.Sprintf("neo-%s", appName), imageTag)

	// Success card
	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " " + appName + " is live!")
	card.Blank()
	if domain != "" {
		serverHost := srv.Host[strings.Index(srv.Host, "@")+1:]
		card.AddKV("URL", "http://"+domain)
		card.Blank()
		card.Add(ui.Bold.Render("DNS Setup:"))
		card.Add(fmt.Sprintf("  Add A record: %s → %s", domain, serverHost))
		card.Blank()
		card.Add(ui.Bold.Render("Enable HTTPS (after DNS is ready):"))
		card.Add("  Open neo dashboard → select app → Enable HTTPS")
		card.Blank()
	}
	card.Add("Redeploy after changes:")
	card.Add("  neo deploy" + func() string {
		if projectPath != "." {
			return " " + projectPath
		}
		return ""
	}())
	card.Render()

	return nil
}

// countingReader wraps an io.Reader and tracks total bytes read.
type countingReader struct {
	r     io.Reader
	bytes int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.bytes += int64(n)
	return n, err
}

// deployViaLocalBuild builds the image locally and transfers it to the server.
func deployViaLocalBuild(projectPath, dockerfile, imageTag, platform string, sshExec *neossh.Executor, licenseKey string) error {
	ui.Info(fmt.Sprintf("Docker detected locally — building on this machine (%s)", platform))

	if err := buildImageLocally(projectPath, dockerfile, imageTag, platform); err != nil {
		return err
	}

	tmpFile, fileSize, err := saveImageToTempFile(imageTag)
	if err != nil {
		return fmt.Errorf("save image: %w", err)
	}
	defer os.Remove(tmpFile)

	plan := license.CurrentPlan(licenseKey)
	numStreams := license.Limit(license.FeatureParallelUploads, plan)
	if numStreams <= 0 {
		numStreams = 2
	}
	return transferImageParallel(tmpFile, fileSize, sshExec, numStreams)
}

// deployViaRemoteBuild uploads source and builds on the server.
func deployViaRemoteBuild(projectPath, dockerfile, imageTag string, sshExec *neossh.Executor, docker *remote.Docker) error {
	ui.Info("No local Docker — building on server")

	// Create tar.gz of the project
	spin := ui.NewSpinner("Packaging source code...")
	spin.Start()
	tarBuf, err := createTarGz(projectPath)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	size := int64(tarBuf.Len())
	ui.Success(fmt.Sprintf("Source packaged (%.1f MB)", float64(size)/(1024*1024)))

	// Upload to server
	appDir := sanitizeName(filepath.Base(projectPath))
	remoteBuildDir := fmt.Sprintf("/tmp/neo-build/%s", appDir)
	remoteTarPath := remoteBuildDir + "/source.tar.gz"

	spin = ui.NewSpinner("Uploading to server...")
	spin.Start()
	sshExec.RunQuiet(fmt.Sprintf("mkdir -p %s", neossh.ShellQuote(remoteBuildDir)))
	err = sshExec.UploadReader(tarBuf, size, remoteTarPath, 0644)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	ui.Success("Source uploaded")

	// Extract on server
	spin = ui.NewSpinner("Extracting source...")
	spin.Start()
	_, err = sshExec.Run(fmt.Sprintf("cd %s && tar xzf source.tar.gz && rm source.tar.gz", neossh.ShellQuote(remoteBuildDir)))
	spin.Stop()
	if err != nil {
		return fmt.Errorf("extract source: %w", err)
	}

	// Build on server
	fmt.Println()
	ui.Info("Building image on server (this may take a while)...")
	fmt.Println()

	// Determine relative dockerfile path for remote build
	relDockerfile := filepath.Join(remoteBuildDir, "Dockerfile")
	if dockerfile != "" {
		// Preserve relative path within the build context (e.g. neo-builder/Dockerfile)
		rel, err := filepath.Rel(projectPath, dockerfile)
		if err == nil {
			relDockerfile = filepath.Join(remoteBuildDir, rel)
		} else {
			relDockerfile = filepath.Join(remoteBuildDir, filepath.Base(dockerfile))
		}
	}

	err = docker.Build(remoteBuildDir, relDockerfile, imageTag, os.Stdout)
	if err != nil {
		return fmt.Errorf("remote build failed: %w", err)
	}
	ui.Success("Image built on server")

	// Clean up build context
	sshExec.RunQuiet(fmt.Sprintf("rm -rf %s", neossh.ShellQuote(remoteBuildDir)))

	return nil
}

// createTarGz creates a gzipped tar archive of a directory, respecting .dockerignore.
func createTarGz(dir string) (*bytes.Buffer, error) {
	ignorePatterns := loadDockerignore(dir)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(dir, path)
		if relPath == "." {
			return nil
		}

		// Skip ignored paths
		if shouldIgnore(relPath, info.IsDir(), ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})

	if err != nil {
		return nil, err
	}

	tw.Close()
	gz.Close()
	return &buf, nil
}

// loadDockerignore reads .dockerignore patterns from the project directory.
func loadDockerignore(dir string) []string {
	patterns := []string{
		".git",
		"node_modules",
	}

	f, err := os.Open(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		return patterns
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	return patterns
}

// shouldIgnore checks if a path matches any ignore pattern.
func shouldIgnore(path string, isDir bool, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, path); matched {
			return true
		}
		// Also check if any path component matches
		parts := strings.Split(path, string(filepath.Separator))
		for _, part := range parts {
			if matched, _ := filepath.Match(p, part); matched {
				return true
			}
		}
	}
	return false
}

// detectPort reads a Dockerfile and finds the EXPOSE directive.
func detectPort(dockerfile string) int {
	f, err := os.Open(dockerfile)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToUpper(line), "EXPOSE") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var port int
				// Handle "EXPOSE 8080/tcp" format
				portStr := strings.Split(parts[1], "/")[0]
				fmt.Sscanf(portStr, "%d", &port)
				return port
			}
		}
	}
	return 0
}

// sanitizeName converts a directory name to a valid app name.
func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "app"
	}
	return name
}

// restartPolicy returns the restart policy, defaulting to "unless-stopped".
func restartPolicy(r string) string {
	if r == "" {
		return "unless-stopped"
	}
	return r
}

// neoBasicAuthToRouteOpts converts a NeoConfig's BasicAuth into remote.RouteOptions.
// Returns nil slice (no options) when basic auth is not configured.
func neoBasicAuthToRouteOpts(cfg *NeoConfig) []remote.RouteOptions {
	if cfg == nil || cfg.BasicAuth == nil || cfg.BasicAuth.User == "" || cfg.BasicAuth.Password == "" {
		return nil
	}
	return []remote.RouteOptions{{
		BasicAuth: &remote.BasicAuthConfig{
			Username:    cfg.BasicAuth.User,
			Password:    cfg.BasicAuth.Password,
			BypassPaths: cfg.BasicAuth.Bypass,
		},
	}}
}

// neoHealthToState converts a NeoHealth config to a state HealthCheck.
func neoHealthToState(h *NeoHealth) *state.HealthCheck {
	if h == nil || (h.Cmd == "" && h.Path == "") {
		return nil
	}
	return &state.HealthCheck{
		Cmd:         h.Cmd,
		Interval:    h.Interval,
		Timeout:     h.Timeout,
		Retries:     h.Retries,
		StartPeriod: h.StartPeriod,
		Path:        h.Path,
	}
}

// httpHealthOpts builds HTTPHealthOpts from config. Returns opts with empty Path when
// no health path is configured — callers must check opts.Path != "" before running.
// Falls back to stateHealth when neoHealth is nil (redeploy without .neo.yml).
func httpHealthOpts(neoHealth *NeoHealth, stateHealth *state.HealthCheck) remote.HTTPHealthOpts {
	var path, interval, timeout, startPeriod string
	var retries int
	if neoHealth != nil {
		path, interval, timeout, startPeriod, retries =
			neoHealth.Path, neoHealth.Interval, neoHealth.Timeout, neoHealth.StartPeriod, neoHealth.Retries
	} else if stateHealth != nil {
		path, interval, timeout, startPeriod, retries =
			stateHealth.Path, stateHealth.Interval, stateHealth.Timeout, stateHealth.StartPeriod, stateHealth.Retries
	}
	opts := remote.HTTPHealthOpts{Path: path, Retries: retries}
	if d, err := time.ParseDuration(interval); err == nil {
		opts.Interval = d
	}
	if d, err := time.ParseDuration(timeout); err == nil {
		opts.Timeout = d
	}
	if d, err := time.ParseDuration(startPeriod); err == nil {
		opts.StartPeriod = d
	}
	return opts
}

// applyHealth sets RunOpts health check fields from a state HealthCheck.
func applyHealth(opts *remote.RunOpts, h *state.HealthCheck) {
	if h == nil || h.Cmd == "" {
		return
	}
	opts.HealthCmd = h.Cmd
	opts.HealthInterval = h.Interval
	opts.HealthTimeout = h.Timeout
	opts.HealthRetries = h.Retries
	opts.HealthStartPeriod = h.StartPeriod
}

// isProductionEnv returns true for environment names considered primary/production,
// which do not get an app name suffix.
func isProductionEnv(name string) bool {
	switch strings.ToLower(name) {
	case "production", "prod", "main", "default", "live":
		return true
	}
	return false
}

// checkServerMemory returns an error if the server has less than minMB MB of free RAM.
// Parses /proc/meminfo via SSH. Returns nil (proceeds) if the check cannot be performed.
func checkServerMemory(exec *neossh.Executor, minMB int) error {
	out, err := exec.Run("grep MemAvailable /proc/meminfo")
	if err != nil {
		return nil // can't check, proceed optimistically
	}
	fields := strings.Fields(out) // ["MemAvailable:", "123456", "kB"]
	if len(fields) < 2 {
		return nil
	}
	var kb int
	fmt.Sscanf(fields[1], "%d", &kb)
	if kb == 0 {
		return nil
	}
	availMB := kb / 1024
	if availMB < minMB {
		return fmt.Errorf(
			"server has only %dMB free RAM (%dMB minimum required)\n\n"+
				"  Deploying to a low-memory server can crash running apps and make\n"+
				"  the server unreachable. Free up memory or upgrade to a larger plan\n"+
				"  before deploying.",
			availMB, minMB,
		)
	}
	return nil
}

// buildSidecarLocal builds a sidecar image locally and transfers it to the server.
func buildSidecarLocal(buildCtx, dockerfile, imageTag, platform string, docker *remote.Docker) error {
	spin := ui.NewSpinner(fmt.Sprintf("Building sidecar image locally (%s)...", platform))
	spin.Start()

	if dockerfile == "" {
		dockerfile = filepath.Join(buildCtx, "Dockerfile")
	}
	buildCmd := exec.Command("docker", "build", "--platform", platform, "-t", imageTag, "-f", dockerfile, buildCtx)
	buildCmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	var buildOutput bytes.Buffer
	buildCmd.Stdout = &buildOutput
	buildCmd.Stderr = &buildOutput
	err := buildCmd.Run()
	spin.Stop()

	if err != nil {
		fmt.Println(buildOutput.String())
		return fmt.Errorf("local docker build failed: %w", err)
	}

	// Compress and transfer: docker save | gzip -1 | ssh (gunzip | docker load)
	spin = ui.NewSpinner("Compressing & transferring sidecar image to server...")
	spin.Start()

	saveGzCmd := exec.Command("sh", "-c", "docker save "+imageTag+" | gzip -1")
	saveGzOut, err := saveGzCmd.StdoutPipe()
	if err != nil {
		spin.Stop()
		return fmt.Errorf("docker save pipe: %w", err)
	}
	if err := saveGzCmd.Start(); err != nil {
		spin.Stop()
		return fmt.Errorf("docker save: %w", err)
	}

	_, loadErr := docker.LoadImageGzipped(saveGzOut)
	waitErr := saveGzCmd.Wait()
	spin.Stop()

	if waitErr != nil {
		return fmt.Errorf("docker save failed: %w", waitErr)
	}
	if loadErr != nil {
		return fmt.Errorf("docker load on server: %w", loadErr)
	}

	return nil
}

// isLocalDockerAvailable checks if Docker is running locally.
func isLocalDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// waitForHealthy polls until a container is running and (if port > 0) accepting TCP
// connections on that port. Passing port=0 skips the TCP check and only verifies the
// container is in running state (suitable for workers/sidecars with no HTTP endpoint).
func waitForHealthy(docker *remote.Docker, containerName string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if docker.IsRunning(containerName) {
			if port <= 0 || docker.IsPortOpen(containerName, port) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// buildImageLocally runs `docker build` on the local machine.
func buildImageLocally(projectPath, dockerfile, imageTag, platform string) error {
	spin := ui.NewSpinner(fmt.Sprintf("Building image locally (%s)...", platform))
	spin.Start()

	buildCmd := exec.Command("docker", "build", "--platform", platform, "-t", imageTag, "-f", dockerfile, projectPath)
	buildCmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	var buildOutput bytes.Buffer
	buildCmd.Stdout = &buildOutput
	buildCmd.Stderr = &buildOutput
	err := buildCmd.Run()
	spin.Stop()

	if err != nil {
		fmt.Println()
		fmt.Println(buildOutput.String())
		return fmt.Errorf("local docker build failed: %w", err)
	}
	ui.Success("Image built locally")
	return nil
}

// saveImageToTempFile compresses a local Docker image to a temp .tar.gz file.
// Returns the file path and its byte size. The caller must remove the file when done.
func saveImageToTempFile(imageTag string) (string, int64, error) {
	f, err := os.CreateTemp("", "neo-transfer-*.tar.gz")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	name := f.Name()

	spin := ui.NewSpinner("Compressing image for transfer...")
	spin.Start()
	cmd := exec.Command("sh", "-c", "docker save "+imageTag+" | gzip -1")
	cmd.Stdout = f
	err = cmd.Run()
	f.Close()
	spin.Stop()

	if err != nil {
		os.Remove(name)
		return "", 0, fmt.Errorf("compress image: %w", err)
	}

	info, _ := os.Stat(name)
	var size int64
	if info != nil {
		size = info.Size()
	}
	return name, size, nil
}

// transferImageParallel uploads a gzipped image to the server using numStreams parallel SSH
// connections for faster throughput on high-latency links. Each connection uploads an
// equal byte-range slice of the file; the server then reassembles with `cat` and pipes
// the result into `docker load`.
func transferImageParallel(tmpFile string, fileSize int64, sshExec *neossh.Executor, numStreams int) error {

	remoteDir := fmt.Sprintf("/tmp/neo-upload-%d", time.Now().UnixNano())
	if err := sshExec.RunQuiet("mkdir -p " + remoteDir); err != nil {
		return fmt.Errorf("create upload dir: %w", err)
	}
	defer sshExec.RunQuiet("rm -rf " + remoteDir)

	f, err := os.Open(tmpFile)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	chunkSize := (fileSize + int64(numStreams) - 1) / int64(numStreams)

	var sent int64 // updated atomically by upload goroutines
	spin := ui.NewSpinner(fmt.Sprintf("Transferring image (%d parallel streams)... 0 MB sent", numStreams))
	spin.Start()

	doneCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-doneCh:
				return
			case <-time.After(500 * time.Millisecond):
				mb := float64(atomic.LoadInt64(&sent)) / (1024 * 1024)
				spin.Update(fmt.Sprintf("Transferring image (%d parallel streams)... %.0f MB sent", numStreams, mb))
			}
		}
	}()

	var wg sync.WaitGroup
	errs := make([]error, numStreams)

	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			offset := int64(idx) * chunkSize
			size := chunkSize
			if offset+size > fileSize {
				size = fileSize - offset
			}

			// Each goroutine opens its own SSH connection for a separate TCP stream.
			conn := neossh.New(sshExec.Host, sshExec.Port)
			conn.PrivateKey = sshExec.PrivateKey
			if sshExec.IsInsecureHostKey() {
				conn.SetInsecureHostKey()
			}
			if cErr := conn.Connect(); cErr != nil {
				errs[idx] = cErr
				return
			}
			defer conn.Close()

			section := io.NewSectionReader(f, offset, size)
			cr := &atomicCountingReader{r: section, sent: &sent}
			remotePath := fmt.Sprintf("%s/part.%02d", remoteDir, idx)
			errs[idx] = conn.UploadReader(cr, size, remotePath, 0644)
		}(i)
	}

	wg.Wait()
	close(doneCh)
	spin.Stop()

	for _, e := range errs {
		if e != nil {
			return fmt.Errorf("upload chunk: %w", e)
		}
	}

	mb := float64(fileSize) / (1024 * 1024)
	ui.Success(fmt.Sprintf("Image transferred to server (%.0f MB, %d parallel streams)", mb, numStreams))

	// Reassemble chunks on server and pipe into docker load
	spin2 := ui.NewSpinner("Loading image on server...")
	spin2.Start()
	_, loadErr := sshExec.Run(fmt.Sprintf("cat %s/part.* | gunzip | docker load", remoteDir))
	spin2.Stop()

	if loadErr != nil {
		return fmt.Errorf("docker load: %w", loadErr)
	}
	return nil
}

// atomicCountingReader wraps an io.Reader and atomically adds each read's byte count
// to a shared int64 counter, enabling progress tracking across parallel goroutines.
type atomicCountingReader struct {
	r    io.Reader
	sent *int64
}

func (cr *atomicCountingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		atomic.AddInt64(cr.sent, int64(n))
	}
	return n, err
}

// runDeployAll builds the Docker image once locally, saves it to a temp file, then
// concurrently transfers and deploys to every environment defined in .neo.yml.
func runDeployAll(absPath, dockerfile string, flags deployFlags, neoConfig *NeoConfig) error {
	timestamp := time.Now().UTC().Format("20060102-150405")

	baseAppName := flags.appName
	if baseAppName == "" {
		if neoConfig.Name != "" {
			baseAppName = sanitizeName(neoConfig.Name)
		} else {
			baseAppName = sanitizeName(filepath.Base(absPath))
		}
	}
	imageTag := fmt.Sprintf("neo-%s:%s", baseAppName, timestamp)

	fmt.Println()
	fmt.Printf("  Deploying %s to %d environment(s) in parallel\n",
		ui.Bold.Render(baseAppName), len(neoConfig.Environments))
	fmt.Println()

	// Run top-level pre-build hook once before the shared build
	if neoConfig.Hooks != nil {
		hEnv := hookEnvVars(baseAppName, "", "", "")
		if err := runHook("pre_build", neoConfig.Hooks.PreBuild, absPath, hEnv); err != nil {
			return err
		}
	}

	// Build image once for all environments (default linux/amd64; all envs must share arch).
	if err := buildImageLocally(absPath, dockerfile, imageTag, "linux/amd64"); err != nil {
		return err
	}

	// Save compressed image to a temp file so every goroutine can open its own reader.
	tmpFile, tmpSize, err := saveImageToTempFile(imageTag)
	if err != nil {
		return fmt.Errorf("save image for transfer: %w", err)
	}
	defer os.Remove(tmpFile)
	ui.Success(fmt.Sprintf("Image ready for parallel transfer (%.0f MB)", float64(tmpSize)/(1024*1024)))
	fmt.Println()

	// Launch one goroutine per environment — each opens its own SSH connection and
	// streams the temp file into `docker load` on the target server.
	type envResult struct {
		name string
		url  string
		err  error
	}

	// Validate all environments have an explicit server (runDeployAll is always multi-env).
	var missing []string
	for envName, envCfg := range neoConfig.Environments {
		if len(envCfg.EffectiveServers()) == 0 {
			missing = append(missing, envName)
		}
	}
	if len(missing) > 0 {
		for _, envName := range missing {
			ui.Error(fmt.Sprintf("environment %q has no server: defined in .neo.yml", envName))
			ui.Info(fmt.Sprintf("Add to the %q environment:", envName))
			fmt.Printf("\n    environments:\n      %s:\n        server: your-server-name\n\n", envName)
		}
		return fmt.Errorf("every environment must specify a server:")
	}

	// Count total deploy targets (each server in a group is a separate target)
	totalTargets := 0
	for _, envCfg := range neoConfig.Environments {
		servers := envCfg.EffectiveServers()
		if len(servers) == 0 {
			totalTargets++
		} else {
			totalTargets += len(servers)
		}
	}

	results := make(chan envResult, totalTargets)
	var wg sync.WaitGroup

	// Semaphore to cap concurrent SSH connections + docker load operations.
	// Each parallel deploy opens an SSH connection and decompresses the image,
	// which spikes CPU and RAM — small servers (1GB/1vCPU) can't handle many at once.
	// Default is 3; users can lower it with --parallel 1 for underpowered servers.
	maxParallel := flags.parallel
	if maxParallel < 1 {
		maxParallel = 1
	}
	sem := make(chan struct{}, maxParallel)

	for envName, envCfg := range neoConfig.Environments {
		servers := envCfg.EffectiveServers()
		if len(servers) <= 1 {
			// Single server (or fallback to current) — original behaviour
			wg.Add(1)
			go func(name string, cfg NeoEnvironment) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				url, deployErr := deployEnvFromFile(name, cfg, "", imageTag, tmpFile, absPath, flags, neoConfig)
				results <- envResult{name: name, url: url, err: deployErr}
			}(envName, envCfg)
		} else {
			// Server group — deploy to each server in parallel
			for _, srvName := range servers {
				wg.Add(1)
				go func(envName, srvName string, cfg NeoEnvironment) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					label := fmt.Sprintf("%s[%s]", envName, srvName)
					url, deployErr := deployEnvFromFile(envName, cfg, srvName, imageTag, tmpFile, absPath, flags, neoConfig)
					results <- envResult{name: label, url: url, err: deployErr}
				}(envName, srvName, envCfg)
			}
		}
	}

	wg.Wait()
	close(results)

	anyErr := false
	for r := range results {
		if r.err != nil {
			ui.Error(fmt.Sprintf("[%s] %s", r.name, r.err))
			anyErr = true
		} else {
			msg := fmt.Sprintf("[%s] deployed", r.name)
			if r.url != "" {
				msg += " → " + r.url
			}
			ui.Success(msg)
		}
	}
	fmt.Println()

	if anyErr {
		return fmt.Errorf("one or more environments failed to deploy")
	}
	return nil
}

// deployEnvFromFile handles the transfer + container lifecycle for a single environment
// during a parallel --all deploy. It opens its own SSH connection to the env's server,
// loads the pre-built image from tmpFile, and does a blue-green container swap.
//
// serverOverride — when non-empty, overrides the server from the environment config.
// Used by runDeployAll when deploying to a server group (servers: [...]).
func deployEnvFromFile(envName string, envCfg NeoEnvironment, serverOverride, imageTag, tmpFile, absPath string, flags deployFlags, neoConfig *NeoConfig) (string, error) {
	// Resolve app name for this environment
	appName := flags.appName
	if appName == "" {
		if envCfg.Name != "" {
			appName = sanitizeName(envCfg.Name)
		} else {
			base := sanitizeName(neoConfig.Name)
			if base == "" {
				base = sanitizeName(filepath.Base(absPath))
			}
			if isProductionEnv(envName) {
				appName = base
			} else {
				appName = base + "-" + sanitizeName(envName)
			}
		}
	}

	// Resolve port
	port := flags.port
	if port == 0 {
		switch {
		case envCfg.Port > 0:
			port = envCfg.Port
		case neoConfig.Port > 0:
			port = neoConfig.Port
		default:
			port = detectPort(filepath.Join(absPath, "Dockerfile"))
			if port == 0 {
				port = 8080
			}
		}
	}

	// Resolve domain (domains: list takes precedence over domain: string in both env and top-level)
	domain := flags.domain
	if domain == "" {
		if len(envCfg.Domains) > 0 {
			domain = envCfg.Domains[0]
		} else if envCfg.Domain != "" {
			domain = envCfg.Domain
		} else if neoConfig.PrimaryDomain() != "" {
			domain = neoConfig.PrimaryDomain()
		}
	}

	// Merge env vars: base .neo.yml → environment override
	env := make(map[string]string)
	for k, v := range neoConfig.Env {
		env[k] = v
	}
	for k, v := range envCfg.Env {
		env[k] = v
	}

	httpsFlag := neoConfig.HTTPS
	if envCfg.HTTPS != nil {
		httpsFlag = envCfg.HTTPS
	}

	// Resolve target server: explicit override (server group) > env config > top-level config
	serverName := serverOverride
	if serverName == "" {
		serverName = envCfg.Server
	}
	if serverName == "" {
		serverName = neoConfig.Server
	}

	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	var srv *config.Server
	if serverName != "" {
		if strings.Contains(serverName, "@") {
			s := config.Server{Name: serverName, Host: serverName, Port: 22}
			srv = &s
		} else {
			s, ok := cfg.Servers[serverName]
			if !ok {
				return "", fmt.Errorf("server %q not found — run 'neo servers' to list configured servers", serverName)
			}
			srv = &s
		}
	} else {
		s, cErr := cfg.CurrentServer()
		if cErr != nil {
			return "", fmt.Errorf("resolve server: %w", cErr)
		}
		srv = s
	}

	sshExec, err := connectSSH(srv)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", srv.Host, err)
	}
	defer sshExec.Close()

	st, err := state.Load(sshExec)
	if err != nil {
		return "", fmt.Errorf("load state: %w", err)
	}

	existing, isRedeploy := st.Apps[appName]

	// Auto-assign a temporary sslip.io domain on first deploy if none set
	if domain == "" && !isRedeploy && st.ServerIP != "" {
		domain = appName + "." + st.ServerIP + ".sslip.io"
	}

	// Auto-set APP_URL
	if domain != "" {
		if _, ok := env["APP_URL"]; !ok {
			httpsEnabled := false
			if httpsFlag != nil {
				httpsEnabled = *httpsFlag
			} else if isRedeploy && !existing.HTTPOnly {
				httpsEnabled = true
			}
			scheme := "http"
			if httpsEnabled {
				scheme = "https"
			}
			env["APP_URL"] = scheme + "://" + domain
		}
	}

	docker := remote.NewDocker(sshExec)
	caddy := remote.NewCaddy(sshExec)
	containerName := config.AppContainer(appName)

	// Transfer image: open the shared temp file and stream into docker load
	f, err := os.Open(tmpFile)
	if err != nil {
		return "", fmt.Errorf("open image file: %w", err)
	}
	defer f.Close()

	if _, err := docker.LoadImageGzipped(f); err != nil {
		return "", fmt.Errorf("load image: %w", err)
	}

	// Merge environment-specific overrides into neoConfig
	if len(envCfg.Volumes) > 0 {
		if neoConfig.Volumes == nil {
			neoConfig.Volumes = make(map[string]NeoVolume)
		}
		for k, v := range envCfg.Volumes {
			neoConfig.Volumes[k] = v
		}
	}
	if len(envCfg.Workers) > 0 {
		neoConfig.Workers = envCfg.Workers
	}
	if len(envCfg.Sidecars) > 0 {
		neoConfig.Sidecars = envCfg.Sidecars
	}
	if envCfg.Restart != "" {
		neoConfig.Restart = envCfg.Restart
	}
	if envCfg.Health != nil {
		neoConfig.Health = envCfg.Health
	}

	// Resolve restart policy and health check
	allRestart := neoConfig.Restart
	allHealth := neoHealthToState(neoConfig.Health)

	// Build volumes list
	var allExistingApp *state.App
	if isRedeploy {
		allExistingApp = &existing
	}
	volumes, declaredVolumes := buildDeployVolumes(appName, neoConfig, allExistingApp)

	// Blue-green: start new container alongside old one
	nextName := containerName + "-next"
	docker.Remove(nextName)

	allOpts := remote.RunOpts{
		Name:    nextName,
		Image:   imageTag,
		Network: config.DockerNetwork,
		Restart: restartPolicy(allRestart),
		Volumes: volumes,
		Env:     env,
	}
	applyHealth(&allOpts, allHealth)
	if _, err := docker.Run(allOpts); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	if !waitForHealthy(docker, nextName, port, 120*time.Second) {
		docker.Remove(nextName)
		return "", fmt.Errorf("container failed health check")
	}

	// Determine HTTP mode
	httpOnly := true
	if isRedeploy {
		httpOnly = existing.HTTPOnly
	}
	if httpsFlag != nil {
		httpOnly = !*httpsFlag
	}

	var deployDomains []string
	if domain != "" {
		seen := map[string]bool{domain: true}
		deployDomains = []string{domain}
		// Extra domains from .neo.yml domains: list
		envExtraDomains := envCfg.Domains
		if len(envExtraDomains) == 0 {
			envExtraDomains = neoConfig.ExtraConfigDomains()
		} else {
			envExtraDomains = envExtraDomains[1:] // skip the first (used as primary)
		}
		for _, d := range envExtraDomains {
			if !seen[d] {
				seen[d] = true
				deployDomains = append(deployDomains, d)
			}
		}
		// State extra domains (manually added via neo domain --add)
		if isRedeploy {
			for _, d := range existing.ExtraDomains {
				if !seen[d] {
					seen[d] = true
					deployDomains = append(deployDomains, d)
				}
			}
		}
	}

	swapCaddy := func(cName, upstream string) {
		if len(deployDomains) == 0 {
			return
		}
		authOpts := neoBasicAuthToRouteOpts(neoConfig)
		if httpOnly {
			caddy.UpdateRouteHTTP(cName, deployDomains, upstream, authOpts...)
		} else {
			caddy.UpdateRoute(cName, deployDomains, upstream, authOpts...)
		}
	}

	if isRedeploy {
		swapCaddy(containerName, fmt.Sprintf("%s:%d", nextName, port))
		docker.Remove(containerName)
		docker.Rename(nextName, containerName)
		swapCaddy(containerName, fmt.Sprintf("%s:%d", containerName, port))
	} else {
		docker.Rename(nextName, containerName)
		if domain != "" {
			upstream := fmt.Sprintf("%s:%d", containerName, port)
			authOpts := neoBasicAuthToRouteOpts(neoConfig)
			if httpOnly {
				caddy.AddRouteHTTP(containerName, deployDomains, upstream, authOpts...)
			} else {
				caddy.AddRoute(containerName, deployDomains, upstream, authOpts...)
			}
		}
	}

	// Persist state
	stateApp := state.App{
		Name:         appName,
		Image:        imageTag,
		Domain:       domain,
		HTTPOnly:     httpOnly,
		Status:       "running",
		InternalPort: port,
		Env:          env,
		Volumes:      declaredVolumes,
		Services:     make(map[string]state.AppService),
		Workers:      make(map[string]state.AppWorker),
		Restart:      allRestart,
		Health:       allHealth,
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if isRedeploy {
		stateApp.Services = existing.Services
		stateApp.Workers = existing.Workers
		stateApp.Sidecars = existing.Sidecars
		stateApp.InstalledAt = existing.InstalledAt
		stateApp.ExtraDomains = existing.ExtraDomains
	}
	st.Apps[appName] = stateApp
	state.Save(sshExec, st)

	// Run per-environment post-deploy hook
	hooks := resolveHooks(neoConfig.Hooks, envCfg.Hooks)
	if hooks != nil {
		hEnv := hookEnvVars(appName, envName, domain, srv.Host)
		if err := runHook("post_deploy", hooks.PostDeploy, absPath, hEnv); err != nil {
			ui.Error(fmt.Sprintf("[%s] post_deploy hook failed: %s", envName, err))
		}
	}

	// Prune old images in the background
	go docker.PruneImages(fmt.Sprintf("neo-%s", appName), imageTag)

	url := ""
	if domain != "" {
		scheme := "http"
		if !httpOnly {
			scheme = "https"
		}
		url = scheme + "://" + domain
	}
	return url, nil
}
