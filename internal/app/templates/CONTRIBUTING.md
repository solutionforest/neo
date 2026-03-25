# Contributing Templates

Thank you for contributing a new template to Neo. Follow these steps to create, test, and submit your template.

## Steps

1. Create a directory under `templates/<name>/` (lowercase, hyphens for multi-word names).
2. Add a `manifest.yml` file following the schema below.
3. Add a `README.md` with quick facts and post-install instructions (see any existing template for the format).
4. Run validation: `go test ./internal/app/...` to verify the manifest parses correctly.
5. Test on a real server: `neo install <name>` and confirm the app starts, the health check passes, and the domain routes correctly.
6. Submit a pull request.

## Manifest Reference

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique template identifier (lowercase, e.g. `ghost`). |
| `title` | string | Human-readable display name (e.g. `"Ghost"`). |
| `description` | string | One-line description of the application. |
| `category` | string | Template category (see allowed list below). |
| `version` | string | Application version being deployed. |
| `image` | string | Docker image with tag (e.g. `ghost:5-alpine`). |
| `port` | int | Port the application listens on inside the container. |

### Metadata Fields (optional)

| Field | Type | Description |
|-------|------|-------------|
| `maintainer` | string | Maintainer identifier (use `vxero` for official, or your GitHub username). |
| `official` | bool | `true` if maintained by the Vxero team. |
| `website` | string | Application's official website URL. |
| `tags` | list | Searchable tags (e.g. `["blog", "cms"]`). |
| `min_neo_version` | string | Minimum Neo CLI version required. |
| `min_ram` | string | Minimum recommended RAM (e.g. `"512MB"`, `"2GB"`). |
| `min_disk` | string | Minimum recommended disk space (e.g. `"1GB"`). |
| `notes` | string | Multi-line post-install notes shown to the user. |
| `links` | map | Key-value pairs of link labels to URLs (e.g. `documentation`, `source`, `docker_hub`). |

### Volumes

```yaml
volumes:
  - name: ghost-content       # Volume name (used as Docker volume name)
    path: /var/lib/ghost/content  # Mount path inside the container
    label: "Ghost content"     # Human-readable description (optional)
```

### Environment Variables

```yaml
env:
  - key: APP_URL               # Environment variable name
    label: "Public URL"         # Prompt label shown to the user (optional)
    value: "production"         # Static default value (optional)
    from: domain                # Auto-fill: "domain" (full URL) or "domain_host" (hostname only)
    from_service: postgres      # Auto-wire: references a bundled service by name
    template: "${POSTGRES_PASSWORD}"  # Template string with variable interpolation
    generate: "hex:64"          # Auto-generate: "hex:<length>" or "base64:<length>"
    ask: true                   # Prompt the user for input during install
```

Fields are mutually exclusive in practice: use `value` for static defaults, `from` for domain auto-fill, `generate` for secrets, `from_service`+`template` for service wiring, or `ask` for interactive prompts.

### Services

Bundled services (databases, caches) that are deployed alongside the main app:

```yaml
services:
  - name: postgres             # Service name (used in container naming)
    image: postgres:16-alpine  # Docker image
    port: 5432                 # Service port
    volumes:                   # Service-specific volumes (same schema as above)
      - name: app-db
        path: /var/lib/postgresql/data
    env:                       # Service-specific env vars (same schema as above)
      - key: POSTGRES_PASSWORD
        generate: hex:32
```

### Health Check

```yaml
health:
  path: /healthz               # HTTP path to check
  interval: 10s                # Time between checks
  timeout: 5s                  # Request timeout
  retries: 3                   # Failures before marking unhealthy
```

## Allowed Categories

- `analytics` -- Web analytics and metrics
- `automation` -- Workflow and task automation
- `blogging` -- Publishing and blogging platforms
- `cms` -- Content management systems
- `developer-tools` -- Git, CI/CD, and development tools
- `monitoring` -- Uptime and infrastructure monitoring
- `reading` -- RSS readers and content aggregation
- `security` -- Password managers, vaults, and security tools
- `support` -- Customer support and engagement

## Review Criteria

All submitted templates are reviewed against these requirements:

- **Trusted image**: Docker image must be from an official or well-known source (Docker Hub official images, GitHub Container Registry from the upstream project, etc.).
- **Health check required**: Every template must include a `health` block with a working endpoint.
- **No hardcoded secrets**: Secrets must use `generate` for auto-generation. Never commit plaintext passwords or API keys.
- **README.md**: Must include quick facts (image, port, services, min RAM) and post-install instructions.
- **Tested on a real server**: The template must be verified end-to-end with `neo install` on a live server.

## Official vs Community

- **Official** templates are maintained by the Vxero team (`maintainer: vxero`, `official: true`).
- **Community** templates set `maintainer` to the contributor's GitHub username and omit or set `official: false`.

Community templates follow the same quality standards and review process as official ones.
