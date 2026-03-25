package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/app"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// serviceType defines a creatable shared service template.
type serviceType struct {
	Name    string
	Image   string
	Port    int
	RootEnv string // env var for root password
	Volumes map[string]string
}

var serviceTypes = []serviceType{
	{
		Name:    "mysql",
		Image:   "mysql:8.0",
		Port:    3306,
		RootEnv: "MYSQL_ROOT_PASSWORD",
		Volumes: map[string]string{"svc-mysql": "/var/lib/mysql"},
	},
	{
		Name:    "postgres",
		Image:   "postgres:16-alpine",
		Port:    5432,
		RootEnv: "POSTGRES_PASSWORD",
		Volumes: map[string]string{"svc-postgres": "/var/lib/postgresql/data"},
	},
	{
		Name:    "redis",
		Image:   "redis:7-alpine",
		Port:    6379,
		Volumes: map[string]string{"svc-redis": "/data"},
	},
	{
		Name:    "mariadb",
		Image:   "mariadb:11",
		Port:    3306,
		RootEnv: "MARIADB_ROOT_PASSWORD",
		Volumes: map[string]string{"svc-mariadb": "/var/lib/mysql"},
	},
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage shared services (databases, caches)",
		Long:  "Create and manage shared services that can be linked to multiple apps.",
	}

	cmd.AddCommand(
		newServiceCreateCmd(),
		newServiceListCmd(),
		newServiceLinkCmd(),
		newServiceUnlinkCmd(),
		newServiceStartCmd(),
		newServiceStopCmd(),
		newServiceRestartCmd(),
		newServiceRemoveCmd(),
		newServiceLogsCmd(),
	)

	return cmd
}

func newServiceCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create [type] [name]",
		Short: "Create a shared service",
		Long:  "Create a shared service (mysql, postgres, redis, mariadb). If no name given, uses the type as the name.",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var typeName, svcName string
			if len(args) >= 1 {
				typeName = args[0]
			}
			if len(args) >= 2 {
				svcName = args[1]
			}
			return runServiceCreate(typeName, svcName)
		},
	}
}

func newServiceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List shared services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceList()
		},
	}
}

func newServiceLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link <service> <app>",
		Short: "Link a shared service to an app",
		Long:  "Creates a database and user for the app, injects connection env vars.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceLink(args[0], args[1])
		},
	}
}

func newServiceUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <service> <app>",
		Short: "Unlink a shared service from an app",
		Long:  "Removes injected env vars from the app. Database and data are preserved.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceUnlink(args[0], args[1])
		},
	}
}

func newServiceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <service>",
		Short: "Start a stopped shared service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceManage(args[0], "start")
		},
	}
}

func newServiceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <service>",
		Short: "Stop a shared service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceManage(args[0], "stop")
		},
	}
}

func newServiceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart a shared service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceManage(args[0], "restart")
		},
	}
}

func newServiceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <service>",
		Short: "Remove a shared service (keeps data volumes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceRemove(args[0])
		},
	}
}

func newServiceLogsCmd() *cobra.Command {
	var (
		tail   int
		follow bool
	)

	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream shared service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceLogs(args[0], tail, follow)
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 100, "number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return cmd
}

