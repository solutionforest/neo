# Neo — Claude Instructions

This is `vxero-neo` (command: `neo`) — a Go CLI for managing remote servers over SSH. It runs locally and executes all Docker/Caddy operations on the remote server via SSH.

## Build Requirements

**Docker is the default build path.** We do not rely on the host Go toolchain for normal builds.

```bash
cd neo
make build       # Dockerized build → bin/neo
make build-all   # Dockerized cross-compile → dist/
make image-build # Runtime image → vxero/neo:latest
```

**Never run `go get @latest`** for charmbracelet packages — use `go mod tidy` only.

## Module

```
module github.com/vxero/neo
go 1.23
```

## Key Dependencies and Their Quirks

- **`charmbracelet/huh v1.0.0`** — the `huh/spinner` sub-package does NOT exist in v1.0.0. We use a manual braille spinner in `internal/ui/spinner.go`.
- **`charmbracelet/lipgloss v1.1.0`** — for colors and terminal styling.
- **`golang.org/x/crypto/ssh`** — SSH client with key-based auth, ssh-agent, and known_hosts support.
- **No Docker SDK** — all Docker commands run as shell commands over SSH.
- **No table library** — uses manual formatting with `fmt.Sprintf` alignment.

## Architecture

### SSH Executor (`internal/ssh/executor.go`)
Central abstraction. Every remote operation goes through here:
- `Run(cmd)` → execute + capture stdout
- `Stream(cmd, writer)` → stream output
- `StreamInput(cmd, reader)` → pipe reader into stdin, return stdout
- `Upload(local, remote)` → SCP file
- `UploadReader(reader, size, remote, mode)` → SCP from reader
- `ReadFile(path)` → read remote file
- `WriteFile(path, data, mode)` → write remote file via SCP
- `FileExists(path)` → test -f

Auth priority: ssh-agent → ~/.ssh/id_ed25519 → ~/.ssh/id_rsa → password (prompted if no keys found)
- `HasKeyAuth()` — checks if any key-based auth is available
- `Password` field on Executor — set before `Connect()` for password auth

### Remote Docker (`internal/remote/docker.go`)
All Docker operations as SSH commands. Key methods:
- `Pull`, `Run`, `Stop`, `Start`, `Restart`, `Remove`
- `Build(contextDir, dockerfile, tag, writer)` — build image on server
- `LoadImage(writer)` — docker load from stdin
- `Tag(src, dst)` — tag an image
- `Logs(name, tail, follow, writer)`
- `IsRunning`, `ContainerStatus`
- `CopyVolume` — for volume mounting

### Remote Caddy (`internal/remote/caddy.go`)
Caddy Admin API calls via `curl` over SSH:
- `StartContainer()` — launches neo-caddy with auto-SSL
- `AddRoute(appID, domain, upstream)` — adds reverse proxy route
- `RemoveRoute(appID)` — removes route by ID
- `UpdateRoute` — remove + add (atomic replace)

### Config (`internal/config/config.go`)
Local multi-server config at `~/.neo/config.json`:
```json
{
  "current": "production",
  "servers": {
    "production": { "name": "production", "host": "root@1.2.3.4", "port": 22 }
  }
}
```

### State (`internal/state/state.go`)
Remote server state at `/etc/neo/state.json` — read/written over SSH:
```json
{
  "initialized": true,
  "server_ip": "1.2.3.4",
  "apps": { "ghost": { "name": "ghost", "image": "...", "domain": "...", "status": "running" } }
}
```

### App Registry (`internal/app/`)
YAML manifests embedded in the binary via `//go:embed`. Each template defines:
- Image, port, volumes, env vars
- Bundled services (postgres, mysql, redis, clickhouse)
- Health check endpoint
- Auto-generation specs for secrets (`generate: hex:64`)

### UI (`internal/ui/`)
- **banner.go** — ASCII logo with ⚡ emoji
- **spinner.go** — braille spinner (goroutine-based, thread-safe)
- **cards.go** — boxed success/info cards
- **styles.go** — lipgloss color constants
- **progress.go** — progress bar + status bullets

## Self-Update

- `neo version` — shows current version, checks `version.json` on the download server for updates
- `neo upgrade` — downloads the latest binary for the current OS/arch and replaces itself in-place
- Version check endpoint: `https://get.vxero.dev/neo/version.json` → `{"version":"0.2.0","released":"2026-03-19"}`
- Download endpoint: `https://get.vxero.dev/neo/download.php?os=<os>&arch=<arch>`
- Version is stamped at build time via `-ldflags "-X main.version=0.2.0"`

