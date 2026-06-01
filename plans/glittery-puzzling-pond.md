# Post-Deploy HTTP Health Check + Zero-Downtime Deployment

## Context

Neo deploys Docker apps via a blue-green pattern: new container starts as `{appName}-next`, TCP port
is verified, then Caddy's upstream is atomically switched. The gap: TCP-open doesn't mean HTTP-healthy.

This adds an opt-in HTTP health check that mirrors Docker's health check semantics. It only activates
when `health.path` is explicitly configured. Without it, deployment behavior is unchanged.

---

## Opt-in Design

```yaml
# health.path not set → HTTP check skipped (current behavior, no regression)
# health.path set    → HTTP check runs before Caddy traffic switch
health:
  path: /health
```

This means:
- Laravel scheduler, queue workers, API-only apps: just don't set `path` → no HTTP check
- Apps with a health endpoint: set `path: /health` (or `/ping`, `/status`, etc.)
- `port == 0` (background workers): always skipped regardless

---

## Docker Health Check Semantics

All timing fields already exist in `.neo.yml`. The HTTP check reuses them:

```
start_period:  grace period — failures during this window are ignored (app initializing)
interval:      how often to poll (default: 10s)
timeout:       max time per HTTP request, default: 5s
retries:       consecutive non-2xx before declaring unhealthy (default: 3)
```

With `start_period: 60s, interval: 10s, retries: 3`:
- 0–60s: poll every 10s, ignore failures (migrations, cache warm-up)
- 60s+: 3 consecutive non-2xx → rollback
- Any 2xx at any point → pass

---

## How Zero-Downtime Already Works

```
1. Build image
2. Start app-{name}-next            ← old container still serving
3. TCP port open? (120s, 500ms poll)
4. [NEW, opt-in] HTTP 2xx on path?  ← only when health.path is set
     → unhealthy → remove -next, old keeps serving = rollback
5. PatchUpstream()                  ← single Caddy API call, zero gap
6. Remove old container
7. Rename -next → canonical name
8. PatchUpstream() back to canonical name
```

---

## Changes

### 1. `commands/neoconfig.go` — Add `Path` to `NeoHealth`

```go
type NeoHealth struct {
    Cmd         string `yaml:"cmd"`
    Interval    string `yaml:"interval,omitempty"`
    Timeout     string `yaml:"timeout,omitempty"`
    Retries     int    `yaml:"retries,omitempty"`
    StartPeriod string `yaml:"start_period,omitempty"`
    Path        string `yaml:"path,omitempty"` // HTTP path for post-deploy check; empty = disabled
}
```

### 2. `internal/state/state.go` — Add `Path` to `HealthCheck`

```go
type HealthCheck struct {
    Cmd         string `json:"cmd"`
    Interval    string `json:"interval,omitempty"`
    Timeout     string `json:"timeout,omitempty"`
    Retries     int    `json:"retries,omitempty"`
    StartPeriod string `json:"start_period,omitempty"`
    Path        string `json:"path,omitempty"` // persisted so redeploys without .neo.yml keep it
}
```

### 3. `internal/remote/docker.go` — Add `HTTPHealthCheck()`

Add after `IsPortOpen()` at line ~192:

```go
// HTTPHealthOpts configures the HTTP health check with Docker-compatible semantics.
type HTTPHealthOpts struct {
    Path        string        // HTTP path — must be non-empty to run the check
    Interval    time.Duration // poll interval (default 10s)
    Timeout     time.Duration // per-request curl timeout (default 5s)
    Retries     int           // consecutive failures before unhealthy (default 3)
    StartPeriod time.Duration // grace period where failures are ignored (default 0)
}

// HTTPHealthCheck polls containerName:port/path until healthy or unhealthy.
// Mirrors Docker healthcheck: start_period → interval × retries.
// Returns nil on first 2xx response.
// Returns error after Retries consecutive non-2xx responses post-start_period.
func (d *Docker) HTTPHealthCheck(containerName string, port int, opts HTTPHealthOpts) error {
    if opts.Interval <= 0 { opts.Interval = 10 * time.Second }
    if opts.Timeout <= 0  { opts.Timeout = 5 * time.Second }
    if opts.Retries <= 0  { opts.Retries = 3 }

    ip, err := d.exec.Run(fmt.Sprintf(
        "docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' %s 2>/dev/null",
        ssh.ShellQuote(containerName),
    ))
    if err != nil || strings.TrimSpace(ip) == "" {
        return fmt.Errorf("cannot resolve container IP for %s", containerName)
    }

    url := fmt.Sprintf("http://%s:%d%s", strings.TrimSpace(ip), port, opts.Path)
    curlCmd := fmt.Sprintf(
        "curl -s --max-time %d -o /dev/null -w '%%{http_code}' %s 2>/dev/null",
        int(opts.Timeout.Seconds()), ssh.ShellQuote(url),
    )

    startDeadline := time.Now().Add(opts.StartPeriod)
    consecutive := 0
    var lastCode string

    for {
        out, _ := d.exec.Run(curlCmd)
        code := strings.TrimSpace(out)

        if len(code) == 3 && code[0] == '2' {
            return nil // healthy
        }
        lastCode = code

        if !time.Now().Before(startDeadline) { // past start_period
            consecutive++
            if consecutive >= opts.Retries {
                if lastCode == "" || lastCode == "000" {
                    return fmt.Errorf("no HTTP response after %d checks", opts.Retries)
                }
                return fmt.Errorf("HTTP %s for %d consecutive checks", lastCode, opts.Retries)
            }
        }
        time.Sleep(opts.Interval)
    }
}
```

