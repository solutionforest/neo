# Neo Desktop

Menu-bar (macOS) / notification-area (Windows) tray application for the
[Neo CLI](../../README.md), built with **Tauri 2 + React + TypeScript + Vite**.

## Why Neo Desktop exists

The Neo CLI is how you *change* servers — init, deploy, env, lifecycle. But most
of the day you are not deploying; you just want to know your servers are still
healthy without opening a terminal, SSHing in, and running `neo status` against
each one.

Neo Desktop fills that gap. It is a lightweight, always-there monitor — think
Laravel Herd for your remote fleet — that lives in the macOS menu bar / Windows
notification area and answers "is everything OK?" at a glance:

- **Ambient health, zero effort** — one tray icon rolls every configured server's
  reachability, CPU/RAM/disk/latency, apps, and services into a single
  at-a-glance state, polled for you in the background.
- **Tells you when something breaks** — low-noise, transition-only notifications
  when a server or app crosses into a warning/critical/offline state, so you find
  out before your users do instead of by chance.
- **Logs without a terminal** — recent and live-streaming application logs from
  the popover or a larger management window.
- **A few safe actions** — a strict allowlist of corrective actions (start / stop
  / restart) with confirmation; nothing destructive.

It deliberately stays a **read-only-plus-safe-actions monitor**: no deploys, no
env editing, no secrets shown, no backups, no arbitrary remote shell — those stay
in the CLI. And it keeps Neo's **agentless** model: it reads your existing
`~/.neo/config.json` and connects over SSH, so there is no new agent to install on
any server.

The app is built and feature-complete through the plan's ten implementation
slices — tray shell, bridge sidecar, live polling, logs, diagnostics,
notifications, safe actions, release engineering, beta hardening, and UI/UX
refinement. See
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
Rust tests together, and `make desktop-bridge [TRIPLE=<target-triple>]` builds a
version-stamped sidecar under the Tauri `externalBin` filename
(`scripts/desktop-bridge.sh`).

## Release

Desktop releases are tagged independently of the CLI:

```text
v0.22.0          # Neo CLI release      → .github/workflows/release.yml
desktop-v0.1.0   # Neo Desktop release  → .github/workflows/desktop-release.yml
```

To cut a release: bump `version` in `package.json` **and**
`src-tauri/tauri.conf.json` to match, merge, then push the `desktop-v<version>`
tag. The release workflow verifies the versions match, builds macOS ARM64,
macOS Intel, and Windows x64 on native runners (bridge from the tag commit →
sidecar filename → frontend → package → sign → signed updater artifacts →
smoke test), and publishes all artifacts and a `SHA256SUMS` atomically via a
draft release that is undrafted only after every target succeeds. The in-app
updater reads `latest.json` from the rolling `desktop-latest` prerelease, so
`bridge.hello` version info, artifacts, and the update feed always move as one
unit — the bridge is never updated separately from the desktop app.

Update behavior (see `src/lib/update-controller.ts`): silent check ~20s after
startup and every 6 hours; the user is prompted (version + release notes)
before anything downloads; "Later" defers without re-prompting that session;
the Tauri updater verifies the artifact signature against the public key in
`tauri.conf.json` before install and fails closed on mismatch.

Signing material lives **only in GitHub Actions secrets** — never in this
repository:

| Secret | Purpose |
| --- | --- |
| `TAURI_SIGNING_PRIVATE_KEY` / `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` | Updater artifact signing key |
| `TAURI_UPDATER_PUBKEY` | Updater public key, injected at build until committed to `tauri.conf.json` |
| `APPLE_CERTIFICATE` / `APPLE_CERTIFICATE_PASSWORD` / `APPLE_SIGNING_IDENTITY` | Developer ID Application cert (base64 `.p12`) |
| `APPLE_ID` / `APPLE_PASSWORD` / `APPLE_TEAM_ID` | Notarization credentials |
| `WINDOWS_CERTIFICATE` / `WINDOWS_CERTIFICATE_PASSWORD` | Authenticode cert (base64 `.pfx`) |

Missing secrets never block a release: the workflow emits warnings, ships the
affected target unsigned, and leaves the updater feed untouched for platforms
without a signed artifact.

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

