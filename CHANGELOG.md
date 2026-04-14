# Changelog

All notable changes to Neo will be documented here.

---

## v0.11.0 — 2026-04-14

### Improvements

- **`--parallel` flag for `neo deploy --all`** — Caps the number of concurrent SSH connections and `docker load` operations when deploying to multiple environments. Defaults to `3`, which is safe for most servers. Lower it for underpowered targets (1 GB RAM / 1 vCPU):

  ```bash
  neo deploy --all                    # default: 3 concurrent deploys
  neo deploy --all --parallel 1       # serial — safest for small servers
  neo deploy --all --parallel 5       # max throughput for beefy servers
  ```

  Previously, `--all` opened one SSH connection per environment simultaneously with no cap, which could OOM small servers during the `docker load` decompression spike.

---

## v0.10.0 — 2026-04-14

### New Features

- **`neo prune`** — Remove old Docker images from the server to free up disk space. Shows a preview table of what will be kept vs removed per app, then asks for confirmation before deleting.

  ```bash
  neo prune              # keep 2 most recent images per app (default)
  neo prune --keep 1     # keep only the current image
  neo prune --dry-run    # preview without making changes
  neo prune --force      # skip confirmation prompt
  ```

  Running containers are never affected — Docker skips images still in use and the summary reports how many were skipped.

### Bug Fixes

- **Image pruning after deploy** — Fixed a silent bug where `docker rmi` by image ID would fail when multiple tags share the same layer digest. Old images are now removed by tag, which correctly handles all cases.

---

## v0.9.0 — 2026-04-13

### New Features

- **Domain redirects** — Redirect any domain to another URL without deploying an app, sidecar, or service. Powered by Caddy's native redirect handler — auto-SSL is provisioned for the source domain automatically. Request paths are preserved (`vxero.dev/blog` → `vxero.com/blog`).

  ```bash
  neo redirect add vxero.dev vxero.com          # 301 permanent (default)
  neo redirect add old.api.com new.api.com --temporary  # 302 temporary
  neo redirect list                              # show all redirects
  neo redirect remove vxero.dev                 # remove a redirect
  ```

---

## v0.8.0 — 2026-04-13

### Improvements

- **Automatic SSH key discovery** — `neo init` now scans all private key files in `~/.ssh/` (not just `id_ed25519` and `id_rsa`). Cloud provider keys at non-standard paths (e.g. `~/.ssh/do_rsa`, `~/.ssh/hetzner_key`) are tried automatically — no extra steps needed for most fresh VPS setups.

- **Actionable "HOST KEY HAS CHANGED" error** — When neo detects a changed host key (common after server rebuilds or IP reuse), the error now includes the exact fix command:
  ```
  Fix: ssh-keygen -R <ip>
  Then run neo init again
  ```

- **`--key` flag hint on auth failure** — If all SSH key attempts fail, `neo init` now shows a clear tip suggesting `neo init --key ~/.ssh/your_key root@<ip>` instead of a bare error message.

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
