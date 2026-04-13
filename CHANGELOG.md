# Changelog

All notable changes to Neo will be documented here.

---

## v0.7.0 — 2026-04-13

### Improvements

- **Environment config validation** — When `environments:` are defined, root-level `server:` and `domains:` are now blocked with a clear error and migration instructions. Previously they were silently ignored, which could cause deploys to go to the wrong server.

- **Every environment must declare `server:`** — Neo errors early if any environment is missing a `server:`, regardless of how many environments are defined.

- **`--all` now works correctly** — Moving `server:/domains:` into each environment means `neo deploy --all` deploys every environment (e.g. both `production` and `staging`) as intended.

### Migration

If your `.neo.yml` has `environments:` defined, move `server:` and `domains:` out of the root and into each environment:

```yaml
# Before
server: my-server
domains:
  - app.example.com
environments:
  staging:
    domains:
      - staging.example.com

# After
environments:
  production:
    server: my-server
    domains:
      - app.example.com
  staging:
    server: my-server
    domains:
      - staging.example.com
```

Root-level `env:`, `workers:`, and `volumes:` remain shared across all environments.

---

## v0.6.0 — 2026-04-13

### Improvements

- **Environment server validation** — When a `.neo.yml` defines multiple environments, every environment must now explicitly declare a `server:`. Neo errors early with a clear message instead of silently falling back to the top-level server, which could cause accidental deploys to the wrong target.

---

## v0.5.0 — 2026-04-13

### New Features

- **Team SSH key management** — Share server access with teammates in seconds, no GitHub or manual SSH required.

  ```bash
  neo key show              # generate + print your public key to share
  neo key add "<pubkey>"    # authorize a teammate on the server
  neo key list              # see all authorized keys (marks your own)
  neo key remove <number>   # revoke access by number
  ```

  **Workflow:** Teammate runs `neo key show`, sends you the one-line key. You run `neo key add "<key>"`. They add `server: root@your-ip` to their `.neo.yml` and can deploy immediately with their own neo key. No key files to copy, no passwords to share.

---

## v0.4.0 — 2026-04-13

### New Features

- **Server groups** — Deploy one environment to multiple servers in parallel using `servers: [server-a, server-b, server-c]` in `.neo.yml`. Supports web clusters, queue worker fleets, and mixed topologies from a single config file.

  ```yaml
  environments:
    web:
      servers: [velvet-134, web-sg2, web-sg3]
    queue:
      servers: [queue-sg1, queue-sg2, queue-sg3]
    scheduler:
      server: schedule-sg1
  ```

- **Per-server deploy targeting** — Deploy to a single server within a group using `neo deploy --env web --server velvet-134`, without affecting the other servers in the group.

- **TUI server group support** — The interactive dashboard now prompts for environment and then "All servers in group" or a specific server when a server group is configured.

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
