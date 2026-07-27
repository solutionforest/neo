package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
)

// runInteractivePty is a raw stdio mode (NOT the JSON protocol): it bridges the
// process's stdin/stdout to a remote PTY over neo's own SSH auth. The desktop
// app spawns `neo-bridge pty …` for each integrated terminal.
//
// It connects to a login shell, or to `docker exec -it <container> sh` when a
// container is given. Args: <server> <container|-> [cols] [rows].
func runInteractivePty(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: neo-bridge pty <server> <container|-> [cols] [rows]")
		return 2
	}
	server := args[0]
	container := ""
	if len(args) > 1 && args[1] != "-" && args[1] != "" {
		container = args[1]
	}
	cols := ptyArgInt(args, 2, 80)
	rows := ptyArgInt(args, 3, 24)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if server == "" {
		server = cfg.Current
	}
	srv, ok := cfg.Servers[server]
	if !ok {
		fmt.Fprintf(os.Stderr, "server not found: %s\r\n", server)
		return 1
	}

	exec := ssh.New(srv.Host, srv.Port)
	exec.NonInteractive = true // never prompt on stdin (it's the terminal stream)
	if srv.Key != "" {
		if data, e := os.ReadFile(srv.Key); e == nil {
			exec.PrivateKey = data
		}
	}
	if err := exec.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to %s: %v\r\n", srv.Host, err)
		return 1
	}
	defer exec.Close()

	cmd := ""
	if container != "" {
		cmd = fmt.Sprintf("docker exec -it %s sh", config.AppContainer(container))
	}
	if err := exec.InteractiveShell(cols, rows, cmd); err != nil {
		fmt.Fprintf(os.Stderr, "\r\nsession ended: %v\r\n", err)
		return 1
	}
	return 0
}

func ptyArgInt(args []string, i, def int) int {
	if i >= len(args) {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(args[i])); err == nil && n > 0 {
		return n
	}
	return def
}
