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
		Image:   "mysql:8.4",
		Port:    3306,
		RootEnv: "MYSQL_ROOT_PASSWORD",
		Volumes: map[string]string{"svc-mysql": "/var/lib/mysql"},
	},
	{
		Name:    "postgres",
		Image:   "postgres:17-alpine",
		Port:    5432,
		RootEnv: "POSTGRES_PASSWORD",
		Volumes: map[string]string{"svc-postgres": "/var/lib/postgresql/data"},
	},
	{
		Name:    "redis",
		Image:   "redis:7.2-alpine",
		Port:    6379,
		Volumes: map[string]string{"svc-redis": "/data"},
	},
	{
		Name:    "mariadb",
		Image:   "mariadb:11.4",
		Port:    3306,
		RootEnv: "MARIADB_ROOT_PASSWORD",
		Volumes: map[string]string{"svc-mariadb": "/var/lib/mysql"},
	},
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage shared services (databases, caches)",
		Long:  "Create and manage shared services that multiple apps can connect to.",
	}

	cmd.AddCommand(
		newServiceCreateCmd(),
		newServiceListCmd(),
		newServiceInfoCmd(),
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

func newServiceInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <service>",
		Short: "Show connection details for a shared service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceInfo(args[0])
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

	// Generate root password
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

	// Save state (before DB creation so info is persisted even if DB step fails)
	svcState := state.SharedService{
		Name:        svcName,
		Image:       svcType.Image,
		Status:      "running",
		Env:         svcEnv,
		Volumes:     svcType.Volumes,
		Port:        svcType.Port,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	st.Services[svcName] = svcState
	state.Save(exec, st)

	// Create a default database so the service is immediately usable
	defaultDB := createDefaultDatabase(docker, containerName, svcType.Name, svcName, svcEnv)

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Shared " + svcType.Name + " ready")
	card.Blank()
	card.AddKV("Container", containerName)
	card.AddKV("Image", svcType.Image)
	card.Blank()
	printConnInfoLines(card, st.Services[svcName], defaultDB)
	card.Render()

	return nil
}

// createDefaultDatabase waits for the service to accept connections, then creates
// a default database named after the service. Returns the database name (or "").
func createDefaultDatabase(docker *remote.Docker, containerName, svcTypeName, svcName string, env map[string]string) string {
	dbName := strings.ReplaceAll(sanitizeName(svcName), "-", "_") + "_db"

	spin := ui.NewSpinner("Waiting for " + svcTypeName + " to be ready...")
	spin.Start()

	var ready bool
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		var pingCmd string
		switch svcTypeName {
		case "mysql", "mariadb":
			rootPass := env["MYSQL_ROOT_PASSWORD"]
			if rootPass == "" {
				rootPass = env["MARIADB_ROOT_PASSWORD"]
			}
			pingCmd = fmt.Sprintf(`mysql -uroot -p'%s' -e "SELECT 1" 2>/dev/null`, safeSQLValue(rootPass))
		case "postgres":
			pingCmd = `psql -U postgres -c "SELECT 1" -q 2>/dev/null`
		default:
			ready = true // redis etc — no DB to create
		}
		if pingCmd == "" {
			break
		}
		if _, err := docker.Exec(containerName, pingCmd); err == nil {
			ready = true
			break
		}
	}
	spin.Stop()

	if !ready {
		ui.Info(svcTypeName + " not ready after 60s — skipping default database creation")
		return ""
	}

	switch svcTypeName {
	case "mysql", "mariadb":
		rootPass := env["MYSQL_ROOT_PASSWORD"]
		if rootPass == "" {
			rootPass = env["MARIADB_ROOT_PASSWORD"]
		}
		createDB := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE DATABASE IF NOT EXISTS %s;" 2>/dev/null`, safeSQLValue(rootPass), dbName)
		if _, err := docker.Exec(containerName, createDB); err != nil {
			ui.Info("Could not create default database — create it manually with: CREATE DATABASE " + dbName)
			return ""
		}
		ui.Success(fmt.Sprintf("Default database %q created", dbName))
		return dbName

	case "postgres":
		createDB := fmt.Sprintf(`psql -U postgres -c "CREATE DATABASE %s;" 2>/dev/null`, dbName)
		if _, err := docker.Exec(containerName, createDB); err != nil {
			ui.Info("Could not create default database — create it manually with: CREATE DATABASE " + dbName)
			return ""
		}
		ui.Success(fmt.Sprintf("Default database %q created", dbName))
		return dbName
	}

	return ""
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

	fmt.Println("  " + fmt.Sprintf("%-18s %-25s %-12s %s", "NAME", "IMAGE", "STATUS", "HOST:PORT"))
	fmt.Println("  " + ui.Faint.Render("──────────────────────────────────────────────────────────────────────────"))

	for _, svc := range st.Services {
		bullet := ui.StatusBullet(svc.Status)
		hostPort := fmt.Sprintf("%s:%d", config.SvcContainerShared(svc.Name), svc.Port)
		fmt.Printf("  %s %-17s %-25s %-12s %s\n", bullet, svc.Name, ui.Faint.Render(svc.Image), svc.Status, ui.Faint.Render(hostPort))
	}

	fmt.Println()
	fmt.Printf("  %s\n", ui.Faint.Render("Run 'neo service info <name>' to see credentials"))
	fmt.Println()
	return nil
}

