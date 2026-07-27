mod pty;

use tauri::{
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};
use tauri_plugin_positioner::{Position, WindowExt};
use tauri_plugin_shell::ShellExt;

// Proxy a request to the bundled neo-bridge sidecar. The frontend never gets
// generic shell access — only this typed command, which runs a fixed binary.
#[tauri::command]
async fn bridge(
    app: tauri::AppHandle,
    method: String,
    params: Option<String>,
) -> Result<String, String> {
    let mut args = vec![method];
    if let Some(p) = params {
        if !p.trim().is_empty() {
            args.push(p);
        }
    }
    let output = app
        .shell()
        .sidecar("neo-bridge")
        .map_err(|e| e.to_string())?
        .args(args)
        .output()
        .await
        .map_err(|e| e.to_string())?;

    if !output.status.success() {
        return Err(String::from_utf8_lossy(&output.stderr).to_string());
    }
    Ok(String::from_utf8_lossy(&output.stdout).to_string())
}

#[tauri::command]
fn open_dashboard(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.set_focus();
    }
}

#[tauri::command]
fn hide_popover(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("popover") {
        let _ = w.hide();
    }
}

#[tauri::command]
fn quit_app(app: tauri::AppHandle) {
    app.exit(0);
}

// Position the popover just below the tray icon, then show it.
fn show_popover(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("popover") {
        let _ = w.move_window(Position::TrayBottomCenter);
        let _ = w.show();
        let _ = w.set_focus();
    }
}

fn toggle_popover(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("popover") {
        if w.is_visible().unwrap_or(false) {
            let _ = w.hide();
        } else {
            show_popover(app);
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_popover(app);
        }))
        .plugin(tauri_plugin_positioner::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![
            bridge,
            open_dashboard,
            hide_popover,
            quit_app,
            pty::pty_spawn,
            pty::pty_write,
            pty::pty_resize,
            pty::pty_kill
        ])
        .setup(|app| {
            pty::init(app);

            // macOS: menu-bar app, no dock icon.
            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            // No menu — a click opens the popover directly (Quit / Open Dashboard
            // live inside the popover). Anchor the popover under the tray icon.
            let tray_icon = tauri::image::Image::from_bytes(include_bytes!("../icons/tray.png"))
                .expect("valid tray icon");
            TrayIconBuilder::with_id("neo-tray")
                .icon(tray_icon)
                .icon_as_template(true)
                .tooltip("Neo Desktop")
                .on_tray_icon_event(|tray, event| {
                    tauri_plugin_positioner::on_tray_event(tray.app_handle(), &event);
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        toggle_popover(tray.app_handle());
                    }
                })
                .build(app)?;

            Ok(())
        })
        .on_window_event(|window, event| match event {
            // Closing a window hides it — the tray process keeps running.
            WindowEvent::CloseRequested { api, .. } => {
                let _ = window.hide();
                api.prevent_close();
            }
            // Dismiss the popover when it loses focus (click outside), like a menu.
            WindowEvent::Focused(false) if window.label() == "popover" => {
                let _ = window.hide();
            }
            _ => {}
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
