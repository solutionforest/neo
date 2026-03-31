# Plan: Add Workers & Sidecars to `neo dev`

## Context

`neo dev` in standalone Dockerfile mode only starts the main app container. Workers and sidecars defined in `.neo.yml` are ignored, meaning background jobs (queue workers) and supporting services (Redis, etc.) don't run locally. This forces developers to manually start these containers or create a docker-compose.yml just for local dev.

Fix: `neo dev` should automatically start workers and sidecars alongside the app, matching production behavior.

## Design

### Container naming & network

All dev containers share a Docker network for inter-container communication:

| Component | Container name | Network |
|-----------|---------------|---------|
| App | `neo-dev-{app}` | `neo-dev-{app}` |
| Worker | `neo-dev-{app}-worker-{name}` | `neo-dev-{app}` |
| Sidecar | `neo-dev-{app}-sidecar-{name}` | `neo-dev-{app}` |

Network name: `neo-dev-{sanitizedAppName}` (created before containers, removed on `down`).

### Workers (same image, different command)

- Use the already-built `neo-dev-{app}:latest` image
- Override command with `worker.Command`
- Share the same env vars and volumes as the app
- Always run detached (`-d`) — even when the app runs in foreground
- No restart policy in dev (containers are ephemeral)
- No health check wait (keep dev startup fast)

### Sidecars (separate image)

- **`image:`** — pull from registry (`docker pull`)
- **`build:`** — build locally (`docker build -t neo-dev-{app}-sidecar-{name}:latest {context}`)
- Sidecar-specific env vars only (not inherited from app, matching deploy behavior)
- Sidecar volumes: check if volume name matches a top-level app volume (share it), otherwise create a standalone dev volume
- Always run detached
- No restart policy or health check in dev

### Startup order

1. Create network `neo-dev-{app}`
2. Start sidecars first (services the app depends on, e.g. Redis)
3. Start the main app (with `--network neo-dev-{app}`)
4. Start workers (they may need the app or sidecars to be up)

### Cleanup (`neo dev down`)

1. Remove worker containers: `docker rm -f neo-dev-{app}-worker-*`
2. Remove sidecar containers: `docker rm -f neo-dev-{app}-sidecar-*`
3. Remove app container: `docker rm -f neo-dev-{app}`
4. Remove network: `docker network rm neo-dev-{app}`

### Compose mode

No changes — docker-compose.yml already defines its own services, workers, and sidecars. The `dev:` section env/volumes still apply.

## Changes — [dev.go](commands/dev.go)

### New helper: `startDevWorkers(appName, imageName, networkName, env, volumes, workers)`

```go
func startDevWorkers(appName, imageName, networkName string, env map[string]string, volumes []string, workers map[string]NeoWorker) {
    for name, cfg := range workers {
        containerName := "neo-dev-" + sanitizeName(appName) + "-worker-" + name
        exec.Command("docker", "rm", "-f", containerName).Run()

        args := []string{"run", "-d", "--name", containerName, "--network", networkName}
        for _, v := range volumes {
            args = append(args, "-v", v)
        }
        for k, v := range env {
            args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
        }
        args = append(args, imageName)
        args = append(args, strings.Fields(cfg.Command)...)

        if err := exec.Command("docker", args...).Run(); err != nil {
            ui.Error(fmt.Sprintf("Worker %s failed: %s", name, err))
        } else {
            ui.Success(fmt.Sprintf("Worker %s started", name))
        }
    }
}
```

### New helper: `startDevSidecars(appName, projectDir, networkName, sidecars)`

