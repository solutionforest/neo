package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Executor runs commands on a remote server over SSH.
type Executor struct {
	Host            string // user@host
	Port            int
	Password        string // optional password for auth
	PrivateKey      []byte // optional raw PEM key (for programmatic use)
	insecureHostKey bool   // skip host key verification (for tests only)
	NonInteractive  bool   // reject unknown hosts instead of prompting (for background use)
	Verbose         bool   // log SSH commands and results to stderr
	client          *ssh.Client
	agentConn       net.Conn // SSH agent connection, closed on Close()
}

// SetInsecureHostKey disables SSH host key verification. Only use in tests.
func (e *Executor) SetInsecureHostKey() {
	e.insecureHostKey = true
}

// IsInsecureHostKey returns whether host key verification is disabled.
func (e *Executor) IsInsecureHostKey() bool {
	return e.insecureHostKey
}

// debugf writes a debug line to stderr when Verbose is enabled.
func (e *Executor) debugf(format string, args ...any) {
	if e.Verbose {
		fmt.Fprintf(os.Stderr, "[ssh] "+format+"\n", args...)
	}
}

// New creates a new SSH executor. Does not connect yet.
func New(host string, port int) *Executor {
	if port == 0 {
		port = 22
	}
	return &Executor{Host: host, Port: port}
}

// Connect establishes the SSH connection.
func (e *Executor) Connect() error {
	user, host := parseHost(e.Host)

	hkCallback := hostKeyCallback(e.NonInteractive)
	if e.insecureHostKey {
		hkCallback = ssh.InsecureIgnoreHostKey()
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            e.authMethods(),
		HostKeyCallback: hkCallback,
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, e.Port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("ssh connect %s: %w", addr, err)
	}
	e.client = client
	return nil
}

// Close closes the SSH connection and any associated resources.
func (e *Executor) Close() error {
	if e.agentConn != nil {
		e.agentConn.Close()
		e.agentConn = nil
	}
	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

// Run executes a command and returns combined stdout.
func (e *Executor) Run(cmd string) (string, error) {
	e.debugf("run: %s", cmd)
	session, err := e.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		e.debugf("error: %s", errMsg)
		return "", fmt.Errorf("ssh run: %s", errMsg)
	}
	out := strings.TrimSpace(stdout.String())
	e.debugf("ok: %s", truncate(out, 200))
	return out, nil
}

// RunQuiet executes a command and returns error only (discards output).
func (e *Executor) RunQuiet(cmd string) error {
	_, err := e.Run(cmd)
	return err
}

// Stream executes a command and streams stdout to the writer.
func (e *Executor) Stream(cmd string, stdout io.Writer) error {
	e.debugf("stream: %s", cmd)
	session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stdout

	return session.Run(cmd)
}

// Upload copies a local file to the remote server via SCP.
func (e *Executor) Upload(localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}
	return e.WriteFile(remotePath, data, 0644)
}

// WriteFile writes content to a remote file.
func (e *Executor) WriteFile(remotePath string, data []byte, mode os.FileMode) error {
	session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	filename := filepath.Base(remotePath)
	dir := filepath.Dir(remotePath)

	go func() {
		w, _ := session.StdinPipe()
		fmt.Fprintf(w, "C%04o %d %s\n", mode, len(data), filename)
		w.Write(data)
		fmt.Fprint(w, "\x00")
		w.Close()
	}()

	return session.Run(fmt.Sprintf("scp -t %s", dir))
}

// ReadFile reads a file from the remote server.
func (e *Executor) ReadFile(remotePath string) ([]byte, error) {
	out, err := e.Run(fmt.Sprintf("cat %s", ShellQuote(remotePath)))
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// StreamInput runs a remote command, piping the reader into its stdin and returning stdout.
func (e *Executor) StreamInput(cmd string, stdin io.Reader) (string, error) {
	e.debugf("stream-input: %s", cmd)
	session, err := e.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	session.Stdin = stdin
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("ssh run: %s", errMsg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// UploadReader copies data from a reader to a remote file via SCP.
func (e *Executor) UploadReader(r io.Reader, size int64, remotePath string, mode os.FileMode) error {
	session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	filename := filepath.Base(remotePath)
	dir := filepath.Dir(remotePath)

	var stderr bytes.Buffer
	session.Stderr = &stderr

	go func() {
		w, _ := session.StdinPipe()
		fmt.Fprintf(w, "C%04o %d %s\n", mode, size, filename)
		io.Copy(w, r)
		fmt.Fprint(w, "\x00")
		w.Close()
	}()

	if err := session.Run(fmt.Sprintf("scp -t %s", dir)); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// FileExists checks if a file exists on the remote server.
func (e *Executor) FileExists(path string) bool {
	err := e.RunQuiet(fmt.Sprintf("test -f %s", ShellQuote(path)))
	return err == nil
}

// truncate shortens s to at most n runes for debug display.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ShellQuote wraps a value in single quotes for safe shell usage,
// escaping any embedded single quotes.
func ShellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

// ValidateRestartPolicy returns true if s is a valid Docker restart policy.
func ValidateRestartPolicy(s string) bool {
	switch s {
	case "no", "always", "unless-stopped", "on-failure":
		return true
	}
	return false
}

// ValidateDuration returns true if s looks like a valid Docker duration (e.g. "30s", "5m", "1h30m").
func ValidateDuration(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || r == 'h' || r == 'm' || r == 's' || r == '.') {
			return false
		}
	}
	return true
}

// SafeSQLIdentifierMySQL escapes a SQL identifier for MySQL using backticks.
func SafeSQLIdentifierMySQL(s string) string {
	s = strings.ReplaceAll(s, "`", "``")
	return "`" + s + "`"
}

// SafeSQLIdentifierPG escapes a SQL identifier for PostgreSQL using double quotes.
func SafeSQLIdentifierPG(s string) string {
	s = strings.ReplaceAll(s, `"`, `""`)
	return `"` + s + `"`
}

// parseHost splits "user@host" into user and host.
func parseHost(h string) (string, string) {
	parts := strings.SplitN(h, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "root", h
}

// HasKeyAuth returns true if any key-based auth (agent or key files) is available.
// Checks ssh-agent, neo's managed key, and all private key files in ~/.ssh/.
func HasKeyAuth() bool {
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			conn.Close()
			return true
		}
	}

	if _, err := os.Stat(NeoKeyPath()); err == nil {
		return true
	}

	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	skip := map[string]bool{"known_hosts": true, "config": true, "authorized_keys": true}
	if entries, err := os.ReadDir(sshDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && !strings.HasSuffix(name, ".pub") && !skip[name] {
				return true
			}
		}
	}
	return false
}

