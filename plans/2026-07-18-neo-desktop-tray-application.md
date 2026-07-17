# Neo Desktop Tray Application

## Status

- **Document type:** implementation plan
- **Date:** 2026-07-18
- **Target platforms:** macOS and Windows
- **Repository decision:** keep the desktop application in this repository
- **Implementation order:** scaffold the desktop shell first, then connect it to shared Neo operations
- **Working product name:** Neo Desktop
- **Bundle identifier:** `dev.vxero.neo.desktop`

## Goal

Create a lightweight desktop application, similar in behavior to Laravel Herd, that lives in the macOS menu bar and Windows notification area. It must let a Neo user:

1. See whether configured remote servers are reachable.
2. Check CPU, memory, disk, latency, applications, and services quickly.
3. View recent and streaming application logs.
4. Receive useful, low-noise incident notifications.
5. Run a small allowlist of safe corrective actions such as start and restart.
6. Open a larger management window when the tray popover is not enough.

The desktop app continues Neo's existing agentless architecture. It connects to remote servers over SSH and does not require installing a new monitoring agent.

## Non-goals for the first release

The first beta will not include:

- Deploying a local project.
- Editing environment variables or displaying unmasked secrets.
- Restoring backups.
- Interactive database consoles.
- Arbitrary remote shell execution.
- Firewall, Caddy DNS, or destructive server configuration changes.
- AI-generated fixes or uploading server logs to an AI service.
- Linux desktop packaging.
- Cloud synchronization of server configuration.

These can be added after the read-only monitoring and safe-action foundation is reliable.

## Architecture decisions

### 1. Keep the desktop app in the Neo monorepo

Create `apps/desktop/` in this repository, but give it its own frontend and Rust dependencies. Do not make it a nested Git repository.

Reasons:

- CLI and desktop changes can be reviewed and merged atomically.
- The desktop bridge can use the existing Go `internal/` packages safely.
- Existing SSH, state, configuration, licensing, sandbox, and test infrastructure remains the source of truth.
- The bundled bridge and desktop UI can always be built from the same commit.
- The CLI and desktop can still have independent tags and release workflows.

Consider splitting `apps/desktop` into a separate repository only after the bridge protocol is stable and one of these becomes true:

- Desktop and CLI are maintained by different teams.
- The desktop has a substantially different release cadence.
- Other consumers need a supported public Neo SDK or API.

### 2. Use Tauri 2 for the desktop shell

Use Tauri 2 with React, TypeScript, and Vite.

Tauri owns:

- The native tray/menu-bar icon and menu.
- The small attached popover and larger management window.
- Application lifecycle and single-instance behavior.
- Start-at-login and desktop notifications.
- Bundling and launching the Go bridge sidecar.
- Signed automatic updates.
- macOS and Windows installers.

Keep Rust code thin. It should supervise the sidecar, expose a small set of typed commands to the frontend, and implement OS integration. It must not duplicate Neo's SSH or server-management logic.

Do not use Wails 3 for the first production release while the upstream project describes it as alpha. Revisit Wails after it reaches a stable release if removing the Rust/Go sidecar boundary would materially reduce maintenance.

### 3. Bundle a Go `neo-bridge` sidecar

Create `cmd/neo-bridge`. Tauri bundles a platform-specific build of this binary inside the application.

The desktop application must not depend on an externally installed `neo` executable. GUI applications do not reliably inherit a user's shell `PATH`, and an external CLI could be a different version from the desktop UI.

The bridge and CLI share Go application services. The bridge provides a stable machine-facing protocol; Cobra commands remain human-facing adapters.

### 4. Reuse the existing Neo configuration

The bridge reads and writes the existing `~/.neo/config.json` through `internal/config`.

The desktop app must not create a second server registry. A server added by the CLI appears in the desktop app after refresh, and a server added by the desktop app must be usable by the CLI.

