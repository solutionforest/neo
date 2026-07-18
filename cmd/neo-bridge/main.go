// Command neo-bridge is the long-running sidecar that the Neo Desktop tray app
// (apps/desktop) supervises. It speaks versioned newline-delimited JSON over
// stdio: requests in on stdin, responses and streaming events out on stdout.
//
// Invariants (see plans/2026-07-18-neo-desktop-tray-application.md, "Phase 2"):
//   - stdout carries ONLY protocol messages. All human-readable diagnostics go
//     to stderr via structured logging, so debug logging can never corrupt the
//     protocol stream.
//   - It never prompts on stdin for passwords, host trust, license activation,
//     or selections. stdin is protocol input only.
//   - It shuts down gracefully when stdin closes, on a bridge.shutdown request,
//     or on SIGINT/SIGTERM.
//
// Slice 2 implements the walking skeleton: bridge.hello and bridge.shutdown.
// Server, snapshot, log, diagnostics, and action methods land in later slices.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/operations"
)

// Stamped at build time via -ldflags (see the Makefile's build-bridge target
// and scripts/desktop-bridge.sh). bridgeVersion is this sidecar's version;
// coreVersion is the Neo CLI core it is built from; buildCommit is the Git
// commit it was built at. All default to dev values for un-stamped builds.
var (
	bridgeVersion = "dev"
	coreVersion   = "dev"
	buildCommit   = "unknown"
)

func main() {
	logger := newLogger(os.Getenv("NEO_BRIDGE_LOG_LEVEL"))
	slog.SetDefault(logger)

	// SIGINT/SIGTERM trigger a graceful shutdown. stdin closing (the desktop
	// app exiting or the pipe breaking) does too, via Run returning on EOF.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("neo-bridge starting",
		"protocolVersion", ProtocolVersion,
		"bridgeVersion", bridgeVersion,
		"coreVersion", coreVersion,
		"commit", buildCommit)

	// The shared operation service backs the data methods over the real
	// ~/.neo/config.json via SSH. Deadlines default to the plan's 12s connect /
	// 15s snapshot budgets.
	ops := operations.NewService(
		operations.ConfigLoader(config.Load),
		operations.NewSSHConnector(),
		operations.SystemClock(),
		operations.Options{},
	)

	// bridge.hello reports activation without ever exposing the license key.
	// Activation() is cache-only, so hello never blocks on the license server.
	activation := func() string {
		cfg, err := config.Load()
		if err != nil {
			return string(license.ActivationUnknown)
		}
		return string(license.Activation(cfg.LicenseKey))
	}

	srv := NewServer(bridgeVersion, coreVersion, logger,
		WithOperations(ops),
		WithActivation(activation),
		WithCommit(buildCommit),
	)

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, os.Stdin, os.Stdout) }()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-done:
		if err != nil {
			logger.Error("bridge stopped with error", "err", err)
			os.Exit(1)
		}
	}
	logger.Info("neo-bridge exiting")
}

// newLogger returns a structured JSON logger writing to stderr. The level is
// controlled by NEO_BRIDGE_LOG_LEVEL (debug|info|warn|error), defaulting to
// info. stderr is used deliberately: stdout is reserved for the protocol.
func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
