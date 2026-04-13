# Changelog

All notable changes to Neo will be documented here.

---

## v0.3.0 — 2026-04-13

### New Features

- **Horizontal scaling** — Set `scale: N` in `.neo.yml` to run multiple app replicas. Caddy automatically load-balances across them. Zero-downtime redeploy and scale changes (1→N, N→M) are fully supported. Lifecycle commands (`start`, `stop`, `restart`, `remove`) operate on the full replica set.

- **WebSocket / WSS support** — Caddy's reverse proxy transparently handles WebSocket upgrades, including `wss://` with auto-SSL. No configuration required.

- **Opt-in HTTP health check** — Add a `health.path` to `.neo.yml` to run an HTTP health check before switching Caddy traffic. If the check fails, the old container keeps serving (true zero-downtime rollback).

- **SSH tunnel command** — `neo tunnel` opens SSH tunnels to remote services for local tools like TablePlus and DataGrip.

- **Interactive DB browser** — `neo db <app>` now supports table data browsing (Enter = `SELECT *`, `d` = `DESCRIBE`).

- **HTTP Basic Auth** — Protect apps at the proxy layer via `basic_auth:` in `.neo.yml`. Supports path bypass rules (`bypass: [/api/*, /webhooks/*]`).

- **Shared services** — Multiple apps can share a single MySQL, Postgres, Redis, or MariaDB instance to save RAM on small VMs (`neo service create/link/unlink`).

### Improvements

- **Image retention** — After each deploy, neo keeps the current image plus the previous one on the server for instant rollback. Older images are pruned automatically.

- **SHA256 checksums** — neo-builder now computes per-binary SHA256 checksums for all release artifacts.

- **Windows/ARM64 support** — Added `windows/arm64` build target to neo-builder.

- **Broader OS install support** — Install script now handles `i686` arch (32-bit Git Bash on Windows).

### Bug Fixes

- Fixed DB browser panic when switching between queries with different column counts.
- Fixed DB browser for shared services (uses `DefaultDB`, prefers app user).
- Fixed 21 security vulnerabilities across the codebase.
- Fixed staging build URL injection for neo-builder.
