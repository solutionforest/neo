# Changelog

All notable changes to Neo will be documented here.

---

## v0.26.3 — 2026-08-20

Four defects found by an independent review of v0.26.1/v0.26.2. Two of them were introduced by those releases.

### Fixes

- **A transient read failure could delete every route on the server.** `loadHTTPServerConfig` answered a failed GET of `srv0` with a fabricated empty server and no error. Its callers edit that value and write it straight back, and Caddy replaces the whole value at a path rather than merging — so one hiccup during any `--http` or `--cloudflare-flexible` domain change wiped every app's route. This was dormant until v0.26.1: the write used `PUT` and always failed with 409, so the bad config could never land. Fixing the verb made it land. A failed read is now an error; only a genuinely absent `srv0` yields an empty server, where there is nothing to lose.

- **`neo domain` and `neo deploy` could leave an app with no route at all.** `UpdateRoute`, `UpdateRouteMulti` and `UpdateRouteMultiHTTP` deleted the live route *before* the auto-HTTPS edit that v0.26.1 made fallible. If that edit failed, the function returned having deleted the route and never re-added it — and most deploy call sites discard the error, so the deploy reported success while the app was offline. The fallible step now runs first, so a failure changes nothing.

- **Caddy returning `null` for a missing `srv0` crashed the CLI.** `json.Unmarshal` leaves the map nil, and the next write into it panicked with `assignment to entry in nil map` instead of reporting a problem.

- **Setting a custom SSL certificate clobbered neighbouring TLS config.** v0.26.2 wrote the whole `certificates` object, taking `automate`, `load_folders` and `load_pem` with it — a narrower repeat of the `/load` mistake it was fixing. The write now targets `load_files` alone, as the pre-v0.26.2 code did.

### Tests

Five existing tests asserted only how many calls were made, never which URL — a wrong path passed silently. They now assert the path. Each of the four fixes above has a regression test that was confirmed to fail against the reintroduced defect.

---

## v0.26.2 — 2026-08-19

### Fixes

- **Setting a custom SSL certificate wiped every other app's route.** `neo domain --cert/--key` sent its certificate block to Caddy's `/load` endpoint, which replaces the *entire* active configuration — and the payload carried only `apps.tls`, with no `apps.http`, no servers and no routes. Every route on the server was dropped. `neo domain` then rebuilt the route for the app being configured, so the app in front of you came back and the others silently did not. Certificates are now pointed at with a targeted write to `/config/apps/tls/certificates`.

- **A route ID reached the shell unquoted.** Admin API URLs embed a route `@id` derived from the app name, and the URL was interpolated into the `curl` command without quoting — so an app name containing shell metacharacters could break out of the command. The URL is now quoted as a single argument.

- **The remaining admin calls no longer discard Caddy's error.** `PatchUpstream`, `PatchUpstreams` and the welcome-page route write still ran through `curl -sf`, which suppresses the response body. They now report Caddy's own message like the rest. The four `DELETE` calls are deliberately left as they were: an absent route is the expected case there and callers ignore it by design.

---

## v0.26.1 — 2026-08-19

### Fixes

- **`neo domain` could not add a domain.** Setting or adding a domain failed, and the app kept running with no route in Caddy at all. Neo wrote Caddy's server config with `PUT`, which Caddy defines as *create* — it answers `409 key already exists: srv0` whenever the key is present, and `srv0` is created by `neo init`, so the call could never succeed on a real server.

  Every path that adjusts `automatic_https.skip` went through that write, which is why it hit `--http`, `--cloudflare-flexible` (`edge_https`) and plain HTTPS alike. On the HTTP-only paths the error propagated and aborted the command before the route was added; on the HTTPS path it was discarded, so a domain moved back from HTTP-only silently kept its skip entry and never got a certificate.

  Writes now use replace semantics, falling back to create for a genuinely new key.

- **Caddy's error message is no longer thrown away.** Admin API calls ran through `curl -sf`, and `-f` suppresses the response body while reporting only a non-zero exit — so an admin-API rejection was indistinguishable from a network failure, and the actual reason was visible only inside the Caddy container's log. Failures now report Caddy's own message and status, e.g. `caddy admin PATCH /config/apps/http/servers/srv0: HTTP 409: key already exists: srv0`.

- **Wildcard and on-demand TLS setup could not write its config.** The guard that creates the TLS app tested curl's exit status, but Caddy answers `200` with a literal `null` body for a missing key, so `-f` saw success, the create step was skipped, and every later write to `/config/apps/tls/automation` failed with `invalid traversal path`. The same flaw affected the four copies of the `srv0` guard, now a single body-checking helper.

- **Scaled apps always took the slow path when switching upstreams.** `PatchUpstreams` used `PUT` on a key that already exists, so it always failed and fell through to a full route replacement — including its brief routing gap — instead of the atomic swap it exists to provide.

---

## v0.26.0 — 2026-08-18

### New Features

- **Deployments now record which build is running.** An image tag carried only a timestamp, so two deploys of different commits were indistinguishable and a redeploy of the same commit looked like a new version. Neo captures the git commit, branch, tag and dirty state at deploy and keeps them with the app.

  ```
  $ neo list
  NAME               DOMAIN                 PORT   STATUS      VERSION
  ● shop             shop.example.com       8080   running     v1.4.2 (a1b2c3d)
  ● api              api.example.com        3000   running     9f01e2a *
  ```

  The image tag gains the same suffix — `neo-shop:20260818-045536-a1b2c3d`. The timestamp stays, so tags remain sortable and unique when the same commit is redeployed after a config change. Works outside git too: a scaffolded project or a tarball simply has no version to show, and CI shallow checkouts fall back to `NEO_GIT_COMMIT`, `GITHUB_SHA` or `CI_COMMIT_SHA`.

- **`neo status <app>`** — full detail for one app: which build, which commit, who deployed it and when, its domains, basic auth, replicas and containers. Until now nothing showed one app in full; `neo list` truncates and `neo status` was server-only. `--json` makes it a CI gate: compare `.deployment.commit` against the sha you expected to ship.