## OS Requirements

`neo init` validates the server OS before proceeding. Supported distros:
- **Ubuntu 24.04+**
- **Debian** (any version)
- **Fedora 39+**
- **CentOS / RHEL / AlmaLinux / Rocky 9+**

The check reads `/etc/os-release` for `ID` and `VERSION_ID`. Unsupported distros or old versions get a clear error and `init` aborts. Package manager is auto-detected: `apt` for Debian/Ubuntu, `dnf` for RPM-based distros.

## Command Pattern

All commands follow the same structure:

```go
func newFooCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "foo <arg>",
        Short: "Description",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            return runFoo(args[0])
        },
    }
}
```

### Interactive Dashboard (`dashboard.go`):
`neo` with no arguments launches an interactive TUI loop:
- `tuiMainMenu(cfg)` — top-level menu (Servers, Applications, Deploy, Install, Connect)
- `tuiServersMenu(cfg)` — list/add/switch servers
- `tuiAppsMenu(cfg)` — list apps, select one for actions
- `tuiAppActions(appName, exec, st)` — start/stop/restart/logs/domain/update/remove

### Environment Variables (`env.go`, `envfile.go`, `compose.go`, `neoconfig.go`):
- `neo env <app>` — list env vars (masks secrets)
- `neo env set <app> KEY=VALUE` — set vars, auto-restarts container
- `neo env unset <app> KEY` — remove vars, auto-restarts container
- `neo env import <app> .env` — bulk import from .env file
- `neo deploy --env KEY=VALUE` — set env on deploy (repeatable `-e`)
- `neo deploy --env-file .env` — load env file on deploy

**Env var priority** (highest wins): CLI `--env` > `--env-file` > `.neo.yml` env > `docker-compose.yml` > server state (redeploy)

### Project Config (`.neo.yml`):
Optional file in project root. All fields optional:
```yaml
name: my-app              # app name (default: directory name)
domain: app.example.com   # domain (default: prompt)
port: 8080                # container port (default: Dockerfile EXPOSE)
https: true               # nil=default, true=HTTPS, false=HTTP-only
env_file: .env.production # load env vars from file
compose_service: app      # which docker-compose service to extract from
restart: unless-stopped   # Docker restart policy
env:                      # env var defaults (non-sensitive)
  APP_ENV: production
  LOG_LEVEL: info

# Deploy lifecycle hooks (run locally)
hooks:
  pre_build:              # before Docker build
    - npm run build
    - npm test
  post_deploy:            # after successful deploy
    - curl -X POST https://hooks.slack.com/...

# Health check
health:
  cmd: "curl -f http://localhost:8080/health"
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 40s

# Background workers (separate containers sharing app image)
workers:
  queue:
    command: "node worker.js"
    restart: always

# Sidecar containers (separate images, same network)
sidecars:
  redis:
    image: redis:7-alpine
    volumes:
      data: /data

# Persistent volumes
volumes:
  uploads:
    path: /app/uploads

# Custom SSL certificates
ssl:
  certificate: certs/cert.pem
  private_key: certs/key.pem

# Named deployment environments (override top-level fields)
environments:
  staging:
    server: staging-server
    domain: staging.example.com
    env:
      APP_ENV: staging
    hooks:
      pre_build: ["npm test"]
  production:
    server: prod-server
    domain: app.example.com
    env:
      APP_ENV: production
```

### Docker Compose Auto-Detection:
If a `docker-compose.yml` / `compose.yml` exists in the project dir, `neo deploy` auto-extracts:
- Environment variables (map or list format)
- `env_file` references
- Container port from `ports:`
- Auto-detects the app service (prefers `build:` context, skips infra images like mysql/redis/postgres)
- Use `compose_service` in `.neo.yml` to specify which service if auto-detection fails

### Deploy Hooks (`hooks.go`):
Local shell commands that run during deploy lifecycle:
- **`pre_build`** — runs before Docker build (e.g., `npm test`, `npm run build`)
- **`post_deploy`** — runs after successful deploy (e.g., Slack notification)
- Commands run via `sh -c` with NEO_* environment variables: `NEO_APP`, `NEO_ENV`, `NEO_DOMAIN`, `NEO_SERVER`
- Hooks abort on first failure
- Environment-level hooks in `.neo.yml` fully replace top-level hooks

