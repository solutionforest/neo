package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/app"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [app]",
		Short: "Install an application on the server",
		Long:  "Installs a Docker-based application with guided setup. If no app name given, shows an interactive picker.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := ""
			if len(args) > 0 {
				appName = args[0]
			}
			return runInstall(appName)
		},
	}
}

func runInstall(appName string) error {
	registry, err := app.NewRegistry()
	if err != nil {
		return fmt.Errorf("load app registry: %w", err)
	}

	// If no app specified, show interactive picker
	var manifest *app.Manifest
	if appName == "" {
		manifest, err = pickApp(registry)
		if err != nil {
			return err
		}
	} else {
		m, ok := registry.Get(appName)
		if !ok {
			ui.Error(fmt.Sprintf("Unknown app %q. Run 'neo install' to see available apps.", appName))
			return nil
		}
		manifest = m
	}

	// Connect to server
	cfg, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()
	_ = cfg

	// Check if already installed
	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if _, exists := st.Apps[manifest.Name]; exists {
		ui.Error(fmt.Sprintf("%s is already installed. Use 'neo update %s' to update.", manifest.Name, manifest.Name))
		return nil
	}

	fmt.Println()
	fmt.Printf("  Installing %s %s\n", ui.Bold.Render(manifest.Title), ui.Faint.Render("v"+manifest.Version))
	fmt.Printf("  Server: %s (%s)\n", srv.Name, srv.Host)
	fmt.Println()

	// Collect configuration via interactive prompts
	domain, envVars, err := collectConfig(manifest)
	if err != nil {
		return err
	}

	// Resolve all env vars
	resolvedEnv := resolveEnvVars(manifest, domain, envVars)

	// Confirm
	fmt.Println()
	var confirm bool
	huh.NewConfirm().
		Title("Deploy " + manifest.Title + "?").
		Affirmative("Yes, deploy").
		Negative("Cancel").
		Value(&confirm).
		Run()
	if !confirm {
		return nil
	}

	fmt.Println()
	docker := remote.NewDocker(exec)
	caddy := remote.NewCaddy(exec)

	// Start pulling app image in background while services are set up
	appPullDone := make(chan error, 1)
	go func() {
		appPullDone <- docker.Pull(manifest.Image)
	}()

	// Pull services first — offer to reuse existing shared services
	serviceEnvs := make(map[string]map[string]string)
	linkedShared := make(map[string]string) // svc spec name → shared service name
	for _, svc := range manifest.Services {
		// Check if a compatible shared service exists
		sharedName := findCompatibleSharedService(st, svc.Image)
		if sharedName != "" {
			var useShared bool
			huh.NewConfirm().
				Title(fmt.Sprintf("Reuse existing shared %s service %q?", detectServiceType(svc.Image), sharedName)).
				Description("This avoids running a duplicate database container.").
				Affirmative("Yes, reuse").
				Negative("No, create new").
				Value(&useShared).
				Run() //nolint:errcheck

			if useShared {
				linkedShared[svc.Name] = sharedName
				// We'll link after the app is in state — for now, gather env from existing service
				shared := st.Services[sharedName]
				serviceEnvs[svc.Name] = shared.Env
				ui.Success(fmt.Sprintf("Will reuse shared service %q", sharedName))
				continue
			}
		}

		containerName := config.SvcContainer(manifest.Name, svc.Name)

		spin := ui.NewSpinner(fmt.Sprintf("Pulling %s...", svc.Image))
		spin.Start()
		if err := docker.Pull(svc.Image); err != nil {
			spin.Stop()
			return fmt.Errorf("pull %s: %w", svc.Image, err)
		}
		spin.Stop()
		ui.Success(fmt.Sprintf("Pulled %s", svc.Image))

		// Resolve service env vars
		svcEnv := make(map[string]string)
		for _, e := range svc.Env {
			if e.Generate != "" {
				val, err := app.GenerateValue(e.Generate)
				if err != nil {
					return err
				}
				svcEnv[e.Key] = val
			} else if e.Value != "" {
				svcEnv[e.Key] = e.Value
			}
		}
		serviceEnvs[svc.Name] = svcEnv

		// Build volumes
		var volumes []string
		for _, v := range svc.Volumes {
			volumes = append(volumes, fmt.Sprintf("%s:%s", v.Name, v.Path))
		}

		// Start service container
		_, err := docker.Run(remote.RunOpts{
			Name:    containerName,
			Image:   svc.Image,
			Network: config.DockerNetwork,
			Restart: "unless-stopped",
			Volumes: volumes,
			Env:     svcEnv,
		})
		if err != nil {
			return fmt.Errorf("start %s: %w", containerName, err)
		}
		ui.Success(fmt.Sprintf("Started %s", containerName))
	}

	// Resolve env var templates that reference service passwords
	for k, v := range resolvedEnv {
		if strings.Contains(v, "${") {
			resolvedEnv[k] = expandServiceVars(v, serviceEnvs)
		}
	}

	// Wait for app image pull (started in background before service setup)
	spin := ui.NewSpinner(fmt.Sprintf("Pulling %s...", manifest.Image))
	spin.Start()
	if err := <-appPullDone; err != nil {
		spin.Stop()
		return fmt.Errorf("pull %s: %w", manifest.Image, err)
	}
	spin.Stop()
	ui.Success(fmt.Sprintf("Pulled %s", manifest.Image))

	// Build app volumes
	var appVolumes []string
	for _, v := range manifest.Volumes {
		appVolumes = append(appVolumes, fmt.Sprintf("%s:%s", v.Name, v.Path))
	}

	// Start app container
	containerName := config.AppContainer(manifest.Name)
	_, err = docker.Run(remote.RunOpts{
		Name:    containerName,
		Image:   manifest.Image,
		Network: config.DockerNetwork,
		Restart: "unless-stopped",
		Volumes: appVolumes,
		Env:     resolvedEnv,
	})
	if err != nil {
		return fmt.Errorf("start %s: %w", containerName, err)
	}
	ui.Success("Container started")

	// Add Caddy route
	if domain != "" {
		upstream := fmt.Sprintf("%s:%d", containerName, manifest.Port)
		if err := caddy.AddRoute(containerName, []string{domain}, upstream); err != nil {
			ui.Error(fmt.Sprintf("Failed to add Caddy route: %s", err))
			ui.Info("You can add the route manually: neo domain " + manifest.Name + " " + domain)
		} else {
			ui.Success(fmt.Sprintf("SSL certificate issued for %s", domain))
		}
	}

	// Health check
	if manifest.Health != nil {
		spin = ui.NewSpinner("Waiting for health check...")
		spin.Start()
		healthy := waitForHealth(docker, containerName, manifest.Health.Path, manifest.Health.Retries)
		spin.Stop()
		if healthy {
			ui.Success("Health check passed")
		} else {
			ui.Error("Health check failed — app may still be starting")
		}
	}

	// Update remote state
	stateApp := state.App{
		Name:         manifest.Name,
		Image:        manifest.Image,
		Domain:       domain,
		Status:       "running",
		InternalPort: manifest.Port,
		Env:          resolvedEnv,
		Volumes:      make(map[string]state.VolumeInfo),
		Services:     make(map[string]state.AppService),
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	for _, v := range manifest.Volumes {
		stateApp.Volumes[v.Name] = state.VolumeInfo{ContainerPath: v.Path}
	}
	// Only store bundled services that were NOT reused from shared services
	for _, svc := range manifest.Services {
		if _, isShared := linkedShared[svc.Name]; !isShared {
			stateApp.Services[svc.Name] = state.AppService{Image: svc.Image}
		}
	}
	st.Apps[manifest.Name] = stateApp

	// Configure shared services for this app — create DB/user and inject env vars
	for svcSpecName, sharedName := range linkedShared {
		shared := st.Services[sharedName]
		svcType := detectServiceType(shared.Image)
		containerName := config.SvcContainerShared(sharedName)

		switch svcType {
		case "mysql", "mariadb":
			dbName := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_") + "_db"
			dbUser := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_")
			dbPass, _ := app.GenerateValue("hex:32")
			rootPass := shared.Env[serviceRootEnvKey(svcType)]

			safeDBName := neossh.SafeSQLIdentifierMySQL(dbName)
			createDB := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE DATABASE IF NOT EXISTS %s;"`, safeSQLValue(rootPass), safeDBName)
			docker.Exec(containerName, createDB)

			createUser := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%'; FLUSH PRIVILEGES;"`,
				safeSQLValue(rootPass), safeSQLValue(dbUser), safeSQLValue(dbPass), safeDBName, safeSQLValue(dbUser))
			docker.Exec(containerName, createUser)

			stateApp.Env["database__connection__host"] = containerName
			stateApp.Env["database__connection__user"] = dbUser
			stateApp.Env["database__connection__password"] = dbPass
			stateApp.Env["database__connection__database"] = dbName

		case "postgres":
			dbName := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_") + "_db"
			dbUser := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_")
			dbPass, _ := app.GenerateValue("hex:32")

			safeDBUser := neossh.SafeSQLIdentifierPG(dbUser)
			safeDBName := neossh.SafeSQLIdentifierPG(dbName)
			createUser := fmt.Sprintf(`psql -U postgres -c "CREATE USER %s WITH PASSWORD '%s';" 2>/dev/null; true`, safeDBUser, safeSQLValue(dbPass))
			docker.Exec(containerName, createUser)

			createDB := fmt.Sprintf(`psql -U postgres -c "CREATE DATABASE %s OWNER %s;" 2>/dev/null; true`, safeDBName, safeDBUser)
			docker.Exec(containerName, createDB)

			stateApp.Env["DATABASE_URL"] = fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, dbPass, containerName, dbName)

		case "redis":
			stateApp.Env["REDIS_URL"] = fmt.Sprintf("redis://%s:6379", containerName)
		}

		_ = svcSpecName
	}

	st.Apps[manifest.Name] = stateApp
	state.Save(exec, st)

	// Success card
	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " " + manifest.Title + " is live!")
	card.Blank()
	if domain != "" {
		card.AddKV("URL", "https://"+domain)
	}
	card.Blank()
	card.Add("Data stored on server:")
	for _, v := range manifest.Volumes {
		card.Add(fmt.Sprintf("  %s  →  docker volume", v.Name))
	}
	card.Render()

	return nil
}

