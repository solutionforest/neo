# Neo Technical Reference

Internal technical reference for the Neo CLI (`vxero-neo`). Covers every command, flag, config field, internal package, and architectural decision.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Commands Reference](#commands-reference)
- [`.neo.yml` Configuration Schema](#neoyml-configuration-schema)
- [Internal Packages](#internal-packages)
- [App Templates](#app-templates)
- [Naming Conventions](#naming-conventions)
- [Deployment Deep Dive](#deployment-deep-dive)
- [Shared Services](#shared-services)
- [Licensing & Feature Gates](#licensing--feature-gates)
- [Security Features](#security-features)
- [Testing Infrastructure](#testing-infrastructure)
- [Self-Update System](#self-update-system)
- [OS Support Matrix](#os-support-matrix)

---

## Architecture Overview

Neo is a Go CLI that manages remote servers over SSH. It runs locally and executes all Docker/Caddy operations on the remote server via SSH commands. There is no agent installed on the server — everything is stateless shell commands over SSH.

### Key Architectural Principles

- **No Docker SDK** — All Docker commands are shell commands executed over SSH
- **No server-side agent** — Pure SSH + shell commands
- **State on server** — `/etc/neo/state.json` persisted via SSH read/write
- **State on client** — `~/.neo/config.json` for server list, license, cache
- **Cobra CLI** — All commands follow the `newXxxCmd() *cobra.Command` pattern
- **Embedded templates** — App manifests embedded in binary via `//go:embed`

### Data Flow

```
User Machine                          Remote Server
┌──────────────┐     SSH              ┌──────────────────────┐
│ neo CLI      │◄───────────────────► │ Docker daemon        │
│              │                      │ Caddy (ports 80/443) │
│ ~/.neo/      │                      │ /etc/neo/state.json  │
│  config.json │                      │ /var/backups/neo/    │
│  neo_ed25519 │                      │ /etc/neo/certs/      │
│  cache.json  │                      └──────────────────────┘
└──────────────┘
```

---

## Commands Reference

### Global Flags

| Flag | Type | Description |
|------|------|-------------|
| `--server` | string | Target server by name or `user@host` |
| `--debug` | bool | Verbose SSH command logging to stderr |

### Root: Interactive Dashboard

**`neo`** (no arguments) — Launches interactive TUI dashboard.

Menus: Servers, Applications, Services, Deploy, Metrics, Vxero. Background SSH status checks (up to 10 concurrent) for instant rendering.

---

### Server Setup & Management

#### `neo init <user@host>`

Initialize a remote server for Neo.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--name` | string | auto | Server name |
| `--key` | string | auto | Path to SSH private key |

**Steps:** OS validation → system update → swap config → Docker install → network creation → Caddy start → welcome page → port check → state init → SSH key deployment.

**Swap Sizing:** <=2GB RAM → 2GB swap; 2-8GB RAM → 1x RAM (rounded); >8GB → skip.

**Server Name Derivation:** `--name` flag → hostname subdomain → random word + last IP octet.

#### `neo servers`

List all configured servers. Shows name, host, and active indicator.

#### `neo servers remove <name>`

Remove a server from local configuration.

#### `neo use <name>`

Switch the active/default server.

#### `neo ssh`

SSH into the current server. Uses system `ssh` binary (replaces process on Unix via `syscall.Exec`).

---

### Deployment

#### `neo deploy [path]`

Deploy a Dockerfile project with zero-downtime blue-green deployment.

| Flag | Short | Type | Description |
|------|-------|------|-------------|
| `--domain` | `-d` | string | Domain name |
| `--temp` | | bool | Auto-assign `{app}.{ip}.sslip.io` |
| `--no-domain` | | bool | Skip domain (internal service) |
| `--port` | `-p` | int | Container port (auto-detected from Dockerfile EXPOSE) |
| `--name` | `-n` | string | App name (default: directory name) |
| `--dockerfile` | `-f` | string | Dockerfile path (default: `Dockerfile`) |
| `--env` | `-e` | string[] | `KEY=VALUE` (repeatable, highest priority) |
| `--env-file` | | string | Path to .env file |
| `--to` | | string | Named environment from `.neo.yml` |
| `--env-only` | | bool | Update env only, skip rebuild |
| `--all` | | bool | Build once, deploy to all environments in parallel |

See [Deployment Deep Dive](#deployment-deep-dive) for full flow.

---

### App Lifecycle

#### `neo list` / `neo ls`

List all apps and services on the server.

| Flag | Type | Description |
|------|------|-------------|
| `--format` | string | `table` or `json` |
| `--json` | bool | Output as JSON |

#### `neo start <app>`

Start a stopped app container.

#### `neo stop <app>`

Stop a running app container.

#### `neo restart <app>`

Restart an app container.

#### `neo update <app>`

Pull latest image and perform blue-green update.

#### `neo remove <app>`

Remove app container (keeps data volumes).

| Flag | Type | Description |
|------|------|-------------|
| `--force` | bool | Skip confirmation |

---

### App Installation

#### `neo install [app]`

Interactive installer for pre-configured app templates from the registry.

Features:
- Category-grouped template selection
- Interactive prompts for domain, env vars
- Shared service reuse detection (prompts to reuse existing MySQL/Redis)
- Parallel image pull while services set up
- Bundled service creation with env var generation
- Health check validation via HTTP endpoint

---

### Logs & Monitoring

#### `neo logs <app|service>`

Stream container logs.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--tail` | | int | 100 | Lines to show |
| `--follow` | `-f` | bool | false | Stream output |
| `--worker` | `-w` | string | | Specific worker name |
| `--service` | `-s` | bool | | Target shared service |
| `--sidecar` | `-c` | string | | Specific sidecar name |
| `--grep` | `-g` | string | | Filter lines by pattern |

#### `neo status`

Show server health and container stats.

| Flag | Type | Description |
|------|------|-------------|
| `--live` | bool | Live-updating metrics (3s refresh) |
| `--json` | bool | JSON output |

Shows: SSH latency, CPU/RAM/disk usage, uptime, container stats, network info (live mode).

---

### Environment Variables

#### `neo env [app]`

List env vars for an app (masks secrets).

| Flag | Type | Description |
|------|------|-------------|
| `--json` | bool | JSON output (secrets still masked) |

**Secret masking:** Keys containing `PASSWORD`, `TOKEN`, `SECRET`, `KEY`, `PRIVATE`, `CREDENTIAL` → first 2 chars + asterisks.

#### `neo env set [app] KEY=VALUE [KEY=VALUE...]`

Set env vars and auto-restart app.

#### `neo env unset [app] KEY [KEY...]`

Remove env vars and auto-restart app.

#### `neo env import [app] <file>`

Bulk import from .env file and auto-restart.

**Deploy env var priority (highest wins):**
1. CLI `--env KEY=VALUE`
2. `--env-file`
3. `.neo.yml` env section (environment-specific overrides top-level)
4. `docker-compose.yml` (auto-detected)
5. Server state (redeploy)
6. Auto-generated (`APP_URL`)

**Dev env var priority** (`neo dev`, highest wins):
1. `dev.env` from `.neo.yml`
2. `dev.env_file` from `.neo.yml`
3. Top-level `env` from `.neo.yml`
4. Top-level `env_file` from `.neo.yml`
5. Auto-loaded `.env` from project root

`${VAR}` interpolation is applied after merging (dev only). Resolves from the merged env map, then `os.Getenv`. Unresolved refs are left as-is.

---

### Domains & SSL

#### `neo domain <app> [domain]`

Set or manage domains for an app.

| Flag | Type | Description |
|------|------|-------------|
| `--temp` | bool | Auto-assign `{app}.{ip}.sslip.io` |
| `--add` | bool | Add alongside existing (multi-domain) |
| `--remove` | bool | Remove specific domain |
| `--cert` | string | PEM certificate file path |
| `--key` | string | PEM private key file path |

Features: Multiple domains per app, auto HTTPS via Let's Encrypt/Caddy, custom SSL certs (uploaded to `/etc/neo/certs/{app}/`), HTTP-only mode.

---

### Shared Services

#### `neo service create [type] [name]`

Create a shared database/cache service.

**Types:** `mysql` (8.0), `postgres` (16-alpine), `redis` (7-alpine), `mariadb` (11).

#### `neo service list`

List all shared services with linked apps.

#### `neo service link <svc> <app>`

Link service to app — creates database + user, injects `DATABASE_URL` and `DB_*` env vars.

**MySQL/MariaDB linking:** Creates database `{app}_db`, user `{app}` with random 32-char hex password. Injects `DATABASE_URL`, `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`.

**PostgreSQL linking:** Same as MySQL but with `postgres://` scheme.

**Redis linking:** Assigns next available DB number (0, 1, 2...). Injects `REDIS_URL`.

#### `neo service unlink <svc> <app>`

Remove env vars link (preserves data).

#### `neo service start|stop|restart <svc>`

Lifecycle management. Stop warns if apps are linked.

#### `neo service remove <svc>`

Remove service. Blocked if apps still linked.

#### `neo service logs <svc>`

Stream service container logs.

---

### Running Commands

#### `neo run <app> [flags] -- <command> [args...]`

Run a one-off command in a container.

| Flag | Short | Type | Description |
|------|-------|------|-------------|
| `--worker` | `-w` | string | Run in worker container |
| `--sidecar` | `-c` | string | Run in sidecar container |
| `--interactive` | `-i` | bool | Interactive PTY mode |

Examples: `neo run myapp -- php artisan migrate`, `neo run myapp -i -- bash`.

Interactive mode uses system SSH for proper TTY support.

---

### Data Management

#### `neo backup <app>`

Backup app volumes to `.tar.gz`. **Requires Neo+.**

Steps: Pause app → tar volumes → store in `/var/backups/neo/` → offer local download → resume app.

#### `neo restore <app> <backup-file>`

Restore from backup. Stops app, extracts to volume mount points, restarts.

#### `neo volumes`

List Docker volumes on the server with ownership info.

#### `neo volumes mount <volume> <host-path>`

Convert Docker volume to host bind mount. Stops app, copies data, recreates with bind mount. Enables direct file access via SFTP/rsync.

---

### Database Browser

#### `neo db <app> [shell]`

Interactive database browser TUI for MySQL/PostgreSQL.

- No argument: TUI browser (keys: `/` = query, `t` = tables, `d` = describe, `q` = quit)
- `shell`: Raw `mysql`/`psql` client with interactive TTY

Auto-detects linked database service and reads connection details from env.

---

### Development

#### `neo dev [down]`

Run app locally for development. Two modes:
- **Compose mode** — if `docker-compose.yml` exists, wraps `docker compose up`
- **Standalone mode** — builds from `Dockerfile`, runs with `docker run`

| Flag | Short | Type | Description |
|------|-------|------|-------------|
| `--build` | | bool | Rebuild images |
| `--detach` | `-d` | bool | Run in background |

`down` subcommand stops containers.

**Env loading** — auto-loads `.env` from project root (lowest priority), then `.neo.yml` env sources, then `dev:` section overrides. `${VAR}` interpolation resolves from the merged env or OS environment.

**Dev env priority** (lowest → highest):
1. Auto-loaded `.env` from project root
2. Top-level `env_file:` from `.neo.yml`
3. Top-level `env:` from `.neo.yml`
4. `dev.env_file:` from `.neo.yml`
5. `dev.env:` from `.neo.yml`

**Volume mounting** (standalone mode only) — top-level `volumes:` are auto-mounted as bind mounts to `./{volume-name}` in the project directory. Use `dev.volumes` to override local paths or add dev-only mounts. Compose mode uses the compose file's own volume definitions.

**Workers & sidecars** (standalone mode only) — if `.neo.yml` defines `workers:` or `sidecars:`, they are automatically started alongside the app:
- All containers join a shared Docker network (`neo-dev-{appName}`)
- Sidecars start first (services like Redis/DB), then workers, then the app
- Workers share the app image and env vars; sidecars use their own image and env
- `neo dev down` removes all containers and the network

| Container | Naming | Image |
|-----------|--------|-------|
| App | `neo-dev-{app}` | `neo-dev-{app}:latest` |
| Worker | `neo-dev-{app}-worker-{name}` | Same as app |
| Sidecar | `neo-dev-{app}-sidecar-{name}` | Own image (built or pulled) |

---

### Configuration

#### `neo config generate`

Generate `.neo.yml` from `docker-compose.yml`.

| Flag | Type | Description |
|------|------|-------------|
| `--compose` | string | Path to compose file (auto-detected) |

Auto-detects main app service, classifies services as sidecars/workers, extracts volumes/env/ports.

#### `neo sync [app]`

Sync server state back to `.neo.yml`.

| Flag | Type | Description |
|------|------|-------------|
| `--dry-run` | bool | Show changes without writing |

Reads current server state, shows diff, requires confirmation.

---

### Security

#### `neo stealth`

Toggle stealth mode — removes Caddy welcome page from direct IP access. Only configured domains serve traffic. Run again to disable.

#### `neo firewall install`

Install CrowdSec + nftables bouncer. Auto-bans brute-force, syncs community blocklist.

#### `neo firewall status`

Show CrowdSec engine/bouncer status and active decision count.

#### `neo firewall block <ip>`

Permanently ban an IP.

| Flag | Type | Description |
|------|------|-------------|
| `--reason` | string | Block reason |

#### `neo firewall unblock <ip>`

Remove ban for an IP.

#### `neo firewall list` / `neo firewall ls`

List all active firewall decisions with IP, type, origin, duration, reason.

---

### Licensing (Neo+)

#### `neo plus`

Interactive license management menu.

#### `neo plus activate <key>`

Activate a Neo+ license key on this machine.

#### `neo plus status`

Show plan (Free/Plus), server limits, feature availability, expiry.

#### `neo plus deactivate`

Remove license from this machine.

---

### Updates

#### `neo version`

Show current version and check for updates from remote endpoint.

#### `neo upgrade`

Download and install latest binary. Detects OS/arch, verifies checksum, atomic replacement with backup.

---

### Help

#### `neo help`

Categorized command listing.

| Flag | Type | Description |
|------|------|-------------|
| `--llm` | bool | Plain-text machine-readable output (no colors) |

Categories: Getting Started, Development, Apps, Services, Lifecycle, Data, Servers, Updates, Vxero.

---

### Other

#### `neo ask`

Interactive skill assistant. Guided Q&A for deploying, env vars, domains, services, troubleshooting.

#### `neo connect`

Opens Vxero platform in default browser.

---

## `.neo.yml` Configuration Schema

Complete schema with all fields and types:

```yaml
# ─── App Identity ───
name: myapp                          # string: app/container name (default: directory name)
server: production                   # string: server name or user@host
domain: myapp.com                    # string: primary domain, or "none" to skip
port: 8080                           # int: container port (auto-detected from Dockerfile)
https: true                          # *bool: nil=default, true=HTTPS, false=HTTP-only
restart: unless-stopped              # string: Docker restart policy

# ─── SSL ───
ssl:
  certificate: ./cert.pem           # string: PEM certificate path (relative to .neo.yml)
  private_key: ./key.pem            # string: PEM private key path

# ─── Environment ───
env:                                 # map[string]string
  NODE_ENV: production
  LOG_LEVEL: info
env_file: .env.production            # string: path to .env file
compose_service: web                 # string: docker-compose service to extract from

# ─── Health Check ───
health:
  cmd: "curl -f http://localhost:8080/health || exit 1"  # string: required
  interval: 30s                      # string: check interval
  timeout: 10s                       # string: per-check timeout
  retries: 3                         # int: failures before unhealthy
  start_period: 40s                  # string: grace period before checks start

# ─── Persistent Volumes ───
# Supports flat string, flat bind mount, and structured formats:
volumes:                             # map[string]NeoVolume
  data: /app/data                    # flat string → named Docker volume
  logs: /var/log/myapp:/var/log/app  # host:container → bind mount on server
  uploads:
    path: /app/uploads               # structured → named Docker volume
  backups:
    path: /app/backups               # structured with mount → bind mount on server
    mount: /mnt/ssd/backups

# ─── Workers (same image, different command) ───
workers:                             # map[string]NeoWorker
  queue:
    command: "python manage.py worker"   # string: required, override entrypoint
    health_check: "pgrep -f worker"      # string: optional health command
    restart: unless-stopped              # string: Docker restart policy
  scheduler:
    command: "python manage.py scheduler"

# ─── Sidecars (separate image) ───
sidecars:                            # map[string]NeoSidecar
  redis:
    image: redis:7-alpine            # string: pre-built image
    # OR build from Dockerfile:
    # build: ./redis                 # string shorthand
    # build:                         # object form
    #   context: ./redis
    #   dockerfile: Dockerfile.custom
    volumes:                         # map[string]string: name → containerPath
      cache: /data
    env:                             # map[string]string
      REDIS_PASSWORD: secret
    command: redis-server             # string: override cmd
    restart: always                  # string: restart policy
    health:                          # *NeoHealth
      cmd: "redis-cli ping"
      interval: 10s

# ─── Dev-Only Settings (used by `neo dev`, ignored during deploy) ───
dev:                                 # *NeoDevConfig
  env_file: .env                     # string: dev-only env file
  port: 8000                         # int: local port override
  env:                               # map[string]string: dev-only env vars
    APP_ENV: local
    APP_DEBUG: "true"
    APP_KEY: "${APP_KEY}"            # ${VAR} interpolated from env/OS
  volumes:                           # map[string]string: volume overrides
    data: ./local-data               # short form: override local path for top-level volume
    cache: ./tmp/cache:/tmp/cache    # full form: dev-only bind mount (local:container)

# ─── Deploy Hooks ───
hooks:
  pre_build: ./scripts/prepare.sh    # HookCommands: string or []string
  post_deploy:                       # HookCommands: string or []string
    - ./scripts/migrate.sh
    - ./scripts/notify.sh

# ─── Multi-Environment ───
environments:                        # map[string]NeoEnvironment
  staging:
    name: myapp-stage                # string: override container name
    server: staging-server           # string: different server
    domain: staging.myapp.com        # string: different domain
    port: 3000                       # int: different port
    https: false                     # *bool
    env:                             # map[string]string: merged with top-level (env overrides)
      NODE_ENV: staging
      DEBUG: "true"
    env_file: .env.staging           # string
    ssl:                             # *NeoSSL: overrides top-level
      certificate: ./certs/staging.pem
      private_key: ./certs/staging.key
    volumes:                         # map[string]NeoVolume: merged (env overrides same keys)
      data: /data-staging            # flat or structured format supported
    workers:                         # map[string]NeoWorker: FULL REPLACE if any defined
      queue:
        command: "python worker --queue staging"
    sidecars:                        # map[string]NeoSidecar: FULL REPLACE if any defined
      redis:
        image: redis:7-alpine
    restart: always                  # string: overrides top-level
    health:                          # *NeoHealth: overrides top-level
      cmd: "curl -f http://localhost:3000/health"
    hooks:                           # *NeoHooks: FULL REPLACE if defined
      pre_build: ./scripts/staging-build.sh
      post_deploy: ./scripts/staging-notify.sh

  production:
    domain: myapp.com
    https: true
    env:
      NODE_ENV: production
```

### Merging Rules (Environments)

| Field | Merge Strategy |
|-------|---------------|
| `env` | Merged — environment values override same keys from top-level |
| `volumes` | Merged — environment values override same keys |
| `workers` | Full replace — if environment defines any, replaces all top-level |
| `sidecars` | Full replace — if environment defines any, replaces all top-level |
| `hooks` | Full replace — if environment defines hooks, replaces top-level |
| All other fields | Simple override — environment value wins if set |

### Dev Section

The `dev:` section is used exclusively by `neo dev` and completely ignored during `neo deploy`. It does not merge with environment configs — it is a separate namespace for local development settings.

### Environment Suffix Logic

Non-production environment names automatically get `-{envname}` appended to the app name:

- **No suffix** (production names): `production`, `prod`, `main`, `default`, `live`
- **Gets suffix**: `staging` → `myapp-staging`, `preview` → `myapp-preview`
- Override with explicit `name:` in environment config

### HookCommands Type

Accepts both formats:
```yaml
# Single string
pre_build: ./scripts/build.sh

# List of strings
pre_build:
  - npm run lint
  - npm run test
  - npm run build
```

### SidecarBuild Type

Accepts both formats:
```yaml
# String shorthand (context path only)
build: ./sidecar

# Object form
build:
  context: ./sidecar
  dockerfile: Dockerfile.custom
```

---

## Internal Packages

### `internal/ssh/` — SSH Executor

Central abstraction for all remote operations.

**Type: `Executor`**

| Field | Type | Description |
|-------|------|-------------|
| `Host` | string | `user@host` format |
| `Port` | int | SSH port (default 22) |
| `Password` | string | Optional password auth |
| `PrivateKey` | []byte | Optional PEM key (programmatic) |
| `InsecureHostKey` | bool | Skip host verification (tests only) |
| `NonInteractive` | bool | Reject unknown hosts without prompting |
| `Verbose` | bool | Log commands to stderr |

**Methods:**

| Method | Signature | Description |
|--------|-----------|-------------|
| `New` | `(host string, port int) *Executor` | Create (doesn't connect) |
| `Connect` | `() error` | Establish connection |
| `Close` | `() error` | Close connection |
| `Run` | `(cmd string) (string, error)` | Execute, return stdout |
| `RunQuiet` | `(cmd string) error` | Execute, discard output |
| `Stream` | `(cmd string, stdout io.Writer) error` | Stream stdout |
| `StreamInput` | `(cmd string, stdin io.Reader) (string, error)` | Pipe stdin |
| `Upload` | `(localPath, remotePath string) error` | SCP file |
| `UploadReader` | `(r io.Reader, size int64, remotePath string, mode os.FileMode) error` | SCP from reader |
| `WriteFile` | `(remotePath string, data []byte, mode os.FileMode) error` | Write bytes |
| `ReadFile` | `(remotePath string) ([]byte, error)` | Read remote file |
| `FileExists` | `(path string) bool` | Check file exists |

**Auth priority:** In-memory key → `~/.neo/neo_ed25519` → SSH agent → `~/.ssh/id_ed25519` → `~/.ssh/id_rsa` → password fallback.

**Key Management Functions:**

| Function | Description |
|----------|-------------|
| `NeoKeyPath()` | `~/.neo/neo_ed25519` |
| `NeoKeyPubPath()` | `~/.neo/neo_ed25519.pub` |
| `NeoKeyExists()` | Check key pair exists |
| `GenerateNeoKey()` | Create ed25519 key pair, return public key |
| `LoadNeoKey()` | Read public key file |
| `HasKeyAuth()` | Any key-based auth available |
| `ShellQuote(s)` | Escape for shell (single quotes) |

---

### `internal/remote/` — Docker, Caddy, CrowdSec

#### Docker

All Docker operations as SSH commands.

**Type: `Docker`** — wraps `ssh.Executor`

| Method | Description |
|--------|-------------|
| `IsInstalled()` | Check docker available |
| `Install()` | Install via get.docker.com |
| `Version()` | Docker version string |
| `CreateNetwork(name)` | Create network (idempotent) |
| `Pull(image)` | Pull image |
| `PullStream(image, w)` | Pull with output streaming |
| `Run(opts RunOpts)` | Create and start container |
| `Stop(name)` | Stop container |
| `Start(name)` | Start stopped container |
| `Restart(name)` | Restart container |
| `Remove(name)` | Force remove container |
| `Rename(old, new)` | Rename container |
| `Logs(name, tail, follow, w)` | Stream logs |
| `IsRunning(name)` | Check running |
| `IsPortOpen(name, port)` | TCP port check via container IP |
| `ContainerStatus(name)` | Status string |
| `VolumeList()` | List volumes |
| `VolumeSize(name)` | Disk usage |
| `RemoveVolume(name)` | Delete volume |
| `Stats(format)` | Resource snapshot |
| `Exec(container, cmd)` | Run in container |
| `Build(context, dockerfile, tag, w)` | Build image |
| `LoadImage(r)` | Load from tar |
| `LoadImageGzipped(r)` | Load from gzipped tar |
| `Tag(src, dst)` | Tag image |
| `CopyVolume(vol, hostPath)` | Copy volume to host |
| `RunningContainers()` | List running names |
| `StopAll(names)` | Stop multiple |

**RunOpts:**

| Field | Type | Description |
|-------|------|-------------|
| `Name` | string | Container name |
| `Image` | string | Docker image |
| `Network` | string | Docker network |
| `Restart` | string | Restart policy |
| `Ports` | []string | `host:container` |
| `Volumes` | []string | `name:/path` |
| `Env` | map[string]string | Environment |
| `Entrypoint` | string | Override entrypoint |
| `Cmd` | string | Override cmd |
| `HealthCmd` | string | Health check command |
| `HealthInterval` | string | Check interval |
| `HealthTimeout` | string | Check timeout |
| `HealthRetries` | int | Failure threshold |
| `HealthStartPeriod` | string | Grace period |

#### Caddy

Caddy Admin API via `curl` over SSH.

**Constants:** Container `neo-caddy`, image `caddy:2-alpine`, network `neo`, admin `http://localhost:2019`.

| Method | Description |
|--------|-------------|
| `StartContainer()` | Pull, write Caddyfile, start with `--resume` |
| `Version()` | Caddy version |
| `AddRoute(appID, domains[], upstream)` | HTTPS reverse proxy route |
| `RemoveRoute(appID)` | Delete route |
| `UpdateRoute(appID, domains[], upstream)` | Replace route |
| `AddRouteHTTP(...)` | HTTP-only route |
| `UpdateRouteHTTP(...)` | Update HTTP-only route |
| `PatchUpstream(appID, dial)` | Atomic upstream update (zero-downtime) |
| `LoadCertificate(certPath, keyPath)` | Load custom TLS cert |
| `IsRunning()` | Check container running |
| `Exists()` | Check container exists |
| `Start()` | Start stopped container |
| `CheckPortConflict()` | Warn if 80/443 in use |
| `RemoveWelcomePage()` | Delete welcome route |
| `AddWelcomePage(serverIP)` | Branded HTML for direct IP |

#### CrowdSec

Firewall operations.

| Method | Description |
|--------|-------------|
| `IsInstalled()` | Check cscli available |
| `Install(w)` | Install CrowdSec + nftables bouncer |
| `ServiceStatus()` | systemd state |
| `BouncerStatus()` | Bouncer systemd state |
| `ListDecisions()` | Active bans |
| `BlockIP(ip, reason)` | Permanent ban |
| `UnblockIP(ip)` | Remove ban |

---

### `internal/state/` — Remote Server State

Stored at `/etc/neo/state.json` (600 permissions, directory 700).

**Type: `State`**

| Field | Type | Description |
|-------|------|-------------|
| `Initialized` | bool | Server initialized |
| `ServerIP` | string | Server IP address |
| `ServerArch` | string | `amd64`/`arm64` (cached) |
| `StealthMode` | bool | Welcome page hidden |
| `FirewallInstalled` | bool | CrowdSec installed |
| `Apps` | map[string]App | Installed applications |
| `Services` | map[string]SharedService | Shared services |
| `Connected` | bool | Vxero bridge connected |
| `VxeroURL` | string | Control plane URL |
| `VxeroToken` | string | API token |

**Type: `App`**

| Field | Type | Description |
|-------|------|-------------|
| `Name` | string | App name |
| `Image` | string | Docker image tag |
| `Domain` | string | Primary domain |
| `ExtraDomains` | []string | Additional domains |
| `HTTPOnly` | bool | HTTP vs HTTPS |
| `Status` | string | `running`/`stopped` |
| `InternalPort` | int | Container port |
| `InstalledAt` | string | ISO timestamp |
| `ContainerID` | string | Docker container ID |
| `Restart` | string | Restart policy |
| `Volumes` | map[string]VolumeInfo | Persistent data |
| `Env` | map[string]string | Environment variables |
| `Services` | map[string]AppService | Bundled services (legacy) |
| `Workers` | map[string]AppWorker | Background workers |
| `Sidecars` | map[string]AppSidecar | Sidecar containers |
| `Health` | *HealthCheck | Health probe config |

**Type: `VolumeInfo`** — `ContainerPath` string + `Mount` *string (optional host path).

**Type: `AppWorker`** — `Command`, `ContainerID`, `Status`, `Restart` strings.

**Type: `AppSidecar`** — `Image`, `Command`, `Status`, `Restart` strings + `Volumes`, `Env` maps + `Health` *HealthCheck.

**Type: `SharedService`**

| Field | Type | Description |
|-------|------|-------------|
| `Name` | string | Service name |
| `Image` | string | Docker image |
| `Status` | string | Running state |
| `ContainerID` | string | Container ID |
| `Port` | int | Service port |
| `CreatedAt` | string | Timestamp |
| `Env` | map[string]string | Service env |
| `Volumes` | map[string]string | Volume mounts |
| `LinkedApps` | map[string]Link | App connections |

**Type: `Link`** — `Database`, `User` strings + `EnvVars` map (injected vars).

**Functions:** `NewState()`, `Load(exec)`, `Save(exec, st)`, `Init(exec, serverIP)`.

**App Domain Methods:** `AllDomains()`, `AddDomain(domain)`, `RemoveDomain(domain)`.

---

### `internal/config/` — Local CLI Configuration

Stored at `~/.neo/config.json`.

**Type: `Config`**

| Field | Type | Description |
|-------|------|-------------|
| `Current` | string | Active server name |
| `Servers` | map[string]Server | Configured servers |
| `LicenseKey` | string | Neo+ key |

**Type: `Server`** — `Name`, `Host`, `Key` strings + `Port` int + `InitializedAt` string.

**Constants:**

| Constant | Value |
|----------|-------|
| `DefaultAPIBaseURL` | `https://get.vxero.dev/neo` |
| `DefaultVersionURL` | `https://get.vxero.dev/neo/version.json` |
| `DefaultDownloadBaseURL` | `https://get.vxero.dev/neo/download.php` |
| `DefaultDockerInstallURL` | `https://get.docker.com` |
| `DefaultFreeServerLimit` | `1` |
| `AppContainerPrefix` | `app-` |
| `SvcContainerPrefix` | `svc-` |
| `DockerNetwork` | `neo` |
| `BackupDir` | `/var/backups/neo` |

**Container Naming Functions:**

| Function | Output |
|----------|--------|
| `AppContainer("ghost")` | `app-ghost` |
| `SvcContainer("ghost", "mysql")` | `svc-ghost-mysql` |
| `WorkerContainer("ghost", "queue")` | `app-ghost-worker-queue` |
| `SvcContainerShared("mysql")` | `svc-mysql` |

**Dashboard Cache:**

`~/.neo/cache.json` — `DashboardCache` with `map[string]ServerCache` (app/service counts, reachable status). Thread-safe via `UpdateServerCache()`.

---

### `internal/app/` — App Templates

Templates embedded via `//go:embed` and served from remote `templates.json`.

**Type: `Manifest`**

| Field | Type | Description |
|-------|------|-------------|
| `Name` | string | Template identifier |
| `Title` | string | Display name |
| `Description` | string | Short description |
| `Category` | string | Grouping |
| `Version` | string | App version |
| `Image` | string | Docker image |
| `Port` | int | Internal port |
| `Volumes` | []VolumeSpec | Persistent volumes |
| `Env` | []EnvSpec | Env var definitions |
| `Services` | []ServiceSpec | Bundled services |
| `Health` | *HealthSpec | Health check |
| `Maintainer` | string | Template author |
| `Official` | bool | Vxero-maintained |
| `Tags` | []string | Search tags |
| `MinRAM` | string | Minimum RAM |

**EnvSpec special fields:**

| Field | Description |
|-------|-------------|
| `From: "domain"` | Auto-fill from user's domain |
| `FromService: "mysql"` | Auto-wire from linked service |
| `Template: "${VAR}"` | Variable substitution |
| `Generate: "hex:64"` | Random hex (64 chars) |
| `Generate: "base64:32"` | Random base64 (32 bytes) |
| `Ask: true` | Prompt user for value |

**Registry:** `NewRegistry()` → `Get(name)`, `List()`, `Categories()`.

---

### `internal/ui/` — Terminal UI

**Styles:** `Green`, `Red`, `Yellow`, `Cyan`, `Gray` (lipgloss colors), `Bold`, `Faint`.

**Output Functions:**

| Function | Icon | Description |
|----------|------|-------------|
| `Success(msg)` | green checkmark | Positive feedback |
| `Error(msg)` | red cross | Error message |
| `Info(msg)` | cyan arrow | Information |

**Components:**

| Component | Description |
|-----------|-------------|
| `Card` | Boxed output with border — `NewCard()`, `Add()`, `AddKV()`, `Blank()`, `Render()` |
| `Spinner` | Braille animation — `NewSpinner(msg)`, `Start()`, `Update()`, `Stop()` |
| `Select` | Full-screen menu — `Select(title, options) string` |
| `LiveView` | Polling display — `RunLiveView(cfg)` with configurable interval |
| `ProgressBar` | `[████░░░░] 50%` — `ProgressBar(current, total, width)` |
| `StatusBullet` | Colored status dot — `StatusBullet(status)` |
| `ReadKey()` | Single keypress read (arrows, enter, esc, q) |

---

### `internal/license/` — Licensing

**Constants:**

| Constant | Value |
|----------|-------|
| `DefaultLicenseAPIURL` | `https://neo.vxero.dev/api/license` |
| `OfflineGraceDays` | 7 |
| `MaxActivations` | 2 per key |
| `PlanFree` | `"free"` |
| `PlanPlus` | `"plus"` |

**Features:**

| Feature | Free | Plus |
|---------|------|------|
| `FeatureMultiServer` (Servers) | 1 server | Unlimited |
| `FeatureBackup` (Backups) | Blocked | Unlimited |

**Functions:** `MachineID()` (stable fingerprint), `Activate(key)`, `Validate(key)`, `Deactivate(key)`, `Check(key)` (cached, 3s timeout), `CurrentPlan(key)`, `Allowed(feature, plan, count)`, `Limit(feature, plan)`, `MaskKey(key)`.

**Caching:** Valid license cached locally. Trusted for `OfflineGraceDays` (7 days) without network check. Falls back to stale cache on network error.

---

### `internal/bridge/` — Vxero Integration

Currently disabled (`neo connect` shows "Coming Soon" / opens browser).

**VxeroClient** methods: `Whoami()`, `CreateServer()`, `ListClusters()`, `CreateService()`, `GetServiceCredentials()`, `UpdateEnvironment()`.

**Migration:** `BuildMigrationPlan(state)` analyzes apps/services. `ExecuteMigration()` transfers to Vxero platform. Service type mapping: Docker images → Vxero ServiceType.

---

## App Templates

Available pre-built templates:

| Name | Category | Description | Services |
|------|----------|-------------|----------|
| chatwoot | support | Customer engagement platform | PostgreSQL, Redis |
| ghost | blogging | Professional publishing platform | MySQL |
| gitea | developer-tools | Self-hosted Git service | — |
| miniflux | reading | RSS feed reader | PostgreSQL |
| n8n | automation | Workflow automation | — |
| plausible | analytics | Privacy-friendly analytics | PostgreSQL, ClickHouse |
| umami | analytics | Simple web analytics | PostgreSQL |
| uptime-kuma | monitoring | Self-hosted monitoring | — |
| vaultwarden | security | Bitwarden-compatible password manager | — |
| wordpress | cms | World's most popular CMS | MySQL |

Templates are fetched from remote `templates.json` at install time with embedded fallback manifests.

---

## Naming Conventions

### Container Names (Deploy)

| Type | Pattern | Example |
|------|---------|---------|
| App | `app-{name}` | `app-ghost` |
| Worker | `app-{app}-worker-{worker}` | `app-ghost-worker-queue` |
| Bundled service | `svc-{app}-{service}` | `svc-ghost-mysql` |
| Shared service | `svc-{name}` | `svc-mysql` |
| Caddy | `neo-caddy` | `neo-caddy` |

### Container Names (Dev — `neo dev`)

| Type | Pattern | Example |
|------|---------|---------|
| App | `neo-dev-{app}` | `neo-dev-my-app` |
| Worker | `neo-dev-{app}-worker-{name}` | `neo-dev-my-app-worker-queue` |
| Sidecar | `neo-dev-{app}-sidecar-{name}` | `neo-dev-my-app-sidecar-redis` |

### Docker Resources

| Resource | Pattern | Example |
|----------|---------|---------|
| Deploy network | `neo` | `neo` |
| Dev network | `neo-dev-{app}` | `neo-dev-my-app` |
| App volume | `{app}-{purpose}` | `ghost-content` |
| Shared service volume | `{svc}-data` | `mysql-data` |
| Deploy image tag | `neo-{app}:{timestamp}` | `neo-ghost:20260328-143000` |
| Dev image tag | `neo-dev-{app}:latest` | `neo-dev-my-app:latest` |

### File Paths (Server)

| Path | Description |
|------|-------------|
| `/etc/neo/state.json` | Server state (600) |
| `/etc/neo/certs/{app}/` | Custom SSL certificates |
| `/var/backups/neo/` | App backups |

### File Paths (Client)

| Path | Description |
|------|-------------|
| `~/.neo/config.json` | Server list, license |
| `~/.neo/neo_ed25519` | SSH private key |
| `~/.neo/neo_ed25519.pub` | SSH public key |
| `~/.neo/cache.json` | Dashboard cache |

---

## Deployment Deep Dive

### Full Deploy Flow (`runDeploy`)

```
1. Resolve path, validate Dockerfile exists
2. Load .neo.yml (if exists)
3. If --all: delegate to runDeployAll (parallel)
4. Resolve named environment (--to or prompt if multiple)
5. Merge environment config into top-level
6. Derive app name: --name > .neo.yml > directory name
7. Apply environment suffix (non-production gets -envname)
8. Detect port: --port > .neo.yml > Dockerfile EXPOSE > prompt
9. Resolve server: env server > .neo.yml server > --server > config.Current
10. SSH connect, load remote state
11. Memory preflight check (<150MB free → abort)
12. Merge env vars (priority: state → compose → .neo.yml → --env-file → --env)
13. Resolve domain: --temp > --no-domain > flag > state > .neo.yml > prompt
14. Auto-set APP_URL if domain exists
15. Run pre_build hook (locally, abort on failure)
16. Detect build strategy (local Docker vs remote)
17. Detect server architecture (cache in state)
18. Build image (local or remote)
19. Build volumes list
20. Start new container with -next suffix (blue-green)
21. Health check new container (120s timeout)
22. If redeploy: atomic Caddy upstream patch → stop old → rename new → restore route
23. If first deploy: rename new → add Caddy route
24. Deploy workers (serial for 1, parallel for multiple)
25. Deploy sidecars (serial, with blue-green per sidecar)
26. Save state
27. Run post_deploy hook (locally, warn on failure, don't abort)
28. Print success card
```

### Build Strategy: Local Docker

```
docker build --platform {platform} -f {dockerfile} {path}
    → docker save {tag} | gzip -1 > /tmp/neo-transfer-*.tar.gz
    → Split into 5 chunks
    → 5 parallel SSH streams upload chunks to /tmp/neo-upload-{ts}/part.0X
    → cat parts | gunzip | docker load
    → Cleanup temp dir
```

### Build Strategy: Remote (No Local Docker)

```
Walk project directory respecting .dockerignore
    → Create gzipped tar archive in memory
    → Upload to /tmp/neo-build/{app}/source.tar.gz
    → Extract on server
    → docker build -f {dockerfile} /tmp/neo-build/{app}
    → Cleanup build dir
```

### Blue-Green Swap

```
1. Run new container as app-{name}-next
2. Health check: poll TCP port connectivity (or custom health cmd)
3. If healthy and redeploy:
   a. PatchUpstream (Caddy): point to -next container (no route gap)
   b. Stop old container
   c. Remove old container
   d. Rename -next to canonical name
   e. PatchUpstream (Caddy): point to canonical name
4. If healthy and first deploy:
   a. Rename -next to canonical name
   b. AddRoute (Caddy): create reverse proxy route
5. If unhealthy:
   a. Remove -next container
   b. Old container untouched (automatic rollback)
```

### Parallel Deploy (`--all`)

```
1. Run top-level pre_build hook once
2. Single docker build (linux/amd64, shared image)
3. Save compressed image to temp file
4. For each environment (goroutines):
   a. Resolve server from environment config
   b. Open dedicated SSH connection
   c. Stream temp file into docker load
   d. Full container lifecycle (blue-green)
   e. Run per-environment post_deploy hook
5. Aggregate results, report per-environment success/failure
```

### Deploy Hooks

| Hook | When | Failure Behavior |
|------|------|-----------------|
| `pre_build` | After config resolution, before Docker build | Abort deploy |
| `post_deploy` | After state save, before success card | Log warning, continue |

Hook environment variables:

| Variable | Description |
|----------|-------------|
| `NEO_APP` | App name being deployed |
| `NEO_ENV` | Environment name (empty for top-level) |
| `NEO_DOMAIN` | Assigned domain |
| `NEO_SERVER` | Server host |

Hooks run via `sh -c` in project directory, inherit parent env + `NEO_*` vars.

### `--env-only` Fast Path

Skips build entirely. Stops container, recreates with same image + updated env, health check, persist state.

---

## Shared Services

### Supported Types

| Type | Image | Port | Volume Path |
|------|-------|------|-------------|
| mysql | `mysql:8.0` | 3306 | `/var/lib/mysql` |
| postgres | `postgres:16-alpine` | 5432 | `/var/lib/postgresql/data` |
| redis | `redis:7-alpine` | 6379 | `/data` |
| mariadb | `mariadb:11` | 3306 | `/var/lib/mysql` |

### Linking Details

**MySQL/MariaDB:**
- Creates database: `{app}_db` (dashes → underscores)
- Creates user: `{app}` with random 32-char hex password
- Injects: `DATABASE_URL`, `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`

**PostgreSQL:**
- Same structure, `postgres://` scheme

**Redis:**
- Assigns next available DB number (0, 1, 2...)
- Injects: `REDIS_URL` with DB number path

### Install Integration

When `neo install` deploys a template that needs a service (e.g., Ghost → MySQL), if a compatible shared service already exists, the user is prompted to reuse it instead of creating a bundled one.

---

## Licensing & Feature Gates

### Plans

| Feature | Free | Plus |
|---------|------|------|
| Servers | 1 | Unlimited |
| Backups | Blocked | Unlimited |
| All other features | Full access | Full access |

### Validation Flow

```
Check(key):
  1. If cached status exists and within grace period (7 days) → return cached
  2. HTTP POST to license API (3s timeout)
  3. On success: cache result, return
  4. On network error: return stale cache if exists, else free status
```

**Machine ID:** `SHA256(hostname + OS + arch)` first 8 bytes. Max 2 activations per key.

---

## Security Features

### Stealth Mode

`neo stealth` toggles welcome page removal. When enabled:
- Direct IP access returns nothing (no welcome page)
- Only configured domains serve traffic
- Reduces server fingerprinting surface

### CrowdSec Firewall

`neo firewall install` sets up:
- CrowdSec engine (detects brute-force patterns)
- nftables bouncer (enforces bans at network level)
- Community blocklist sync (known bad IPs)
- Auto-bans on SSH brute-force, HTTP scanning

Manual controls: `block <ip>`, `unblock <ip>`, `list`, `status`.

---

## Testing Infrastructure

### Docker Sandbox (Local)

Spins up Docker containers simulating real VPS (Docker-in-Docker + SSH).

```
make sandbox                    # all 13 distros
make sandbox-supported          # supported only (full suite)
make sandbox-unsupported        # unsupported only (rejection tests)
make sandbox-distro DISTRO=X    # single distro
```

**Test phases (supported):** SSH connect → OS detection → server init → template install → app lifecycle → env vars → domain → volumes → update/remove → deploy + build.

**Test phases (unsupported):** SSH connect → OS detection → validate rejection.

Structure: `test/sandbox/` (Dockerfiles, compose, entrypoint, test script) + `internal/sandbox/` (Go test code).

### Real VPS Tests (DigitalOcean)

```
make build-neotest
./bin/neotest --token $DIGITALOCEAN_TOKEN
```

Creates real droplet, runs full test suite, destroys. `--keep` flag preserves VM.

Code: `internal/testinfra/` (DO API client, test runner, ephemeral SSH keys).

---

## Self-Update System

**Version check:** `GET https://get.vxero.dev/neo/version.json` → `{"version":"0.2.0","released":"2026-03-19"}`

**Download:** `GET https://get.vxero.dev/neo/download.php?os={os}&arch={arch}`

**Supported platforms:** darwin/linux/windows × amd64/arm64/arm.

**Build-time stamping:** `-ldflags "-X main.version=0.2.0"`

---

## OS Support Matrix

| Distro | Versions | Package Manager | Status |
|--------|----------|-----------------|--------|
| Ubuntu | 24.04+ | apt | Supported |
| Debian | Any | apt | Supported |
| Fedora | 39+ | dnf | Supported |
| CentOS Stream | 9+ | dnf | Supported |
| RHEL | 9+ | dnf | Supported |
| AlmaLinux | 9+ | dnf | Supported |
| Rocky Linux | 9+ | dnf | Supported |
| Ubuntu <24.04 | | | Rejected |
| CentOS 7 | | | Rejected |
| Fedora <39 | | | Rejected |

OS detection reads `/etc/os-release` for `ID` and `VERSION_ID`. Unsupported → clear error, `init` aborts.
