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
	Name     string
	Image    string
	Port     int
	RootEnv  string            // env var for root password
	DBEnv    string            // env var for auto-creating a default database on container start
	ExtraEnv map[string]string // additional env vars always set at container creation
	Volumes  map[string]string
}

var serviceTypes = []serviceType{
	{
		Name:    "mysql",
		Image:   "mysql:8.4",
		Port:    3306,
		RootEnv: "MYSQL_ROOT_PASSWORD",
		DBEnv:   "MYSQL_DATABASE",
		// Allow root connections from any host in the Docker network (default is localhost-only)
		ExtraEnv: map[string]string{"MYSQL_ROOT_HOST": "%"},
		Volumes:  map[string]string{"svc-mysql": "/var/lib/mysql"},
	},
	{
		Name:    "postgres",
		Image:   "postgres:17-alpine",
		Port:    5432,
		RootEnv: "POSTGRES_PASSWORD",
		DBEnv:   "POSTGRES_DB",
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
		DBEnv:   "MARIADB_DATABASE",
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

	// Auto-create a default database via the image's built-in env var support
	var defaultDB string
	if svcType.DBEnv != "" {
		defaultDB = strings.ReplaceAll(sanitizeName(svcName), "-", "_") + "_db"
		svcEnv[svcType.DBEnv] = defaultDB
	}

	// Apply any additional static env vars defined for this service type
	for k, v := range svcType.ExtraEnv {
		svcEnv[k] = v
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

	// Save state
	svcState := state.SharedService{
		Name:      svcName,
		Image:     svcType.Image,
		Status:    "running",
		Env:       svcEnv,
		Volumes:   svcType.Volumes,
		Port:      svcType.Port,
		DefaultDB: defaultDB,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	st.Services[svcName] = svcState
	if err := state.Save(exec, st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Shared " + svcType.Name + " ready")
	card.Blank()
	card.AddKV("Container", containerName)
	card.AddKV("Image", svcType.Image)
	card.Blank()
	printConnInfoLines(card, svcState, defaultDB)
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
	printConnInfoLines(card, svc, svc.DefaultDB)
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
