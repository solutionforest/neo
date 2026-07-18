package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Context-aware wrappers around the SSH executor.
//
// The base Executor methods (Run, Stream, ReadFileElevated, Connect) predate
// the bridge and do not accept a context, so they cannot be cancelled once a
// remote command has started. The neo-bridge sidecar (cmd/neo-bridge) needs a
// hard deadline and cancellation path on every operation — see
// plans/2026-07-18-neo-desktop-tray-application.md "Phase 3". Rather than
// churn every existing CLI caller at once, we add ...Context variants and
// migrate callers incrementally (the plan explicitly sanctions this).
//
// Cancellation semantics: x/crypto/ssh sessions have no native context support,
// so we run the command on a goroutine and Close the session when ctx fires.
// Closing an in-flight session unblocks session.Run, so the goroutine always
// finishes and we never leak it. Output captured before cancellation is
// discarded — a cancelled operation has no trustworthy result.

// ConnectContext establishes the SSH connection, honouring ctx's deadline for
// both the TCP dial and the SSH handshake. It is the cancellable counterpart of
// Connect and is used by background callers (the bridge) that must not block
// past their operation deadline.
func (e *Executor) ConnectContext(ctx context.Context) error {
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

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("ssh connect %s: %w", addr, err)
	}
	// Bound the handshake by ctx's deadline (if any) so a stalled server cannot
	// hang the operation past its budget.
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	conn.SetDeadline(time.Time{}) // clear the handshake deadline for the live session
	e.client = ssh.NewClient(c, chans, reqs)
	return nil
}

// RunContext runs a command and returns its stdout, cancelling the session if
// ctx is done first. On cancellation it returns ctx.Err() (context.Canceled or
// context.DeadlineExceeded) so callers can classify timeouts distinctly.
func (e *Executor) RunContext(ctx context.Context, cmd string) (string, error) {
	e.debugf("run(ctx): %s", cmd)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := e.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case <-ctx.Done():
		session.Close() // unblocks session.Run in the goroutine
		<-done
		return "", ctx.Err()
	case runErr := <-done:
		session.Close()
		if runErr != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = runErr.Error()
			}
			e.debugf("error: %s", msg)
			return "", fmt.Errorf("ssh run: %s", msg)
		}
		out := strings.TrimSpace(stdout.String())
		e.debugf("ok: %s", truncate(out, 200))
		return out, nil
	}
}

// StreamContext streams a command's combined output to w, stopping the remote
// command when ctx is done. Returns ctx.Err() on cancellation.
func (e *Executor) StreamContext(ctx context.Context, cmd string, w io.Writer) error {
	e.debugf("stream(ctx): %s", cmd)
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}

	session.Stdout = w
	session.Stderr = w

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case <-ctx.Done():
		session.Close()
		<-done
		return ctx.Err()
	case runErr := <-done:
		session.Close()
		return runErr
	}
}

// ReadFileElevatedContext is the cancellable counterpart of ReadFileElevated:
// it reads a possibly root-only file (using sudo for non-root sessions) with a
// context deadline.
func (e *Executor) ReadFileElevatedContext(ctx context.Context, remotePath string) ([]byte, error) {
	cmd := "cat " + ShellQuote(remotePath)
	if e.User() != "root" {
		cmd = "sudo " + cmd
	}
	out, err := e.RunContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}
