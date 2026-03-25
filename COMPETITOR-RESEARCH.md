# Neo Competitor Research & Pricing Analysis

*Research date: 2026-03-21*

---

## Executive Summary

Neo occupies a unique niche: **CLI-first, SSH-native, no-agent Docker deployment** with a beautiful TUI. Its closest competitors are Kamal (free, CLI, SSH) and Sidekick (free, CLI, SSH). The broader market includes web-panel PaaS tools (Coolify, Dokploy, Easypanel) and SaaS server management (Ploi, RunCloud, Laravel Forge).

**Key finding:** The market is saturated with free, open-source alternatives. Neo's differentiation must come from UX quality, the app template catalog, and the Vxero upgrade path — not from gating basic features.

**Pricing recommendation:** Reduce Neo+ to **$49/year** (~$4/mo) and add higher-value features that justify the cost. The current $79/year is above market expectations for a CLI tool competing against free alternatives.

---

## Competitor Landscape

### Direct Competitors (CLI + SSH, same architecture)

| Tool | Price | Model | Servers | Key Differentiator |
|------|-------|-------|---------|-------------------|
| **Kamal** (37signals) | Free forever | CLI, SSH, Docker | Unlimited | Battle-tested (runs HEY), bundled with Rails 8, 10K+ GitHub stars |
| **Sidekick** (MightyMoud) | Free forever | CLI, SSH, Docker | Unlimited | Encrypted env vars, preview apps, Go binary |
| **Neo** (current) | Free / $79/yr | CLI, SSH, Docker | 2 free / unlimited paid | App template catalog, TUI dashboard, Vxero bridge |

**Problem:** Both direct competitors are 100% free with unlimited servers. Neo's 2-server cap on the free tier and $79/year paid tier compete against $0.

### Web Panel PaaS (self-hosted, different UX model)

| Tool | Price | GitHub Stars | Servers | Notes |
|------|-------|-------------|---------|-------|
| **Coolify** | Free (self-hosted) / $5/mo cloud | 50K+ | Unlimited | 280+ templates, full web UI, dominant market leader |
| **Dokploy** | Free (self-hosted) / ~$4.50/mo cloud | 24K+ | Unlimited | Modern UI, Docker Swarm, 67 MCP tools |
| **CapRover** | Free forever | ~12K | Unlimited | Docker Swarm clustering, development slowing |
| **Dokku** | Free / $849 lifetime (Pro) | ~28K | Single only | Heroku buildpacks, git push deploy |
| **Easypanel** | Free / $14-32/mo | ~5K | Multi (beta) | Modern cPanel replacement, 200+ templates |
| **Dockge** | Free forever | ~15K | Multi | Compose file manager only, by Uptime Kuma creator |

### SaaS Server Management (hosted panels)

| Tool | Price | Focus | Notes |
|------|-------|-------|-------|
| **Laravel Forge** | $12-39/mo | PHP/Laravel | Market leader for Laravel, no Docker focus |
| **Ploi** | Free-$30/mo | PHP/Laravel | Laravel-optimized, team-based, white-label (Ploi Core) |
| **RunCloud** | $9-49/mo | PHP/WordPress | Agency-focused, WordPress toolkit |
| **Sliplane** | EUR 9/mo per server | Docker | Hetzner-based, unlimited containers per server |
| **Elestio** | $14/mo per service | 400+ apps | Managed hosting, multi-cloud, all-inclusive |
| **PikaPods** | From $1.20/mo | 60+ apps | Usage-based, revenue-shares with OSS devs |

### Emerging/Notable

| Tool | Price | Model | Notes |
|------|-------|-------|-------|
| **Haloy** | Free | CLI + AI | AI agent integration, auto-detects frameworks |
| **Server Compass** | $29 one-time | Desktop app | No subscription, 166+ templates |
| **Portainer** | Free CE / $995+/yr BE | Web panel | Enterprise container management, overkill for indie |

---

## Current Neo+ Analysis

### What Neo+ offers today ($79/user/year)

| Feature | Market Value | Available Free Elsewhere? |
|---------|-------------|--------------------------|
| Unlimited servers | Core expectation | Yes — Kamal, Sidekick, Coolify, Dokploy all unlimited |
| App terminal (shell into container) | Medium | Yes — `docker exec` over SSH is trivial |
| Real-time app metrics (CPU, RAM, I/O) | Medium | Yes — Coolify, Portainer, `docker stats` |
| Real-time VM metrics | Medium | Yes — Coolify, any monitoring stack |
| One-off exec (`neo run`) | Low-Medium | Yes — `ssh server docker exec app cmd` |
| Local backup download | Medium | Yes — `scp` the backup file |
| Priority support | Low (for CLI tool) | N/A |

