# Vaultwarden

Lightweight, self-hosted Bitwarden-compatible password manager server.

## Quick Facts

- **Image**: `vaultwarden/server:1.32.5`
- **Port**: 80
- **Services**: None (uses SQLite internally)
- **Min RAM**: 256MB

## Post-Install

1. Create your account by visiting the web vault at your domain.
2. Access the admin panel at `https://<your-domain>/admin` using the generated admin token.
3. Connect using any official Bitwarden client (set the server URL to your domain).

## Links

- [Documentation](https://github.com/dani-garcia/vaultwarden/wiki)
- [Source Code](https://github.com/dani-garcia/vaultwarden)
- [Docker Hub](https://hub.docker.com/r/vaultwarden/server)
