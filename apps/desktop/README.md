# Neo Desktop

Menu-bar (macOS) / notification-area (Windows) tray application for the
[Neo CLI](../../README.md), built with **Tauri 2 + React + TypeScript + Vite**.

Through **slice 4 — live tray behavior**: the runnable tray shell, the bundled
`neo-bridge` Go sidecar (`../../cmd/neo-bridge`) reading the real Neo config over
SSH, and a single desktop application service that polls every configured server,
rolls their health into one tray state, and fires transition notifications. See
[`plans/2026-07-18-neo-desktop-tray-application.md`](../../plans/2026-07-18-neo-desktop-tray-application.md).

## Layout

```
apps/desktop/
├── index.html            # tray popover entry
├── management.html       # management window entry
├── src/
│   ├── app/              # App router, windows, data hook
│   ├── components/       # presentational pieces (badge, metric card, logo)
│   ├── features/         # servers / diagnostics / apps / logs slices
│   ├── lib/              # DesktopAPI, protocol types, fixtures, transport
│   ├── styles/           # global CSS (light + dark)
│   └── test/             # vitest setup
└── src-tauri/            # thin Rust shell (tray, windows, commands)
    ├── src/{main,lib,tray,commands,bridge}.rs
    ├── capabilities/     # ACL — no shell/fs exposed to the webview
    ├── icons/            # generated placeholder brand marks
    └── tauri.conf.json
```

## Architecture boundary

The React UI talks only to the `DesktopAPI` interface (`src/lib/desktop-api.ts`).
Two implementations back it:

- **Fixture provider** (`src/lib/fixtures.ts`) — deterministic data for tests,
  local UI iteration, and this first visual shell. Default everywhere in slice 1.
- **Tauri transport** (`src/lib/tauri-api.ts`) — calls typed Rust commands that
  (from slice 2) forward versioned JSON to the `neo-bridge` sidecar. Enabled only
  under Tauri with `VITE_USE_BRIDGE=true`.

The frontend never gets shell access and never builds SSH commands. Every Rust
command exposed to the webview is a named, allowlisted entry in `commands.rs`.

## Develop

Prerequisites: **Node** (frontend), **Rust** (shell), and the **Go toolchain**
(the `neo-bridge` sidecar), plus the
[Tauri 2 system dependencies](https://v2.tauri.app/start/prerequisites/).

```bash
npm install          # or, from repo root: make desktop-install
npm run tauri dev    # or: make desktop-dev  (launches the tray app)
```

The sidecar is built automatically by `src-tauri/build.rs` into
`src-tauri/binaries/neo-bridge-<target-triple>` on every `cargo`/`tauri` build,
so no manual step is needed — only a working `go` on `PATH`.

Frontend-only (no Rust, runs in a browser tab against fixtures):

```bash
npm run dev
```

## Test & check

```bash
npm run test:run     # vitest unit tests (no Tauri required)
npm run typecheck    # tsc --noEmit
npm run build        # tsc + vite production build
cd src-tauri && cargo test   # Rust tests
```

From the repo root, `make desktop-test` runs the Go suite plus the frontend and
Rust tests together.

## Scope so far

Slice 1 (scaffold): tray icon + menu, hidden-at-startup popover, larger
management window, close-hides-not-quits, single-instance, strict CSP, fixture
UI, frontend unit tests, Windows CI compile job.

Slice 2 (bridge skeleton): the `neo-bridge` Go sidecar speaking protocol v1 over
stdio (`bridge.hello`, `bridge.shutdown`); stable error codes shared across Go,
Rust, and TypeScript; Rust supervision (single process, handshake,
protocol-version rejection, request-id correlation, event routing, ≤3 restarts
with exponential backoff, terminate on exit); the sidecar bundled via Tauri
`externalBin` and never exposed to the webview as a generic shell.

Slice 3 (servers and snapshots): shared Go `internal/operations` service behind
dependency-injected connector/executor/config interfaces; `server.list` and
`server.snapshot` over the real `~/.neo/config.json`; typed numeric snapshots
with graceful per-metric degradation.

Slice 4 (app list and tray polling): the `app.list` bridge method (applications,
workers, sidecars, and shared services, flattened and stable-sorted); a single
`DesktopService` that owns all refresh timers — selected server every 30s while
visible, others every 120s, ≤3 concurrent snapshots, ≤10% jitter, unreachable
backoff 30/60/120/300s, debounced manual refresh that bypasses backoff once, and
a cached last snapshot marked stale (with its age) when offline; an aggregate
tray state across all servers reflected as a macOS template-icon badge (shape,
not color alone); and transition-only notifications with per-finding dedup, a
5-minute cooldown, and silence until the initial scan completes. The on-demand
management window loads once instead of starting a second poller, so opening it
never multiplies polling.

Not yet: log streaming, deterministic diagnostics rules, lifecycle actions, and
signed release packaging.
