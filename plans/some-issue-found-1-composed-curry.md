# Fix: TUI Logs Flash + SSL ERR_SSL_PROTOCOL_ERROR on First Deploy

## Context

Two bugs were reported from the TUI dashboard:

1. **Logs flash** — "View Logs" in app/worker/sidecar/service menus shows output for ~0.1s then immediately returns to the menu. The root cause is that after `runLogs()` (or equivalent) returns, the TUI loop continues straight into re-rendering the menu with no blocking wait.

2. **SSL broken on first deploy** — After first deploy (when HTTPS is active), the browser shows `ERR_SSL_PROTOCOL_ERROR`. The workaround is to use the TUI to "Switch to HTTP only" then "Switch to HTTPS" again. The root cause: `UpdateRoute()` (used by the toggle path) calls `removeFromAutoHTTPSSkip()` before adding the HTTPS route — this is the critical step missing from the initial `AddRoute()` call in deploy. Also, `--temp` and auto-assigned sslip.io domains don't default to HTTPS even though their descriptions say "auto-SSL".

---

## Fix 1: TUI Logs — Block Until Keypress

**File:** `commands/dashboard.go`

After each `runLogs` / `runSidecarLogs` / `runServiceLogs` call that returns immediately (follow=false), add a "press any key" prompt before the loop continues.

Add a small helper (inline, not a new export needed) — or just use the existing `fmt.Print` + `ui.ReadKey()` pattern already used in `internal/ui/select.go:50`.

**4 locations to patch:**

| Line | Code | Context |
|------|------|---------|
| 779–781 | `runLogs(appName, 50, false, ...)` | App action menu |
| 879–882 | `runLogs(appName, 50, false, workerName, ...)` | Worker menu |
| 960–963 | `runSidecarLogs(appName, sidecarName)` | Sidecar menu |
| 1349–1351 | `runServiceLogs(svcName, 50, false)` | Service menu |

**Change pattern** (same for all 4):
```go
case "logs":
    if err := runLogs(appName, 50, false, "", "", ""); err != nil {
        return false, err
    }
    fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
    ui.ReadKey()
    fmt.Println()
```

`ui.Faint` is already defined in `internal/ui/styles.go`. `ui.ReadKey()` is in `internal/ui/tui.go:30`.

---

## Fix 2: SSL — Use UpdateRoute on First HTTPS Deploy + Default --temp to HTTPS

### Part A: Replace AddRoute with UpdateRoute in first-deploy HTTPS paths

**File:** `commands/deploy.go`

`UpdateRoute` = `RemoveRoute` (no-op if absent) + `removeFromAutoHTTPSSkip` + `AddRoute`.  
Using it on first deploy is safe and guarantees the domain is never stuck in the auto-HTTPS skip list.

Two locations:

**Single-container first deploy** (line ~958):
```go
// BEFORE:
if err := caddy.AddRoute(containerName, domains, upstream, authOpts...); err != nil {

// AFTER:
if err := caddy.UpdateRoute(containerName, domains, upstream, authOpts...); err != nil {
```

**Multi-replica first deploy** (line ~787–789 in the scale>1 path):
```go
// BEFORE:
if err := caddy.AddRoute(containerName, deployDomains, upstream, authOpts...); err != nil {

// AFTER:
if err := caddy.UpdateRoute(containerName, deployDomains, upstream, authOpts...); err != nil {
```

### Part B: Default --temp and auto-sslip.io domains to HTTPS

The `--temp` flag description says "with auto-SSL" but never enables it. Similarly the auto-assigned sslip.io domain (lines 433–441) carries a comment "supports Let's Encrypt auto-SSL" but defaults to HTTP-only.

**File:** `commands/deploy.go`

In the `httpOnly` determination block (lines 620–626), add:
```go
httpOnly := true
if isRedeploy {
    httpOnly = existing.HTTPOnly
}
if neoConfig != nil && neoConfig.HTTPS != nil {
    httpOnly = !*neoConfig.HTTPS
}
// --temp or auto-sslip.io assignment: default to HTTPS (fulfils "auto-SSL" promise)
if !isRedeploy && strings.HasSuffix(domain, ".sslip.io") && (neoConfig == nil || neoConfig.HTTPS == nil) {
    httpOnly = false
}
```

This ensures: if the user hasn't explicitly set `https:` in `.neo.yml` but the domain is sslip.io (from `--temp` or auto-assignment), HTTPS is defaulted on.

The route setup at lines 956–967 also needs to respect this: replace the `neoConfig.HTTPS != nil && *neoConfig.HTTPS` condition with `!httpOnly`:
```go
} else if !httpOnly {
    if err := caddy.UpdateRoute(containerName, domains, upstream, authOpts...); err != nil {
        ...
    }
} else {
    if err := caddy.AddRouteHTTP(containerName, domains, upstream, authOpts...); err != nil {
        ...
    }
}
```

(Same change for the multi-replica path at ~787–796.)

---

## Files to Modify

- `commands/dashboard.go` — 4 log-call sites (lines 779, 880, 961, 1350)
- `commands/deploy.go` — HTTPS route setup (lines ~620–626, ~787–796, ~956–967)

## Verification

1. **Logs flash**: Open TUI dashboard → select any app → View Logs → confirm output stays visible until a key is pressed.
2. **SSL**: Deploy a fresh app with `--temp` (or no domain) → confirm browser opens HTTPS directly without needing the HTTP→HTTPS toggle. Also test with `https: true` in `.neo.yml`.