// runServiceCreate creates a new shared service on the server.
func runServiceCreate(typeName, svcName string) error {
	// If no type given, show interactive picker
	if typeName == "" {
		opts := make([]ui.SelectOption, len(serviceTypes))
		for i, st := range serviceTypes {
			opts[i] = ui.SelectOption{fmt.Sprintf("%-12s %s", st.Name, ui.Faint.Render(st.Image)), st.Name}
		}
		typeName = ui.Select("Choose a service type", opts)
		if typeName == "" {
			return nil
		}
	}

	// Find the service type definition
	var svcType *serviceType
	for _, st := range serviceTypes {
		if st.Name == typeName {
			svcType = &st
			break
		}
	}
	if svcType == nil {
		ui.Error(fmt.Sprintf("Unknown service type %q. Available: mysql, postgres, redis, mariadb", typeName))
		return nil
	}

	if svcName == "" {
		svcName = svcType.Name
	}

	// Connect to server
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Check if service name already exists
	if _, exists := st.Services[svcName]; exists {
		ui.Error(fmt.Sprintf("Service %q already exists", svcName))
		return nil
	}

	fmt.Println()
	fmt.Printf("  Creating shared %s service %s\n", ui.Bold.Render(svcType.Name), ui.Faint.Render(svcName))
	fmt.Printf("  Server: %s (%s)\n", srv.Name, srv.Host)
	fmt.Println()

	docker := remote.NewDocker(exec)
	containerName := config.SvcContainerShared(svcName)

	// Pull image
	spin := ui.NewSpinner(fmt.Sprintf("Pulling %s...", svcType.Image))
	spin.Start()
	if err := docker.Pull(svcType.Image); err != nil {
		spin.Stop()
		return fmt.Errorf("pull %s: %w", svcType.Image, err)
	}
	spin.Stop()
	ui.Success(fmt.Sprintf("Pulled %s", svcType.Image))

	// Generate env vars (root password, etc.)
	svcEnv := make(map[string]string)
	if svcType.RootEnv != "" {
		rootPass, err := app.GenerateValue("hex:32")
		if err != nil {
			return err
		}
		svcEnv[svcType.RootEnv] = rootPass
	}

	// Build volumes
	var volumes []string
	for _, containerPath := range svcType.Volumes {
		// Use service name in volume name for uniqueness
		actualVolName := svcName + "-data"
		volumes = append(volumes, fmt.Sprintf("%s:%s", actualVolName, containerPath))
	}

	// Start container
	_, err = docker.Run(remote.RunOpts{
		Name:    containerName,
		Image:   svcType.Image,
		Network: config.DockerNetwork,
		Restart: "unless-stopped",
		Volumes: volumes,
		Env:     svcEnv,
	})
	if err != nil {
		return fmt.Errorf("start %s: %w", containerName, err)
	}

	// Save state
	st.Services[svcName] = state.SharedService{
		Name:       svcName,
		Image:      svcType.Image,
		Status:     "running",
		Env:        svcEnv,
		Volumes:    svcType.Volumes,
		Port:       svcType.Port,
		LinkedApps: make(map[string]state.Link),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	state.Save(exec, st)

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Shared " + svcType.Name + " service created")
	card.Blank()
	card.AddKV("Container", containerName)
	card.AddKV("Image", svcType.Image)
	card.Blank()
	card.Add("Link to an app:")
	card.Add(fmt.Sprintf("  %s", ui.Bold.Render("neo service link "+svcName+" <app>")))
	card.Render()

	return nil
}

// runServiceList lists all shared services.
func runServiceList() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	fmt.Println()
	fmt.Printf("  Server: %s (%s)\n", ui.Bold.Render(srv.Name), srv.Host)
	fmt.Println()

	if len(st.Services) == 0 {
		fmt.Println("  No shared services.")
		fmt.Println()
		ui.Info("Create one: neo service create")
		fmt.Println()
		return nil
	}

	fmt.Println("  " + fmt.Sprintf("%-18s %-25s %-12s %s", "NAME", "IMAGE", "STATUS", "LINKED APPS"))
	fmt.Println("  " + ui.Faint.Render("──────────────────────────────────────────────────────────────────────────"))

	for _, svc := range st.Services {
		bullet := ui.StatusBullet(svc.Status)
		linked := "—"
		if len(svc.LinkedApps) > 0 {
			names := make([]string, 0, len(svc.LinkedApps))
			for appName := range svc.LinkedApps {
				names = append(names, appName)
			}
			linked = strings.Join(names, ", ")
		}
		fmt.Printf("  %s %-17s %-25s %-12s %s\n", bullet, svc.Name, ui.Faint.Render(svc.Image), svc.Status, linked)
	}

	fmt.Println()
	return nil
}