```go
func startDevSidecars(appName, projectDir, networkName string, sidecars map[string]NeoSidecar) {
    for name, cfg := range sidecars {
        containerName := "neo-dev-" + sanitizeName(appName) + "-sidecar-" + name
        exec.Command("docker", "rm", "-f", containerName).Run()

        var imageName string
        if cfg.Image != "" {
            // Pull pre-built image
            imageName = cfg.Image
            exec.Command("docker", "pull", imageName).Run()
        } else if cfg.Build.Context != "" {
            // Build from Dockerfile
            imageName = "neo-dev-" + sanitizeName(appName) + "-sidecar-" + name + ":latest"
            buildCtx := cfg.Build.Context
            if !filepath.IsAbs(buildCtx) {
                buildCtx = filepath.Join(projectDir, buildCtx)
            }
            buildArgs := []string{"build", "-t", imageName}
            if cfg.Build.Dockerfile != "" {
                buildArgs = append(buildArgs, "-f", filepath.Join(buildCtx, cfg.Build.Dockerfile))
            }
            buildArgs = append(buildArgs, buildCtx)
            if err := exec.Command("docker", buildArgs...).Run(); err != nil {
                ui.Error(fmt.Sprintf("Sidecar %s build failed: %s", name, err))
                continue
            }
        } else {
            ui.Error(fmt.Sprintf("Sidecar %s: must have 'image' or 'build'", name))
            continue
        }

        args := []string{"run", "-d", "--name", containerName, "--network", networkName}
        for volName, containerPath := range cfg.Volumes {
            // Use a dev-named volume
            volDockerName := "neo-dev-" + sanitizeName(appName) + "-" + volName
            args = append(args, "-v", fmt.Sprintf("%s:%s", volDockerName, containerPath))
        }
        for k, v := range cfg.Env {
            args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
        }
        args = append(args, imageName)
        if cfg.Command != "" {
            args = append(args, strings.Fields(cfg.Command)...)
        }

        if err := exec.Command("docker", args...).Run(); err != nil {
            ui.Error(fmt.Sprintf("Sidecar %s failed: %s", name, err))
        } else {
            ui.Success(fmt.Sprintf("Sidecar %s started", name))
        }
    }
}
```

### Modify `runDevDockerfile`

- Create network before starting containers
- Add `--network` flag to main app container
- Call `startDevSidecars()` before app (services the app depends on)
- Call `startDevWorkers()` after app starts (if detached) or skip if foreground (workers still start detached)
- Show worker/sidecar count in output

### Modify `runDevDown`

- Stop and remove worker containers: `docker rm -f` each
- Stop and remove sidecar containers: `docker rm -f` each
- Remove network: `docker network rm`
- Need to enumerate containers by prefix or read from `.neo.yml`

### Cleanup strategy for `runDevDown`

Since `runDevDown` loads `.neo.yml`, it knows the worker and sidecar names. Clean up by constructing container names from config:

```go
if neoConfig != nil {
    for name := range neoConfig.Workers {
        exec.Command("docker", "rm", "-f", "neo-dev-"+sanitizeName(appName)+"-worker-"+name).Run()
    }
    for name := range neoConfig.Sidecars {
        exec.Command("docker", "rm", "-f", "neo-dev-"+sanitizeName(appName)+"-sidecar-"+name).Run()
    }
}
exec.Command("docker", "rm", "-f", containerName).Run()
exec.Command("docker", "network", "rm", "neo-dev-"+sanitizeName(appName)).Run()
```

## Files to Modify

| File | Change |
|------|--------|
| [commands/dev.go](commands/dev.go) | Add `startDevWorkers`, `startDevSidecars`, modify `runDevDockerfile` and `runDevDown` |
| [commands/dev_test.go](commands/dev_test.go) | Tests for new helpers (unit tests, no Docker required) |
| [CLAUDE.md](CLAUDE.md) | Update `neo dev` docs |
| [TECHNICAL.md](TECHNICAL.md) | Update `neo dev` docs |

## Verification

```bash
make build                   # binary builds
make test                    # unit tests pass (via -vet=off)

# Manual test with .neo.yml:
# workers:
#   queue:
#     command: "echo worker running && sleep 3600"
# sidecars:
#   redis:
#     image: redis:7-alpine

./bin/neo dev --build        # should start app + worker + redis
docker ps                    # verify all 3 containers on same network
./bin/neo dev down           # should stop all 3 + remove network
```