// runServiceInfo shows connection details for a shared service.
func runServiceInfo(svcName string) error {
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
		ui.Error(fmt.Sprintf("Service %q not found", svcName))
		return nil
	}

	card := ui.NewCard()
	card.Add(ui.Bold.Render(svc.Name) + "  " + ui.Faint.Render(svc.Image))
	card.Add(fmt.Sprintf("Status: %s %s", ui.StatusBullet(svc.Status), svc.Status))
	card.Blank()
	printConnInfoLines(card, svc, "")
	card.Render()

	return nil
}

// printConnInfoLines adds connection detail lines to a card based on service type.
// defaultDB is the auto-created database name (empty string if none).
func printConnInfoLines(card *ui.Card, svc state.SharedService, defaultDB string) {
	containerName := config.SvcContainerShared(svc.Name)
	svcType := detectServiceType(svc.Image)

	card.Add("Connection details (within Docker network):")
	card.AddKV("  Host", containerName)
	card.AddKV("  Port", fmt.Sprintf("%d", svc.Port))

	dbPlaceholder := "<your_db>"
	if defaultDB != "" {
		dbPlaceholder = defaultDB
	}

	switch svcType {
	case "mysql", "mariadb":
		rootPass := svc.Env["MYSQL_ROOT_PASSWORD"]
		if rootPass == "" {
			rootPass = svc.Env["MARIADB_ROOT_PASSWORD"]
		}
		card.AddKV("  User", "root")
		card.AddKV("  Password", rootPass)
		if defaultDB != "" {
			card.AddKV("  Database", defaultDB)
		}
		card.AddKV("  URL", fmt.Sprintf("mysql://root:%s@%s:3306/%s", rootPass, containerName, dbPlaceholder))
	case "postgres":
		rootPass := svc.Env["POSTGRES_PASSWORD"]
		card.AddKV("  User", "postgres")
		card.AddKV("  Password", rootPass)
		if defaultDB != "" {
			card.AddKV("  Database", defaultDB)
		}
		card.AddKV("  URL", fmt.Sprintf("postgres://postgres:%s@%s:5432/%s", rootPass, containerName, dbPlaceholder))
	case "redis":
		card.AddKV("  URL", fmt.Sprintf("redis://%s:6379", containerName))
	}
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

	if _, ok := st.Services[svcName]; !ok {
		ui.Error(fmt.Sprintf("Shared service %q not found", svcName))
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

// safeSQLValue escapes a value for use in single-quoted SQL strings.
func safeSQLValue(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// serviceRootEnvKey returns the root password env var name for a service type.
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
