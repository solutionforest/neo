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

    let mut builder = TrayIconBuilder::with_id(TRAY_ID)
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

/// The tray icon's registered id, shared between construction and later lookups.
const TRAY_ID: &str = "neo-tray";

/// Reflect the aggregate health state on the tray: update the tooltip and, on
/// macOS, a monochrome badge glyph beside the template icon. macOS template
/// icons follow the menu-bar appearance and carry NO color, so the plan's
/// requirement that "shape or badge (not color alone) distinguishes critical vs
/// unknown" is met with a distinct glyph per non-healthy state. On Windows/Linux
/// the colored icon plus the tooltip convey the state (the title API is
/// macOS-only).
pub fn apply_tray_state<R: Runtime>(app: &AppHandle<R>, state: &str, summary: &str) {
    let Some(tray) = app.tray_by_id(TRAY_ID) else {
        return;
    };
    let _ = tray.set_tooltip(Some(summary));

    let badge = state_badge(state);
    #[cfg(target_os = "macos")]
    {
        let _ = tray.set_title(badge);
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = badge;
    }
}

/// Map an aggregate state to its menu-bar badge glyph. Healthy shows no badge;
/// every other state gets a distinct shape so critical and unknown are never
/// told apart by color alone. Kept as a pure function so it is unit-testable
/// without a running tray.
fn state_badge(state: &str) -> Option<&'static str> {
    match state {
        "critical" => Some("!"),
        "warning" => Some("△"),
        "unknown" => Some("…"),
        _ => None, // healthy
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

#[cfg(test)]
mod tests {
    use super::state_badge;

    #[test]
    fn healthy_has_no_badge() {
        assert_eq!(state_badge("healthy"), None);
    }

    #[test]
    fn non_healthy_states_have_distinct_shapes() {
        // Each state's glyph must be unique so critical and unknown are never
        // distinguishable by color alone (they carry none on a template icon).
        let crit = state_badge("critical");
        let warn = state_badge("warning");
        let unknown = state_badge("unknown");
        assert!(crit.is_some() && warn.is_some() && unknown.is_some());
        assert_ne!(crit, unknown);
        assert_ne!(crit, warn);
        assert_ne!(warn, unknown);
    }
}