- **`neo deploys <app>`** — deployment history: what shipped, when, and from whose machine. Recorded server-side in `/etc/neo/deploys/<app>.jsonl`, so it is shared by everyone who deploys, survives a fresh clone, and is not tied to one laptop. Entries whose image has since been pruned are marked, since those can no longer be restored from the image alone.

- **`NEO_*` variables in the container.** `NEO_DEPLOYMENT_ID`, `NEO_GIT_COMMIT`, `NEO_GIT_SHORT_COMMIT`, `NEO_GIT_BRANCH`, `NEO_GIT_TAG` and `NEO_DEPLOYED_AT` are injected, so `printenv` on the server answers "which release is this?". They are set *before* `.neo.yml` interpolation runs, so a project can wire them anywhere without Neo knowing about the tool:

  ```yaml
  env:
    SENTRY_RELEASE: "${NEO_GIT_COMMIT}"
  ```

  An explicitly set value always wins.

- **OCI labels on the built image** — `org.opencontainers.image.revision` and `.version`, plus the deployment id and branch. The image identifies itself through `docker inspect` even if server state is lost.

- **Deploying uncommitted changes now warns.** The recorded commit describes only part of what shipped, which is how "production is broken but git says the fix is in" happens. The deployment is marked dirty in `neo list`, `neo status` and the history.

---

## v0.25.6 — 2026-08-18

### Fixes

- **`neo status` counted apps from state alone.** It is the command you run to answer "is everything up?", but the numbers came only from `/etc/neo/state.json` — so an app running without a record was quietly missing from the count. It now runs the same state-vs-server check as `neo list` and reports untracked, missing or stopped apps under the summary.

### Internal

- The bundled app templates are now covered by tests. The existing manifest tests all parsed inline fixtures, so nothing verified that the ten templates Neo ships still load — a broken manifest, or a `go:embed` pattern that stopped matching, would have kept `make test` green and only failed at `neo install <name>`. The new tests load the real registry and assert every template has a name, title, image and usable port, is retrievable by the name a user types, and carries an explicit image tag rather than `:latest`.

---

## v0.25.5 — 2026-08-18

### Fixes

- **`servers:` groups were ignored outside `--all`.** `EffectiveServers()` exists to unify `server:` and `servers:`, but three code paths read `.Server` directly. An environment declared with `servers: [web-1]` therefore resolved to nothing and fell through to whatever `neo use` last selected — deploying production config to the active server without a word. Single-server groups now resolve correctly in `neo deploy --to`, in the `--all` grouping, and in the per-environment deploy.
- **A multi-server environment no longer deploys to one arbitrary machine.** `neo deploy --to production` where production declares two or more servers now stops and tells you to use `--all` (whole group) or `--server` (one member), instead of silently shipping to the active server.
- **`neo sync` read the wrong server.** It resolved which environment to write into but always connected to the active server, so syncing an environment hosted elsewhere reported "app not found" — or, where two environments share an app name, copied the wrong server's state into the environment block. It now targets the environment's server, and asks you to pick with `--server` when the environment is a group.
- **`--server` combined with `--all` is now rejected.** It was silently ignored, since `--all` deploys every environment to the servers it declares.

---

## v0.25.4 — 2026-08-17

### New Features

- **`neo config generate` now reports everything it did not carry over.** It parsed about ten service keys and discarded the rest without a word, so a `healthcheck:`, a `deploy.replicas`, or Traefik routing `labels:` vanished and you only found out when the deploy behaved differently from `docker compose up`. The file is now re-read generically and every unhandled key is listed per service, with a pointer where Neo has an equivalent:

  ```
  Not carried over — review these by hand
  ~  app     deploy:       replicas map to scale: in .neo.yml
  ~  app     healthcheck:  add health: to .neo.yml
  ~  app     labels:       Traefik/proxy labels are not read — set domain: in .neo.yml
  ~  app     user:         set the user in the Dockerfile instead
  ```

  Keys that genuinely don't affect a Neo deploy (`container_name`, `networks`, `depends_on`, `logging`) stay quiet.

- **`restart:` is migrated.** It was read to detect one-shot services but never written to `.neo.yml`, so a service declared `restart: unless-stopped` silently fell back to the default.

---

## v0.25.3 — 2026-08-17

### Fixes

- **`neo config generate` disqualified the web service it was meant to pick.** The background-process check matched "worker" anywhere in the command, so an Octane or FrankenPHP server started with `--workers=auto` was ruled out as a worker and the app role fell to whatever sorted next — on a real Laravel compose it chose the Reverb websocket server as the site. Flags are stripped before matching now. Regression from 0.25.2.
- **`entrypoint:` was ignored.** Compose routinely splits `entrypoint: ["php","artisan"]` from `command: horizon`, and reading only `command` produced workers whose command was `horizon` — not a program. Entrypoint and command are now joined the way Docker runs them.
- **One-shot services became looping workers.** `composer install`, an asset build, or a `migrate` step declared with `restart: "no"` was mapped to a worker, which would re-run it forever, or to a sidecar, which would exit and look broken. They are now skipped, and the output says where the work belongs: `hooks.pre_build` for build steps, `release:` for anything touching the app's own state (migrations, `key:generate`, `storage:link`).
- **Build stage and args were dropped in silence.** A service pinning `target: development` would have deployed the development stage to production. Both are now reported as not migrated.

---

## v0.25.2 — 2026-08-17

### Fixes

- **`neo config generate` picked a random service as the app.** All three selection passes iterated a map, so a compose file with several services built from one image produced a different `.neo.yml` on every run — including naming a queue worker as the public site. Selection is now ranked and deterministic: `build:` beats published ports, ports beat a `VIRTUAL_HOST`, ties break by name, and a service whose command is a worker or scheduler (`queue:work`, `schedule:work`, `horizon`, `supervisord`) is never eligible.
- **Workers were only detected for services built from source.** A service sharing the app's *image* with a different command — the normal shape for a prebuilt-image compose — became a sidecar. Same image or same build context plus a command now maps to `workers:`.
- **Except when it would change behaviour.** Neo workers inherit the app's environment with no per-worker override, so a service that sets a variable to a different value can't be one — mapping it would silently point a `--queue=study` worker at the nomination queue. Those stay sidecars, and the output names the conflicting variables.