Desktop-only preferences, such as polling interval and notification settings, are stored separately in the platform application-data directory. SSH hosts, keys, and the current server remain in Neo's existing configuration.

### 5. Keep the frontend outside the trust boundary

The React frontend never receives unrestricted shell access and never constructs SSH commands.

The only permitted path is:

```text
React UI
   |
   | typed Tauri commands/events
   v
Tauri Rust shell
   |
   | versioned newline-delimited JSON over stdio
   v
neo-bridge
   |
   | shared Go operations
   v
SSH -> remote Neo server
```

All state-changing bridge methods use an explicit allowlist and validate server, application, and action identifiers.

## Target repository layout

```text
neo/
|-- apps/
|   `-- desktop/
|       |-- README.md
|       |-- package.json
|       |-- package-lock.json
|       |-- tsconfig.json
|       |-- vite.config.ts
|       |-- index.html
|       |-- src/
|       |   |-- main.tsx
|       |   |-- app/
|       |   |-- components/
|       |   |-- features/
|       |   |   |-- apps/
|       |   |   |-- diagnostics/
|       |   |   |-- logs/
|       |   |   `-- servers/
|       |   |-- lib/
|       |   |   |-- desktop-api.ts
|       |   |   `-- protocol.ts
|       |   |-- styles/
|       |   `-- test/
|       `-- src-tauri/
|           |-- Cargo.toml
|           |-- build.rs
|           |-- capabilities/
|           |-- icons/
|           |-- src/
|           |   |-- bridge.rs
|           |   |-- commands.rs
|           |   |-- lib.rs
|           |   |-- main.rs
|           |   `-- tray.rs
|           |-- binaries/
|           `-- tauri.conf.json
|-- cmd/
|   |-- neo/
|   `-- neo-bridge/
|       `-- main.go
|-- internal/
|   |-- operations/
|   |   |-- actions.go
|   |   |-- diagnostics.go
|   |   |-- logs.go
|   |   |-- service.go
|   |   |-- snapshot.go
|   |   |-- types.go
|   |   `-- operations_test.go
|   |-- config/
|   |-- remote/
|   |-- ssh/
|   `-- state/
|-- commands/
|-- test/
|-- Makefile
`-- .github/workflows/
    |-- desktop-ci.yml
    `-- desktop-release.yml
```

Do not add Tauri, Rust, or frontend dependencies to the root Go module. The existing CLI remains buildable using its current Go and Docker workflow.

## Phase 1: scaffold first

The first implementation slice creates a runnable tray application before refactoring CLI behavior. This gives the team a visible walking skeleton and validates macOS/Windows tray behavior early.

### Scaffold commands

Use the official Tauri 2 React/TypeScript template and npm so contributors only need Node and Rust, without requiring another package manager:

```bash
mkdir -p apps
cd apps
npm create tauri-app@latest desktop -- \
  --template react-ts \
  --manager npm
