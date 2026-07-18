// Host actions bridge tray/window lifecycle to the Tauri Rust layer. Outside
// Tauri (tests, browser dev) they degrade gracefully instead of throwing, so
// the UI stays exercisable without the native shell.

import { isTauri } from "./desktop-api";
import type { AggregateStatus, DesktopNotification } from "./protocol";
import type { TrayDetail } from "./desktop-service";

async function tryInvoke(command: string, args?: Record<string, unknown>): Promise<void> {
  if (!isTauri()) return;
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    await invoke(command, args);
  } catch (err) {
    // Surface to the console for diagnostics; never crash the UI on a
    // best-effort window action.
    console.error(`host command "${command}" failed`, err);
  }
}

/**
 * Persist an exported diagnostic bundle to disk via the trusted shell, returning
 * the file path it was written to. Unlike the fire-and-forget window actions
 * above this must surface success/failure, so it does not swallow errors. The
 * body is already redacted by the frontend (see diagnostic-bundle.ts); Rust only
 * chooses the directory and writes the bytes. Outside Tauri it returns null so
 * browser dev/tests exercise the flow without a native filesystem.
 */
export async function exportDiagnosticBundle(
  filename: string,
  content: string,
): Promise<string | null> {
  if (!isTauri()) return null;
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke<string>("export_diagnostic_bundle", { filename, content });
}

/** Open (and focus) the larger management window. */
export function openManagementWindow(): Promise<void> {
  return tryInvoke("open_management_window");
}

/** Hide the tray popover without quitting the process. */
export function hidePopover(): Promise<void> {
  return tryInvoke("hide_popover");
}

/** Quit the entire desktop application. */
export function quitApp(): Promise<void> {
  return tryInvoke("quit_app");
}

/**
 * Push the aggregate tray state to the native shell, which swaps the tray icon
 * (shape/badge, not color alone) and tooltip. A no-op outside Tauri.
 */
export function setTrayState(state: AggregateStatus, detail: TrayDetail): Promise<void> {
  return tryInvoke("set_tray_state", {
    state,
    summary: detail.summary,
    reachable: detail.reachable,
    total: detail.total,
  });
}

/** Deliver a native OS notification for a transition. A no-op outside Tauri. */
export function notify(note: DesktopNotification): Promise<void> {
  return tryInvoke("notify", { title: note.title, body: note.body });
}