### Workers and Sidecars:
- **Workers** — background containers sharing the app image but running a different command (e.g., queue workers)
- **Sidecars** — separate containers with their own image/build, running alongside the app on the same Docker network
- Both support per-environment overrides in `.neo.yml`

### Shared helpers in `root.go`:
- `resolveServer(cfg)` — resolves --server flag or config.Current
- `connectSSH(srv)` — creates and connects SSH executor
- `mustResolveAndConnect()` — load config + resolve server + SSH connect (returns cfg, srv, exec, err)

### Vxero Transfer (`internal/bridge/`):
- **Currently disabled** — `neo connect` shows "Coming Soon" card
- `api.go` — lightweight Vxero REST API client (VxeroClient)
- `migrate.go` — `BuildMigrationPlan(state)` analyzes apps/services and creates a plan
- `connect.go` — agent install (one-way, no disconnect)
- Service type mapping: Docker images → Vxero ServiceType (postgres→postgresql, mysql→mysql, redis→redis, etc.)
- Bridge code is retained in `internal/bridge/` for future activation

### Help system (`root.go`):
- `neo help` — categorized command listing (Getting Started, Apps, Lifecycle, Data, Servers, Vxero)
- `neo --help` — compact usage with pointer to `neo help`
- `printHelp()` in root.go generates the grouped output with colors

### Adding a New Command

1. Create `commands/<name>.go` with `func new<Name>Cmd() *cobra.Command`
2. Register in `root.go`'s `root.AddCommand(...)` block
3. Use `mustResolveAndConnect()` to get SSH executor
4. Use `state.Load(exec)` to read remote state
5. Use `remote.NewDocker(exec)` / `remote.NewCaddy(exec)` for operations
6. Use `state.Save(exec, st)` to persist changes

### Adding a New App Template

1. Create `internal/app/templates/<name>.yml`
2. Follow the manifest schema (see existing templates)
3. The registry auto-discovers it via `//go:embed`

## Shared Services

Server-level shared services allow multiple apps to share a single database or cache instance, saving RAM on small VMs.

### State
- `state.Services` — `map[string]SharedService` at server level (not nested under apps)
- Each `SharedService` has `LinkedApps` — tracks which apps use it and what DB/user was created

### Commands
- `neo service create [type] [name]` — create a shared MySQL, Postgres, Redis, or MariaDB
- `neo service list` — list shared services and their linked apps
- `neo service link <svc> <app>` — creates a database + user in the service, injects `DATABASE_URL`/`DB_*` env vars into the app
- `neo service unlink <svc> <app>` — removes injected env vars (keeps data)
- `neo service start|stop|restart <svc>` — lifecycle management (warns if apps are linked)
- `neo service remove <svc>` — blocked if apps are still linked
- `neo service logs <svc>` — stream service container logs

### Install Integration
When installing a template app that needs a service (e.g., Ghost → MySQL), if a compatible shared service exists, the user is prompted to reuse it instead of creating a new bundled one.

### Container Naming
- Shared service containers: `svc-<name>` (e.g., `svc-mysql`)
- Bundled service containers (legacy): `svc-<app>-<service>` (e.g., `svc-ghost-mysql`)

## Docker Naming Conventions

- App containers: `app-<name>` (e.g., `app-ghost`)
- Shared service containers: `svc-<name>` (e.g., `svc-mysql`, `svc-redis`)
- Bundled service containers (legacy): `svc-<app>-<service>` (e.g., `svc-ghost-mysql`)
- Caddy container: `neo-caddy`
- Docker network: `neo`
- Volumes: `<app>-<purpose>` (e.g., `ghost-content`, `ghost-mysql`), `<svc>-data` (shared services)

## Neo+ Licensing (`internal/license/`)

Feature-gated commercial tier:
- **`neo plus`** — interactive license management menu
- **`neo plus activate <key>`** — activate license on this machine
- **`neo plus status`** — show current license state
- **`neo plus deactivate`** — remove license from machine

### Feature Gates
- `FeatureMultiServer` — Free: 1 server, Plus: unlimited
- `FeatureBackup` — Free: blocked, Plus: unlimited
- Max 2 device activations per license key

