//! Tray icon, menu, and popover toggle behavior.

use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Manager, Runtime,
};

/// Build the tray icon with its menu and click behavior.
pub fn create_tray<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<()> {
    let open_i = MenuItem::with_id(app, "open", "Open Neo", true, None::<&str>)?;
    let dashboard_i =
        MenuItem::with_id(app, "dashboard", "Open Dashboard", true, None::<&str>)?;
    let settings_i = MenuItem::with_id(app, "settings", "Settings…", true, None::<&str>)?;
    let quit_i = MenuItem::with_id(app, "quit", "Quit Neo Desktop", true, None::<&str>)?;
    let sep = PredefinedMenuItem::separator(app)?;
    let menu = Menu::with_items(
        app,
        &[&open_i, &dashboard_i, &sep, &settings_i, &quit_i],
    )?;

    let mut builder = TrayIconBuilder::with_id("neo-tray")
        .tooltip("Neo Desktop")
        .menu(&menu)
        // Left click toggles the popover; the menu is reserved for right click.
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "open" => show_popover(app),
            // Settings has no dedicated UI yet; route it to the management
            // window until slice-later work adds a preferences pane.
            "dashboard" | "settings" => {
                let _ = crate::commands::show_management(app);
            }
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                toggle_popover(tray.app_handle());
            }
        });

    // Use the app icon; render it as a macOS template so it follows the
    // light/dark menu bar. On Windows the colored icon is used as-is.
    if let Some(icon) = app.default_window_icon().cloned() {
        builder = builder
            .icon(icon)
            .icon_as_template(cfg!(target_os = "macos"));
    }

    builder.build(app)?;
    Ok(())
}

/// Show and focus the popover.
pub fn show_popover<R: Runtime>(app: &AppHandle<R>) {
    if let Some(win) = app.get_webview_window("popover") {
        let _ = win.show();
        let _ = win.set_focus();
    }
}

/// Toggle popover visibility.
pub fn toggle_popover<R: Runtime>(app: &AppHandle<R>) {
    if let Some(win) = app.get_webview_window("popover") {
        if win.is_visible().unwrap_or(false) {
            let _ = win.hide();
        } else {
            let _ = win.show();
            let _ = win.set_focus();
        }
    }
}