// runServiceLink links a shared service to an app by creating a database+user and injecting env vars.
func runServiceLink(svcName, appName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	svc, ok := st.Services[svcName]
	if !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
		return nil
	}

	appState, ok := st.Apps[appName]
	if !ok {
		ui.Error(fmt.Sprintf("App %q not found", appName))
		return nil
	}

	if _, already := svc.LinkedApps[appName]; already {
		ui.Error(fmt.Sprintf("%s is already linked to %s", svcName, appName))
		return nil
	}

	docker := remote.NewDocker(exec)
	containerName := config.SvcContainerShared(svcName)

	// Determine the service type from the image
	svcTypeName := detectServiceType(svc.Image)

	link := state.Link{
		EnvVars: make(map[string]string),
	}

	switch svcTypeName {
	case "mysql", "mariadb":
		dbName := strings.ReplaceAll(sanitizeName(appName), "-", "_") + "_db"
		dbUser := strings.ReplaceAll(sanitizeName(appName), "-", "_")
		dbPass, _ := app.GenerateValue("hex:32")

		// Validate identifiers are safe for SQL
		if _, err := safeSQLIdentifier(dbName); err != nil {
			return fmt.Errorf("invalid database name: %w", err)
		}
		if _, err := safeSQLIdentifier(dbUser); err != nil {
			return fmt.Errorf("invalid user name: %w", err)
		}

		rootPass := svc.Env[serviceRootEnvKey(svcTypeName)]

		// Wait a moment for the service to be ready, then create database + user
		spin := ui.NewSpinner(fmt.Sprintf("Creating database %s...", dbName))
		spin.Start()

		// Create database
		createDB := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE DATABASE IF NOT EXISTS %s;"`, safeSQLValue(rootPass), dbName)
		if _, err := docker.Exec(containerName, createDB); err != nil {
			spin.Stop()
			return fmt.Errorf("create database: %w", err)
		}

		// Create user
		createUser := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%'; FLUSH PRIVILEGES;"`,
			safeSQLValue(rootPass), dbUser, safeSQLValue(dbPass), dbName, dbUser)
		if _, err := docker.Exec(containerName, createUser); err != nil {
			spin.Stop()
			return fmt.Errorf("create user: %w", err)
		}
		spin.Stop()

		link.Database = dbName
		link.User = dbUser
		link.EnvVars["DATABASE_URL"] = fmt.Sprintf("mysql://%s:%s@%s:3306/%s", dbUser, dbPass, containerName, dbName)
		link.EnvVars["DB_HOST"] = containerName
		link.EnvVars["DB_PORT"] = "3306"
		link.EnvVars["DB_DATABASE"] = dbName
		link.EnvVars["DB_USERNAME"] = dbUser
		link.EnvVars["DB_PASSWORD"] = dbPass

	case "postgres":
		dbName := strings.ReplaceAll(sanitizeName(appName), "-", "_") + "_db"
		dbUser := strings.ReplaceAll(sanitizeName(appName), "-", "_")
		dbPass, _ := app.GenerateValue("hex:32")

		// Validate identifiers are safe for SQL
		if _, err := safeSQLIdentifier(dbName); err != nil {
			return fmt.Errorf("invalid database name: %w", err)
		}
		if _, err := safeSQLIdentifier(dbUser); err != nil {
			return fmt.Errorf("invalid user name: %w", err)
		}

		spin := ui.NewSpinner(fmt.Sprintf("Creating database %s...", dbName))
		spin.Start()

		// Create user + database
		createUser := fmt.Sprintf(`psql -U postgres -c "CREATE USER %s WITH PASSWORD '%s';" 2>/dev/null; true`, dbUser, safeSQLValue(dbPass))
		if _, err := docker.Exec(containerName, createUser); err != nil {
			spin.Stop()
			return fmt.Errorf("create user: %w", err)
		}

		createDB := fmt.Sprintf(`psql -U postgres -c "CREATE DATABASE %s OWNER %s;" 2>/dev/null; true`, dbName, dbUser)
		if _, err := docker.Exec(containerName, createDB); err != nil {
			spin.Stop()
			return fmt.Errorf("create database: %w", err)
		}
		spin.Stop()

		link.Database = dbName
		link.User = dbUser
		link.EnvVars["DATABASE_URL"] = fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, dbPass, containerName, dbName)
		link.EnvVars["DB_HOST"] = containerName
		link.EnvVars["DB_PORT"] = "5432"
		link.EnvVars["DB_DATABASE"] = dbName
		link.EnvVars["DB_USERNAME"] = dbUser
		link.EnvVars["DB_PASSWORD"] = dbPass

	case "redis":
		// Redis uses DB numbers, assign the next available
		dbNum := len(svc.LinkedApps)
		link.EnvVars["REDIS_URL"] = fmt.Sprintf("redis://%s:6379/%d", containerName, dbNum)
		link.EnvVars["REDIS_HOST"] = containerName
		link.EnvVars["REDIS_PORT"] = "6379"
		link.EnvVars["REDIS_DB"] = fmt.Sprintf("%d", dbNum)

	default:
		ui.Error(fmt.Sprintf("Don't know how to link service type %q. Set env vars manually with neo env set.", svcTypeName))
		return nil
	}

	// Inject env vars into the app
	if appState.Env == nil {
		appState.Env = make(map[string]string)
	}
	for k, v := range link.EnvVars {
		appState.Env[k] = v
	}

	// Update state
	svc.LinkedApps[appName] = link
	st.Services[svcName] = svc
	st.Apps[appName] = appState
	state.Save(exec, st)

	// Restart app to pick up new env vars
	if appState.Status == "running" {
		appContainer := config.AppContainer(appName)
		spin := ui.NewSpinner(fmt.Sprintf("Restarting %s...", appName))
		spin.Start()
		docker.Restart(appContainer)
		// Also restart workers
		for wName := range appState.Workers {
			docker.Restart(fmt.Sprintf("app-%s-worker-%s", appName, wName))
		}
		spin.Stop()
	}

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + fmt.Sprintf(" Linked %s → %s", svcName, appName))
	card.Blank()
	if link.Database != "" {
		card.AddKV("Database", link.Database)
		card.AddKV("User", link.User)
	}
	card.Add("Injected env vars:")
	for k := range link.EnvVars {
		card.Add("  " + k)
	}
	card.Render()

	return nil
}

