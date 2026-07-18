# Neo Desktop visual sign-off

This checklist is the repeatable visual and accessibility review for the Stage
10 interface refinement. Browser review uses the deterministic fixture API;
production Tauri builds continue to use the Go bridge.

## Fixture routes

- Tray, healthy/warning: `http://127.0.0.1:1420/`
- Tray, offline/critical: `http://127.0.0.1:1420/?server=edge`
- Management, healthy/warning: `http://127.0.0.1:1420/management.html`
- Management, offline/critical: `http://127.0.0.1:1420/management.html?server=edge`

Run `npm run dev -- --host 127.0.0.1` from `apps/desktop` before opening the
routes. The optional `server` query is only consumed by the fixture provider.

## Completed review matrix

- [x] Tray at 380 × 560 in light and dark appearance: no horizontal overflow;
      status, server, metrics, findings, recent logs, and primary actions remain
      visible.
- [x] Management at the minimum 960 × 680 size and at 1440 × 900: tables scroll
      within their panel and the grid reflows without clipping.
- [x] Healthy/warning and offline/critical fixtures: text, shape, and iconography
      communicate state without requiring color perception.
- [x] Action confirmation and successful result: target, action, impact, progress,
      outcome, and changes remain explicit; focus starts and stays in the dialog.
- [x] Empty workloads and no-server state: an explanation and next step replace
      blank panels.
- [x] Keyboard: visible focus ring, modal Escape behavior, modal focus loop,
      native selector navigation, scrollable workload table, and focusable logs.
- [x] Screen reader: named status regions, semantic meters, labelled server/log
      controls, live log/action status, table headers, and modal name/description.
- [x] Reduced motion: transforms and animations collapse to effectively static
      feedback while state/color changes remain.
- [x] Reduced transparency: glass materials become opaque and blur is removed.
- [x] Increased contrast: surfaces and controls gain stronger, two-pixel edges;
      status shapes retain their labels.
- [x] Text scaling: system fonts and relative typography are used; the management
      toolbar and metric grid reflow at narrower/scaled layouts.

## Intentionally deferred

- The tray's existing **Settings…** item still opens the management window. A
  dedicated preferences screen remains deferred until the desktop preferences
  transport exists; this visual slice does not introduce a second settings store.
- Final native Windows rendering is covered by the existing Windows build/check
  workflow and still needs the release team's signed-installer matrix. No
  platform-specific CSS or security capability was introduced here.