### New Features

- **Reads nginx-proxy conventions.** `VIRTUAL_PORT` fills in `port:` and `VIRTUAL_HOST` fills in `domain:` when the compose file publishes no ports, instead of emitting `port 0` and making you retype values already in the file.
- **Says when the result cannot be deployed.** A compose file with no `build:` anywhere describes prebuilt images, and `neo deploy` builds from a Dockerfile — generate now reports that instead of leaving you to hit "No Dockerfile found" later.
- **Flags multiple public services.** Several services with ports or a `VIRTUAL_HOST` means several sites; Neo routes one public app per `.neo.yml`, so it names which one it chose and that the others need their own project.
- **Skips sibling public apps instead of making them sidecars.** A second web service with its own `VIRTUAL_HOST` was emitted as a sidecar, which would run a second copy of the site with no route to it.

---

## v0.25.1 — 2026-08-17

### New Features

- **`command:` for the app container.** Workers, sidecars and compose services could all override their image CMD; the app was the arbitrary exception. It now takes `command:` at the top level and per environment — useful for running Octane with different worker counts per environment, or a different entrypoint from a shared image. Both YAML forms are accepted:

  ```yaml
  command: php artisan octane:frankenphp --workers=4
  # or, docker-compose style
  command: ["sh", "-lc", "php artisan octane:frankenphp --workers=4"]
  ```

  The command is persisted to server state, so `--env-only` restarts keep it.

- **`neo config generate` carries over the app service's `command:`.** It was read for sidecars only, so migrating a compose project whose app overrode its CMD silently dropped it and deployed the wrong process.

### Fixes

- **A container that exits immediately now says why.** If `command:` is set and the container dies on start, the deploy names it as the likely cause instead of reporting a bare health-check failure — a container's command is its main process, so a one-off task exits in milliseconds and takes the container with it. Use `release:` for those.

---

## v0.25.0 — 2026-08-17

### New Features

- **`release:` — commands that run inside the new container before traffic switches.** The gap `hooks:` could not fill: hooks run on your machine, so they can't run `php artisan migrate --force` or `storage:link` in the deployed container. Release commands run on the server, in the new container, after its health check and *before* Caddy switches traffic to it.

  ```yaml
  release:
    - php artisan migrate --force
    - php artisan config:cache

  environments:
    dev:
      release:                       # replaces the top-level list
        - php artisan storage:link
  ```

  - **A failure rolls the deploy back.** The new container is removed and the old one keeps serving, so a broken migration never goes live.
  - **Scaled apps run the list once**, in the first new replica — migrations must not run concurrently.
  - `--env-only` runs them against the live container (there is no staging container on that path), so a failure is reported rather than rolled back. Useful for `config:cache` after an env change.
  - `--all` runs them per environment before that environment's traffic switch.
  - Output is streamed, so a long migration shows progress instead of a frozen spinner.

  Release commands must exit — a long-running process belongs in `workers:` or the image's `CMD`.

---

## v0.24.5 — 2026-08-17

### New Features

- **Per-environment `dockerfile:`.** An environment can now name its own build file, overriding the top-level `dockerfile:`. Resolution is `--dockerfile` > `environments.<env>.dockerfile` > top-level `dockerfile:` > `./Dockerfile`. A path an environment names but that doesn't exist is a hard error rather than a silent fallback to `./Dockerfile`.
- **`neo deploy --all` checks the environments agree on a Dockerfile.** `--all` builds one image and ships it everywhere, so environments naming different Dockerfiles now stop the run with the conflict listed, instead of quietly shipping one build as if it were both. Pass `--dockerfile` to force one, or deploy the environments individually.

---

## v0.24.4 — 2026-08-17

### Fixes

- **`neo sync` produced a `.neo.yml` that `neo deploy` refused.** Sync wrote `domain:` at the root even when the project used `environments:` — the one combination deploy rejects outright ("root-level domain:/domains: is ignored when environments: are defined"). Sync is now environment-aware: it resolves the environment (`--to`, the only one, or a prompt), derives the app name the way deploy does (including the `-<environment>` suffix), and writes into that environment block.
- **`neo sync` no longer rewrites your whole file.** It used to re-marshal the config struct over `.neo.yml`, destroying every comment, reordering keys, re-indenting from 2 to 4 spaces and re-quoting values (`CADDY_AUTO_HTTPS: on` became `"on"`). It now edits the YAML node tree in place and touches only the keys that actually changed. Blank lines between blocks are still lost — the YAML library does not model them.
- **`domains:` lists are left alone.** Sync rewrote a multi-domain list from a single state value, dropping the rest. It now reports the mismatch and leaves the list for you to edit.

### New Features

- **`neo sync --to <environment>`** — pick the environment to sync without being prompted.

---

## v0.24.3 — 2026-08-16

### Fixes

- **Basic auth could be added and never enforced.** `AddRoute` POSTed a new route without removing the existing one carrying the same `@id`. Caddy keeps route IDs unique, so adding `basic_auth` to an app that already had a route either failed silently or left the old, unauthenticated route in front of the new one — the site stayed open while Neo reported success. Route creation now replaces rather than stacks, in both the single-upstream and scaled paths.

### New Features

- **`neo caddy routes`** — prints what the proxy is *actually* serving: route ID, domains, upstreams, and whether basic auth is in the handler chain. Reading Caddy back is the only way to tell a route that was configured from a route that was accepted.
- **`neo caddy reload`** — rebuilds every app's route from `/etc/neo/state.json` (domains, upstream, HTTP/HTTPS mode, basic auth, edge-HTTPS headers). Use `--app <name>` for one app. This is the repair tool when the live proxy has drifted from state after an interrupted deploy or a manual container change.

### Notes

- Caddy has no config "reload" to trigger: the admin API applies changes the moment they are made, and `neo caddy update` updates the Caddy *image*, not the routes. `neo caddy reload` exists for drift repair, not as a step you need in a normal deploy.

---

## v0.24.2 — 2026-08-16

### Fixes