### Problems with current positioning

1. **Price vs competition:** $79/year for a CLI tool when Kamal and Sidekick are free with all features. Coolify (full web PaaS) is free self-hosted.

2. **Server cap feels punitive:** Capping at 2 servers on free tier, when every competitor offers unlimited, makes Neo feel restrictive rather than generous.

3. **Features don't justify price:** Most Neo+ features are things users can do manually with SSH + Docker commands. The paid tier needs features that are genuinely hard to replicate.

4. **"Coming soon" erodes trust:** Neo+ is listed as "coming soon" — users can't evaluate it, and it signals the product may not have a sustainable business model yet.

---

## Pricing Recommendations

### Option A: Lower Price, Stronger Features (Recommended)

**Neo Free — $0 forever**
- Everything currently in Neo free
- **Remove the 2-server cap** — unlimited servers
- This matches market expectations and removes the biggest friction point

**Neo+ — $49/year (~$4/mo)**
- All free features, plus:
- **Scheduled backups** — cron-based automated backups with retention policies (not just manual `neo backup`)
- **Backup to S3/R2** — push backups to cloud storage, not just local tar files
- **Health monitoring & alerts** — get notified (email/webhook/Slack) when an app goes down or a health check fails
- **App metrics dashboard** — `neo metrics` TUI showing CPU/RAM/disk per container over time (not just real-time `docker stats`)
- **Rolling updates** — update all apps across all servers in one command with rollback
- **`neo run`** — one-off exec into containers
- **App terminal** — interactive shell
- **Team config sharing** — share `neo` configs across a team (encrypted, git-friendly)
- **Priority support**

**Rationale:** $49/year is the sweet spot — below $5/mo (impulse buy territory), well below Ploi/Forge/RunCloud, and the features (automated backups, alerts, cloud backup targets) are genuinely hard to replicate manually.

### Option B: Usage-Based (Alternative)

**Neo Free — $0 forever, unlimited**
- All current features, no caps

**Neo Pro — $5/server/month**
- Monitoring & alerts per server
- Automated backups per server
- App metrics history
- Priority support

**Rationale:** Per-server pricing scales naturally. Similar to Coolify Cloud ($5/mo base + $3/server) and Dokploy Cloud (~$4.50/server). Only pay for servers you want premium features on.

### Option C: One-Time Purchase (Alternative)

**Neo — Free forever**
**Neo+ — $29-49 one-time lifetime**
- All premium features, forever
- Similar to Server Compass ($29 one-time) and Dokku Pro ($849 lifetime)

**Rationale:** Developers hate subscriptions for CLI tools. A one-time purchase converts better for solo/indie developers. Risk: no recurring revenue.

---

## Feature Suggestions for Neo+

### High-Value (hard to replicate manually)

| Feature | Why It Matters | Competitor Coverage |
|---------|---------------|-------------------|
| **Scheduled automated backups** | `neo backup` is manual — automation is the real value | Coolify has it, Kamal doesn't |
| **Backup to S3/R2/B2** | Offsite backup is critical, tedious to set up manually | Coolify, Elestio have it |
| **Health monitoring + alerts** | Email/Slack/webhook when apps crash or health checks fail | Coolify, Uptime Kuma (separate tool) |
| **Metrics history** | Not just live `docker stats` but 7/30-day charts in TUI | Coolify (Grafana), none in CLI tools |
| **Rolling multi-server updates** | `neo update --all` across production + staging with rollback | Kamal has multi-server, but no catalog updates |
| **Docker Compose deploy** | Deploy a full `docker-compose.yml` stack, not just single containers | Dockge, Coolify have it; Neo partially has it |
| **Cron jobs** | Schedule recurring tasks inside containers | Coolify, Ploi, RunCloud have it |
| **Log search/filtering** | `neo logs ghost --grep ERROR --since 1h` | Not in any CLI competitor |

### Medium-Value (nice-to-have differentiators)