// pickApp shows an interactive app picker.
func pickApp(registry *app.Registry) (*app.Manifest, error) {
	apps := registry.List()
	opts := make([]ui.SelectOption, len(apps))
	for i, a := range apps {
		label := fmt.Sprintf("%-15s %s", a.Name, ui.Faint.Render(a.Description))
		opts[i] = ui.SelectOption{label, a.Name}
	}

	selected := ui.Select("Choose an app to install", opts)
	if selected == "" {
		return nil, nil
	}

	m, _ := registry.Get(selected)
	return m, nil
}

// collectConfig prompts the user for domain and any ask-able env vars.
func collectConfig(m *app.Manifest) (string, map[string]string, error) {
	var domain string
	envVars := make(map[string]string)

	// Domain prompt
	err := huh.NewInput().
		Title("Domain for " + m.Title).
		Placeholder("app.example.com").
		Value(&domain).
		Run()
	if err != nil {
		return "", nil, err
	}

	// Prompt for any env vars that need user input
	for _, e := range m.Env {
		if e.Ask {
			var val string
			err := huh.NewInput().
				Title(e.Label).
				Value(&val).
				Run()
			if err != nil {
				return "", nil, err
			}
			envVars[e.Key] = val
		}
	}

	return domain, envVars, nil
}