Slices 5–7 added log streaming, deterministic diagnostics, and safe lifecycle
actions (see the plan and PRs #10–#12).

Slice 8 (release engineering): independent `desktop-v*` tagging with a release
pipeline (`desktop-release.yml`) that builds/signs/packages macOS ARM64 + Intel
and Windows x64 and publishes artifacts + checksums + a signed updater feed
atomically; the finalized desktop CI matrix (root Go tests, bridge contract
tests, frontend checks, Rust fmt/clippy/tests, native macOS and Windows builds
with sidecar-handshake smoke tests); `bridge.hello` now reports the build
commit alongside versions, shown in the management window's About panel; and
in-app update behavior per the plan (silent 6-hourly checks, prompt before
download, session-scoped deferral, signature-verified installs).

Slice 9 (beta hardening): laptop-lifecycle resilience, accessibility, local
observability, and a redacted diagnostic bundle.

- **Sleep/wake & network transitions** — a `LifecycleMonitor`
  (`src/lib/lifecycle-monitor.ts`) infers wake from a frozen-timer gap and
  listens for `online`/focus; each transition collapses (debounced) into one
  `DesktopService.refreshAll()` that re-checks every server and bypasses backoff.
  Only the always-alive popover runs it, so opening the management window never
  adds a second reconnect-refresh loop.
- **Accessibility** — a single visible focus ring across all controls; the action
  dialog takes focus on open and closes on `Escape` (never mid-run); and
  `prefers-reduced-motion`, `prefers-reduced-transparency`, and
  `prefers-contrast` are all honored in `global.css`. Status stays distinguishable
  by shape/text, not color alone.
- **Local observability** (`src/lib/observability.ts`) — a bounded ring buffer
  recording versions, bridge lifecycle (`bridge://ready|error|unavailable`),
  request method/duration/error-code (never parameters), poll scheduling and
  cache age, notification transitions, and update checks + signature failures.
- **Export Diagnostic Bundle** — the management window's Support panel previews a
  fully redacted bundle (`src/lib/diagnostic-bundle.ts`) before writing it; it
  excludes private keys, passwords/passphrases, license keys, app env values, and
  full server logs unless explicitly opted in after preview. The Rust
  `export_diagnostic_bundle` command chooses the directory and writes the bytes
  (the webview never picks a path).
- **Performance** — the log viewer paints only the most recent
  `MAX_RENDERED_LINES` window (full history stays searchable) to bound DOM work
  on a live follow stream.

Slice 10 (UI/UX refinement): Apple-style visual polish across the popover and
management window — consistent spacing, typography, and healthy / warning /
critical / offline state treatments — with accessibility (visible focus,
reduced-motion / reduced-transparency / contrast preferences, and
color-independent status) preserved throughout. Final visual sign-off is recorded
in [`VISUAL_SIGNOFF.md`](VISUAL_SIGNOFF.md).

### SSH edge cases (strict, no accept-all)

The bridge connector dials **non-interactively** (`internal/operations/deps.go`),
so unknown hosts are **rejected**, changed host keys are **always rejected**, and
there is no "accept all host keys" option anywhere in the desktop/bridge path
(`ssh.InsecureIgnoreHostKey` is reachable only via the test-only
`SetInsecureHostKey`). Every SSH failure — agent key, configured key file,
encrypted key the bridge cannot unlock, missing key, unknown host — is mapped to
a stable code (`ssh_unknown_host` / `ssh_auth_failed` / `ssh_unreachable` /
`operation_timeout`) the UI branches on; see
`TestSnapshotConnectErrorsAreClassified`.

### Manual platform matrix (verify before public beta)

Automated tests cover the deterministic logic; these require real hardware/OSes:

- [ ] macOS current + previous major (Apple Silicon); macOS Intel before claiming Intel support.
- [ ] Windows 11 x64; Windows 10 x64 while the WebView2 baseline supports it.
- [ ] Light, dark, scaled displays, and multiple displays.
- [ ] Laptop sleep/wake, offline↔online, and VPN up/down each trigger one debounced refresh.
- [ ] SSH: agent key, configured key file, encrypted key, missing key, and unknown host.
- [ ] Keyboard-only navigation and a screen reader across the popover and management window.
