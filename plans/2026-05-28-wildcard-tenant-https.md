# Wildcard Tenant HTTPS Implementation Plan

> **For Copilot:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable true HTTPS for arbitrary GatherHub tenant subdomains under `*.gatherpro.events` without adding each tenant domain to Neo manually.

**Architecture:** Keep GatherHub tenant resolution as-is: tenant rows own the subdomain, and Laravel routes `{subdomain}.gatherpro.events` through `ResolveTenant`. Change the Neo/Caddy layer so Caddy can both route `*.gatherpro.events` and obtain a wildcard certificate through ACME DNS-01 using the DNS provider for `gatherpro.events`. Preserve the current exact-domain setup as the rollback path.

**Tech Stack:** Neo Go CLI, Docker, Caddy Admin API JSON config, Caddy DNS provider plugin, ACME DNS-01, Laravel tenant routing.

---

## Current State

- `https://gatherpro.events` works.
- `https://demo.gatherpro.events/portal/login` works after adding `demo.gatherpro.events` as an exact Neo domain.
- `*.gatherpro.events` DNS points at `167.172.71.161`.
- Stock Neo starts `neo-caddy` from `caddy:2-alpine` in `internal/remote/caddy.go`.
- Caddy can route wildcard hosts, but stock `caddy:2-alpine` cannot solve wildcard certificates because Let’s Encrypt requires DNS-01 for wildcard names.
- Caddy log proof: `*.gatherpro.events: no solvers available ... offered=[dns-01]`.

## Decision Needed

Pick the DNS provider that hosts `gatherpro.events` and create a narrowly scoped API token:

- Cloudflare: `github.com/caddy-dns/cloudflare`, token with DNS edit access for `gatherpro.events` only.
- DigitalOcean: `github.com/caddy-dns/digitalocean`, token with DNS write access.
- Route53, Porkbun, Namecheap, etc.: use the matching `caddy-dns` module.

Do not commit the token. Store it root-only on the server.

---

## Task 1: Capture DNS Provider Details

**Files:**
- No code changes.

**Step 1: Confirm authoritative DNS provider**

Run locally:

```bash
dig NS gatherpro.events +short
```

Expected: nameservers identify the DNS provider.

**Step 2: Create provider token**

Create the minimal token in the DNS provider dashboard.

Expected: token can create/delete TXT records for `_acme-challenge.gatherpro.events`.

**Step 3: Store token on server**

Example for Cloudflare:

```bash
ssh -i "$HOME/.ssh/id_rsa" root@167.172.71.161 'install -d -m 700 /etc/neo/secrets && cat > /etc/neo/secrets/caddy-dns.env <<EOF
CLOUDFLARE_API_TOKEN=replace-me
EOF
chmod 600 /etc/neo/secrets/caddy-dns.env'
```

Expected: `/etc/neo/secrets/caddy-dns.env` exists and is readable only by root.

---

## Task 2: Build A DNS-Enabled Caddy Image

**Files:**
- Create: `neo-caddy/Dockerfile` or equivalent build artifact if the image should live in this repo.
- Modify: optional later, `internal/remote/caddy.go` if Neo should manage this image directly.

**Step 1: Create a custom Caddy Dockerfile**

Cloudflare example:

```dockerfile
FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
```

DigitalOcean example swaps the plugin:

```dockerfile
FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/digitalocean

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
```

**Step 2: Build and publish or build on the server**

Server-local example:

```bash
ssh -i "$HOME/.ssh/id_rsa" root@167.172.71.161 'mkdir -p /etc/neo/caddy-dns'
scp -i "$HOME/.ssh/id_rsa" neo-caddy/Dockerfile root@167.172.71.161:/etc/neo/caddy-dns/Dockerfile
ssh -i "$HOME/.ssh/id_rsa" root@167.172.71.161 'docker build -t neo-caddy-dns:latest /etc/neo/caddy-dns'
```

Expected: `docker image inspect neo-caddy-dns:latest` succeeds.

---

## Task 3: Recreate Caddy With DNS Credentials

**Files:**
- Modify later for productized Neo support: `internal/remote/caddy.go`.
- No GatherHub changes.

**Step 1: Preserve current Caddy volumes**

Neo already uses:

```text
neo-caddy-data:/data
neo-caddy-config:/config
/etc/neo/caddy/Caddyfile:/etc/caddy/Caddyfile
```

**Step 2: Recreate the container with the custom image**

```bash
ssh -i "$HOME/.ssh/id_rsa" root@167.172.71.161 '
set -e
if docker ps --format "{{.Names}}" | grep -qx neo-caddy; then docker stop neo-caddy; fi
if docker ps -a --format "{{.Names}}" | grep -qx neo-caddy; then docker rm neo-caddy; fi
docker run -d \
  --name neo-caddy \
  --network neo \
  --restart unless-stopped \
  -p 80:80 \
  -p 443:443 \
  -p 127.0.0.1:2019:2019 \
  --env-file /etc/neo/secrets/caddy-dns.env \
  -v neo-caddy-data:/data \
  -v neo-caddy-config:/config \
  -v /etc/neo/caddy/Caddyfile:/etc/caddy/Caddyfile \
  neo-caddy-dns:latest \
  caddy run --config /etc/caddy/Caddyfile --resume
'
```

