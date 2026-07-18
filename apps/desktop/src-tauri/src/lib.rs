//! Neo Desktop — thin Tauri shell.
//!
//! Rust owns only OS integration: the tray icon/menu, the popover and
//! management windows, single-instance behavior, and (from slice 2) supervising
//! the `neo-bridge` sidecar. All Neo behavior lives in Go. See
//! `plans/2026-07-18-neo-desktop-tray-application.md`.

pub mod bridge;
mod commands;
mod tray;

use std::sync::Arc;

use tauri::{Manager, RunEvent, WindowEvent};

use bridge::BridgeManager;

pub fn run() {
    let mut builder = tauri::Builder::default();

    // single-instance MUST be registered first so a second launch is collapsed
    // into the running process before any window work happens (desktop only).
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            // Second launch: surface the existing popover instead of starting anew.
            tray::show_popover(app);
        }));
    }

    let app = builder
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None::<Vec<&str>>,
        ))
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        // The shell plugin is registered so Rust can spawn the bundled sidecar.
        // No shell/sidecar permission is granted to the webview (see
        // capabilities/default.json) — spawning happens only in bridge.rs.
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            commands::open_management_window,
            commands::hide_popover,
            commands::quit_app,
            commands::set_tray_state,
            commands::notify,
            bridge::bridge_hello,
            bridge::server_list,
            bridge::server_snapshot,
            bridge::app_list,
            bridge::diagnostics_run,
            bridge::app_action,
        ])
        .setup(|app| {
            // macOS: behave as a menu-bar accessory (no dock icon) until a full
            // management window is opened. Slice-1 approximation of the plan's
            // "no dock icon when only the popover is open" requirement.
            #[cfg(target_os = "macos")]
            let _ = app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            // Start supervising the single neo-bridge sidecar. The manager is
            // shared state so the typed commands can issue requests.
            let manager = Arc::new(BridgeManager::new(app.handle().clone()));
            app.manage(manager.clone());
            manager.start();

            tray::create_tray(app.handle())?;

            // Closing either window hides it — the tray process keeps running.
            // Quit happens only via the tray menu / quit_app command.
            for label in ["popover", "management"] {
                if let Some(window) = app.get_webview_window(label) {
                    let win = window.clone();
                    let label = label.to_string();
                    window.on_window_event(move |event| {
                        if let WindowEvent::CloseRequested { api, .. } = event {
                            api.prevent_close();
                            let _ = win.hide();
                            // Dropping back to menu-bar-only when the big window
                            // is dismissed keeps the dock clean on macOS.
                            #[cfg(target_os = "macos")]
                            if label == "management" {
                                let _ = win
                                    .app_handle()
                                    .set_activation_policy(tauri::ActivationPolicy::Accessory);
                            }
                            let _ = &label;
                        }
                    });
                }
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Neo Desktop");

    // Terminate the bridge child when the desktop process exits so no sidecar is
    // left orphaned.
    app.run(|app_handle, event| {
        if let RunEvent::Exit = event {
            if let Some(manager) = app_handle.try_state::<Arc<BridgeManager>>() {
                manager.shutdown();
            }
        }
    });
}