// runServiceUnlink removes the link between a shared service and an app.
func runServiceUnlink(svcName, appName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	svc, ok := st.Services[svcName]
	if !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
		return nil
	}

	link, linked := svc.LinkedApps[appName]
	if !linked {
		ui.Error(fmt.Sprintf("%s is not linked to %s", svcName, appName))
		return nil
	}

	appState, appOk := st.Apps[appName]

	// Remove injected env vars from the app
	if appOk {
		for k := range link.EnvVars {
			delete(appState.Env, k)
		}
		st.Apps[appName] = appState
	}

	// Remove link from service
	delete(svc.LinkedApps, appName)
	st.Services[svcName] = svc
	state.Save(exec, st)

	// Restart app if running
	if appOk && appState.Status == "running" {
		docker := remote.NewDocker(exec)
		docker.Restart(config.AppContainer(appName))
		for wName := range appState.Workers {
			docker.Restart(fmt.Sprintf("app-%s-worker-%s", appName, wName))
		}
	}

	ui.Success(fmt.Sprintf("Unlinked %s from %s. Database and data preserved.", svcName, appName))
	return nil
}

// runServiceManage starts/stops/restarts a shared service.
func runServiceManage(svcName, action string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	svc, ok := st.Services[svcName]
	if !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
		return nil
	}

	docker := remote.NewDocker(exec)
	containerName := config.SvcContainerShared(svcName)

	spin := ui.NewSpinner(fmt.Sprintf("%sing %s...", action, svcName))
	spin.Start()

	var actionErr error
	switch action {
	case "start":
		actionErr = docker.Start(containerName)
		svc.Status = "running"
	case "stop":
		if len(svc.LinkedApps) > 0 {
			spin.Stop()
			names := make([]string, 0, len(svc.LinkedApps))
			for appName := range svc.LinkedApps {
				names = append(names, appName)
			}
			var confirm bool
			huh.NewConfirm().
				Title(fmt.Sprintf("%s is linked to %s. Stop anyway?", svcName, strings.Join(names, ", "))).
				Affirmative("Yes, stop").
				Negative("Cancel").
				Value(&confirm).
				Run() //nolint:errcheck
			if !confirm {
				return nil
			}
			spin = ui.NewSpinner(fmt.Sprintf("Stopping %s...", svcName))
			spin.Start()
		}
		actionErr = docker.Stop(containerName)
		svc.Status = "stopped"
	case "restart":
		actionErr = docker.Restart(containerName)
		svc.Status = "running"
	}

	spin.Stop()

	if actionErr != nil {
		ui.Error(fmt.Sprintf("Failed to %s %s: %s", action, svcName, actionErr))
		return nil
	}

	st.Services[svcName] = svc
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("%s %sed", svcName, action))
	return nil
}

