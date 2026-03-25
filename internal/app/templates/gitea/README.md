# Gitea

Lightweight, self-hosted Git service with a GitHub-like interface.

## Quick Facts

- **Image**: `gitea/gitea:1.22-rootless`
- **Port**: 3000
- **Services**: PostgreSQL 16 (bundled)
- **Min RAM**: 512MB

## Post-Install

1. Visit your domain to create your admin account on first launch.
2. The rootless image is used for improved security.
3. SSH access for Git operations can be configured on port 2222 if needed.

## Links

- [Documentation](https://docs.gitea.com/)
- [Source Code](https://github.com/go-gitea/gitea)
- [Docker Hub](https://hub.docker.com/r/gitea/gitea)
