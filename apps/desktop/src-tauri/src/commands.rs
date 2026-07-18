//! Window/lifecycle commands exposed to the webview. This module deliberately
//! contains no generic shell or process command. The bridge-facing commands
//! (`bridge_hello`, `server_list`, …) live in `bridge.rs`; each forwards one
//! allowlisted, versioned method to the `neo-bridge` sidecar.

use tauri::{AppHandle, Manager, Runtime};

/// Open (and focus) the larger management window.
#[tauri::command]
pub fn open_management_window<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    show_management(&app)
}

/// Hide the tray popover without quitting the process.
#[tauri::command]
pub fn hide_popover<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    if let Some(win) = app.get_webview_window("popover") {
        win.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Quit the entire desktop application.
#[tauri::command]
pub fn quit_app<R: Runtime>(app: AppHandle<R>) {
    app.exit(0);
}

/// Shared helper used by both the command and the tray menu.
pub fn show_management<R: Runtime>(app: &AppHandle<R>) -> Result<(), String> {
    // A full window means the app should show in the dock/taskbar on macOS.
    #[cfg(target_os = "macos")]
    let _ = app.set_activation_policy(tauri::ActivationPolicy::Regular);

    let win = app
        .get_webview_window("management")
        .ok_or_else(|| "management window is not available".to_string())?;
    win.show().map_err(|e| e.to_string())?;
    win.set_focus().map_err(|e| e.to_string())?;
    Ok(())
}
