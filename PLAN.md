# vxero-neo — Remote Server Management CLI

## Overview

`vxero-neo` (command: `neo`) is a **local CLI** that manages **remote servers** over SSH.
Like Kamal (37signals) — you run it on your Mac/laptop, it SSHs into your VPS
and manages Docker containers, Caddy reverse proxy, volumes, and backups.

**BYOD (Bring Your Own Device)**: User already has a VPS. We only handle the Docker layer.

```
Your Mac / Laptop (runs neo CLI)
    │
    │  SSH (key-based auth)
    │
    ▼
Remote VPS (user's server — any provider)
    └── Docker
          ├── neo-caddy       (reverse proxy + auto SSL via Let's Encrypt)
          ├── app-plausible   (user's app)
          ├── app-ghost       (user's app)
          ├── svc-postgres    (bundled database service)
          └── ... all on "neo" Docker network
```

## How It Works

The CLI is a beautiful remote control. All prompts and UI render locally (Charm stack).
All Docker/Caddy commands execute on the remote server via SSH.

```
neo install ghost
  ↓ (local — Charm UI)
  Interactive prompts: domain, env vars, confirm
  ↓ (SSH to remote)
  ssh root@159.65.100.42 docker pull ghost:5-alpine
  ssh root@159.65.100.42 docker run -d --name app-ghost --network neo ...
  ssh root@159.65.100.42 curl -s -X POST localhost:2019/config/... (Caddy route)
  ↓ (local — Charm UI)
  ✓ Ghost is live at https://blog.mysite.com
```

No agent installed. No daemon running. Pure SSH — like Kamal, Ansible, or Capistrano.

---

## Architecture

### SSH Execution Layer

Every remote operation goes through a single `ssh.Executor` that:
- Maintains a persistent SSH connection (reuses across commands)
- Supports key-based auth (reads `~/.ssh/id_rsa`, `id_ed25519`, ssh-agent)
- Supports password auth as fallback (for initial setup)
- Multiplexes over a single connection (SSH ControlMaster-style)
- Streams stdout/stderr back for progress display

```go
type Executor struct {
    Host   string          // root@159.65.100.42
    Client *ssh.Client     // persistent connection
}

func (e *Executor) Run(cmd string) (string, error)          // run + capture output
func (e *Executor) Stream(cmd string, stdout io.Writer) error // stream output live
func (e *Executor) Upload(local, remote string) error        // scp file to server
```

### Docker on the Remote Server

The CLI manages Docker entirely over SSH:

```bash
# All these run on the remote server via SSH
ssh root@server docker pull ghost:5-alpine
ssh root@server docker run -d --name app-ghost ...
ssh root@server docker stop app-ghost
ssh root@server docker logs -f app-ghost
ssh root@server docker volume ls
```

No Docker SDK needed locally. The CLI just shells out over SSH.

### Caddy (Dockerized Reverse Proxy on Remote Server)

