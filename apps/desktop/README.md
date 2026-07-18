# Neo Desktop

Menu-bar (macOS) / notification-area (Windows) tray application for the
[Neo CLI](../../README.md), built with **Tauri 2 + React + TypeScript + Vite**.

This is **slice 1 — the scaffold**: a runnable tray shell with a fixture-backed
UI. It has no live server connection yet; the `neo-bridge` Go sidecar and the
real transport arrive in later slices. See
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

Prerequisites: **Node** (for the frontend) and **Rust** (for the shell), plus the
[Tauri 2 system dependencies](https://v2.tauri.app/start/prerequisites/).

```bash
npm install          # or, from repo root: make desktop-install
npm run tauri dev    # or: make desktop-dev  (launches the tray app)
```

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

## Slice 1 scope

Included: tray icon + menu, hidden-at-startup popover, larger management window,
close-hides-not-quits, single-instance, strict CSP, fixture UI (status, server
selector, CPU/RAM/disk/latency, app counts, findings, Refresh / Open Dashboard),
frontend unit tests, and a Windows CI compile job.

Not yet: the Go bridge, live SSH data, polling, notifications, log streaming,
lifecycle actions, and signed release packaging.
