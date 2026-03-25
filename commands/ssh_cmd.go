package commands

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
)

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh",
		Short: "SSH into the current server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSH()
		},
	}
}

func runSSH() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	srv, err := resolveServer(cfg)
	if err != nil {
		return err
	}

	// Build ssh command args
	var sshArgs []string
	sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking=no")
	if srv.Key != "" {
		sshArgs = append(sshArgs, "-i", srv.Key)
	}
	if srv.Port != 0 && srv.Port != 22 {
		sshArgs = append(sshArgs, "-p", fmt.Sprintf("%d", srv.Port))
	}
	sshArgs = append(sshArgs, srv.Host)

	// Replace process with ssh
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}

	return execSyscall(sshPath, append([]string{"ssh"}, sshArgs...), os.Environ())
}