Expected: `docker ps` shows `neo-caddy` running and existing exact domains still work.

---

## Task 4: Add DNS-01 TLS Automation Policy

**Files:**
- Productized Neo version: add a method in `internal/remote/caddy.go` to patch `apps.tls.automation.policies`.
- One-off server setup: use Caddy Admin API directly.

**Step 1: Apply provider-specific TLS automation JSON**

Cloudflare shape:

```json
{
  "policies": [
    {
      "subjects": ["gatherpro.events", "*.gatherpro.events"],
      "issuers": [
        {
          "module": "acme",
          "challenges": {
            "dns": {
              "provider": {
                "name": "cloudflare",
                "api_token": "{env.CLOUDFLARE_API_TOKEN}"
              }
            }
          }
        }
      ]
    }
  ]
}
```

DigitalOcean shape uses the provider name and env var for that plugin:

```json
{
  "provider": {
    "name": "digitalocean",
    "api_token": "{env.DIGITALOCEAN_API_TOKEN}"
  }
}
```

**Step 2: Patch Caddy**

Use the exact JSON for the selected provider:

```bash
ssh -i "$HOME/.ssh/id_rsa" root@167.172.71.161 'curl -sf -X PUT http://localhost:2019/config/apps/tls/automation -H "Content-Type: application/json" -d @/etc/neo/caddy/automation.json'
```

Expected: command succeeds and `docker logs neo-caddy` shows no config errors.

---

## Task 5: Re-Add Wildcard Route

**Files:**
- Modify GatherHub deployment config only after DNS-01 works: `.neo.yml`.

**Step 1: Add wildcard to Caddy/Neo**

```bash
neo --server gatherpro domain gatherhub "*.gatherpro.events" --add
```

Expected: Caddy logs show `certificate obtained successfully` for `*.gatherpro.events`.

**Step 2: Add wildcard back to `.neo.yml` only if deploy tooling will preserve it**

Preferred future config:

```yaml
name: gatherhub
server: gatherpro
domains:
  - gatherpro.events
  - "*.gatherpro.events"
```

Expected: future deploys do not remove the wildcard route.

---

## Task 6: Verify Wildcard Behavior

**Files:**
- No code changes.

**Step 1: Check TLS handshake on arbitrary tenant host**

```bash
curl -I --max-time 45 https://randomcheck.gatherpro.events/up
```

Expected: TLS handshake succeeds. `/up` should return `200 OK` because it is Laravel’s global health route.

**Step 2: Check known tenant portal**

```bash
curl -I --max-time 45 https://demo.gatherpro.events/portal/login
```

Expected: `200 OK` with cookies scoped to `.gatherpro.events`.

**Step 3: Check unknown tenant app route**

```bash
curl -I --max-time 45 https://randomcheck.gatherpro.events/portal/login
```

Expected: TLS succeeds, then the app returns tenant-not-found behavior, likely `404`.

**Step 4: Browser check**

Open:

```text
https://demo.gatherpro.events/portal/login
```

Expected: Filament login renders without mixed-content warnings.

---

## Task 7: Productize In Neo Later

**Files:**
- Modify: `internal/remote/caddy.go`
- Modify: `commands/init.go` or add a new `neo caddy dns`/`neo wildcard` command.
- Test: add focused tests around Caddy config JSON generation.

**Step 1: Make Caddy image configurable**

Replace the hardcoded image path with server config or environment override, for example:

```go
const DefaultCaddyImage = "caddy:2-alpine"
```

Add a server-level field or env override:

```go
image := os.Getenv("NEO_CADDY_IMAGE")
if image == "" {
    image = DefaultCaddyImage
}
```

**Step 2: Add DNS provider config command**

Possible UX:

```bash
neo caddy dns enable --server gatherpro --domain gatherpro.events --provider cloudflare --env-file /etc/neo/secrets/caddy-dns.env
```

**Step 3: Generate provider-specific Caddy JSON**

Add a helper that returns the `apps.tls.automation` JSON for known providers.

**Step 4: Test without host Go toolchain**

Run from the Neo repo:

```bash
make build
make test
```

Expected: Dockerized build and tests pass.

---

## Rollback Plan

If wildcard TLS breaks production:

```bash
neo --server gatherpro domain gatherhub "*.gatherpro.events" --remove
neo --server gatherpro domain gatherhub demo.gatherpro.events --add
```

Then recreate `neo-caddy` with stock `caddy:2-alpine` using the same Neo volumes, or rerun the normal Neo server setup flow if available.

Expected rollback state:

- `https://gatherpro.events` works.
- `https://demo.gatherpro.events/portal/login` works.
- Arbitrary tenant subdomains require exact `neo domain --add` until wildcard DNS-01 is restored.

## Security Notes

- DNS API token must never be committed.
- Scope the token to one zone and DNS edit only.
- Store token at `/etc/neo/secrets/caddy-dns.env` with `0600` permissions.
- Rotate token after testing if it was pasted into shell history or shared logs.
