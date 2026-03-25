package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

var (
	serverFlag string // --server flag for targeting a specific server
	debugFlag  bool   // --debug flag for SSH diagnostics
	cliVersion string
)

// NewRootCmd creates the root command.
func NewRootCmd(version string) *cobra.Command {
	cliVersion = version

	root := &cobra.Command{
		Use:   "neo",
		RunE:  runDashboard,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&serverFlag, "server", "", "target a specific server by name")
	root.PersistentFlags().BoolVar(&debugFlag, "debug", false, "log SSH commands for diagnostics")

	root.AddCommand(
		newInitCmd(),
		newConfigCmd(),
		newDevCmd(),
		newDeployCmd(),
		newListCmd(),
		newStatusCmd(),
		newServersCmd(),
		newUseCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newRemoveCmd(),
		newUpdateCmd(),
		newLogsCmd(),
		newDomainCmd(),
		newEnvCmd(),
		newServiceCmd(),
		newVolumesCmd(),
		newBackupCmd(),
		newRestoreCmd(),
		newSyncCmd(),
		newConnectCmd(),
		newSSHCmd(),
		newVersionCmd(),
		newUpgradeCmd(),
		newHelpCmd(),
		newAskCmd(),
	)

	// Replace default help template with grouped commands
	root.SetUsageTemplate(helpTemplate())

	return root
}

func newHelpCmd() *cobra.Command {
	var llmFlag bool

	cmd := &cobra.Command{
		Use:   "help",
		Short: "Show all commands and usage",
		Run: func(cmd *cobra.Command, args []string) {
			if llmFlag {
				printHelpLLM()
			} else {
				printHelp()
			}
		},
	}

	cmd.Flags().BoolVar(&llmFlag, "llm", false, "output plain-text command reference for LLMs (no colors, structured)")
	return cmd
}

func printHelp() {
	type entry struct {
		cmd  string
		desc string
	}
	type group struct {
		title   string
		entries []entry
	}

	groups := []group{
		{
			title: "Getting Started",
			entries: []entry{
				{"neo", "Launch interactive dashboard"},
				{"neo init <user@host>", "Initialize a remote server"},
				{"neo config generate", "Generate .neo.yml from docker-compose.yml"},
				{"neo help", "Show this help"},
			},
		},
		{
			title: "Development",
			entries: []entry{
				{"neo dev", "Run app locally (wraps docker compose)"},
				{"neo dev --build", "Rebuild and run locally"},
				{"neo dev down", "Stop local development containers"},
			},
		},
		{
			title: "Apps",
			entries: []entry{
				{"neo deploy [path]", "Deploy a local Dockerfile project"},
				{"neo list", "List all apps on the server"},
				{"neo logs <app>", "Stream app container logs"},
				{"neo domain <app> <domain>", "Set or change the domain for an app"},
				{"neo domain <app> --temp", "Assign a temporary sslip.io domain with SSL"},
				{"neo sync <app>", "Sync server state back to .neo.yml"},
				{"neo env <app>", "View environment variables"},
				{"neo env set <app> K=V", "Set environment variables"},
				{"neo env unset <app> KEY", "Remove environment variables"},
				{"neo env import <app> .env", "Import from .env file"},
			},
		},
		{
			title: "Shared Services",
			entries: []entry{
				{"neo service create [type]", "Create a shared service (mysql, postgres, redis)"},
				{"neo service list", "List shared services"},
				{"neo service link <svc> <app>", "Link service to app (creates DB + user)"},
				{"neo service unlink <svc> <app>", "Unlink service from app"},
				{"neo service logs <svc>", "Stream service logs"},
				{"neo service start|stop <svc>", "Start/stop a shared service"},
				{"neo service remove <svc>", "Remove a shared service"},
			},
		},
		{
			title: "Lifecycle",
			entries: []entry{
				{"neo start <app>", "Start a stopped app"},
				{"neo stop <app>", "Stop a running app"},
				{"neo restart <app>", "Restart an app"},
				{"neo update <app>", "Update an app to the latest image"},
				{"neo remove <app>", "Remove an app (keeps data volumes)"},
			},
		},
		{
			title: "Data",
			entries: []entry{
				{"neo backup <app>", "Backup an app's data volumes"},
				{"neo restore <app> <file>", "Restore an app from a backup"},
				{"neo volumes", "List Docker volumes on the server"},
			},
		},
		{
			title: "Servers",
			entries: []entry{
				{"neo servers", "List all configured servers"},
				{"neo use <name>", "Switch the active server"},
				{"neo ssh", "SSH into the current server"},
			},
		},
		{
			title: "Updates",
			entries: []entry{
				{"neo version", "Show version and check for updates"},
				{"neo upgrade", "Upgrade neo to the latest version"},
			},
		},
		{
			title: "Vxero",
			entries: []entry{
				{"neo connect", "Transfer server to Vxero (coming soon)"},
			},
		},
	}

	fmt.Println()
	fmt.Println(ui.Bold.Render("  neo") + ui.Faint.Render(" — remote server management over SSH"))
	fmt.Println()

	for _, g := range groups {
		fmt.Println("  " + ui.Bold.Render(g.title))
		for _, e := range g.entries {
			padding := 30 - len(e.cmd)
			if padding < 2 {
				padding = 2
			}
			fmt.Printf("    %s%s%s\n", ui.Green.Render(e.cmd), strings.Repeat(" ", padding), ui.Faint.Render(e.desc))
		}
		fmt.Println()
	}

	fmt.Println("  " + ui.Faint.Render("Use --server <name> to target a specific server"))
	fmt.Println("  " + ui.Faint.Render("Run neo <command> --help for detailed usage"))
	fmt.Println()
}

