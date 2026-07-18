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

/// Reflect the aggregate health state on the tray (tooltip + macOS badge). The
/// desktop application service (the popover window) is the single caller, so the
/// tray never receives conflicting updates from multiple windows.
#[tauri::command]
pub fn set_tray_state<R: Runtime>(
    app: AppHandle<R>,
    state: String,
    summary: String,
    reachable: u32,
    total: u32,
) -> Result<(), String> {
    // reachable/total are accepted for a future "n/m reachable" badge; the
    // tooltip already carries them in `summary`.
    let _ = (reachable, total);
    crate::tray::apply_tray_state(&app, &state, &summary);
    Ok(())
}

/// Deliver a native OS notification for a transition detected by the service.
/// The bridge never has notification permission; delivery happens only here in
/// the trusted shell.
#[tauri::command]
pub fn notify<R: Runtime>(app: AppHandle<R>, title: String, body: String) -> Result<(), String> {
    use tauri_plugin_notification::NotificationExt;
    app.notification()
        .builder()
        .title(title)
        .body(body)
        .show()
        .map_err(|e| e.to_string())
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
