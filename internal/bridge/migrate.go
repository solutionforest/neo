package bridge

import (
	"fmt"
	"strings"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// MigrationPlan describes how to migrate a neo server to Vxero.
type MigrationPlan struct {
	ServerName string
	ServerIP   string
	Apps       []AppMigration
	Services   []ServiceMigration
	Warnings   []string
}

// AppMigration describes how to migrate a single app.
type AppMigration struct {
	Name         string
	Image        string
	Domain       string
	Port         int
	EnvVars      map[string]string
	Volumes      []string
	NeedsCluster bool   // true — deployed via K3s
	Notes        string // migration notes
}

// ServiceMigration describes a database service to create in Vxero.
type ServiceMigration struct {
	AppName     string // which app this belongs to
	ServiceName string // "postgres", "mysql", etc.
	Image       string // original Docker image
	VxeroType   string // Vxero ServiceType value
	NeedsData   bool   // volume data needs migration
	VolumeName  string // Docker volume name
}

// BuildMigrationPlan analyzes the current neo state and creates a migration plan.
func BuildMigrationPlan(st *state.State) *MigrationPlan {
	plan := &MigrationPlan{
		ServerIP: st.ServerIP,
	}

	for _, app := range st.Apps {
		appMigration := AppMigration{
			Name:         app.Name,
			Image:        app.Image,
			Domain:       app.Domain,
			Port:         app.InternalPort,
			EnvVars:      app.Env,
			NeedsCluster: true,
		}

		// Collect volume names
		for name := range app.Volumes {
			appMigration.Volumes = append(appMigration.Volumes, name)
		}

		// Check for migration caveats
		if len(app.Volumes) > 0 {
			appMigration.Notes = "Has persistent volumes — data will need manual migration"
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s: has %d volume(s) that need data migration",
				app.Name, len(app.Volumes),
			))
		}

		// Map bundled services (Docker containers) to Vxero managed services
		for svcName, svc := range app.Services {
			vxeroType := mapServiceType(svcName, svc.Image)
			if vxeroType != "" {
				plan.Services = append(plan.Services, ServiceMigration{
					AppName:     app.Name,
					ServiceName: svcName,
					Image:       svc.Image,
					VxeroType:   vxeroType,
					NeedsData:   true,
				})
			} else {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"%s: service %q (%s) has no Vxero equivalent — will need manual setup",
					app.Name, svcName, svc.Image,
				))
			}
		}

		plan.Apps = append(plan.Apps, appMigration)
	}

	return plan
}

// mapServiceType maps a Docker service image to a Vxero ServiceType.
func mapServiceType(name, image string) string {
	imageLower := strings.ToLower(image)
	nameLower := strings.ToLower(name)

	switch {
	case strings.Contains(imageLower, "postgres"):
		return "postgresql"
	case strings.Contains(imageLower, "mysql") || strings.Contains(imageLower, "mariadb"):
		return "mysql"
	case strings.Contains(imageLower, "redis"):
		return "redis"
	case strings.Contains(imageLower, "mongo"):
		return "mongodb"
	case strings.Contains(imageLower, "elasticsearch"):
		return "elasticsearch"
	case strings.Contains(nameLower, "postgres"):
		return "postgresql"
	case strings.Contains(nameLower, "mysql"):
		return "mysql"
	case strings.Contains(nameLower, "redis"):
		return "redis"
	}

	return ""
}

// ExecuteMigration runs the migration plan against the Vxero API.
// It returns a summary of what was created and any errors encountered.
func ExecuteMigration(
	client *VxeroClient,
	sshExec *ssh.Executor,
	plan *MigrationPlan,
	clusterID int,
	onProgress func(step, detail string),
) error {
	// Step 1: Register the server with Vxero
	onProgress("server", "Registering server with Vxero...")

	serverResp, err := client.CreateServer(plan.ServerName, plan.ServerIP, 22)
	if err != nil {
		return fmt.Errorf("register server: %w", err)
	}

	// Step 2: Install the Vxero agent on the server
	onProgress("agent", "Installing Vxero agent...")

	installCmd := fmt.Sprintf("curl -fsSL %s | sh", config.AgentInstallURL())
	if err := sshExec.RunQuiet(installCmd); err != nil {
		return fmt.Errorf("install agent: %w", err)
	}

	// Register the agent with the install command from the API response
	if serverResp.InstallCommand != "" {
		if err := sshExec.RunQuiet(serverResp.InstallCommand); err != nil {
			// Fall back to manual registration
			registerCmd := fmt.Sprintf("vxero-agent register --url %s --token %s",
				client.BaseURL, client.Token)
			if err := sshExec.RunQuiet(registerCmd); err != nil {
				return fmt.Errorf("register agent: %w", err)
			}
		}
	}

	if err := sshExec.RunQuiet("systemctl enable --now vxero-agent"); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	// Step 3: Create managed services for databases
	serviceCredentials := make(map[string]map[string]string) // appName-svcName → credentials

	for _, svc := range plan.Services {
		onProgress("service", fmt.Sprintf("Creating managed %s for %s...", svc.VxeroType, svc.AppName))

		svcName := fmt.Sprintf("%s-%s", svc.AppName, svc.ServiceName)
		vxeroSvc, err := client.CreateService(clusterID, svcName, svc.VxeroType)
		if err != nil {
			onProgress("warning", fmt.Sprintf("Failed to create %s: %s (skipping)", svcName, err))
			continue
		}

		// Fetch credentials for env var replacement
		creds, err := client.GetServiceCredentials(clusterID, vxeroSvc.ID)
		if err == nil {
			key := svc.AppName + "-" + svc.ServiceName
			serviceCredentials[key] = creds
		}
	}

	// Step 4: Update remote state to mark as connected
	onProgress("state", "Updating server state...")

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	st.Connected = true
	st.VxeroURL = client.BaseURL
	st.VxeroToken = client.Token
	if err := state.Save(sshExec, st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	onProgress("done", "Migration complete")
	return nil
}
