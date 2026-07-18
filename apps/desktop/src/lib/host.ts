// Host actions bridge tray/window lifecycle to the Tauri Rust layer. Outside
// Tauri (tests, browser dev) they degrade gracefully instead of throwing, so
// the UI stays exercisable without the native shell.

import { isTauri } from "./desktop-api";

async function tryInvoke(command: string): Promise<void> {
  if (!isTauri()) return;
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    await invoke(command);
  } catch (err) {
    // Surface to the console for diagnostics; never crash the UI on a
    // best-effort window action.
    console.error(`host command "${command}" failed`, err);
  }
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