- **"0 apps" when apps are running.** Nothing checked that `/etc/neo/state.json` matched the server, so a lost state write or a container removed with plain `docker rm` left `neo list` reporting "No apps installed" while the app was up and serving traffic. Neo now compares state against `docker ps` and reports apps that are running but untracked, tracked but missing a container, or present but stopped. The dashboard shows `⚠ N untracked` next to the app count instead of quietly undercounting, and `neo list --json` gains a `drift` object so scripts can tell an empty server from a server whose state was lost.
- **Dashboard could crash with "concurrent map read and map write".** The cache handed callers the live map while background refresh goroutines wrote to it. Reads now take a snapshot under the lock. Verified with the race detector.
- **A transient error no longer kicks you out of the dashboard.** An error inside the Servers, Applications or Services menu propagated all the way out, dropping you to the shell and forcing a restart of `neo`. Errors now render in place and the menu stays open — the same applies to failed worker and sidecar actions, which used to close the screen you were working in.

### Internal

- `make test-race` runs the suite under the race detector.
- `plans/2026-08-16-full-screen-tui.md` proposes a persistent full-screen TUI for a future release. Proposal only — no behaviour change.

---

## v0.24.1 — 2026-08-16

### Fixes

- **`neo deploy --all` no longer loses apps from server state.** `/etc/neo/state.json` is rewritten whole with no locking, so two environments deploying to the *same* server in parallel each loaded the old state and the second write dropped the first one's app entry — the container kept running while Neo forgot it existed, and the next deploy treated it as a first deploy (losing its domain and stored env vars). Targets are now grouped by server: different servers still deploy concurrently, targets sharing a server run one after another.
- **Failed state writes are no longer silent.** `state.Save` was called for its side effect in 16 places with the error discarded, so a failed write still reported success and left `/etc/neo/state.json` describing a server that no longer matched reality. Those paths now report the failure and say what to check.
- **A mistyped encryption key is no longer cached.** The key was written to `~/.neo/keys.json` before anything decrypted with it, so one typo made every later deploy fail the MAC check with no prompt to correct it. Keys are saved only after they decrypt the file, and a failed decrypt now says where the key came from and how to clear it.
- **Encryption keys are found across environments.** A key saved by `neo env encrypt` at the project root was invisible to `neo deploy --to staging`, which looked up `my-app-staging` only. Lookup now falls back to the bare project name.
- **Long `.env` lines parse correctly.** The parser used `bufio.Scanner`'s 64 KB default, so a PEM certificate or long base64 secret on a single line made it error out — and callers dropped that error, losing *every* variable in the file. The cap is now 1 MB, and an `env_file` that exists but fails to parse is reported instead of ignored.

---

## v0.24.0 — 2026-08-16

### New Features

- **Encrypted environment files (Laravel).** Run `php artisan env:encrypt`, commit the resulting `.env.encrypted`, and point `env_encrypted:` at it in `.neo.yml` — Neo decrypts it in memory at deploy, so secrets live in git while the key lives in your password manager. Per-environment files are supported (`env_encrypted: .env.staging.encrypted` inside an `environments:` block), and a bare `.env.encrypted` is picked up automatically when it is the only env source. All four Laravel ciphers decrypt (AES-128/256 in CBC or GCM).
- **Key lookup without prompts in CI.** `--env-key` > `NEO_ENV_KEY` > `LARAVEL_ENV_ENCRYPTION_KEY` > a key saved in `~/.neo/keys.json` (mode 0600) > an interactive prompt that offers to remember it. `LARAVEL_ENV_ENCRYPTION_KEY` is the same variable Laravel's own `env:decrypt` reads, so a pipeline that already exports it needs no Neo-specific setup.
- **`neo env encrypt` / `neo env decrypt` / `neo env key set|list|forget`** — for machines without PHP. The files they write are byte-compatible with `php artisan env:decrypt`.

### Changes

- **`neo deploy --all` now loads the file env sources declared in `.neo.yml`** (`env_encrypted` and `env_file`). It previously built the environment from the `env:` block alone and silently ignored both. Keys are resolved before the parallel fan-out, so a wrong key fails before anything is shipped and prompts can't interleave.

### Notes

- Encryption covers your repository and your laptop. Decrypted values are still sent to the server as container environment variables and stored in `/etc/neo/state.json` (root-only) so redeploys keep them. Saved keys sit in `~/.neo/keys.json` in plain text at mode 0600 — treat that file like an SSH key. Lose the key and the file cannot be recovered.

---

## v0.23.2 — 2026-08-14

### Fixes

- **HTTP basic auth works and stays on.** Three bugs fixed: (1) `${VAR}` references in `.neo.yml` — e.g. `basic_auth: password: ${NEO_BASIC_AUTH_PASSWORD}` — are now resolved at deploy instead of reaching Caddy as literal text; (2) basic auth is persisted to server state, so `neo domain` and `neo caddy update` no longer silently strip it from the live route; (3) the `--all` deploy path now honors environment-level `basic_auth`.
- **`env_file` long form parses correctly.** The modern `env_file: [{path: …, required: …}]` form was mangled into a garbage string, so those variables never loaded (in both `neo deploy` and `neo config generate`). It now handles the string, list-of-strings, and `{path, required}` forms.

### New Features

- **`${VAR:-default}` interpolation** in `.neo.yml`, and interpolation now runs during `neo deploy` (previously dev-only), covering the env map and `basic_auth`.
- **`dockerfile:` field in `.neo.yml`** — point Neo at a Dockerfile that isn't at the project root (e.g. `Dockerfile.local`) without passing `--dockerfile` on every deploy.
- **Better `neo config generate`** — records a custom `dockerfile:`, captures sidecar `command`s, and warns (instead of silently dropping) bind mounts and extra `env_file` entries when converting a large compose file.

### Docs

- Documented `neo service info <svc>` (host, port, user, password, URL) — the command existed but was missing from the docs.

---

## v0.22.0 — 2026-07-20

### New Features

- **`neo install` scaffolds a project instead of installing on the server** — `neo install <app>` (or the interactive picker) now asks for a folder and writes a ready-to-deploy `docker-compose.yml` (the template's app image + its bundled databases), a `.neo.yml`, and a `.env` with generated secrets. You then run `neo deploy` in that folder — and can edit the files first. (The install command previously wasn't wired up at all.)

