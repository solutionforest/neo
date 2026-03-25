# Neo Templates

Neo templates are pre-configured application manifests that enable one-command Docker app deployments on any server managed by Neo. Each template bundles the app image, required services (databases, caches), volumes, environment variables, and health checks into a single `manifest.yml` file.

## Available Templates

| Name | Category | Services | Description |
|------|----------|----------|-------------|
| [chatwoot](chatwoot/) | support | PostgreSQL, Redis | Customer engagement platform |
| [ghost](ghost/) | blogging | MySQL | Professional publishing platform |
| [gitea](gitea/) | developer-tools | PostgreSQL | Self-hosted Git service |
| [miniflux](miniflux/) | reading | PostgreSQL | Minimalist RSS feed reader |
| [n8n](n8n/) | automation | PostgreSQL | Workflow automation tool |
| [plausible](plausible/) | analytics | PostgreSQL, ClickHouse | Privacy-friendly analytics |
| [umami](umami/) | analytics | PostgreSQL | Simple web analytics |
| [uptime-kuma](uptime-kuma/) | monitoring | None (SQLite) | Self-hosted monitoring |
| [vaultwarden](vaultwarden/) | security | None (SQLite) | Bitwarden-compatible password manager |
| [wordpress](wordpress/) | cms | MySQL | The world's most popular CMS |

## Usage

Install any template with a single command:

```bash
neo install <name>
```

For example:

```bash
neo install ghost
neo install wordpress
neo install plausible
```

Neo will pull the Docker image, start bundled services, generate secrets, configure the reverse proxy, and provision a TLS certificate automatically.

## Creating a New Template

See [CONTRIBUTING.md](CONTRIBUTING.md) for instructions on adding new templates.

## License

MIT
