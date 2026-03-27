package commands

import (
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

	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs, srv.Host)

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}

	return execSyscall(sshPath, append([]string{"ssh"}, sshArgs...), os.Environ())
}