### Changes

- **Dropped `neo connect`** — the one-time "transfer to Vxero" command was only a browser-redirect stub, so it's been removed from the CLI and the dashboard. The `internal/bridge/` package is retained but no longer wired to any command.

---

## v0.21.7 — 2026-07-17

### Changes

- **Release notes come from the CHANGELOG** — Each GitHub release now uses this file's matching `## v<version>` section as its body (falling back to auto-generated notes if no section is found), so the release page shows the real, curated changelog instead of a raw commit list.

---

## v0.21.6 — 2026-07-17

### New Features

- **Star prompt after `neo init`** — After a server is set up, neo asks if you'd like to star the project on GitHub and opens the repo in your browser (just prints the link in non-interactive shells). The default Caddy "server ready" page also carries a **★ Star us on GitHub** link.

---

## v0.21.5 — 2026-07-17

### New Features

- **`neo key add` picks the right server** — With no `--server` flag and more than one server configured, `neo key add` now asks which server to authorize the key on instead of silently using the current one. Pass `--server <name>` to skip the prompt.

- **Friendlier, stricter activation prompt** — The free-license email prompt now makes clear it's free ("we only use it to reach you about important updates"), rejects obvious throwaways like `x@abc.com` (validated on both the CLI and the server), re-prompts up to three times, and then lets you skip after a confirmation rather than trapping you.

- **`neo caddy update` — patch the reverse proxy** — Pulls the newest `caddy:2-alpine` and recreates the `neo-caddy` container so security fixes actually land on running servers (previously the Caddy image was only pulled once, at `neo init`, and never refreshed). Routes and TLS certificates are preserved through the persistent data/config volumes and `--resume`, so the only cost is a brief restart. If the proxy is a custom DNS-enabled build (from `neo caddy dns`), the image is rebuilt from the stored Dockerfile with a fresh base layer instead, and the DNS credentials env file is re-attached.

- **`neo firewall update` — keep CrowdSec current** — Upgrades the CrowdSec engine and nftables bouncer via the server's package manager (`apt`/`dnf`), refreshes the community hub content (`cscli hub update && cscli hub upgrade` — scenarios, parsers, blocklists), then restarts the services. Complements `neo firewall install`; no-ops with a clear message if CrowdSec isn't installed.

- **`neo destroy` — tear down a server** — Removes everything neo installed, at two levels. *Remove neo, keep data* deletes all neo containers (apps, workers, services, `neo-caddy`), the `neo` Docker network, and `/etc/neo`, leaving data volumes and Docker intact for a clean re-deploy. *Full wipe* also prunes data volumes and uninstalls CrowdSec and the Docker engine, returning the server close to its pre-`init` state. Requires typing the server host to confirm, then removes it from local config. Also available in the dashboard under **Servers → Destroy Server Setup**.

### Bug Fixes

- **License API errors surface cleanly** — The CLI now sends `Accept: application/json` on license requests, so a server-side validation error (e.g. a rejected email) comes back as JSON with the real message instead of an unparseable HTML redirect.

- **Default Caddy welcome page is no longer indexable** — The "server ready" catch-all page served for un-configured domains now carries `<meta name="robots" content="noindex,nofollow">` so search engines don't index bare server IPs. (Run `neo stealth` to remove the page entirely.)

- **`neo upgrade` now works for curl-installed binaries** — The self-updater wrote the new binary directly to the install path, which fails with "permission denied" when neo lives in a root-owned directory like `/usr/local/bin` (the default `curl | sh` location) — so upgrading silently failed and you had to re-run the install script. Neo now falls back to `sudo install` (prompting for your password) when the target isn't writable. It also pins the download to the exact version it just checked, so the binary can never mismatch the checksums served by a briefly-stale `version.json`.

- **Ctrl+C in a menu no longer corrupts the terminal** — Pressing Ctrl+C inside any interactive menu (the dashboard and every `ui.Select` prompt) called `os.Exit` while the terminal was still in raw mode, skipping the deferred restore. The shell was left with output line-wrapping broken (each line "staircasing" to the right) until you ran `reset`. Neo now restores cooked mode before exiting, and exits `130` (128 + SIGINT) as expected.

---

## v0.20.0 — 2026-07-07

### Breaking Changes

- **Neo is now free for everyone — and requires a free license.** The paid Neo+ tier is gone. Every feature is unlocked for all users, but you must activate a free license key before running commands. The first time you run any command (or run `neo activate`), Neo asks for your email and issues a free key instantly, then continues. In non-interactive contexts (CI, no TTY) it prints a clear "run `neo activate`" message instead. Set `NEO_DEV_PLUS=true` to bypass activation in local development.
  - Existing paid `plus`/`team` license keys are **grandfathered** — they still validate and keep working.
  - **Deploy order for self-hosters:** the CMS `/register` endpoint must be live before this CLI is rolled out, otherwise clients cannot activate.

### New Features

- **`neo activate` — one-step free activation** — `neo activate` with no argument prompts for your email and registers a free license (`POST /api/license/register`); `neo activate <key>` activates an existing key. One key works on **unlimited servers and unlimited devices**.

- **`neo config init` — scaffold a `.neo.yml`** — Creates a commented `.neo.yml` for projects without a `docker-compose.yml`. Prompts for name (defaults to the directory), domain (optional), and port (auto-detected from the Dockerfile `EXPOSE`, default 8080), then stubs `env`, `volumes`, `workers`, `sidecars`, `health`, `hooks`, and `environments` as commented examples. Use `--yes` to accept defaults non-interactively. Complements `neo config generate` (which builds from `docker-compose.yml`).

### Changes

- **No more feature gates.** Multi-server (previously capped at 1 on the free tier), backups (previously blocked), and device activations are now **unlimited** for everyone. Parallel image-upload streams are fixed at 3 for all users.

- **`neo plus` → `neo license`.** License management moved to `neo license` (`activate`/`status`/`deactivate`); `neo plus` remains as a hidden alias. All paid-tier upsell UI (pricing, upgrade prompts, expiry banners) has been removed, and the marketing site and docs are reframed around the free model.

### Bug Fixes