### License Validation
- API: `https://neo.vxero.dev/api/license` (overridable via `NEO_LICENSE_URL` env var)
- Machine fingerprint: SHA-256 of `hostname-os-arch`
- Offline cache: `~/.neo/license.json` with 7-day grace period
- Config stores license key in `~/.neo/config.json` as `license_key`

## CrowdSec / Firewall (`commands/firewall.go`, `internal/remote/crowdsec.go`)

CrowdSec intrusion prevention via SSH:
- `neo firewall install` — install CrowdSec + nftables bouncer on server
- `neo firewall status` — show CrowdSec status and decision count
- `neo firewall block <ip>` — manually ban an IP
- `neo firewall unblock <ip>` — remove ban
- `neo firewall list` — list active decisions (bans)

### Stealth Mode (`commands/stealth.go`)
- `neo stealth` — toggle: hides server from IP-based discovery by removing Caddy's catch-all welcome page. Only configured domains serve traffic.

## Additional Commands

- **`neo dev [down]`** — local development: wraps `docker compose` with Neo's env loading. Flags: `--build`, `--detach`
- **`neo db <app> [shell]`** — interactive TUI database browser for app's linked DB, or raw `mysql`/`psql` shell
- **`neo ask`** — interactive skill assistant, guides through common tasks via Q&A
- **`neo sync [app]`** — sync server state back to `.neo.yml` (shows diff before writing). Flag: `--dry-run`
- **`neo backup <app>`** / **`neo restore <app> <backup>`** — volume backup/restore (Neo+ feature)

## Platform-Specific Code

- `exec_unix.go` — uses `syscall.Exec` for `neo ssh` (replaces process)
- `exec_windows.go` — uses `os/exec.Command` fallback

## Testing

### Unit Tests

```bash
make test        # go test ./...
```

### Docker Sandbox (Integration Tests)

The sandbox spins up Docker containers that simulate real VPS servers (Docker-in-Docker with SSH), runs `neo init`, deploys apps, tests lifecycle operations, then tears everything down. No real VPS or cloud API token needed.

```bash
make sandbox                           # test all 13 distros
make sandbox-supported                 # only supported distros (full test suite)
make sandbox-unsupported               # only unsupported distros (OS rejection test)
make sandbox-distro DISTRO=debian-12   # single distro
make sandbox-list                      # show the distro matrix
make sandbox-keep                      # keep containers alive after tests
make sandbox-down                      # tear down all sandbox containers
```

#### Distro Matrix

| Distro | Port | Expected | Dockerfile |
|---|---|---|---|
| Ubuntu 24.04 | 2224 | supported | Dockerfile |
| Ubuntu 24.10 | 2225 | supported | Dockerfile |
| Debian 12 | 2230 | supported | Dockerfile |
| Debian 11 | 2231 | supported | Dockerfile |
| Fedora 41 | 2240 | supported | Dockerfile.rpm |
| Fedora 40 | 2241 | supported | Dockerfile.rpm |
| CentOS Stream 9 | 2250 | supported | Dockerfile.rpm |
| AlmaLinux 9 | 2251 | supported | Dockerfile.rpm |
| Rocky 9 | 2252 | supported | Dockerfile.rpm |
| Ubuntu 22.04 | 2222 | rejected | Dockerfile |
| Ubuntu 20.04 | 2220 | rejected | Dockerfile |
| CentOS 7 | 2253 | rejected | Dockerfile.legacy |
| Fedora 38 | 2242 | rejected | Dockerfile.legacy |

Supported distros run 9 test phases (30 steps): SSH connect, server init, template install, app lifecycle, env vars, domain, volumes, update/remove, deploy + build.
Unsupported distros only test SSH + OS detection to verify `validateOS()` correctly rejects them.

#### Sandbox Structure

```
test/sandbox/
├── Dockerfile          # apt-based (Ubuntu, Debian)
├── Dockerfile.rpm      # dnf-based (Fedora, CentOS, Alma, Rocky)
├── Dockerfile.legacy   # SSH-only, no DinD (for unsupported OS rejection tests)
├── docker-compose.yml  # all 13 services with unique ports
├── entrypoint.sh       # starts dockerd + sshd
└── run-tests.sh        # automation: build → start → inject key → test → destroy
```

Go test code:
- `internal/sandbox/matrix.go` — distro definitions (name, image, port, supported flag)
- `internal/sandbox/runner.go` — test runner (reuses `testinfra.PrintResults` for reporting)
- `cmd/neosandbox/main.go` — CLI entry point