// runServiceRemove removes a shared service container.
func runServiceRemove(svcName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	svc, ok := st.Services[svcName]
	if !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
		return nil
	}

	if len(svc.LinkedApps) > 0 {
		names := make([]string, 0, len(svc.LinkedApps))
		for appName := range svc.LinkedApps {
			names = append(names, appName)
		}
		ui.Error(fmt.Sprintf("Cannot remove %s — still linked to: %s", svcName, strings.Join(names, ", ")))
		ui.Info("Unlink all apps first: neo service unlink " + svcName + " <app>")
		return nil
	}

	var confirm bool
	huh.NewConfirm().
		Title(fmt.Sprintf("Remove shared service %s? Data volumes will be kept.", svcName)).
		Affirmative("Yes, remove").
		Negative("Cancel").
		Value(&confirm).
		Run() //nolint:errcheck
	if !confirm {
		return nil
	}

	docker := remote.NewDocker(exec)
	containerName := config.SvcContainerShared(svcName)

	spin := ui.NewSpinner(fmt.Sprintf("Removing %s...", svcName))
	spin.Start()
	docker.Remove(containerName)
	spin.Stop()

	delete(st.Services, svcName)
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("%s removed. Data volumes preserved on server.", svcName))
	return nil
}

// runServiceLogs streams logs from a shared service.
func runServiceLogs(svcName string, tail int, follow bool) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	if _, ok := st.Services[svcName]; !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
		return nil
	}

	docker := remote.NewDocker(exec)
	return docker.Logs(config.SvcContainerShared(svcName), tail, follow, os.Stdout)
}

// safeSQLIdentifier validates and returns a safe SQL identifier (alphanumeric and underscores only).
// Returns an error if the name contains unsafe characters.
func safeSQLIdentifier(name string) (string, error) {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return "", fmt.Errorf("invalid SQL identifier character %q in %q", string(c), name)
		}
	}
	if name == "" {
		return "", fmt.Errorf("empty SQL identifier")
	}
	return name, nil
}

// safeSQLValue escapes a value for use in single-quoted SQL strings.
func safeSQLValue(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// detectServiceType determines the service type from a Docker image name.
func detectServiceType(image string) string {
	lower := strings.ToLower(image)
	switch {
	case strings.Contains(lower, "mysql"):
		return "mysql"
	case strings.Contains(lower, "mariadb"):
		return "mariadb"
	case strings.Contains(lower, "postgres"):
		return "postgres"
	case strings.Contains(lower, "redis"):
		return "redis"
	default:
		return "unknown"
	}
}

// serviceRootEnvKey returns the root password env var for a service type.
func serviceRootEnvKey(svcType string) string {
	switch svcType {
	case "mysql":
		return "MYSQL_ROOT_PASSWORD"
	case "mariadb":
		return "MARIADB_ROOT_PASSWORD"
	case "postgres":
		return "POSTGRES_PASSWORD"
	default:
		return ""
	}
}

