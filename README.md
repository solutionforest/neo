# ⚡ Vxero Neo

**Remote server management over SSH.** Deploy Docker apps, manage Caddy reverse proxy, and handle backups — all from your laptop. No agent required.

```
  ▀▄▀ █ █ █▀▀ █▀█ █▀█   ┃   █▄ █ █▀▀ █▀█
  █ █ ▀▄▀ ██▄ █▀▄ █▄█   ┃   █ ▀█ ██▄ █▄█
  ⚡ neo v1.0.0
```

Like [Kamal](https://kamal-deploy.org/) — you run it locally, it SSHes into your VPS and manages everything remotely.

```
Your Mac / Laptop (runs neo)
    │
    │  SSH (key-based auth)
    │
    ▼
Remote VPS (any provider)
    └── Docker
          ├── neo-caddy       (reverse proxy + auto SSL)
          ├── app-plausible   (your app)
          ├── app-ghost       (your app)
          ├── svc-postgres    (bundled database)
          └── ... all on "neo" Docker network
```

## Using Neo with AI Agents (Skills)

Coding with Claude, Cursor, or another AI agent? Install the **Neo skills** so your
agent knows how to drive neo correctly — commands, `.neo.yml` config, deploy flow,
and gotchas — instead of guessing.

👉 **[github.com/solutionforest/neo-skill](https://github.com/solutionforest/neo-skill)**

The skills teach an agent the same things a fluent neo user knows, so it can init
servers, write `.neo.yml`, deploy, and troubleshoot as well as it knows the tool.

**Quick install** (auto-detects Claude Code, Cursor, Copilot, Windsurf, Cline):

```bash
git clone https://github.com/solutionforest/neo-skill.git
cd neo-skill
./install.sh /path/to/your/project
```

Claude Code users can add it as a plugin instead:

```bash
claude plugin add /path/to/neo-skill
```

See the [neo-skill README](https://github.com/solutionforest/neo-skill) for per-tool manual steps.

## Installation

```bash
# macOS
brew install vxero/tap/neo

# Linux / macOS (curl)
curl -fsSL https://get.vxero.dev/neo | sh

# Build local binaries using Dockerized Go
cd neo && make build
```

## Build With Docker

`neo` now builds through Docker by default. `make build` and `make build-all` do not use your host Go toolchain.

Build a local binary for your current platform:

```bash
cd neo
make build
./bin/neo --help
```

Cross-compile all release binaries through Docker:

```bash
cd neo
make build-all
```

If you want to build and run the CLI as a container image:

```bash
cd neo
make image-build
```

That builds `vxero/neo:latest` from the local `Dockerfile`.

Run it with your SSH keys, local neo config, and current working directory mounted:

```bash
docker run --rm -it \
  -v "$HOME/.ssh:/root/.ssh:ro" \
  -v "$HOME/.neo:/root/.neo" \
  -v "$PWD:/workspace" \
  -w /workspace \
  vxero/neo:latest
```

Or use the Make target:

```bash
make docker-run
```

Example: run the containerized CLI against a project directory and deploy it:

```bash
docker run --rm -it \
  -v "$HOME/.ssh:/root/.ssh:ro" \
  -v "$HOME/.neo:/root/.neo" \
  -v "/path/to/your-app:/workspace" \
  -w /workspace \
  vxero/neo:latest deploy . --domain app.example.com
```

Notes:
- `make build` writes `bin/neo` using the `golang:1.24-alpine` Docker image.
- `make build-all` writes cross-platform binaries to `dist/` through the same Dockerized build flow.
- `~/.ssh` is mounted read-only so the container can use your existing SSH keys.
- `~/.neo` is mounted so configured servers persist between container runs.
- `"$PWD:/workspace"` lets you run `neo deploy .` against the project on your host machine.

---

## Quick Start

### 1. Run `neo` — everything starts here

```bash
$ neo
```

```
  ▀▄▀ █ █ █▀▀ █▀█ █▀█   ┃   █▄ █ █▀▀ █▀█
  █ █ ▀▄▀ ██▄ █▀▄ █▄█   ┃   █ ▀█ ██▄ █▄█
  ⚡ neo v1.0.0

  No servers configured.

  ? Server SSH address
  │ root@159.65.100.42

  ? SSH password           ← only if no SSH keys found
  │ ••••••••••

  ? Server name (leave empty to auto-detect)
  │ production

  Initializing server...

  ✓ Connected (Ubuntu 24.04 LTS, 4GB RAM, 2 CPU)
  ✓ System packages updated
  ✓ Docker 27.1.1 installed
  ✓ Docker network "neo" created
  ✓ Caddy 2.9.1 running (ports 80, 443)
  ✓ State initialized
```

First run detects no servers and drops you straight into setup — asks for SSH password if no keys are found, runs `apt update && upgrade`, then installs Docker and Caddy. After init completes, you land in the **interactive dashboard**.

### 2. The interactive dashboard

Every time you run `neo`, you get a full TUI hub:

```
  ▀▄▀ █ █ █▀▀ █▀█ █▀█   ┃   █▄ █ █▀▀ █▀█
  █ █ ▀▄▀ ██▄ █▀▄ █▄█   ┃   █ ▀█ ██▄ █▄█
  ⚡ neo v1.0.0

  Server: production (root@159.65.100.42)

  ? What would you like to do?
  > Servers              2 configured
    Applications         3 apps, 2 running
    Deploy Project       deploy local repo
    Install App          from template catalog
    Transfer to Vxero    one-time migration to K3s
    Quit
```

#### Servers submenu

```
  Servers
  ─────────────────────────────────────────────────
  ● production      root@159.65.100.42    (active)
    staging         root@staging.mysite.com

  ? ›
  > Add New Server
    Switch Server
    ← Back
```

#### Applications submenu

```
  Apps on production
  ──────────────────────────────────────────────────────
  ● plausible       analytics.mysite.com              running
  ● gitea           git.mysite.com                    running
  ○ ghost           blog.mysite.com                   stopped

  3 apps · 2 running · 1 stopped

  ? Select an app to manage
  > ● plausible       analytics.mysite.com
    ● gitea           git.mysite.com
    ○ ghost           blog.mysite.com
    ← Back
```

#### App actions

```
  plausible  ghcr.io/plausible/community-edition:v2.1.4
  https://analytics.mysite.com
  Status: ● running
  └ postgres

  ? Action for plausible
  > View Logs
    Restart
    Stop
    Change Domain
    Update Image
    Remove
    ← Back
```

### 3. Install an app (from the dashboard or CLI)

From the dashboard, choose **Install App**, or run directly:

```bash
$ neo install plausible
```

```
  Installing Plausible Analytics v2.1.4
  Server: production (159.65.100.42)

  ? Domain for Plausible Analytics
  │ analytics.mysite.com

  ? Deploy Plausible Analytics? Yes, deploy

  ✓ Pulled postgres:16-alpine
  ✓ Started svc-plausible-postgres
  ✓ Pulled clickhouse/clickhouse-server:24.3-alpine
  ✓ Started svc-plausible-clickhouse
  ✓ Pulled ghcr.io/plausible/community-edition:v2.1.4
  ✓ Container started
  ✓ SSL certificate issued for analytics.mysite.com
  ✓ Health check passed

  ╭────────────────────────────────────────────╮
  │  ✓ Plausible Analytics is live!            │
  │                                            │
  │  URL:     https://analytics.mysite.com     │
  │                                            │
  │  Data stored on server:                    │
  │    plausible-events  →  docker volume      │
  │                                            │
  │  Mount to external drive:                  │
  │  neo volumes mount plausible-events        │
  │    /mnt/ssd/plausible                      │
  ╰────────────────────────────────────────────╯
```

### 4. Deploy your own project

```bash
$ neo deploy --domain myapp.example.com
```

```
  Deploying my-app
  Server: production (159.65.100.42)
  Domain: myapp.example.com
  Port:   8080

  → Docker detected locally — building on this machine
  ✓ Image built locally
  ✓ Image transferred to server
  ✓ New container started
  ✓ Health check passed
  ✓ SSL certificate issued for myapp.example.com

  ╭────────────────────────────────────────────╮
  │  ✓ my-app is live!                         │
  │                                            │
  │  URL:     https://myapp.example.com        │
  │                                            │
  │  Redeploy after changes:                   │
  │    neo deploy                              │
  ╰────────────────────────────────────────────╯
```

Redeployments use **zero-downtime blue-green swaps** — the new container starts alongside the old one, gets health-checked, then Caddy switches traffic instantly:

```
  Redeploying my-app
  ...
  ✓ New container started
  ✓ Health check passed
  ✓ Traffic switched to new version (myapp.example.com)
```

If the new container fails the health check, it's automatically rolled back:

```
  ✗ New container failed health check — rolled back
  → Old version still running. Debug with: neo logs my-app
```

No local Docker? No problem — neo uploads your source and builds on the server:

```
  → No local Docker — building on server
  ✓ Source packaged (12.4 MB)
  ✓ Source uploaded
  → Building image on server (this may take a while)...
  ✓ Image built on server
```

---

## Command Examples

### `neo list`

```bash
$ neo list
```

```
  Server: production (159.65.100.42)

  NAME               DOMAIN                            STATUS       IMAGE
  ──────────────────────────────────────────────────────────────────────────────
  ● plausible        analytics.mysite.com              running      ghcr.io/plausible/community-edition:v2.1.4
  ● gitea            git.mysite.com                    running      gitea/gitea:1.22-rootless
  ○ ghost            blog.mysite.com                   stopped      ghost:5-alpine

  3 apps · 2 running · 1 stopped
```

### `neo install` (interactive picker)

```bash
$ neo install
```

```
  ? Choose an app to install

  > chatwoot         Open-source customer engagement platform
    ghost            Professional publishing platform
    gitea            Lightweight self-hosted Git service
    miniflux         Minimalist and opinionated RSS feed reader
    n8n              Workflow automation tool
    plausible        Privacy-friendly Google Analytics alternative
    umami            Simple, fast, privacy-focused web analytics
    uptime-kuma      Self-hosted monitoring tool
    vaultwarden      Lightweight Bitwarden-compatible password manager
    wordpress        The world's most popular CMS
```

### `neo servers`

```bash
$ neo servers
```

```
  Servers
  ─────────────────────────────────────────────────
  ● production      root@159.65.100.42    (active)
    staging         root@staging.mysite.com
```

### `neo use`

```bash
$ neo use staging
```

```
  ✓ Switched to server "staging"
```

### `neo start` / `neo stop` / `neo restart`

```bash
$ neo start ghost
```

```
  ✓ ghost started
```

```bash
$ neo stop plausible
```

```
  ✓ plausible stopped
```

```bash
$ neo restart gitea
```

```
  ✓ gitea restarted
```

### `neo update`

```bash
$ neo update ghost
```

```
  ✓ Image pulled
  ✓ ghost updated and running
```

### `neo remove`

```bash
$ neo remove ghost
```

```
  ? Remove ghost? Data volumes will be kept. Yes, remove

  ✓ ghost removed. Data volumes preserved on server.
```

### `neo logs`

```bash
$ neo logs plausible --tail 20 -f
```

```
  [2026-03-18 14:32:01] Plausible running on port 8000
  [2026-03-18 14:32:02] Connected to PostgreSQL
  [2026-03-18 14:32:02] Connected to ClickHouse
  [2026-03-18 14:32:05] GET /api/health 200 1ms
  [2026-03-18 14:32:10] POST /api/event 202 3ms
  ...
```

### `neo domain`

```bash
$ neo domain plausible stats.newdomain.com
```

```
  ✓ Domain updated — https://stats.newdomain.com
```

### `neo volumes`

```bash
$ neo volumes
```

```
  Volumes on production (159.65.100.42)
  ──────────────────────────────────────────────────────
  plausible-events          plausible       docker volume
  plausible-db              plausible       docker volume
  plausible-events-ch       plausible       docker volume
  gitea-data                gitea           docker volume
  gitea-db                  gitea           → /mnt/ssd/gitea-db
  ghost-content             ghost           docker volume
  ghost-mysql               ghost           docker volume
```

### `neo volumes mount`

```bash
$ neo volumes mount gitea-db /mnt/ssd/gitea-db
```

```
  ? Mount gitea-db to /mnt/ssd/gitea-db? This will briefly stop gitea. Yes, proceed

  ✓ Data copied
  ✓ gitea-db mounted to /mnt/ssd/gitea-db
```

### `neo backup`

```bash
$ neo backup plausible
```

```
  ✓ Backup created: /var/backups/neo/plausible-20260318-143200.tar.gz (2.3G)

  ? Download backup to local machine? No

  → Copy manually:
  → scp root@159.65.100.42:/var/backups/neo/plausible-20260318-143200.tar.gz .
```

### `neo restore`

```bash
$ neo restore plausible /var/backups/neo/plausible-20260318-143200.tar.gz
```

```
  ? Restore plausible from backup? This will overwrite current data. Yes, restore

  ✓ plausible restored successfully
```

### `neo connect` — One-Time Transfer to Vxero

Transfers your server from neo (Docker) to the Vxero platform (K3s). **This is a one-way, one-time migration** — after transfer, neo no longer manages this server.

```bash
$ neo connect
```

```
  ✓ Authenticated as John Doe (My Team)

  Migration Plan — One-Time Transfer
  ──────────────────────────────────────────────────────

  What will happen:
  1. Register server with Vxero control plane
  2. Install Vxero agent (heartbeat, metrics, jobs)
  3. System packages updated (apt upgrade)
  4. Your Docker apps are recorded in Vxero
  5. Vxero transitions apps from Docker → K3s

  Managed services to create:
    • postgres → Vxero managed postgresql
    • redis → Vxero managed redis

  Architecture transition:

  Before (neo/Docker)       After (Vxero/K3s)
  ─────────────────────────────────────────────────
  Docker containers         K3s pods
  Caddy reverse proxy       K3s Ingress + cert-manager
  Docker volumes            Longhorn PVCs
  neo CLI                   Vxero dashboard + CLI
  SSH-only management       Agent-based management

  Apps to transfer:
    ● plausible        analytics.mysite.com
      └ volume: plausible-events
    ● ghost            blog.mysite.com
      └ volume: ghost-content

  Important — this is a one-way transfer:
    • After transfer, this server is managed by Vxero
    • Neo will no longer manage apps on this server
    • Use the Vxero dashboard or vxero CLI going forward
    • Your apps stay running during the transfer (zero downtime)

  ? Transfer this server to Vxero? (this cannot be undone) Yes, transfer to Vxero

  ✓ Registering server with Vxero...
  ✓ Installing Vxero agent...
  ✓ Creating managed postgresql for plausible...
  ✓ Updating server state...

  ╭────────────────────────────────────────────╮
  │  ✓ Server transferred to Vxero!           │
  │                                            │
  │  2 apps now managed by Vxero              │
  │                                            │
  │  Your apps continue running — Vxero will  │
  │  transition them from Docker to K3s.      │
  │                                            │
  │  Next steps:                              │
  │    • Open Vxero dashboard to see server   │
  │    • Use the vxero CLI for management     │
  │    • neo no longer manages this server    │
  ╰────────────────────────────────────────────╯
```

### `neo ssh`

```bash
$ neo ssh
# → Opens SSH session to root@159.65.100.42

$ neo ssh --server staging
# → Opens SSH session to root@staging.mysite.com
```

---

## Command Reference

| Command | Description |
|---------|-------------|
| **Dashboard** | |
| `neo` | Interactive TUI — servers, apps, deploy, install, transfer |
| **Setup** | |
| `neo init <user@host>` | Bootstrap a server (Docker, Caddy, state) |
| `neo init <user@host> --name staging` | Name the server |
| **Apps** | |
| `neo install` | Interactive app picker |
| `neo install <app>` | Install a specific app |
| `neo deploy [path]` | Deploy local Dockerfile project |
| `neo deploy --domain <d> --port <p>` | Deploy with domain and port |
| `neo list` | List all apps on current server |
| `neo start <app>` | Start a stopped app |
| `neo stop <app>` | Stop a running app |
| `neo restart <app>` | Restart an app |
| `neo update <app>` | Pull latest image and redeploy |
| `neo remove <app>` | Remove app (keeps data volumes) |
| **Logs & Domain** | |
| `neo logs <app>` | Show last 100 log lines |
| `neo logs <app> -f` | Stream logs in real-time |
| `neo logs <app> --tail 50` | Custom tail count |
| `neo domain <app> <domain>` | Set/change domain (auto-SSL via Caddy) |
| **Volumes & Backups** | |
| `neo volumes` | List all volumes on server |
| `neo volumes mount <vol> <path>` | Mount volume to host path (e.g., external SSD) |
| `neo backup <app>` | Backup app data volumes |
| `neo restore <app> <file>` | Restore from backup |
| **Servers** | |
| `neo servers` | List all configured servers |
| `neo use <name>` | Switch active server |
| `neo ssh` | SSH into current server |
| **Vxero Transfer** | |
| `neo connect` | One-time transfer to Vxero (Docker → K3s) |

### Global Flags

| Flag | Description |
|------|-------------|
| `--server <name>` | Target a specific server for this command |
| `--help` | Show help for any command |

---

## Bundled App Templates

10 production-ready templates with auto-configured databases, SSL, and health checks:

| App | Category | Bundled Services |
|-----|----------|-----------------|
| **Plausible** | Analytics | PostgreSQL, ClickHouse |
| **Umami** | Analytics | PostgreSQL |
| **Ghost** | Blogging | MySQL |
| **WordPress** | CMS | MySQL |
| **Gitea** | Dev Tools | PostgreSQL |
| **n8n** | Automation | PostgreSQL |
| **Uptime Kuma** | Monitoring | — (SQLite built-in) |
| **Miniflux** | Reading | PostgreSQL |
| **Vaultwarden** | Security | — (SQLite built-in) |
| **Chatwoot** | Support | PostgreSQL, Redis |

Each template auto-generates secrets, wires database URLs, and configures health checks.

---

## How It Works

Neo is a **local CLI** — no daemon, no agent. Every operation runs over SSH:

```
neo install ghost
  ↓ (local — Charm TUI)
  Interactive prompts: domain, env vars, confirm
  ↓ (SSH to remote server)
  docker pull ghost:5-alpine
  docker run -d --name app-ghost --network neo ...
  curl -X POST localhost:2019/config/... (Caddy route)
  ↓ (local — Charm TUI)
  ✓ Ghost is live at https://blog.mysite.com
```

### Architecture

- **SSH Executor**: Persistent connection, key-based auth (ssh-agent, ed25519, rsa)
- **Docker**: All container management via `docker` CLI over SSH
- **Caddy**: Reverse proxy with optional auto-SSL via Admin API (port 2019, localhost-only on server)
- **Config**: `~/.neo/config.json` — multi-server local config
- **State**: `/etc/neo/state.json` — per-server remote state

### HTTP vs HTTPS

Neo deploys apps as **HTTP-only by default**. This lets you verify the app is running before dealing with DNS and SSL certificates.

Once your DNS A record points to the server, enable HTTPS from the dashboard:

```
neo          # open dashboard
→ select app → Enable HTTPS
```

Caddy then automatically provisions a Let's Encrypt certificate.

> **Laravel / frameworks behind a reverse proxy**: when HTTPS is enabled, your app receives requests over HTTP from Caddy internally. Add `$middleware->trustProxies(at: '*')` in `bootstrap/app.php` so Laravel reads the `X-Forwarded-Proto: https` header and generates correct asset URLs.
>
> ```php
> // bootstrap/app.php
> ->withMiddleware(function (Middleware $middleware): void {
>     $middleware->trustProxies(at: '*');
> })
> ```

### Multi-Server

```bash
$ neo init root@159.65.100.42                     # default → "production"
$ neo init root@staging.mysite.com --name staging
$ neo use staging
  ✓ Switched to server "staging"

$ neo install ghost --server production           # or target per-command
```

---

## Server Requirements

Neo supports these Linux distributions on your remote server:

| OS | Minimum Version |
|---|---|
| **Ubuntu** | 24.04+ |
| **Debian** | any |
| **Fedora** | 39+ |
| **CentOS / RHEL** | 9+ |
| **AlmaLinux** | 9+ |
| **Rocky Linux** | 9+ |

Package manager is auto-detected: `apt` for Debian/Ubuntu, `dnf` for RPM-based distros.

---

## Building

```bash
make build       # Dockerized build → bin/neo
make build-all   # Dockerized cross-compile → dist/
make image-build # Build runtime container image → vxero/neo:latest
make test        # go test ./...
make fmt         # gofmt -w .
```

## Testing

### Docker Sandbox (Local Integration Tests)

Test neo against 13 Linux distros without a real VPS. Each distro runs in a Docker container with SSH + Docker-in-Docker:

```bash
make sandbox                           # test all distros
make sandbox-supported                 # supported distros only (full test suite)
make sandbox-unsupported               # unsupported distros only (rejection test)
make sandbox-distro DISTRO=debian-12   # single distro
make sandbox-list                      # show distro matrix
make sandbox-keep                      # keep containers alive after tests
make sandbox-down                      # tear down everything
```

Supported distros run a full 9-phase integration test: server init, template install (Uptime Kuma), app lifecycle (start/stop/restart/logs), env vars, domain routing, volumes, update/remove, and a Docker build+deploy cycle.

Unsupported distros verify that `neo init` correctly rejects them.

### Real VPS Tests

For production-like testing with real networking and SSL:

```bash
make build-neotest
./bin/neotest --token $DIGITALOCEAN_TOKEN
```

## Project Structure

```
neo/
├── cmd/neo/main.go              # Entry point
├── commands/                    # 15 command files → 21 commands
│   ├── root.go                  # Root command + shared helpers
│   ├── dashboard.go             # Interactive TUI (main menu, servers, apps)
│   ├── init.go                  # Server bootstrap
│   ├── install.go               # App wizard
│   ├── deploy.go                # Deploy local projects
│   ├── list.go                  # App table
│   ├── servers.go               # Multi-server + use
│   ├── manage.go                # start/stop/restart/remove/update
│   ├── logs.go                  # Container log streaming
│   ├── domain.go                # Caddy route management
│   ├── volumes.go               # Volume list + mount
│   ├── backup.go                # Backup + restore
│   ├── connect.go               # Vxero SaaS bridge
│   ├── ssh_cmd.go               # Quick SSH
│   ├── exec_unix.go             # syscall.Exec (unix)
│   └── exec_windows.go          # os/exec fallback (windows)
├── internal/
│   ├── ssh/executor.go          # Persistent SSH client
│   ├── remote/
│   │   ├── docker.go            # Docker commands over SSH
│   │   └── caddy.go             # Caddy Admin API over SSH
│   ├── config/config.go         # ~/.neo/config.json
│   ├── state/state.go           # /etc/neo/state.json (remote)
│   ├── app/
│   │   ├── manifest.go          # YAML manifest parser
│   │   ├── registry.go          # Embedded template registry
│   │   ├── generate.go          # Secret value generator
│   │   └── templates/*.yml      # 10 app manifests
│   ├── ui/
│   │   ├── banner.go            # ASCII logo
│   │   ├── cards.go             # Box cards
│   │   ├── spinner.go           # Braille spinner
│   │   ├── progress.go          # Progress bar + status bullets
│   │   └── styles.go            # Lipgloss styles
│   └── bridge/
│       ├── connect.go           # Agent install/remove
│       ├── api.go               # Vxero REST API client
│       └── migrate.go           # Docker → K3s migration planner
├── Makefile
├── go.mod / go.sum
└── .gitignore
```

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `spf13/cobra` | v1.8.1 | CLI framework |
| `charmbracelet/huh` | v1.0.0 | Interactive prompts |
| `charmbracelet/lipgloss` | v1.1.0 | Terminal styling |
| `golang.org/x/crypto/ssh` | v0.31.0 | SSH client |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing |

## License

Vxero Neo is **source-available** software, licensed under the
[Elastic License 2.0 (ELv2)](LICENSE). The source is public — read it, audit it,
self-host it, modify it for your own use, and contribute back.

Neo is **free**, but activation is **required** — every user activates a license
key before use (`neo activate`). There is no paid tier today; all features are
unlocked for any valid license.

Under ELv2 you **may not**:

- provide Neo to third parties as a hosted or managed service;
- move, change, disable, or circumvent the license-key functionality;
- remove or obscure the licensing, copyright, or other notices.

> **Note:** "source-available" is not the same as "open source" (OSI). The code
> is public, but usage is restricted by the terms above.

Copyright © 2026 Solution Forest Limited. All rights reserved.