- Image: `caddy:2-alpine`
- Container name: `neo-caddy`
- Ports: `80:80`, `443:443` on the remote server
- Admin API: `127.0.0.1:2019` (localhost only on the server)
- Volumes:
  - `neo-caddy-data:/data` (SSL certs via Let's Encrypt, persisted)
  - `neo-caddy-config:/config`
- Config: Managed via Caddy Admin API — the CLI sends `curl` commands over SSH

### SSL (Automatic via Caddy + Let's Encrypt)

Since apps run on a remote VPS with a public IP:
- User points their domain DNS (A record) to the server IP
- Caddy auto-issues Let's Encrypt certificates
- Zero SSL config needed — Caddy handles ACME challenge automatically
- Certificates auto-renew

```bash
neo domain ghost blog.mysite.com
# → SSH: curl -X POST localhost:2019/config/... (adds Caddy route)
# → Caddy auto-issues SSL for blog.mysite.com
# → ✓ https://blog.mysite.com is live with HTTPS
```

### Docker Network

- Shared bridge network on remote server: `neo`
- Caddy + all apps + all services join this network
- Caddy routes by domain → container name on internal ports
- Containers reference each other by name (e.g., `svc-plausible-postgres`)

---

## Multi-Server Support

The CLI can manage multiple remote servers:

```bash
# Add servers
neo init root@159.65.100.42                    # default server
neo init root@staging.mysite.com --name staging

# Switch active server
neo use staging

# Or target a specific server per-command
neo list --server staging
neo install ghost --server production
```

### Local Config File

`~/.neo/config.json`:

```json
{
  "current": "production",
  "servers": {
    "production": {
      "name": "production",
      "host": "root@159.65.100.42",
      "port": 22,
      "key": "~/.ssh/id_ed25519",
      "initialized_at": "2026-03-17T14:30:00Z"
    },
    "staging": {
      "name": "staging",
      "host": "root@staging.mysite.com",
      "port": 22,
      "key": "~/.ssh/id_ed25519",
      "initialized_at": "2026-03-18T09:00:00Z"
    }
  }
}
```

### Remote State File

Each server has its own state at `/etc/neo/state.json` (on the remote server):

```json
{
  "initialized": true,
  "server_ip": "159.65.100.42",
  "apps": {
    "plausible": {
      "name": "plausible",
      "image": "plausible/analytics:v2.1.0",
      "domain": "analytics.mysite.com",
      "status": "running",
      "container_id": "abc123",
      "internal_port": 8000,
      "volumes": {
        "plausible-data": {
          "container_path": "/var/lib/plausible/db",
          "mount": null
        },
        "plausible-db": {
          "container_path": "/var/lib/postgresql/data",
          "mount": "/mnt/ssd/plausible-db"
        }
      },
      "env": {
        "BASE_URL": "https://analytics.mysite.com",
        "SECRET_KEY_BASE": "generated..."
      },
      "services": {
        "postgres": {
          "image": "postgres:16-alpine",
          "container_id": "def456"
        }
      },
      "installed_at": "2026-03-17T14:30:00Z"
    }
  },
  "connected": false,
  "vxero_url": "",
  "vxero_token": ""
}
```

---

## App Manifest Format

Apps are defined as YAML manifests (embedded in binary + fetchable from registry):

```yaml
name: plausible
title: "Plausible Analytics"
description: "Privacy-friendly Google Analytics alternative"
category: analytics
version: "2.1.0"
image: plausible/analytics:v2.1.0
port: 8000

volumes:
  - name: plausible-events
    path: /var/lib/plausible/db
    label: "Analytics event data"

env:
  - key: BASE_URL
    label: "Public URL"
    from: domain            # auto-filled from domain answer
  - key: SECRET_KEY_BASE
    label: "Secret key"
    generate: hex:64        # auto-generate
  - key: DATABASE_URL
    from_service: postgres  # auto-wired
    template: "postgres://postgres:${POSTGRES_PASSWORD}@svc-plausible-postgres:5432/plausible"

services:
  - name: postgres
    image: postgres:16-alpine
    port: 5432
    volumes:
      - name: plausible-db
        path: /var/lib/postgresql/data
        label: "PostgreSQL data"
    env:
      - key: POSTGRES_DB
        value: plausible
      - key: POSTGRES_PASSWORD
        generate: hex:32

health:
  path: /api/health
  interval: 10s
  timeout: 5s
  retries: 3
```

### Bundled App Templates (Embedded in Binary)

Phase 1 (launch):
- Plausible Analytics
- Umami (analytics)
- Ghost (blogging)
- Gitea (git hosting)
- n8n (workflow automation)
- Uptime Kuma (monitoring)
- Miniflux (RSS reader)
- Vaultwarden (password manager)
- Chatwoot (customer support)
- WordPress

Phase 2:
- Nextcloud, Outline, Mattermost, Grafana, etc.

---

## CLI Commands & UX

### `neo` (no args — dashboard)

Shows the current server and its apps:

```
  ▀▄▀ █ █ █▀▀ █▀█ █▀█
  █ █ ▀▄▀ ██▄ █▀▄ █▄█  neo v1.0.0

  Server: production (159.65.100.42)

  Apps                                              Volumes
  ──────────────────────────────────────────────     ─────────────────────────────
  plausible   analytics.mysite.com     ● Running    plausible-data    2.1 GB
  gitea       git.mysite.com           ● Running    plausible-db      850 MB  → /mnt/ssd
  ghost       blog.mysite.com          ○ Stopped    gitea-data        1.3 GB
                                                    ghost-content     4.2 GB
  3 apps · 2 running · 1 stopped                    Total: 8.4 GB
```

### `neo init <user@host>`

Connects to the remote server via SSH and bootstraps it:

1. Verify SSH connection works
2. Detect OS on remote server (Ubuntu, Debian, etc.)
3. Check if Docker is installed — if not, install it (`curl -fsSL https://get.docker.com | sh`)
4. Create `neo` Docker network
5. Start `neo-caddy` container (ports 80, 443, 2019-localhost-only)
6. Create `/etc/neo/` directory + state.json on remote
7. Save server config locally in `~/.neo/config.json`
8. Show success + server IP + next steps

```
  Initializing server...

  ? SSH connection
  │ root@159.65.100.42

  ◐ Connecting via SSH...
  ✓ Connected (Ubuntu 24.04 LTS, 4GB RAM, 2 CPU)
  ◐ Installing Docker...
  ✓ Docker 27.1.1 installed
  ✓ Docker network "neo" created
  ◐ Starting Caddy reverse proxy...
  ✓ Caddy running (ports 80, 443)
  ✓ State initialized

  ╭─────────────────────────────────────────────╮
  │  ✓ Server ready!                            │
  │                                             │
  │  Name:   production                         │
  │  Host:   root@159.65.100.42                 │
  │  Docker: 27.1.1                             │
  │  Caddy:  2.9.1 (auto-SSL)                  │
  │                                             │
  │  Install your first app:                    │
  │    neo install                              │
  ╰─────────────────────────────────────────────╯
```

### `neo install [app]`

If `[app]` omitted → interactive app picker (Huh select with categories)
If `[app]` provided → jump to guided setup

Setup flow (Huh form — all prompts render locally):
1. Ask domain (text input)
2. Ask any app-specific env vars (skip auto-generated ones)
3. Ask about bundled services ("Include PostgreSQL?" confirm)
4. Show summary, confirm
5. Pull images on remote (SSH, with progress streamed back)
6. Create volumes on remote
7. Start service containers first, wait for health
8. Start app container, wait for health
9. Add Caddy route via admin API (SSH + curl)
10. Show success card with URL

```
  Installing Plausible Analytics v2.1.0
  Server: production (159.65.100.42)

  ┌ Configuration ─────────────────────────────────────┐
  │                                                     │
  │  ? Domain for Plausible                             │
  │  │ analytics.mysite.com                             │
  │  │                                                  │
  │  ? Admin email                                      │
  │  │ me@mysite.com                                    │
  │  │                                                  │
  │  ? Include bundled PostgreSQL?                       │
  │  │ ● Yes, bundle it (recommended)                   │
  │  │ ○ No, I'll use my own                            │
  │                                                     │
  └─────────────────────────────────────────────────────┘

  ◐ Pulling plausible/analytics:v2.1.0...        [2/3]
  ✓ Image pulled
  ✓ Volumes created
  ✓ Container started
  ✓ SSL certificate issued for analytics.mysite.com
  ✓ Health check passed

  ╭─────────────────────────────────────────────╮
  │  ✓ Plausible is live!                       │
  │                                             │
  │  URL:   https://analytics.mysite.com        │
  │  Admin: me@mysite.com                       │
  │                                             │
  │  Data stored on server:                     │
  │    plausible-data  →  docker volume         │
  │    plausible-db    →  docker volume         │
  │                                             │
  │  Mount to external drive:                   │
  │  neo volumes mount plausible-data           │
  │    /mnt/ssd/plausible                       │
  ╰─────────────────────────────────────────────╯
```

### `neo list`

Table of all apps on the current server with status, domain, ports.

### `neo servers`

List all configured servers:

```
  Servers
  ─────────────────────────────────────────────────
  ● production   root@159.65.100.42         3 apps   (active)
    staging      root@staging.mysite.com    1 app
```

### `neo use <name>`

Switch the active server context.

### `neo stop <app>` / `start <app>` / `restart <app>`

SSH into remote, docker stop/start/restart the app + its services.

### `neo update <app>`

1. SSH: Pull latest image tag on remote
2. SSH: Stop old container (keep volumes)
3. SSH: Start new container with same config
4. SSH: Wait for health check
5. SSH: Remove old container
6. Show success

### `neo remove <app>`

1. Confirm locally ("This will stop the app. Data volumes are kept on the server.")
2. SSH: Stop + remove containers (app + services)
3. SSH: Remove Caddy route
4. Keep volumes on server (user must explicitly delete)
5. SSH: Update remote state.json

### `neo logs <app>`

SSH: `docker logs -f app-<name>` — streams back to local terminal. `--tail 100` default.

### `neo domain <app> <domain>`

1. SSH: Update Caddy route to new domain (via admin API)
2. Caddy auto-provisions Let's Encrypt SSL on the server
3. SSH: Update remote state.json

### `neo volumes`

SSH: List all Docker volumes on server with size + which app owns them.
Shows mount status (docker volume vs bind mount to external path).

### `neo volumes mount <volume> <path>`

Mount a Docker volume to a host path on the remote server (e.g., external SSD):

1. SSH: Stop app
2. SSH: Copy data from Docker volume to host path
   `docker run --rm -v plausible-data:/src -v /mnt/ssd/plausible:/dst alpine cp -a /src/. /dst/`
3. SSH: Recreate container with bind mount instead of named volume
4. SSH: Start app
5. SSH: Update remote state.json

### `neo backup <app>`

1. SSH: Pause app container
2. SSH: Tar + gzip all app volumes into `/var/backups/vxero/`
3. SSH: Resume app container
4. SCP: Download backup to local `~/.neo/backups/` (optional, prompted)
5. Show backup path + size

### `neo restore <app> <file>`

1. SCP: Upload backup to server (if local file)
2. SSH: Stop app
3. SSH: Extract backup to volumes
4. SSH: Start app

### `neo connect`

Bridge to Vxero SaaS — installs the Go agent on the remote server:

1. Ask Vxero URL + API token (Huh form, locally)
2. Validate token (HTTPS request from local machine)
3. SSH: Download + install Go agent on remote server
4. SSH: Register server with Vxero (agent → control plane)
5. SSH: Start agent (heartbeat begins)
6. SSH: Update remote state.json with connection info
7. Show success with Vxero dashboard link

```
  ╭─────────────────────────────────────────────╮
  │  ✓ Connected to Vxero!                      │
  │                                             │
  │  Your server and 3 apps are now visible     │
  │  in your Vxero dashboard.                   │
  │                                             │
  │  → https://app.vxero.dev/servers/a1b2c3     │
  │                                             │
  │  You can still use neo locally.              │
  │  Changes sync automatically via agent.      │
  ╰─────────────────────────────────────────────╯
```

### `neo disconnect`

1. SSH: Stop + remove agent from remote server
2. SSH: Clear connection from remote state.json
3. Apps continue running on the server — just no longer synced to Vxero SaaS

### `neo ssh`

Quick shortcut to SSH into the current server:

```bash
neo ssh                    # → ssh root@159.65.100.42
neo ssh --server staging   # → ssh root@staging.mysite.com
```

### `neo update-self`

Self-update the binary from GitHub releases (runs locally).

---

## Project Structure

```
neo/
├── cmd/neo/
│   └── main.go                    # Entry point
├── commands/
│   ├── root.go                    # Root command (dashboard view)
│   ├── init.go                    # SSH + Docker + Caddy bootstrap
│   ├── install.go                 # App installation wizard
│   ├── list.go                    # List apps on server
│   ├── servers.go                 # List servers + use <name>
│   ├── manage.go                  # start/stop/restart/remove
│   ├── update.go                  # Update app image
│   ├── logs.go                    # Stream container logs
│   ├── domain.go                  # Domain management
│   ├── volumes.go                 # Volume list/mount
│   ├── backup.go                  # Backup/restore
│   ├── connect.go                 # Bridge to Vxero SaaS
│   ├── disconnect.go              # Remove SaaS connection
│   └── ssh_cmd.go                 # Quick SSH shortcut
├── internal/
│   ├── ssh/
│   │   └── executor.go            # SSH client — Run(), Stream(), Upload()
│   ├── remote/
│   │   ├── docker.go              # Docker commands over SSH
│   │   └── caddy.go               # Caddy admin API over SSH (curl)
│   ├── config/
│   │   └── config.go              # ~/.neo/config.json (local)
│   ├── state/
│   │   └── state.go               # /etc/neo/state.json (remote, read/write over SSH)
│   ├── app/
│   │   ├── manifest.go            # App manifest YAML parser
│   │   └── registry.go            # Embedded + remote app registry
│   ├── ui/
│   │   ├── banner.go              # ASCII logo + version
│   │   ├── cards.go               # Success/info/error card boxes
│   │   ├── spinner.go             # Braille spinner (manual, no huh/spinner)
│   │   ├── progress.go            # Progress bar for pulls/transfers
│   │   └── styles.go              # Shared lipgloss styles
│   └── bridge/
│       └── connect.go             # Agent download + registration (over SSH)
├── templates/                     # Embedded YAML app manifests
│   ├── plausible.yml
│   ├── ghost.yml
│   ├── gitea.yml
│   ├── umami.yml
│   ├── n8n.yml
│   ├── uptime-kuma.yml
│   ├── miniflux.yml
│   ├── vaultwarden.yml
│   ├── chatwoot.yml
│   └── wordpress.yml
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Dependencies

```
github.com/spf13/cobra            v1.8.1   # CLI framework
github.com/charmbracelet/huh      v0.6.0   # Interactive prompts (like Laravel Prompts)
github.com/charmbracelet/lipgloss v1.1.0   # Terminal styling
golang.org/x/crypto/ssh                    # SSH client (Go stdlib extension)
gopkg.in/yaml.v3                  v3.0.1   # YAML parsing
```

Note: No Docker SDK needed locally — all Docker commands run over SSH.
No huh/spinner — use manual braille spinner (same as existing CLI).

## Caddy Admin API (Over SSH)

The CLI manages Caddy routes by sending `curl` commands to the remote server's
Caddy Admin API (port 2019, localhost-only on the server):

### Add route (on app install):
```bash
ssh root@server curl -s -X POST http://localhost:2019/config/apps/http/servers/srv0/routes \
  -H "Content-Type: application/json" \
  -d '{
    "@id": "app-plausible",
    "match": [{"host": ["analytics.mysite.com"]}],
    "handle": [{
      "handler": "reverse_proxy",
      "upstreams": [{"dial": "app-plausible:8000"}]
    }]
  }'