- **`neo init` as a non-root (sudo) user no longer fails with `init state: Process exited with status 1`.** State is stored in the root-owned `/etc/neo/`, but the state write used a plain SCP with no privilege escalation, so connecting as e.g. `ubuntu` hit "permission denied". Reading and writing `/etc/neo/state.json` now elevate with `sudo` when the SSH user isn't root (via `WriteFileElevated` / the new `ReadFileElevated`), so init, deploy, and every state-reading command work as any sudo-capable user.

---

## v0.19.0 — 2026-06-05

### New Features

- **`neo attach` — join a server someone else set up** — Registers an already-initialized server into your local config without re-running setup. Unlike `neo init`, it never installs Docker/Caddy and never overwrites the server's `/etc/neo/state.json`, so it is safe to run against a live server with apps deployed. It verifies the server is initialized (refusing a fresh server with a pointer to `neo init`), adds it to `~/.neo/config.json`, and deploys your neo key for passwordless access. Teammates now onboard in one step:
  ```
  neo key show                 # teammate prints their key; admin runs: neo key add "<key>"
  neo attach root@1.2.3.4       # teammate registers the server — dashboard + every command now work
  ```
  Also available from the dashboard: **Servers → Attach Existing Server**.

### Bug Fixes

- **Team Access docs corrected** — The `.neo.yml` `server:` field must be a full `user@host` (e.g. `root@1.2.3.4`), not a bare name. The `@` is what lets a teammate connect without the server being registered locally; a bare name failed with "no server selected". The docs site and CMS now spell this out and point to `neo attach` for the dashboard case.

- **Server requirements docs fixed** — The docs site listed only Ubuntu/Debian as supported and named Fedora/CentOS/RHEL as *unsupported*, contradicting the actual OS validation. Corrected to match the code: Ubuntu 24.04+, Debian, Fedora 39+, and CentOS / RHEL / AlmaLinux / Rocky 9+.

---

## v0.18.0 — 2026-06-03

### Bug Fixes

- **Old deploy images no longer pile up** — Image pruning was launched as a fire-and-forget goroutine after each deploy, so the CLI process exited and killed it before its SSH `docker rmi` calls ran. Every deploy left its predecessor's `neo-<app>:<timestamp>` image on disk and they accumulated indefinitely. Pruning now runs synchronously (it is best-effort and ignores errors, so it never fails a deploy) and reliably keeps the two most recent images per repository for instant rollback.

- **Sidecar images are pruned too** — `PruneImages` now also cleans up sidecar repositories (`neo-<app>-sidecar-*`), keeping the two most recent tags of each independently. Previously only the main app image was considered, so rebuilt sidecar images grew without bound.

- **neo-builder no longer fills the disk with old binaries** — The build service wrote a new `/output/<version>` directory on every build and never cleaned up, so compiled binaries accumulated indefinitely on the server. After each successful build it now keeps only the most recent versions per channel — staging (`-staging` versions) and production tracked separately — and removes the rest. The count is configurable via `NEO_KEEP_VERSIONS` (default `3`). Pruning is best-effort and never fails a build.

### New Features

- **Multiple wildcard certificate trees on one server** — `neo caddy dns` and `neo caddy ondemand` now **merge** their TLS automation policy into Caddy's existing config instead of replacing the whole `automation` block. Independent wildcard trees can coexist — e.g. `*.example.com` for production and `*.staging.example.com` for a staging environment on the same server — each getting its own free Let's Encrypt wildcard certificate. Policies are keyed by base domain, so re-running a command for the same domain is idempotent. Run once per tree:
  ```
  CLOUDFLARE_API_TOKEN=... neo --server prod caddy dns example.com
  CLOUDFLARE_API_TOKEN=... neo --server prod caddy dns staging.example.com
  ```
  Note: on-demand TLS still uses a single automation-level permission endpoint (a Caddy limitation), so DNS-01 is preferred when running independent trees across separate apps.

---

## v0.17.0 — 2026-06-01

### New Features

- **Wildcard HTTPS via ACME DNS-01 (`neo caddy dns`)** — Provisions a custom Caddy build with a DNS provider plugin, stores the API token securely on the server, and configures ACME DNS-01 automation for the base domain and its `*.` wildcard. Currently supports Cloudflare. Usage:
  ```
  CLOUDFLARE_API_TOKEN=... neo --server prod caddy dns example.com --provider cloudflare --app myapp
  ```

- **Guarded on-demand wildcard TLS (`neo caddy ondemand`)** — Enables dynamic tenant subdomains without pre-listing every hostname. Caddy issues a real Let's Encrypt certificate for each subdomain on first use, gated by an ask URL that your app controls. Usage:
  ```
  neo --server prod caddy ondemand example.com --app myapp --replace-domains
  ```

- **Cloudflare Flexible SSL support (`--cloudflare-flexible`)** — For apps behind Cloudflare's Flexible SSL mode (HTTPS at the edge, HTTP to origin): the new `--cloudflare-flexible` flag on `neo domain` sets the origin route to HTTP-only while injecting `X-Forwarded-Proto: https`, `X-Forwarded-Ssl: on`, and `X-Forwarded-Port: 443` headers so the app sees the correct scheme. Also available as `edge_https: true` in `.neo.yml`.

- **`--http-only` and `--https` flags for `neo domain`** — Switch an existing app's route mode without changing its domain:
  ```
  neo domain myapp --https
  neo domain myapp --http-only
  neo domain myapp --cloudflare-flexible
  ```

- **Wildcard domain support** — `neo domain` now accepts `*.example.com` as a valid domain. Deploys and domain changes with wildcard hostnames are guarded: they require DNS-01 or guarded on-demand TLS to be configured first, preventing silent Caddy failures.

- **Dev license bypass (`make build-dev`)** — Local development builds can now exercise Neo+ feature gates without a live license. Build with `make build-dev` (sets `DEV_LICENSE_BYPASS=true`) or export `NEO_DEV_PLUS=true` at runtime. Has no effect on standard `make build` output.

---

## v0.16.0 — 2026-04-20

### Bug Fixes

