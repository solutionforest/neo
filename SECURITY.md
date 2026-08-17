# Security Policy

## Reporting a vulnerability

**Please don't open a public issue.** Use GitHub's private vulnerability reporting:
[Report a vulnerability](https://github.com/solutionforest/neo/security/advisories/new).

That gives us a private thread with you and a way to publish an advisory once there's a fix. We'll
acknowledge your report and keep you posted on what we find — and if you'd like the credit, we'll
name you in the advisory.

## Why this matters more than for a typical CLI

Neo holds the keys to production servers. A vulnerability here is not "an app is affected" — it is
potentially every server a user has run `neo init` against. Specifically, Neo touches:

| Asset | Where it lives |
|---|---|
| SSH credentials | ssh-agent, `~/.ssh/id_*`, or a password held in memory during `neo init` |
| Server config and license key | `~/.neo/config.json` |
| Env encryption keys | `~/.neo/keys.json` (mode 0600, plain text) |
| Every app's environment | `/etc/neo/state.json` on the server, root-only, includes decrypted secrets |
| Reverse proxy config | Caddy admin API on the server, including basic-auth hashes and TLS material |

Anything that lets an attacker read those, write to them, or get a command executed on a remote
server through Neo is in scope — including cases where Neo is the *vector* rather than the target.

Things we treat as security reports, not ordinary bugs:

- Command injection through a value Neo interpolates into a remote shell command (app names,
  domains, env values, volume paths, container names)
- Any path where a secret reaches somewhere it shouldn't: stdout, a log, a world-readable file, a
  committed file, an SSH command line visible in the remote process list
- Host key verification that can be bypassed or silently downgraded
- A deploy that can be induced to run attacker-controlled code on the server
- Privilege escalation through Neo's `sudo` usage on the remote host
- License validation bypass that also exposes another user's data

## Out of scope

- Secrets a user commits to their own repository, or pastes into an issue
- Vulnerabilities in the applications you deploy *with* Neo
- Vulnerabilities in Docker, Caddy or the server OS itself — report those upstream, though tell us
  if Neo's defaults make them materially worse
- The fact that decrypted environment variables are stored in `/etc/neo/state.json` and passed as
  container environment variables. That's documented, deliberate, and the reason `env_encrypted:`
  is described as protecting the repository and the laptop rather than the server at rest.
- Missing hardening that has no exploit path — send those as ordinary issues, they're still
  welcome

## Supported versions

Fixes land on the latest release. Neo self-updates (`neo upgrade`), so we don't backport to older
versions — if you're reporting against an old build, check the current release first.
