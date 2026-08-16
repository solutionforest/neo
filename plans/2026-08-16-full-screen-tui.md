# Full-Screen TUI for Neo (proposal — needs team review)

**Status:** proposal. Nothing here is implemented. Targeting **v0.25.0** as a
separate, opt-in surface — the current prompt-based dashboard stays until this
one is at parity.

**Goal:** replace the scrolling prompt chain with a persistent full-screen
terminal application — the k9s / lazygit / btop shape — so the operator sees
servers, apps, services and logs in one place, without every action reprinting
the whole menu.

**Tech stack:** `charmbracelet/bubbletea` (event loop) + `bubbles` (table,
viewport, textinput, spinner) + existing `lipgloss` styles. All server access
keeps going through `internal/ssh`, `internal/remote` and `internal/state` — no
new transport.

---

## Why (what's wrong with what we have)

The dashboard today is a loop of `huh` prompts printed to the scrollback:

- **No persistent frame.** Every action reprints the menu below the last one.
  After ten actions the terminal is a wall of repeated menus and you cannot see
  what happened three steps ago without scrolling past six copies of the menu.
- **One thing at a time.** Watching logs means leaving the app list. There is no
  way to see an app's status while its logs stream.
- **Blocking work freezes the screen.** SSH round-trips happen inline; the whole
  UI is dead until they return. Only the initial cache refresh is backgrounded.
- **No live refresh.** Counts update when you act, not when the server changes.
  The cache age ("3m ago") exists precisely because the screen is stale.
- **Errors cost navigation.** Fixed in v0.24.2 — errors no longer bounce you to
  the shell — but the fix is a workaround for a model where every screen is a
  function call on a stack rather than a view you can return to.

## What it should look like

```
┌ neo ─ production ● 159.65.100.42 ────────────────── v0.25.0 ─┐
│ [1] Apps  [2] Services  [3] Servers  [4] Logs  [5] Metrics   │
├──────────────────────────────┬───────────────────────────────┤
│ NAME        STATUS   DOMAIN  │ shop                          │
│ ● shop      running  shop.io │ image  neo-shop:a1b2c3        │
│ ● api       running  api.io  │ port   8080 → 443             │
│ ○ worker    stopped  —       │ env    24 vars                │
│ ⚠ legacy    untracked        │ vols   2 (shop-data, uploads) │
│                              │ health ok · 12ms              │
├──────────────────────────────┴───────────────────────────────┤
│ 14:02:11 shop  GET /health 200                               │
│ 14:02:12 api   GET /v1/orders 200                            │
├──────────────────────────────────────────────────────────────┤
│ ↑↓ move  enter details  l logs  r restart  d deploy  ? help   │
└──────────────────────────────────────────────────────────────┘
```

Master list on the left, detail on the right, streaming log pane at the bottom,
a status bar that always shows the current server and reachability, and a key
hint line that changes with context.

## Design rules

1. **One frame, always.** Alt-screen buffer. Nothing scrolls the app away. On
   exit, restore the terminal exactly as found (this is what "don't always need
   to reset" means in practice).
2. **Never block the event loop.** Every SSH call is a `tea.Cmd` returning a
   message. The UI stays responsive with a spinner in the affected pane.
3. **Every screen is reachable and leaveable.** `esc` goes up one level, `q`
   quits from the top, `?` shows contextual help. No dead ends, no screen that
   can only be exited by restarting.
4. **Errors are content, not control flow.** A failed action renders in the pane
   it belongs to. It never unwinds the view stack.
5. **State is one model.** A single `Model` holds servers, apps, services and
   fetch status. Views render from it; they never fetch on their own.
6. **Degrade, don't crash.** No TTY, `NO_COLOR`, tiny window, non-UTF8 terminal:
   fall back to the existing prompt flow rather than rendering garbage.

## Scope

**In:**

- App list + detail, service list + detail, server list + switch
- Streaming logs pane (follow, filter, pause)
- Actions: start / stop / restart / redeploy / remove, with confirmation
- Live refresh on a timer, with a visible "last updated"
- Drift indicator: apps running but missing from state (see v0.24.2)

**Out (stays as today's commands):** `neo init`, `neo deploy` build output,
`neo ssh`, database browser, license activation. These are long-running or
inherently full-terminal; the TUI shells out and returns.

## Migration

1. `neo ui` launches the new interface. The bare `neo` keeps today's behaviour.
2. Both ship for one minor version. Collect feedback.
3. When at parity, bare `neo` switches to the new UI; `neo --classic` keeps the
   old one for a release.
4. Remove the old dashboard only after a release with no blocking reports.

## Testing (the part that needs to be real before we merge)

Today `commands/dashboard.go` has **no tests** — it is prompt-driven and not
callable from a test. That is the main reason a rewrite is attractive: a
Bubble Tea model is a pure function of messages, so it is testable.

- **Model unit tests.** `Update(msg)` against a table of messages: key presses,
  fetch results, errors. Assert on resulting model state, not on rendered bytes.
- **Golden-frame tests.** `View()` output for a fixed model and terminal size,
  compared against checked-in golden files. Catches layout regressions.
- **Navigation invariants.** Property-style: from any screen, `esc` eventually
  reaches the top; no key sequence produces a screen with no exit. This is the
  automated form of "make sure every screen is valid".
- **Race.** `make test-race` — the model is driven from one goroutine but fetch
  commands land from many.
- **Terminal matrix.** Manual pass on iTerm2, Terminal.app, Windows Terminal,
  and inside `tmux`, at 80x24 and at full screen.

Gate: no merge until the model has tests, golden frames exist for every screen,
and `make test-race` is clean.

## Risks

- **Scope.** This is a rewrite of the interactive surface. Keeping the old path
  alive during migration is what makes it safe; skipping that is what makes it
  dangerous.
- **SSH latency.** Every pane wants fresh data. Needs one shared connection per
  server and a fetch scheduler, or it will hammer the box.
- **Windows.** Alt-screen and key handling differ. Must be tested, not assumed.
- **Binary size / deps.** Bubble Tea and bubbles are already transitively
  present via huh; the delta should be small but should be measured.

## Open questions for the team

1. `neo ui` as the entry point, or a `--tui` flag on the bare command?
2. Do we keep the prompt dashboard permanently as the low-bandwidth fallback
   (SSH from a phone, CI shells), or remove it after migration?
3. Live refresh interval — fixed, adaptive, or manual only? Each SSH poll costs
   a round trip against every configured server.
4. Is the log pane per-app only, or a merged multi-app tail?
5. Does this ship alongside a `neo doctor` that surfaces the drift checks added
   in v0.24.2, or does the TUI subsume that?