- **TUI "View Logs" no longer flashes and returns immediately** — Selecting "View Logs" from the app, worker, sidecar, or service action menus previously printed log output and then instantly re-rendered the menu before the user could read anything. All four log viewers now wait for a keypress before returning to the menu.

- **HTTPS works on first deploy without the HTTP→HTTPS toggle workaround** — Two related issues caused `ERR_SSL_PROTOCOL_ERROR` after a fresh deploy with HTTPS:
  1. `--temp` domains and auto-assigned `sslip.io` domains were set up as HTTP-only despite the flag description promising "auto-SSL". They now default to HTTPS on first deploy.
  2. The initial Caddy route for HTTPS deploys used `AddRoute` directly, which could leave the domain stuck in Caddy's `automatic_https.skip` list from a prior run. The first-deploy path now uses `UpdateRoute` / `UpdateRouteMulti`, which clears the skip list before adding the route — the same clean-state logic the HTTP→HTTPS toggle already used.

---

## v0.15.0 — 2026-04-15

### Bug Fixes

- **License cache no longer leaks across staging/production builds** — The license cache (`~/.neo/license.json`) now records which license server validated it (`validated_by` field). A staging binary's cache is rejected by a production binary and vice versa, preventing a staging license from appearing valid on a freshly installed production build. Offline grace period reduced from 7 days to 3 days.

---

## v0.14.0 — 2026-04-15

### Bug Fixes

- **"Restart with New Env" now applies `basic_auth` changes** — `basic_auth` is enforced at the Caddy proxy layer, not inside the container. Previously, adding or changing `basic_auth` in `.neo.yml` and clicking "Restart with New Env" (or running `neo deploy --env-only`) had no effect — the old Caddy route was left untouched. The env-only path now updates the Caddy route after restarting the container, picking up any changes to `basic_auth`, `https`, and domains from `.neo.yml`.

---

## v0.13.0 — 2026-04-15

### Improvements

- **Neo+ upgrade hints for free users** — Free-tier users now see clear, consistent prompts when they hit a feature gate or are exploring the dashboard.

  - **No-server dashboard** — The first screen new users see now includes a `★ Upgrade to Neo+` hint with the URL and activate command.
  - **Dashboard main menu** — The Neo+ menu entry shows `★ Upgrade to Neo+ · neo.vxero.dev` for free users instead of a faint "Free plan" label.
  - **Feature gates** — `neo backup` and adding a second server now show a consistent upgrade card:
    ```
    ✗ Backups require a Neo+ license

    ★  Upgrade to Neo+
       Unlimited servers, automated backups, and more.
       neo.vxero.dev

    Already have a key?  neo plus activate <key>
    ```

---

## v0.12.0 — 2026-04-15

### Improvements

- **Expired Neo+ license — stay open, just warn** — Previously, an expired Plus license silently downgraded the user to the free tier, blocking backups, multi-server access, and any other Plus-gated feature with no explanation. Now:

  - **All Plus features remain active** after expiry — nothing is blocked.
  - A warning banner is printed at the start of every command:
    ```
    ⚠  Your Neo+ license has expired
       Expired: 2026-04-01
       Updates are no longer included. Renew at neo.vxero.dev
       or email support@vxero.dev for support.
    ```
  - `neo plus status` shows `Plus (expired)` with a clear renewal CTA.
  - `neo plus` (interactive menu) routes expired users to a dedicated menu with Renew / Activate New Key / Deactivate options.

### Bug Fixes

- **License expiry detection was fragile** — `Check()` now correctly identifies an expired Plus license even when the API returns `valid: false`, by falling back to the cached `plan` and `expires` fields. Previously, any `valid: false` response was treated as "free tier", losing all context about which plan had expired.

---

## v0.11.2 — 2026-04-15

### Bug Fixes

- **Image upload failure on servers with small `/tmp`** — Parallel chunked uploads write all chunks to `/tmp` simultaneously. On servers where `/tmp` is a `tmpfs` (common on VPS providers — typically capped at 50% of RAM), a large image could exceed available space and cause `scp` to exit with status 1. Neo now falls back automatically to a single-stream transfer that pipes the image directly into `docker load` with no remote temp files. The actual `scp` error message is also now surfaced (previously swallowed as "Process exited with status 1").

---

## v0.11.1 — 2026-04-15

### Bug Fixes

- **Extra domains not persisted to state after deploy** — When an app had multiple domains (e.g. `domains: [vxero.dev, vxero.com]`), only the primary domain was written to `/etc/neo/state.json`. Extra domains were omitted, which caused two problems: (1) `neo redirect add <extra-domain>` would bypass the conflict check and create a redirect that Caddy silently ignored because the app route matched first; (2) `neo domain` commands operated with an incomplete picture of what Caddy was actually serving. Extra domains from both the `.neo.yml` config and manually-added domains are now always written to state after every deploy.

---

## v0.11.0 — 2026-04-14

### Improvements

- **`--parallel` flag for `neo deploy --all`** — Caps the number of concurrent SSH connections and `docker load` operations when deploying to multiple environments. Defaults to `3`, which is safe for most servers. Lower it for underpowered targets (1 GB RAM / 1 vCPU):

  ```bash
  neo deploy --all                    # default: 3 concurrent deploys
  neo deploy --all --parallel 1       # serial — safest for small servers
  neo deploy --all --parallel 5       # max throughput for beefy servers
  ```

  Previously, `--all` opened one SSH connection per environment simultaneously with no cap, which could OOM small servers during the `docker load` decompression spike.

---

## v0.10.0 — 2026-04-14

### New Features

- **`neo prune`** — Remove old Docker images from the server to free up disk space. Shows a preview table of what will be kept vs removed per app, then asks for confirmation before deleting.

  ```bash
  neo prune              # keep 2 most recent images per app (default)
  neo prune --keep 1     # keep only the current image
  neo prune --dry-run    # preview without making changes
  neo prune --force      # skip confirmation prompt
  ```

  Running containers are never affected — Docker skips images still in use and the summary reports how many were skipped.

### Bug Fixes

- **Image pruning after deploy** — Fixed a silent bug where `docker rmi` by image ID would fail when multiple tags share the same layer digest. Old images are now removed by tag, which correctly handles all cases.

---

