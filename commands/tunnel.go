package commands

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newTunnelCmd() *cobra.Command {
	var localPort int

	cmd := &cobra.Command{
		Use:   "tunnel <service>",
		Short: "Open an SSH tunnel to a shared service for local access",
		Long: `Opens an SSH tunnel so you can connect to a remote database from local tools
like TablePlus, DataGrip, or DBeaver.

The tunnel forwards a local port to the service container on the server.
Press Ctrl+C to close the tunnel.

Example:
  neo tunnel mysql          # tunnel MySQL on localhost:13306
  neo tunnel postgres       # tunnel Postgres on localhost:15432
  neo tunnel redis          # tunnel Redis on localhost:16379
  neo tunnel mysql --port 3307`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTunnel(args[0], localPort)
		},
	}

	cmd.Flags().IntVar(&localPort, "port", 0, "local port to listen on (default: 10000+service port)")
	return cmd
}

func runTunnel(svcName string, localPort int) error {
	cfg, srv, sshExec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer sshExec.Close()
	_ = cfg

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	svc, ok := st.Services[svcName]
	if !ok {
		ui.Error(fmt.Sprintf("Service %q not found. Run 'neo service list' to see available services.", svcName))
		return nil
	}

	if svc.Port == 0 {
		ui.Error(fmt.Sprintf("Service %q has no port configured (redis tunnels work too — try --port 16379)", svcName))
		return nil
	}

	// Resolve the container's IP on the server (Docker bridge is reachable from host)
	containerName := config.SvcContainerShared(svcName)
	containerIP, err := sshExec.Run(fmt.Sprintf(
		"docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' %s",
		containerName,
	))
	if err != nil || containerIP == "" {
		ui.Error(fmt.Sprintf("Could not get container IP for %s — is the service running?", svcName))
		return nil
	}

	// Default local port: 10000 + container port (avoids clash with local services)
	if localPort == 0 {
		localPort = 10000 + svc.Port
	}

	svcType := detectServiceType(svc.Image)

	// Build the SSH tunnel command
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}

	tunnelSpec := fmt.Sprintf("%d:%s:%d", localPort, containerIP, svc.Port)
	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs,
		"-N",          // no remote command — tunnel only
		"-L", tunnelSpec,
		srv.Host,
	)

	// Show connection card before starting
	fmt.Println()
	card := ui.NewCard()
	card.Add(ui.Bold.Render("SSH Tunnel — " + svcName))
	card.Blank()
	card.AddKV("Local port", fmt.Sprintf("%d", localPort))
	card.AddKV("Remote", fmt.Sprintf("%s:%d", containerName, svc.Port))
	card.Blank()
	card.Add("Connect with:")

	switch svcType {
	case "mysql", "mariadb":
		user := svc.Env["MYSQL_USER"]
		if user == "" {
			user = svc.Env["MARIADB_USER"]
		}
		pass := svc.Env["MYSQL_PASSWORD"]
		if pass == "" {
			pass = svc.Env["MARIADB_PASSWORD"]
		}
		if user == "" {
			user = "root"
			pass = svc.Env["MYSQL_ROOT_PASSWORD"]
			if pass == "" {
				pass = svc.Env["MARIADB_ROOT_PASSWORD"]
			}
		}
		db := svc.DefaultDB
		if db == "" {
			db = "<your_db>"
		}
		card.AddKV("  Host", "127.0.0.1")
		card.AddKV("  Port", fmt.Sprintf("%d", localPort))
		card.AddKV("  User", user)
		card.AddKV("  Password", pass)
		card.AddKV("  Database", db)
		card.AddKV("  URL", fmt.Sprintf("mysql://%s:%s@127.0.0.1:%d/%s", user, pass, localPort, db))

	case "postgres":
		pass := svc.Env["POSTGRES_PASSWORD"]
		db := svc.DefaultDB
		if db == "" {
			db = "postgres"
		}
		card.AddKV("  Host", "127.0.0.1")
		card.AddKV("  Port", fmt.Sprintf("%d", localPort))
		card.AddKV("  User", "postgres")
		card.AddKV("  Password", pass)
		card.AddKV("  Database", db)
		card.AddKV("  URL", fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/%s", pass, localPort, db))

	case "redis":
		card.AddKV("  Host", "127.0.0.1")
		card.AddKV("  Port", fmt.Sprintf("%d", localPort))
		card.AddKV("  URL", fmt.Sprintf("redis://127.0.0.1:%d", localPort))
	}

	card.Blank()
	card.Add(ui.Faint.Render("Press Ctrl+C to close the tunnel"))
	card.Render()

	// Start SSH tunnel
	proc := exec.Command(sshPath, sshArgs...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Start(); err != nil {
		return fmt.Errorf("start ssh tunnel: %w", err)
	}

	// Wait for Ctrl+C, then kill SSH
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		proc.Process.Kill() //nolint:errcheck
	}()

	proc.Wait() //nolint:errcheck
	fmt.Println()
	ui.Info("Tunnel closed.")
	return nil
}