```

If the installed generator does not accept `--manager`, run the interactive form
`npm create tauri-app@latest` and select project name `desktop`, TypeScript/JavaScript,
npm, React, and TypeScript. Confirm that the generated dependencies are Tauri 2
before normalizing or committing the scaffold.

Treat the generated output as a starting point. Normalize its product name, identifier, scripts, and directory structure before committing.

### Initial Tauri configuration

Configure:

- Product name: `Neo Desktop`
- Identifier: `dev.vxero.neo.desktop`
- Small tray window: approximately 380 x 560 pixels.
- Main management window: minimum 960 x 680 pixels.
- Small window hidden at startup.
- Closing a window hides it; it does not quit the tray process.
- Only one desktop process may run at a time.
- No dock icon on macOS when only the tray popover is open, if supported without harming the full-window experience.
- Strict Content Security Policy.
- Devtools disabled in production builds.

Add only the plugins needed by the first beta:

- `autostart`
- `notification`
- `process`
- `single-instance`
- `updater`

The shell/sidecar capability is configured in Rust and restricted to the bundled `neo-bridge` executable. Do not expose a generic shell command to JavaScript.

### Scaffold UI

Build the first tray popover against a `DesktopAPI` interface and fixture provider. It should render without an SSH server:

```ts
export interface DesktopAPI {
  hello(): Promise<BridgeHello>;
  listServers(): Promise<ServerSummary[]>;
  getSnapshot(server: string): Promise<ServerSnapshot>;
  listApps(server: string): Promise<AppSummary[]>;
  runAppAction(input: AppActionInput): Promise<OperationResult>;
  runDiagnostics(server: string): Promise<Finding[]>;
}
```

The fixture implementation is used only for Storybook-style development, tests, and the first visual shell. Production builds must use the Tauri transport.

The initial popover contains:

- Neo logo and aggregate status.
- Server selector.
- Reachability and last-refreshed timestamp.
- CPU, RAM, disk, and latency cards.
- Application running/stopped counts.
- Up to three findings.
- Refresh and Open Dashboard buttons.
- Settings and Quit tray menu entries.

### Phase 1 acceptance criteria

- `npm run tauri dev` launches a single tray application on macOS.
- The tray icon opens and hides the popover reliably.
- The popover displays fixture server data.
- Open Dashboard shows the full management window.
- Closing either window leaves the tray application running.
- Quit terminates it completely.
- Light and dark system themes are usable.
- Frontend unit tests run without starting Tauri.
- A Windows CI build proves the scaffold compiles before real server logic is added.

## Phase 2: introduce the bridge walking skeleton

### Bridge process behavior

`neo-bridge` is a long-running child process owned by Tauri.

On startup it:

1. Configures structured logging to stderr.
2. Reads newline-delimited JSON requests from stdin.
3. Writes only protocol responses and events to stdout.
4. Handles graceful shutdown when stdin closes or it receives a shutdown request.
5. Never prompts on stdin for passwords, host trust, license activation, or selections.

Human-readable diagnostics go to stderr so they cannot corrupt the protocol stream.

### Protocol envelope

Start with protocol version `1`.

Request:

```json
{"version":1,"id":"req-123","method":"server.snapshot","params":{"server":"production"}}
```

Success response:

```json
{"version":1,"id":"req-123","result":{"reachable":true,"latencyMs":84}}
```

Error response:

```json
{
  "version":1,
  "id":"req-123",
  "error":{
    "code":"ssh_unreachable",
    "message":"Could not connect to production",
    "retryable":true,
    "details":{}
  }
}
```

Streaming event:

```json
{"version":1,"event":"logs.line","subscription":"log-45","data":{"line":"..."}}
```

### Initial methods

Implement in this order:

| Method | Purpose | Mutates remote state |
|---|---|---:|
| `bridge.hello` | Protocol, bridge, CLI-core, platform, and activation information | No |
| `bridge.shutdown` | Graceful process shutdown | No |
| `server.list` | Read configured servers and current selection | No |
| `server.snapshot` | Reachability, metrics, and counts | No |
| `app.list` | Applications, workers, sidecars, and services | No |
| `logs.subscribe` | Start recent/live log stream | No |
| `logs.unsubscribe` | Cancel log stream | No |
| `diagnostics.run` | Produce deterministic findings | No |
| `app.action` | Start, stop, or restart one application | Yes |
| `operation.cancel` | Cancel an outstanding operation | No |

### Stable error codes

Define error codes in Go and mirror them in generated or checked TypeScript types:

- `invalid_request`
- `protocol_mismatch`
- `not_activated`
- `server_not_found`
- `app_not_found`
- `ssh_unknown_host`
- `ssh_auth_failed`
- `ssh_unreachable`
- `remote_state_invalid`
- `operation_timeout`
- `operation_cancelled`
- `action_not_allowed`
- `internal_error`

The UI makes decisions using error codes, never by parsing English error messages.

### Bridge supervision

The Tauri layer:

- Starts exactly one bridge process.
- Performs `bridge.hello` before showing live data.
- Rejects an incompatible protocol version.
- Correlates responses using request IDs.
- Routes streaming events to the correct frontend window.
- Restarts the bridge at most three times after unexpected exits, using exponential backoff.
- Shows a clear error after the restart budget is exhausted.
- Terminates the bridge when the desktop app exits.

### Phase 2 acceptance criteria

- A bundled bridge responds to `bridge.hello` on macOS and Windows.
- The desktop lists servers from the real `~/.neo/config.json`.
- No external Neo CLI installation is required.
- Protocol stdout remains valid when debug logging is enabled.
- The desktop displays structured activation, unknown-host, auth, and unreachable errors.
- Tauri can recover from one forced bridge crash.

## Phase 3: extract shared Go operations

The bridge must not call Cobra commands or capture terminal output. Refactor business behavior out of `commands/` into `internal/operations` while preserving existing CLI output and flags.

### Service dependencies

Use dependency injection so unit tests do not require a live SSH server:

```go
type Executor interface {
    Run(ctx context.Context, command string) (string, error)
    Stream(ctx context.Context, command string, output io.Writer) error
    ReadFileElevated(ctx context.Context, path string) ([]byte, error)
    Close() error
}