```

### Remove route (on app remove):
```bash
ssh root@server curl -s -X DELETE http://localhost:2019/id/app-plausible
```

### Caddy container setup (during init):
```bash
ssh root@server docker run -d \
  --name neo-caddy \
  --network neo \
  --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -p 127.0.0.1:2019:2019 \
  -v neo-caddy-data:/data \
  -v neo-caddy-config:/config \
  caddy:2-alpine \
  caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
```

Initial Caddyfile (minimal, enables admin API + auto HTTPS):
```
{
  admin 0.0.0.0:2019
}
```

Routes are added/removed dynamically via admin API — no Caddyfile editing needed.

## Volume Mounting (External Drives)

Users can mount Docker volumes to external drives on the remote server:

**Default (Docker named volume on the server):**
```bash
# Container uses a Docker-managed volume
docker run -v plausible-data:/var/lib/plausible/db ...
```

**After `volumes mount` (bind mount to external drive on the server):**
```bash
# Container uses a path on the server's filesystem
docker run -v /mnt/ssd/plausible:/var/lib/plausible/db ...
```

The `volumes mount` command (all over SSH):
1. Stops the app container
2. Copies data: `docker run --rm -v plausible-data:/src -v /mnt/ssd/plausible:/dst alpine cp -a /src/. /dst/`
3. Updates remote state.json to record the bind mount path
4. Recreates container with `-v /mnt/ssd/plausible:/var/lib/plausible/db`
5. Optionally removes the old Docker named volume

Use case: user attaches a large SSD/NAS to their VPS and moves data there.

## Build & Distribution

### The CLI binary runs locally (Mac/Win/Linux):
```bash
# macOS
brew install vxero/tap/neo