**Bounded execution**: worst case = `start_period + (retries × interval)`. With defaults: 0 + 3×10s = 30s.

### 4. `commands/deploy.go`

#### 4a. Update `neoHealthToState()` at line 1316 — persist `Path` without requiring `Cmd`

```go
func neoHealthToState(h *NeoHealth) *state.HealthCheck {
    if h == nil || (h.Cmd == "" && h.Path == "") {
        return nil
    }
    return &state.HealthCheck{
        Cmd:         h.Cmd,
        Interval:    h.Interval,
        Timeout:     h.Timeout,
        Retries:     h.Retries,
        StartPeriod: h.StartPeriod,
        Path:        h.Path,
    }
}
```

(`applyHealth()` at line 1330 still guards `h.Cmd == ""` before setting Docker flags — no change.)

#### 4b. Add `httpHealthOpts()` helper near `neoHealthToState()`

```go
// httpHealthOpts builds HTTPHealthOpts from config. Returns opts with empty Path when
// no health path is configured — callers must check opts.Path != "" before running.
func httpHealthOpts(neoHealth *NeoHealth, stateHealth *state.HealthCheck) remote.HTTPHealthOpts {
    var path, interval, timeout, startPeriod string
    var retries int
    if neoHealth != nil {
        path, interval, timeout, startPeriod, retries =
            neoHealth.Path, neoHealth.Interval, neoHealth.Timeout, neoHealth.StartPeriod, neoHealth.Retries
    } else if stateHealth != nil {
        path, interval, timeout, startPeriod, retries =
            stateHealth.Path, stateHealth.Interval, stateHealth.Timeout, stateHealth.StartPeriod, stateHealth.Retries
    }
    opts := remote.HTTPHealthOpts{Path: path, Retries: retries}
    if d, err := time.ParseDuration(interval); err == nil    { opts.Interval = d }
    if d, err := time.ParseDuration(timeout); err == nil     { opts.Timeout = d }
    if d, err := time.ParseDuration(startPeriod); err == nil { opts.StartPeriod = d }
    return opts
}
```

#### 4c. Insert HTTP check after line 590

After `ui.Success("Health check passed")`, before `httpOnly` / `deployDomains`:

```go
// HTTP health check — opt-in, only runs when health.path is configured.
// port==0 or no health.path → skipped (no behavior change for existing deployments).
if port > 0 {
    var stateHealth *state.HealthCheck
    if isRedeploy { stateHealth = existing.Health }
    var neoHealth *NeoHealth
    if neoConfig != nil { neoHealth = neoConfig.Health }

    hOpts := httpHealthOpts(neoHealth, stateHealth)
    if hOpts.Path != "" {
        spin = ui.NewSpinner(fmt.Sprintf("Waiting for HTTP health (%s)...", hOpts.Path))
        spin.Start()
        httpErr := docker.HTTPHealthCheck(nextName, port, hOpts)
        spin.Stop()
        if httpErr != nil {
            docker.Remove(nextName)
            if isRedeploy {
                ui.Error(fmt.Sprintf("HTTP health check failed — rolled back: %s", httpErr))
                ui.Info(fmt.Sprintf("Old version still running. Debug with: neo logs %s", appName))
            } else {
                ui.Error(fmt.Sprintf("HTTP health check failed: %s", httpErr))
                ui.Info("Fix the issue and re-deploy.")
            }
            return nil
        }
        ui.Success(fmt.Sprintf("HTTP health OK (%s)", hOpts.Path))
    }
}
```

---

## Final `.neo.yml` Schema

```yaml
health:
  path: /health        # opt-in: HTTP path to check before traffic switch (empty = disabled)
  interval: 10s        # poll interval (default: 10s)
  timeout: 5s          # per-request timeout (default: 5s)
  retries: 3           # consecutive failures before rollback (default: 3)
  start_period: 30s    # grace period for slow-starting apps (default: 0s)
  cmd: "..."           # Docker-internal health check (unchanged)

# Don't want HTTP check? Don't set health.path.
# Examples:
#   Laravel scheduler (no HTTP): omit health.path entirely
#   API-only app (/ returns 404): use health.path: /api/health
#   Standard web app: health.path: /health
```

---

## Critical Files

- [commands/deploy.go](commands/deploy.go) — insertion after line 590; `neoHealthToState()` at line 1316
- [internal/remote/docker.go](internal/remote/docker.go) — `HTTPHealthOpts` + `HTTPHealthCheck()` after line 192
- [commands/neoconfig.go](commands/neoconfig.go) — `NeoHealth` struct
- [internal/state/state.go](internal/state/state.go) — `HealthCheck` struct

---

## Verification

```bash
make build

# With health.path: /health → shows "Waiting for HTTP health (/health)" → "HTTP health OK (/health)"
# Without health.path → no HTTP check (TCP check only, existing behavior)
# port==0 workers → no HTTP check regardless
# Broken app (500 on /health) → after retries×interval, fail + rollback if redeploy
# Slow app (start_period: 60s) → failures ignored for 60s then retries start counting
```