### Real VPS Tests (DigitalOcean)

For production-like testing with real networking, DNS, and SSL:

```bash
make build-neotest
./bin/neotest --token $DIGITALOCEAN_TOKEN   # creates droplet, tests, destroys
./bin/neotest --keep                        # keep VM alive for manual testing
```

Code: `internal/testinfra/` + `cmd/neotest/`

### Manual Smoke Tests

```bash
make build
./bin/neo --help
make image-build
docker run --rm vxero/neo:latest --help
./bin/neo                    # dashboard (no server configured)
./bin/neo init root@<ip>     # test with a real VPS
./bin/neo install            # interactive app picker
```

## Directory Layout

```
cmd/neo/main.go              # CLI entry point
cmd/neotest/main.go          # DigitalOcean integration test runner
cmd/neosandbox/main.go       # Docker sandbox test runner
commands/                    # All command implementations (~35 files)
internal/
  app/                       # App template system + embedded YAML manifests
    templates/               # 10 app templates (ghost, wordpress, gitea, etc.)
  bridge/                    # Vxero migration API (currently disabled)
  config/                    # Local config (~/.neo/config.json), cache, file locking
  license/                   # Neo+ licensing (feature gates, API client, offline cache)
  remote/                    # Remote operations via SSH (docker.go, caddy.go, crowdsec.go)
  sandbox/                   # Docker sandbox test matrix and runner
  ssh/                       # SSH executor (central abstraction for all remote ops)
  state/                     # Remote server state (/etc/neo/state.json)
  testinfra/                 # DigitalOcean integration test infrastructure
  ui/                        # TUI components (spinner, cards, progress, selection)
neo-builder/                 # Build service (separate Go module)
scripts/                     # build-template-index.go, validate-templates.go
site/                        # Website, download server, install script
test/sandbox/                # Docker Compose sandbox (13 distros)
plans/                       # Planning documents
```

## All CLI Commands

| Command | Description |
|---------|-------------|
| `neo` (no args) | Interactive TUI dashboard |
| `neo init <user@host>` | Initialize remote server |
| `neo deploy [app]` | Deploy app/project to server |
| `neo install` | Interactive app template picker |
| `neo list` | List apps on server |
| `neo status` | Show app/service status |
| `neo start/stop/restart <app>` | App lifecycle |
| `neo remove <app>` | Remove app from server |
| `neo update <app> <image>` | Update app image |
| `neo logs <app>` | View app logs |
| `neo domain <app> <domain>` | Set/update app domain |
| `neo env <app>` | List/set/unset/import env vars |
| `neo volumes <app>` | List app volumes |
| `neo service create/list/link/unlink/remove` | Shared services |
| `neo backup <app>` | Backup app data (Neo+) |
| `neo restore <app> <backup>` | Restore from backup (Neo+) |
| `neo db <app> [shell]` | Interactive database browser |
| `neo dev [down]` | Local development with docker compose |
| `neo sync [app]` | Sync server state to .neo.yml |
| `neo run <cmd>` | Execute command on server |
| `neo ssh` | SSH into server |
| `neo servers` | List configured servers |
| `neo use <name>` | Switch active server |
| `neo config` | Manage local config |
| `neo firewall install/status/block/unblock/list` | CrowdSec firewall |
| `neo stealth` | Toggle stealth mode |
| `neo plus activate/status/deactivate` | Neo+ license management |
| `neo connect` | Vxero bridge (Coming Soon) |
| `neo ask` | Interactive skill assistant |
| `neo version` | Show version, check for updates |
| `neo upgrade` | Self-update binary |
| `neo help` | Grouped command help |

## Differences from Vxero SaaS CLI (`cli/`)

| | `cli/` (Vxero CLI) | `neo/` (Vxero Neo) |
|---|---|---|
| Purpose | Manage Vxero SaaS platform | Manage raw servers over SSH |
| Auth | API token to Vxero control plane | SSH keys to servers |
| Server-side | Vxero agent + control plane | Pure Docker + Caddy |
| Config | `~/.vxero/config.yml` | `~/.neo/config.json` |
| State | Server-side (Vxero DB) | `/etc/neo/state.json` on each server |
| Bridge | N/A | `neo connect` (coming soon — transfers server to Vxero) |