// authMethods returns SSH auth methods in priority order.
func (e *Executor) authMethods() []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	// In-memory private key (for programmatic/test use)
	if len(e.PrivateKey) > 0 {
		if signer, err := ssh.ParsePrivateKey(e.PrivateKey); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	// Try neo's managed key first (~/.neo/neo_ed25519)
	if key, err := loadKey(NeoKeyPath()); err == nil {
		methods = append(methods, key)
	}

	// Try ssh-agent
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			e.agentConn = conn // store for cleanup in Close()
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Try common key files, then scan all remaining keys in ~/.ssh/
	// (catches cloud-provider keys at non-standard paths like ~/.ssh/do_rsa)
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	standardKeys := map[string]bool{"id_ed25519": true, "id_rsa": true}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		if key, err := loadKey(filepath.Join(sshDir, name)); err == nil {
			methods = append(methods, key)
		}
	}
	skipNames := map[string]bool{
		"known_hosts": true, "config": true, "authorized_keys": true,
	}
	if entries, err := os.ReadDir(sshDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasSuffix(name, ".pub") || standardKeys[name] || skipNames[name] {
				continue
			}
			if key, err := loadKey(filepath.Join(sshDir, name)); err == nil {
				methods = append(methods, key)
			}
		}
	}

	// Password auth as fallback
	if e.Password != "" {
		methods = append(methods, ssh.Password(e.Password))
	}

	return methods
}

// loadKey reads a private key file and returns an auth method.
func loadKey(path string) (ssh.AuthMethod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

// hostKeyCallback returns a callback that verifies host keys against known_hosts.
// For unknown hosts, displays the fingerprint and asks for confirmation before accepting.
// When nonInteractive is true, unknown hosts are rejected without prompting.
// Changed host keys are always rejected.
func hostKeyCallback(nonInteractive bool) ssh.HostKeyCallback {
	home, _ := os.UserHomeDir()
	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")

	// Ensure ~/.ssh directory exists
	sshDir := filepath.Dir(knownHostsPath)
	os.MkdirAll(sshDir, 0700)

	// Ensure known_hosts file exists
	if _, statErr := os.Stat(knownHostsPath); os.IsNotExist(statErr) {
		f, createErr := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_WRONLY, 0600)
		if createErr == nil {
			f.Close()
		}
	}

	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		// If we truly can't read known_hosts, reject all connections rather than silently accepting
		return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return fmt.Errorf("cannot read known_hosts file: %w — please ensure ~/.ssh/known_hosts exists", err)
		}
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		// Key unknown (new host) — show fingerprint and ask user to confirm
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			if nonInteractive {
				return fmt.Errorf("unknown host key for %s — run a command manually first to accept the key", hostname)
			}
			fingerprint := ssh.FingerprintSHA256(key)
			fmt.Printf("\n  The authenticity of host %q can't be established.\n", hostname)
			fmt.Printf("  %s key fingerprint is %s\n", key.Type(), fingerprint)
			fmt.Printf("  Are you sure you want to continue connecting? (yes/no): ")

			var answer string
			fmt.Scanln(&answer)
			if answer != "yes" && answer != "y" {
				return fmt.Errorf("host key verification failed — connection aborted by user")
			}

			// User accepted — add to known_hosts
			f, ferr := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
			if ferr == nil {
				defer f.Close()
				fmt.Fprintf(f, "%s\n", knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
			}
			return nil
		}
		// Host key has changed — always reject (potential MITM)
		host, _, splitErr := net.SplitHostPort(hostname)
		if splitErr != nil {
			host = hostname
		}
		return fmt.Errorf("WARNING: HOST KEY HAS CHANGED for %s\n\n  This can happen after a server rebuild or IP reuse.\n  Fix: ssh-keygen -R %s\n  Then run neo init again", hostname, host)
	}
}
