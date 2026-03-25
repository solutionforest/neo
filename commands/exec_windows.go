//go:build windows

package commands

import (
	"os"
	"os/exec"
)

func execSyscall(path string, args []string, env []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	return cmd.Run()
}