## v0.9.0 — 2026-04-13

### New Features

- **Domain redirects** — Redirect any domain to another URL without deploying an app, sidecar, or service. Powered by Caddy's native redirect handler — auto-SSL is provisioned for the source domain automatically. Request paths are preserved (`vxero.dev/blog` → `vxero.com/blog`).

  ```bash
  neo redirect add vxero.dev vxero.com          # 301 permanent (default)
  neo redirect add old.api.com new.api.com --temporary  # 302 temporary
  neo redirect list                              # show all redirects
  neo redirect remove vxero.dev                 # remove a redirect
  ```

---

## v0.8.0 — 2026-04-13

### Improvements

- **Automatic SSH key discovery** — `neo init` now scans all private key files in `~/.ssh/` (not just `id_ed25519` and `id_rsa`). Cloud provider keys at non-standard paths (e.g. `~/.ssh/do_rsa`, `~/.ssh/hetzner_key`) are tried automatically — no extra steps needed for most fresh VPS setups.

- **Actionable "HOST KEY HAS CHANGED" error** — When neo detects a changed host key (common after server rebuilds or IP reuse), the error now includes the exact fix command:
  ```
  Fix: ssh-keygen -R <ip>
  Then run neo init again
  ```

- **`--key` flag hint on auth failure** — If all SSH key attempts fail, `neo init` now shows a clear tip suggesting `neo init --key ~/.ssh/your_key root@<ip>` instead of a bare error message.

---

## v0.7.0 — 2026-04-13

### Improvements

- **Environment config validation** — When `environments:` are defined, root-level `server:` and `domains:` are now blocked with a clear error and migration instructions. Previously they were silently ignored, which could cause deploys to go to the wrong server.

- **Every environment must declare `server:`** — Neo errors early if any environment is missing a `server:`, regardless of how many environments are defined.

- **`--all` now works correctly** — Moving `server:/domains:` into each environment means `neo deploy --all` deploys every environment (e.g. both `production` and `staging`) as intended.

### Migration

If your `.neo.yml` has `environments:` defined, move `server:` and `domains:` out of the root and into each environment:

```yaml
# Before
server: my-server
domains:
  - app.example.com
environments:
  staging:
    domains:
      - staging.example.com

# After
environments:
  production:
    server: my-server
    domains:
      - app.example.com
  staging:
    server: my-server
    domains:
      - staging.example.com
```

Root-level `env:`, `workers:`, and `volumes:` remain shared across all environments.

---

## v0.6.0 — 2026-04-13

### Improvements

- **Environment server validation** — When a `.neo.yml` defines multiple environments, every environment must now explicitly declare a `server:`. Neo errors early with a clear message instead of silently falling back to the top-level server, which could cause accidental deploys to the wrong target.

---

## v0.5.0 — 2026-04-13

### New Features

- **Team SSH key management** — Share server access with teammates in seconds, no GitHub or manual SSH required.

  ```bash
  neo key show              # generate + print your public key to share
  neo key add "<pubkey>"    # authorize a teammate on the server
  neo key list              # see all authorized keys (marks your own)
  neo key remove <number>   # revoke access by number
  ```

  **Workflow:** Teammate runs `neo key show`, sends you the one-line key. You run `neo key add "<key>"`. They add `server: root@your-ip` to their `.neo.yml` and can deploy immediately with their own neo key. No key files to copy, no passwords to share.

---

## v0.4.0 — 2026-04-13

### New Features

- **Server groups** — Deploy one environment to multiple servers in parallel using `servers: [server-a, server-b, server-c]` in `.neo.yml`. Supports web clusters, queue worker fleets, and mixed topologies from a single config file.

  ```yaml
  environments:
    web:
      servers: [velvet-134, web-sg2, web-sg3]
    queue:
      servers: [queue-sg1, queue-sg2, queue-sg3]
    scheduler:
      server: schedule-sg1
  ```

- **Per-server deploy targeting** — Deploy to a single server within a group using `neo deploy --env web --server velvet-134`, without affecting the other servers in the group.

- **TUI server group support** — The interactive dashboard now prompts for environment and then "All servers in group" or a specific server when a server group is configured.

---

## v0.3.0 — 2026-04-13

### New Features

- **Horizontal scaling** — Set `scale: N` in `.neo.yml` to run multiple app replicas. Caddy automatically load-balances across them. Zero-downtime redeploy and scale changes (1→N, N→M) are fully supported. Lifecycle commands (`start`, `stop`, `restart`, `remove`) operate on the full replica set.

- **WebSocket / WSS support** — Caddy's reverse proxy transparently handles WebSocket upgrades, including `wss://` with auto-SSL. No configuration required.

- **Opt-in HTTP health check** — Add a `health.path` to `.neo.yml` to run an HTTP health check before switching Caddy traffic. If the check fails, the old container keeps serving (true zero-downtime rollback).

- **SSH tunnel command** — `neo tunnel` opens SSH tunnels to remote services for local tools like TablePlus and DataGrip.

- **Interactive DB browser** — `neo db <app>` now supports table data browsing (Enter = `SELECT *`, `d` = `DESCRIBE`).

- **HTTP Basic Auth** — Protect apps at the proxy layer via `basic_auth:` in `.neo.yml`. Supports path bypass rules (`bypass: [/api/*, /webhooks/*]`).

- **Shared services** — Multiple apps can share a single MySQL, Postgres, Redis, or MariaDB instance to save RAM on small VMs (`neo service create/link/unlink`).

### Improvements

- **Image retention** — After each deploy, neo keeps the current image plus the previous one on the server for instant rollback. Older images are pruned automatically.

- **SHA256 checksums** — neo-builder now computes per-binary SHA256 checksums for all release artifacts.

- **Windows/ARM64 support** — Added `windows/arm64` build target to neo-builder.

- **Broader OS install support** — Install script now handles `i686` arch (32-bit Git Bash on Windows).

### Bug Fixes

- Fixed DB browser panic when switching between queries with different column counts.
- Fixed DB browser for shared services (uses `DefaultDB`, prefers app user).
- Fixed 21 security vulnerabilities across the codebase.
- Fixed staging build URL injection for neo-builder.