// resolveEnvVars builds the final environment variable map for the app container.
func resolveEnvVars(m *app.Manifest, domain string, userVars map[string]string) map[string]string {
	env := make(map[string]string)

	for _, e := range m.Env {
		switch {
		case e.From == "domain":
			env[e.Key] = "https://" + domain
		case e.From == "domain_host":
			env[e.Key] = domain
		case e.Generate != "":
			val, _ := app.GenerateValue(e.Generate)
			env[e.Key] = val
		case e.Value != "":
			env[e.Key] = e.Value
		case e.Template != "":
			env[e.Key] = e.Template // will be expanded later with service vars
		case e.Ask:
			if v, ok := userVars[e.Key]; ok {
				env[e.Key] = v
			}
		}
	}

	return env
}

// expandServiceVars replaces ${VAR} references with actual service env values.
func expandServiceVars(tmpl string, serviceEnvs map[string]map[string]string) string {
	result := tmpl
	for _, svcEnv := range serviceEnvs {
		for k, v := range svcEnv {
			result = strings.ReplaceAll(result, "${"+k+"}", v)
		}
	}
	return result
}

// findCompatibleSharedService returns the name of an existing shared service
// that matches the given image type, or "" if none exists.
func findCompatibleSharedService(st *state.State, image string) string {
	targetType := detectServiceType(image)
	if targetType == "unknown" {
		return ""
	}
	for name, svc := range st.Services {
		if detectServiceType(svc.Image) == targetType && svc.Status == "running" {
			return name
		}
	}
	return ""
}

// waitForHealth polls a container's health endpoint.
func waitForHealth(docker *remote.Docker, container, path string, retries int) bool {
	if retries == 0 {
		retries = 5
	}
	for i := 0; i < retries; i++ {
		time.Sleep(3 * time.Second)
		out, err := docker.Exec(container, fmt.Sprintf("wget -qO- http://localhost%s || true", neossh.ShellQuote(path)))
		if err == nil && out != "" {
			return true
		}
	}
	return false
}
