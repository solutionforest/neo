# Plan: Create `neo-skill` — Universal AI Coding Assistant Instructions for Neo CLI

## Context

Neo is a CLI for managing remote servers over SSH. Users working with neo alongside AI coding assistants (Claude Code, GitHub Copilot, Cursor, Windsurf, Cline, Codex, etc.) have no context-aware guidance. This plan creates a **standalone repo** at `/Users/alan/Development/solution-forest/projects/neo-skill/` that provides neo knowledge in every major AI assistant format.

## Design: One Core, Multiple Formats

Each AI tool looks for a different filename. Rather than maintaining N copies, we write **one authoritative content file** and provide thin tool-specific wrappers.

## Folder Structure

```
neo-skill/
├── README.md                          # How to install for each AI tool
├── neo.md                             # Core knowledge document (single source of truth)
├── .claude-plugin/
│   └── plugin.json                    # Claude Code plugin manifest
├── skills/
│   └── neo/
│       └── SKILL.md                   # Claude Code skill (frontmatter + includes neo.md content)
├── copilot-instructions.md            # GitHub Copilot format (copy to .github/)
├── .cursorrules                       # Cursor format
├── .windsurfrules                     # Windsurf format
├── .clinerules                        # Cline / Roo Code format
├── AGENTS.md                          # OpenAI Codex CLI format
└── install.sh                         # Script to symlink/copy to a project
```

### How it works

- **`neo.md`** (~350 lines) — the single source of truth with all neo knowledge: commands, workflows, `.neo.yml` reference, troubleshooting
- **Tool-specific files** — each one is essentially `neo.md` content adapted to the tool's format:
  - `skills/neo/SKILL.md` — adds Claude Code YAML frontmatter on top
  - `copilot-instructions.md` — plain markdown (GitHub Copilot's format)
  - `.cursorrules` / `.windsurfrules` / `.clinerules` — plain markdown (identical content, different filenames)
  - `AGENTS.md` — plain markdown (OpenAI Codex format)
- **`install.sh`** — optional helper: detects which AI tools are in use and symlinks the right files into the user's project

### `plugin.json` (Claude Code only)

```json
{
  "name": "neo-skill",
  "description": "Neo CLI skill — deploy apps, manage servers, configure services",
  "version": "1.0.0",
  "author": { "name": "Solution Forest" }
}
```

### `SKILL.md` frontmatter (Claude Code only)

```yaml
---
name: neo
description: Guide for using the neo CLI — deploy, manage servers, configure domains, databases, troubleshoot.
allowed-tools: Bash, Read, Glob, Grep
argument-hint: "[what you want to do, e.g. 'deploy my app', 'set up postgres']"
---
```

Followed by the same content as `neo.md`.

## Core Content: `neo.md` (~350 lines)

### Section 1: Identity + Context Gathering (~25 lines)
- You are a neo CLI operations assistant
- On invocation: read `.neo.yml`, detect `Dockerfile`/`docker-compose.yml`
- Tailor all advice to the user's project

### Section 2: Command Reference (~70 lines)
- Full command list mirroring `printHelpLLM()` from [root.go:227-300](commands/root.go#L227-L300)
- Groups: Setup, Apps, Logs, Domains, Env, Services, Data, Updates
- Global flags (`--server`)

### Section 3: Workflow Guides (~100 lines)
- First-time setup: server requirements → `neo init` → first deploy
- Deploy a project: detect type → generate `.neo.yml` → deploy
- Add a database: `neo service create` → `neo service link` → verify env
- Domain + SSL: DNS → `neo domain` → verification
- Local dev: `neo dev` compose vs standalone, workers/sidecars
- Multi-environment: `.neo.yml` environments section
- Template apps: 12 templates (ghost, wordpress, gitea, n8n, plausible, umami, miniflux, chatwoot, uptime-kuma, vaultwarden)

### Section 4: `.neo.yml` Full Reference (~90 lines)
- All fields from `NeoConfig` struct ([neoconfig.go:181-201](commands/neoconfig.go#L181-L201))
- Volume formats (flat string, bind mount, structured object)
- Workers, sidecars, hooks, health checks
- Basic auth, custom SSL, dev section
- Named environments with per-env overrides
- Multi-domain (`domain` vs `domains`)

### Section 5: Troubleshooting (~35 lines)
- App not starting, deploy failures, domain/SSL issues
- Service linking problems, SSH connection issues

### Section 6: Decision Rules (~25 lines)
- When to suggest `neo install` vs `neo deploy`
- Shared vs bundled services
- Present commands for user to confirm — never execute destructive ops directly

## `README.md`

Documents installation for each tool:

| Tool | How to install |
|------|---------------|
| Claude Code | `claude plugin add /path/to/neo-skill` or add to marketplace |
| GitHub Copilot | Copy `copilot-instructions.md` → `.github/copilot-instructions.md` |
| Cursor | Copy `.cursorrules` to project root |
| Windsurf | Copy `.windsurfrules` to project root |
| Cline / Roo Code | Copy `.clinerules` to project root |
| OpenAI Codex | Copy `AGENTS.md` to project root |
| Any other tool | Use `neo.md` as custom instructions |
| Auto-detect | Run `./install.sh` in your project directory |

## `install.sh`

A simple script that:
1. Takes a target project directory as argument (defaults to `.`)
2. Detects which AI tools are configured (checks for `.cursor/`, `.github/`, etc.)
3. Symlinks or copies the appropriate files
4. Prints what was installed

## Source of Truth Files (in neo repo)

| File | What to extract |
|------|----------------|
| [commands/neoconfig.go](commands/neoconfig.go) | All `.neo.yml` types and fields |
| [commands/root.go:227-300](commands/root.go#L227-L300) | `printHelpLLM()` — command reference |
| [commands/ask.go](commands/ask.go) | 6 workflows users commonly need |
| [CLAUDE.md](CLAUDE.md) | Architecture details for advanced sections |
| [internal/app/templates/](internal/app/templates/) | 12 available app templates |

## Implementation Steps

1. Create directory structure at `/Users/alan/Development/solution-forest/projects/neo-skill/`
2. Write `neo.md` — the core knowledge document
3. Write `skills/neo/SKILL.md` — Claude Code version (frontmatter + neo.md content)
4. Write `.claude-plugin/plugin.json`
5. Generate tool-specific files (`.cursorrules`, `.windsurfrules`, `.clinerules`, `copilot-instructions.md`, `AGENTS.md`) — same content as `neo.md`, different filenames
6. Write `install.sh` — auto-detect + symlink helper
7. Write `README.md` — installation instructions for each tool
8. `git init` the repo

## Verification

1. Install as Claude Code plugin → invoke `/neo deploy my app` → should give context-aware guidance
2. Copy `.cursorrules` to a test project → open in Cursor → ask about neo → should understand commands
3. Copy `copilot-instructions.md` to `.github/` → Copilot should know neo workflows
4. Run `./install.sh /path/to/project` → correct files should be symlinked
5. Verify no tool-specific file exceeds 8K chars (GitHub Copilot limit)