type Connector interface {
    Connect(ctx context.Context, server config.Server) (Executor, error)
}

type Service struct {
    configStore ConfigStore
    connector   Connector
    clock       Clock
}
```

If changing `internal/ssh.Executor` to accept contexts is too disruptive for the first slice, add context-aware wrapper methods and migrate callers incrementally. Every bridge operation still needs a deadline and cancellation path.

### Domain types

Use typed numeric fields in the shared operation layer rather than the current human-oriented strings:

```go
type Snapshot struct {
    Server       ServerSummary   `json:"server"`
    Reachable    bool            `json:"reachable"`
    ObservedAt   time.Time       `json:"observedAt"`
    LatencyMS    int64           `json:"latencyMs"`
    CPUPercent   float64         `json:"cpuPercent"`
    RAMUsedBytes uint64          `json:"ramUsedBytes"`
    RAMTotalBytes uint64         `json:"ramTotalBytes"`
    DiskUsedBytes uint64         `json:"diskUsedBytes"`
    DiskTotalBytes uint64        `json:"diskTotalBytes"`
    UptimeSeconds uint64         `json:"uptimeSeconds"`
    Apps         WorkloadCounts  `json:"apps"`
    Services     WorkloadCounts  `json:"services"`
    Containers   []ContainerStat `json:"containers"`
}
```

Keep the existing CLI `neo status --json` response backward compatible. The CLI adapter maps the shared typed snapshot to the current JSON shape until a deliberate CLI schema-version change is released.

### Snapshot collection

Reduce SSH overhead by collecting VM metrics and Docker information in as few remote commands as practical. Requirements:

- A default 12-second connection deadline.
- A default 15-second snapshot deadline.
- Partial Docker-stat failures do not discard valid server metrics.
- Missing platform commands produce unavailable fields, not misleading zero values.
- Raw remote output is never sent directly to the UI without parsing and validation.

### Lifecycle actions

Move the reusable parts of start, stop, and restart from `commands/manage.go` into the operation service.

Each action returns a structured result:

```go
type OperationResult struct {
    OperationID string         `json:"operationId"`
    Status      string         `json:"status"`
    StartedAt   time.Time      `json:"startedAt"`
    FinishedAt  *time.Time     `json:"finishedAt,omitempty"`
    Summary     string         `json:"summary"`
    Changes     []Change       `json:"changes"`
}
```

The service validates application names against remote Neo state before building container commands. Never interpolate a frontend-provided container name directly into a shell command.

### Log streaming

The bridge owns log-stream cancellation and backpressure:

- Default to the most recent 200 lines.
- Cap a requested tail at 5,000 lines.
- Permit follow mode.
- Limit each desktop process to five simultaneous log subscriptions.
- Batch high-volume events before sending them to the webview.
- Stop the SSH stream when the window closes, the user unsubscribes, or the context expires.
- Keep existing server-side grep behavior out of the first desktop beta; search loaded lines locally.

### Licensing

Move the command pre-run licensing decision into a small reusable guard so CLI and bridge enforce the same activation rules.

`bridge.hello` returns activation status without exposing the license key. If activation is missing, the first beta may direct the user to `neo activate`; a later desktop slice can implement the existing email/key activation flow through shared Go services.

The bridge must never log or return the license key.

### Phase 3 acceptance criteria

- CLI commands and JSON output remain backward compatible.
- CLI and bridge use the same snapshot, listing, log, and lifecycle implementation.
- Unit tests use fake connectors/executors.
- Existing `go test ./...` passes.
- Existing sandbox tests pass.
- Context cancellation stops live logs and long-running SSH operations.
- No Cobra, Huh, Lipgloss, or terminal UI package is imported by `internal/operations`.

## Phase 4: live tray behavior

### Polling policy

The desktop polls configured servers without overwhelming SSH:

- Immediately refresh the selected server when the popover opens.
- Refresh the selected server every 30 seconds while a window is visible.
- Refresh other configured servers every 120 seconds.
- Limit concurrent SSH snapshots to three.
- Add up to 10% random jitter so many desktop clients do not poll together.
- Back off an unreachable server through 30, 60, 120, and 300 seconds.
- Manual refresh bypasses backoff once, but repeated clicks are debounced.
- Cache the last successful snapshot and display its age when the server is offline.

Only one layer owns periodic refresh. Prefer the desktop application service rather than allowing every React component to start its own timer.

### Tray state

Aggregate all configured server results into four states:

| State | Tray appearance | Meaning |
|---|---|---|
| Healthy | Green | All recently checked servers are reachable with no critical finding |
| Warning | Amber | One or more advisories or stopped workloads |
| Critical | Red | Unreachable server or critical diagnostic |
| Unknown | Gray | No configured server, startup, stale cache, or refresh in progress |

On macOS, use a template icon that follows light/dark menu-bar appearance. Do not rely only on color; the icon shape or badge must distinguish critical and unknown states.

### Notifications

Notify only on transitions:

- Server changed from reachable to unreachable.
- Server recovered.
- Application changed to an unexpected stopped/unhealthy state.
- Critical resource threshold persisted for the configured number of samples.
- A user-triggered action succeeded or failed after the popover was closed.

Deduplicate repeated notifications and apply a default five-minute cooldown per finding. Do not notify for the first observation after application startup until the initial scan completes.

### Phase 4 acceptance criteria

- Polling continues while windows are hidden.
- Opening several windows does not multiply polling.
- Last-known data is clearly marked as stale.
- The tray state reflects multiple configured servers.
- Notification transitions and cooldowns have deterministic tests.
- Sleep/wake and network reconnect trigger a debounced refresh.

## Phase 5: insights and safe fixes

### Deterministic diagnostics

Create pure diagnostic rules that accept a snapshot and return findings. Initial rules:

| Rule | Warning | Critical | Persistence |
|---|---:|---:|---:|
| Disk usage | >= 75% | >= 90% | One sample |
| RAM usage | >= 80% | >= 95% | Three samples |
| CPU usage | >= 80% | >= 95% | Three samples |
| SSH latency | >= 750 ms | >= 2,000 ms | Three samples |
| Server reachability | N/A | Unreachable | Two attempts |
| App state | Stopped | Restarting/unhealthy | One sample after initial scan |
| Service state | Stopped | Restarting/unhealthy | One sample after initial scan |

Each finding contains:

```go
type Finding struct {
    ID                string         `json:"id"`
    Rule              string         `json:"rule"`
    Severity          string         `json:"severity"`
    Summary           string         `json:"summary"`
    Evidence          []Evidence     `json:"evidence"`
    RecommendedFixID  string         `json:"recommendedFixId,omitempty"`
    FirstObservedAt   time.Time      `json:"firstObservedAt"`
    LastObservedAt    time.Time      `json:"lastObservedAt"`
}
```

Do not infer a precise cause from one resource sample. Phrase findings as observations and provide the evidence used.

### Fix safety classes

| Class | Examples | Confirmation |
|---|---|---|
| Read only | Refresh, inspect logs, rerun diagnostics | None |
| Reversible | Start or restart an app | One confirmation, optionally remember preference |
| Availability affecting | Stop or update an app | Confirmation every time |
| Destructive | Remove, restore, firewall, database changes | Not available in first beta |

Every state-changing action shows:

- Target server and application.
- Exact high-level action.
- Expected availability impact.
- Start time, progress, and final result.
- A link to relevant logs after failure.

Store a local action history without environment values, passwords, private keys, license keys, or complete unredacted logs.

### Phase 5 acceptance criteria

- Diagnostic rule tests cover boundary values and persistence.
- Findings show their evidence and last observation time.
- Restart/start actions require the correct confirmation.
- Duplicate clicks cannot start the same action twice concurrently.
- The app refreshes server state immediately after an action.
- Destructive operations are absent from the bridge allowlist.

## Phase 6: packaging and release

### Versioning

Use independent tags:

```text
v0.22.0          # Neo CLI release
desktop-v0.1.0  # Neo Desktop release
```

The desktop semantic version, bridge build version, Git commit, and protocol version are returned by `bridge.hello` and shown in About/diagnostics.

### CI workflow

Add `.github/workflows/desktop-ci.yml` with path filters for:

- `apps/desktop/**`
- `cmd/neo-bridge/**`
- `internal/operations/**`
- Shared packages imported by operations.

Required checks:

1. Root Go unit tests.
2. Bridge protocol and contract tests.
3. Frontend lint, TypeScript check, and unit tests.
4. Rust formatting, lint, and tests.
5. macOS application build.
6. Windows x64 application build.

Run cross-platform tray smoke tests on native GitHub runners. Do not make a cross-compiled artifact the only verification for its target OS.

### Release workflow

Add `.github/workflows/desktop-release.yml`, triggered by `desktop-v*` tags.

Build in this order per target:

1. Build the target-specific `neo-bridge` from the tag commit.
2. Place it under the Tauri sidecar filename expected for the target triple.
3. Build the frontend.
4. Build and package the Tauri application.
5. Sign the application, embedded bridge, and installer as required.
6. Generate signed updater artifacts.
7. Smoke test the installed application.
8. Publish all artifacts and checksums atomically.

Initial production targets:

- macOS ARM64.
- macOS Intel, or a universal package if testing confirms the updater works cleanly with it.
- Windows x64.

Add Windows ARM64 after the first beta unless a launch customer requires it immediately.

### Signing prerequisites

Obtain these early; do not leave signing until the release week:

- Apple Developer ID Application certificate.
- App Store Connect/notarization credentials.
- Windows Authenticode certificate and timestamping configuration.
- Tauri updater signing key stored only in CI secrets.

Never place signing keys, certificate passwords, or updater private keys in this repository.

### Update behavior

- Check silently after startup and every six hours.
- Prompt before downloading a normal update.
- Verify the updater signature before installation.
- Show release notes and target version.
- Allow deferral, but do not repeatedly prompt during the same session.
- Treat bridge and desktop as one indivisible update; never update only the sidecar.

### Phase 6 acceptance criteria

- A clean Mac installs, launches, updates, and uninstalls the signed app.
- A clean Windows 10/11 machine installs, launches, updates, and uninstalls it.
- macOS Gatekeeper and Windows SmartScreen recognize signed artifacts appropriately.
- The embedded bridge is the expected version and signature.
- A bad updater signature fails closed.
- Upgrade preserves desktop preferences and existing `~/.neo/config.json`.

## Testing strategy

### Go tests

- Snapshot parsers with fixture output from supported Linux distributions.
- Empty/missing Docker state.
- SSH timeout, authentication failure, unknown host, and cancellation.
- Application lifecycle validation and command quoting.
- Diagnostic thresholds and persistence.
- Protocol encoding, malformed messages, duplicate IDs, and version mismatch.
- Ensure logs and errors redact sensitive values.

### Frontend tests

- Healthy, warning, critical, unknown, stale, and loading states.
- No-server and not-activated onboarding.
- Server switching and manual refresh.
- Confirmation flows.
- Log batching, pause, clear, and bounded in-memory history.
- Notification transition reducer.
- Keyboard navigation and screen-reader labels.

### Rust/Tauri tests

- Bridge launch and handshake.
- Request correlation and cancellation.
- Unexpected bridge exit and restart budget.
- Window show/hide and single-instance behavior.
- Tray menu updates.
- Capability configuration prevents arbitrary process execution.

### Integration tests

Reuse the existing Neo sandbox/test infrastructure where possible:

- Configured server appears in the desktop bridge.
- Snapshot values render correctly.
- Stop/start/restart modifies the expected sandbox container.
- Logs stream and cancel without leaking SSH sessions.
- Desktop and CLI observe the same remote state after an action.

### Manual platform matrix

- macOS current and previous major release, Apple Silicon.
- macOS Intel before declaring Intel support.
- Windows 11 x64.
- Windows 10 x64 while supported by the chosen Tauri/WebView2 baseline.
- Light mode, dark mode, scaled displays, multiple displays.
- Laptop sleep/wake, offline/online transition, and VPN changes.
- SSH agent key, configured key file, encrypted key, missing key, and unknown host.

## Security requirements

- Preserve strict `known_hosts` verification.
- Never introduce an “accept all host keys” desktop option.
- Unknown hosts require an explicit foreground trust flow or use of the existing CLI initialization flow.
- Use the OS credential store if the desktop later stores password or key-passphrase material.
- Never store credentials in localStorage or frontend state longer than required.
- Mask environment values and credentials in logs and bridge errors.
- Validate all identifiers before constructing remote commands.
- Apply timeouts and cancellation to every SSH operation.
- Restrict the Tauri content security policy and plugin capabilities.
- Do not expose generic shell execution to the webview.
- Code-sign the app, embedded bridge, installer, and updates.
- Keep a local, redacted record of user-triggered mutations.

## Observability and support bundle

The desktop application needs its own local diagnostics without exposing secrets.

Record:

- Desktop, bridge, protocol, OS, and architecture versions.
- Bridge start/stop/restart events.
- Request method, duration, and error code, but not sensitive parameters.
- Poll scheduling and cache age.
- Notification transitions.
- Update checks and signature failures.

Add an Export Diagnostic Bundle action before public beta. The bundle should contain redacted desktop logs and version/config metadata, but exclude:

- Private keys or their contents.
- Passwords and passphrases.
- License keys.
- Application environment values.
- Full server logs unless the user explicitly adds them after preview.

## Development commands to add

Add root convenience targets after scaffolding:

```make
desktop-install:
	cd apps/desktop && npm ci

desktop-bridge:
	go build -o apps/desktop/src-tauri/binaries/<host-target-name> ./cmd/neo-bridge

desktop-dev: desktop-bridge
	cd apps/desktop && npm run tauri dev

desktop-test:
	go test ./...
	cd apps/desktop && npm test -- --run
	cd apps/desktop/src-tauri && cargo test
```

Implement target-triple naming in a script or Task target rather than requiring developers to type it manually. The script must support macOS ARM64/Intel and Windows x64 initially.

## Implementation slices

Keep pull requests reviewable in this sequence:

1. **Desktop scaffold first**
   - Tauri/React project, fixture UI, tray, two windows, frontend tests, Windows compile check.
2. **Bridge skeleton**
   - Sidecar packaging, `bridge.hello`, protocol client, supervision, contract tests.
3. **Servers and snapshots**
   - Shared operation service, real config, typed snapshot, CLI compatibility tests.
4. **Application list and tray polling**
   - Multiple servers, cache, backoff, aggregate tray state.
5. **Logs**
   - Bounded recent logs, follow stream, cancellation, full-window viewer.
6. **Diagnostics and notifications**
   - Deterministic rules, persistence, transition notifications.
7. **Safe lifecycle actions**
   - Start/stop/restart, confirmation, action history, immediate refresh.
8. **Release engineering**
   - Native CI, signing, notarization, installers, updater, smoke tests.
9. **Beta hardening**
   - Sleep/wake, offline behavior, accessibility, support bundle, performance.

Each slice must leave the CLI build and test suite green.

## Rough schedule for one developer

| Work | Estimate |
|---|---:|
| Scaffold and validate tray UX | 4-6 days |
| Bridge and shared operation extraction | 6-9 days |
| Live status, caching, and polling | 4-6 days |
| Logs, diagnostics, notifications, actions | 7-10 days |
| Packaging, signing, updates, hardening | 5-8 days |

Expected first beta: approximately four to six weeks, excluding delays obtaining signing certificates.

## Risks and mitigations

### Tray/window differences between platforms

**Risk:** macOS menu-bar behavior and Windows notification-area behavior are not identical.

**Mitigation:** validate the scaffold on both platforms before extracting substantial backend code. Keep platform-specific window positioning in the thin Tauri layer.

### Two implementation languages

**Risk:** Rust plus Go increases contributor requirements.

**Mitigation:** keep all Neo behavior in Go. Limit Rust to process supervision, tray/windows, commands, and events. Document the boundary and test the protocol.

### SSH polling load

**Risk:** frequent checks create connection load and poor battery usage.

**Mitigation:** concurrency limits, jitter, backoff, last-known cache, visibility-aware intervals, and combined remote commands.

### CLI behavior regressions during extraction

**Risk:** moving logic out of Cobra commands changes existing terminal behavior or JSON.

**Mitigation:** characterize existing output before refactoring, add compatibility tests, and keep presentation mapping in `commands/`.

### Bridge/UI version mismatch

**Risk:** incompatible protocol or separately updated bridge.

**Mitigation:** bundle both from one commit, handshake before use, version the protocol, and update them as one signed application.

### Signing delays

**Risk:** unsigned builds trigger Gatekeeper or SmartScreen and block beta adoption.

**Mitigation:** acquire certificates during the scaffold phase and run a signed internal release before feature completion.

## Definition of beta complete

The desktop beta is complete when:

1. Signed installers are available for macOS ARM64 and Windows x64.
2. The app starts from a tray icon and can launch at login.
3. It uses the existing Neo server configuration.
4. It monitors multiple configured servers without installing a remote agent.
5. It shows live server/application status and bounded logs.
6. It provides deterministic findings with evidence.
7. It safely starts, stops, and restarts applications with confirmation.
8. It notifies on failures and recoveries without repeated noise.
9. The bridge rejects unknown methods and arbitrary command execution.
10. CLI behavior and existing tests remain compatible.
11. Signed updates work on clean macOS and Windows test machines.
12. A redacted diagnostic bundle can be exported for support.

## First implementation task

Start with implementation slice 1 only: scaffold `apps/desktop`, create the fixture-backed tray popover, verify the two-window lifecycle on macOS, and add a Windows compile job. Do not begin the shared Go refactor until the tray shell works on both target operating systems.