| Feature | Notes |
|---------|-------|
| **App auto-update** | Auto-pull latest image tags on schedule (opt-in per app) |
| **Firewall management** | `neo firewall allow 5432 --from 10.0.0.0/8` via ufw/nftables |
| **DNS check** | `neo dns check` — verify A records point to server before SSL |
| **Server hardening** | `neo harden` — apply security best practices (fail2ban, SSH config) |
| **Export to Compose** | `neo export ghost` → generates a portable `docker-compose.yml` |
| **Import from Compose** | `neo import ./docker-compose.yml` → deploy existing stack |
| **Webhook deploys** | `neo webhook <app>` — get a URL that triggers redeploy (for CI/CD) |

### Low-Value (don't gate these)

| Feature | Why Keep Free |
|---------|--------------|
| Unlimited servers | Every competitor offers this free |
| App terminal | It's `docker exec -it` over SSH |
| Basic metrics | `docker stats` is already free |
| One-off exec | Trivially done via SSH |

---

## Recommended Free Tier Enhancements

To compete with Kamal and Coolify, Neo's free tier should include:

1. **Unlimited servers** (remove 2-server cap)
2. **More bundled templates** — target 20+ apps (Coolify has 280+, but quality > quantity for CLI)
3. **Docker Compose support** — already partially there, make it first-class
4. **`neo env` management** — already done, this is a strong differentiator vs Kamal
5. **Zero-downtime deploys** — already done, keep this free

---

## Competitive Positioning Strategy

### What Neo does better than competitors

1. **No web panel required** — pure CLI, no browser needed (vs Coolify, Dokploy, Easypanel)
2. **No agent on server** — nothing installed permanently (vs Coolify agent, Portainer agent)
3. **Beautiful TUI** — Charm stack gives a polished feel that Kamal's plain output lacks
4. **App template catalog** — one-command installs (Kamal has no template system)
5. **Vxero upgrade path** — `neo connect` is a unique funnel to the full platform
6. **Env management** — `.neo.yml`, `--env-file`, docker-compose auto-detection is best-in-class

### Messaging to differentiate from Kamal

> "Kamal deploys your code. Neo deploys your infrastructure."
>
> Neo doesn't just deploy your app — it installs databases, configures reverse proxies, manages SSL, handles backups, and gives you a catalog of production-ready apps. Kamal requires you to set all of that up yourself.

### Messaging to differentiate from Coolify

> "Coolify needs a server to manage your servers. Neo runs from your laptop."
>
> No control plane to maintain. No web panel to secure. No agent eating resources. Just SSH and your code.

---

## Market Pricing Reference (as of March 2026)

| Tool | Free Tier | Paid | Model |
|------|-----------|------|-------|
| Kamal | Everything | N/A | 100% free |
| Sidekick | Everything | N/A | 100% free |
| Coolify (self-hosted) | Everything | N/A | 100% free |
| Coolify Cloud | 2 servers | $5/mo + $3/server | Per-server |
| Dokploy Cloud | Hobby | ~$4.50/mo/server | Per-server |
| Ploi | 1 server, 1 site | $9-30/mo | Tiered |
| RunCloud | None | $9-49/mo | Tiered |
| Laravel Forge | None | $12-39/mo | Tiered |
| Easypanel | Generous free | $14-32/mo | Tiered |
| Dokku Pro | CLI only | $849 lifetime | One-time |
| Server Compass | None | $29 one-time | One-time |
| PikaPods | $5 credit | From $1.20/mo | Usage-based |
| Elestio | $20 trial credit | $14/mo/service | Per-service |
| Sliplane | None | EUR 9/mo/server | Per-server |

---

## Final Recommendation

1. **Drop Neo+ to $49/year** (~$4/mo) — this is below the psychological $5/mo barrier
2. **Remove the 2-server free cap** — match competitor expectations
3. **Focus Neo+ on automation features** — scheduled backups, cloud backup targets, health alerts, metrics history
4. **Don't gate basic SSH operations** — terminal, exec, real-time metrics should be free
5. **Lean into the Vxero funnel** — Neo is the gateway drug; make it generous so users hit scale limits and need `neo connect`
6. **Consider a one-time purchase option** — $29-49 lifetime for solo devs, subscription for teams

The biggest risk is pricing Neo+ too high and losing users to Kamal (free, 37signals-backed, Rails 8 default) or Coolify (free, 50K stars, full PaaS). Neo's best play is being the most polished, easiest CLI tool with a generous free tier and a low-cost paid tier for power users — then converting them to Vxero when they outgrow a CLI.