# Linux / macOS (curl)
curl -fsSL https://get.vxero.dev/neo | sh

# Windows (PowerShell)
irm https://get.vxero.dev/neo.ps1 | iex

# Windows (Scoop)
scoop install vxero-neo
```

### Makefile targets:
```makefile
build:          # Build for current platform → bin/neo
build-all:      # Cross-compile all 5 targets → dist/
                #   neo-darwin-arm64, neo-darwin-amd64
                #   neo-linux-amd64, neo-linux-arm64
                #   neo-windows-amd64.exe
clean:          # Remove bin/ dist/
test:           # go test ./...
fmt:            # gofmt -w .
```

### Docker build (from project root):
```makefile
build-neo:
	docker build -f docker/neo/Dockerfile -t neo-build .
	docker run --rm -v ./neo/bin:/out neo-build
```

## Implementation Order

1. **Scaffold** — go.mod, main.go, root command with banner
2. **UI package** — lipgloss styles, banner, cards, spinner
3. **SSH executor** — persistent SSH client with Run(), Stream(), Upload()
4. **Config package** — local ~/.neo/config.json CRUD
5. **State package** — remote /etc/neo/state.json read/write over SSH
6. **Remote Docker** — Docker commands over SSH (pull, run, stop, rm, logs, volume ls)
7. **Remote Caddy** — Caddy admin API calls over SSH (add/remove routes)
8. **init command** — SSH connect, Docker install, network, Caddy container, state init
9. **App manifest** — YAML parser + embedded templates (start with 3: plausible, ghost, gitea)
10. **install command** — interactive wizard + remote pull + start + route
11. **list command** — dashboard table view (reads remote state)
12. **servers command** — list servers + use <name>
13. **manage commands** — start/stop/restart/remove/update (all over SSH)
14. **logs command** — stream docker logs over SSH
15. **domain command** — Caddy route update over SSH
16. **volumes command** — list + mount (over SSH)
17. **backup/restore** — volume snapshot on remote + optional SCP download
18. **connect/disconnect** — Vxero SaaS bridge (agent install over SSH)
19. **ssh command** — quick SSH shortcut
20. **Remaining templates** — n8n, umami, uptime-kuma, etc.