// printHelpLLM outputs a plain-text, machine-readable command reference for LLMs.
// No ANSI colors, no formatting — just structured text that any AI can parse.
func printHelpLLM() {
	text := `# neo — remote server management over SSH

Neo manages Docker-based applications on remote servers via SSH.
It handles deployment, SSL certificates, shared database services, and app lifecycle.

## Commands

### Setup
- neo init <user@host>          Initialize a remote server (installs Docker + Caddy)
- neo servers                   List configured servers
- neo use <name>                Switch active server
- neo ssh                       SSH into the current server

### Apps
- neo deploy [path]             Deploy a Dockerfile-based project (blue-green, zero downtime)
- neo list                      List all apps and shared services
- neo start <app>               Start a stopped app
- neo stop <app>                Stop a running app
- neo restart <app>             Restart an app
- neo update <app>              Update to latest image
- neo remove <app>              Remove app (keeps data volumes)

### Logs
- neo logs <app>                Stream app logs (--tail N, -f to follow)
- neo logs <app> -w <worker>    Stream worker logs
- neo logs <app> -s             Stream shared service logs

### Domains & SSL
- neo domain <app> <domain>     Set domain (auto-provisions SSL via Caddy)

### Environment Variables
- neo env <app>                 View env vars (secrets masked)
- neo env set <app> K=V         Set env var (auto-restarts)
- neo env unset <app> KEY       Remove env var
- neo env import <app> .env     Bulk import from file

### Shared Services
Create shared database/cache instances used by multiple apps:
- neo service create [type] [name]      Create service (mysql, postgres, redis, mariadb)
- neo service list                      List services with linked apps
- neo service link <svc> <app>          Create DB + user, inject DATABASE_URL/DB_* env vars
- neo service unlink <svc> <app>        Remove link (data preserved)
- neo service start|stop|restart <svc>  Manage lifecycle
- neo service remove <svc>              Remove (must unlink apps first)
- neo service logs <svc>                Stream logs

### Data
- neo backup <app>              Backup data volumes
- neo restore <app> <file>      Restore from backup
- neo volumes                   List Docker volumes

### Updates
- neo version                   Show version, check for updates
- neo upgrade                   Upgrade neo binary

## Global Flags
- --server <name>               Target a specific server

## Typical Workflows

### Deploy a project:
  neo init root@1.2.3.4
  neo deploy ./my-app --domain app.example.com

### Manage environment:
  neo env set ghost MAIL_HOST=smtp.example.com MAIL_PORT=587
  neo env import ghost .env.production
`
	fmt.Print(text)
}

func helpTemplate() string {
	return `Remote server management over SSH.

Usage:
  {{.UseLine}}
  neo <command> [flags]

Run 'neo help' for a full list of commands.
{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`
}

// runDashboard is defined in dashboard.go — interactive TUI entry point.

// resolveServer returns the target server from --server flag or config.Current.
// If the value contains "@" it is treated as a direct user@host address and used
// immediately without requiring a config entry.
// If the named server doesn't exist, falls back gracefully:
//   - one server configured → use it with a warning
//   - multiple servers → prompt the user to pick
func resolveServer(cfg *config.Config) (*config.Server, error) {
	name := serverFlag
	if name == "" {
		return cfg.CurrentServer()
	}

	// Direct host format (e.g. root@167.172.249.89) — no config lookup needed
	if strings.Contains(name, "@") {
		srv := config.Server{Name: name, Host: name, Port: 22}
		return &srv, nil
	}

	srv, ok := cfg.Servers[name]
	if ok {
		return &srv, nil
	}

	// Named server not found — try to recover gracefully
	servers := cfg.ServerList()
	switch len(servers) {
	case 0:
		return nil, fmt.Errorf("server %q not found and no servers are configured — run 'neo init' first", name)
	case 1:
		ui.Info(fmt.Sprintf("Server %q not found — using %q (%s)", name, servers[0].Name, servers[0].Host))
		return &servers[0], nil
	default:
		ui.Info(fmt.Sprintf("Server %q not found — pick one:", name))
		opts := make([]ui.SelectOption, len(servers))
		for i, s := range servers {
			opts[i] = ui.SelectOption{Label: fmt.Sprintf("%-15s %s", s.Name, s.Host), Value: s.Name}
		}
		chosen := ui.Select("Which server?", opts)
		if chosen == "" {
			return nil, fmt.Errorf("no server selected")
		}
		s, _ := cfg.Servers[chosen]
		return &s, nil
	}
}

// connectSSH creates and connects an SSH executor for a server.
func connectSSH(srv *config.Server) (*ssh.Executor, error) {
	exec := ssh.New(srv.Host, srv.Port)
	exec.Verbose = debugFlag
	// Load key from config if specified (e.g. test servers with ephemeral keys)
	if srv.Key != "" {
		if data, err := os.ReadFile(srv.Key); err == nil {
			exec.PrivateKey = data
		}
	}
	if err := exec.Connect(); err != nil {
		return nil, err
	}
	return exec, nil
}

// mustResolveAndConnect loads config, resolves the target server, and connects.
func mustResolveAndConnect() (*config.Config, *config.Server, *ssh.Executor, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	srv, err := resolveServer(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	exec, err := connectSSH(srv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to %s: %w", srv.Host, err)
	}
	return cfg, srv, exec, nil
}

// mustResolveAndLoadState loads config, resolves server, connects via SSH, and loads remote state.
func mustResolveAndLoadState() (*ssh.Executor, *state.State, error) {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return nil, nil, err
	}
	st, err := state.Load(exec)
	if err != nil {
		exec.Close()
		return nil, nil, err
	}
	return exec, st, nil
}

// exitErr prints an error and exits.
func exitErr(msg string) {
	ui.Error(msg)
	os.Exit(1)
}
