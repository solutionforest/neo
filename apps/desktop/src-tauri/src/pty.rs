//! Integrated terminals. Each session runs the bundled `neo-bridge pty …`,
//! which opens a real remote PTY over neo's own SSH auth.
//!
//! We spawn it with std::process::Command (NOT tauri-plugin-shell, NOT a PTY
//! crate): with no pre_exec hook, std uses posix_spawn on macOS, so there's no
//! fork inside this multithreaded WebKit process (which modern macOS aborts).
//! We read the child's stdout as RAW BYTES — the shell plugin line-buffers,
//! which breaks a terminal (no per-keystroke echo; prompts without a trailing
//! newline like `$ ` or `password:` never appear).
use std::collections::HashMap;
use std::io::{Read, Write};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::Mutex;

use tauri::{Emitter, Manager};

struct Session {
    child: Child,
    stdin: ChildStdin,
}

#[derive(Default)]
pub struct PtyState {
    sessions: Mutex<HashMap<String, Session>>,
}

/// Locate the bundled neo-bridge next to our executable (Tauri copies the
/// externalBin there without the target-triple suffix).
fn bridge_path() -> Option<std::path::PathBuf> {
    let dir = std::env::current_exe().ok()?.parent()?.to_path_buf();
    for name in [
        "neo-bridge",
        "neo-bridge-aarch64-apple-darwin",
        "neo-bridge-x86_64-apple-darwin",
    ] {
        let cand = dir.join(name);
        if cand.exists() {
            return Some(cand);
        }
    }
    None
}

#[tauri::command]
pub fn pty_spawn(
    app: tauri::AppHandle,
    id: String,
    server: String,
    container: Option<String>,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    if let Some(mut old) = app.state::<PtyState>().sessions.lock().unwrap().remove(&id) {
        let _ = old.child.kill();
    }

    let bridge = bridge_path().ok_or_else(|| "neo-bridge not found".to_string())?;
    let container_arg = container
        .filter(|c| !c.is_empty())
        .unwrap_or_else(|| "-".to_string());

    let mut child = Command::new(&bridge)
        .arg("pty")
        .arg(&server)
        .arg(&container_arg)
        .arg(cols.max(1).to_string())
        .arg(rows.max(1).to_string())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;

    let stdout = child.stdout.take().ok_or("no stdout")?;
    let stderr = child.stderr.take().ok_or("no stderr")?;
    let stdin = child.stdin.take().ok_or("no stdin")?;

    let data_ev = format!("pty://data/{id}");
    let exit_ev = format!("pty://exit/{id}");

    let app_out = app.clone();
    let ev_out = data_ev.clone();
    std::thread::spawn(move || pump(stdout, &app_out, &ev_out));

    let app_err = app.clone();
    let ev_err = data_ev.clone();
    std::thread::spawn(move || pump(stderr, &app_err, &ev_err));

    app.state::<PtyState>()
        .sessions
        .lock()
        .unwrap()
        .insert(id.clone(), Session { child, stdin });

    // Reap the child and notify the frontend when it exits.
    let app_reap = app.clone();
    std::thread::spawn(move || loop {
        std::thread::sleep(std::time::Duration::from_millis(300));
        let state = app_reap.state::<PtyState>();
        let mut guard = state.sessions.lock().unwrap();
        match guard.get_mut(&id) {
            Some(sess) => match sess.child.try_wait() {
                Ok(Some(_)) => {
                    guard.remove(&id);
                    drop(guard);
                    let _ = app_reap.emit(&exit_ev, ());
                    return;
                }
                Ok(None) => {}
                Err(_) => {
                    guard.remove(&id);
                    return;
                }
            },
            None => return, // killed elsewhere
        }
    });

    Ok(())
}

fn pump<R: Read>(mut r: R, app: &tauri::AppHandle, event: &str) {
    let mut buf = [0u8; 8192];
    loop {
        match r.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => {
                let _ = app.emit(event, String::from_utf8_lossy(&buf[..n]).to_string());
            }
        }
    }
}

#[tauri::command]
pub fn pty_write(app: tauri::AppHandle, id: String, data: String) -> Result<(), String> {
    let state = app.state::<PtyState>();
    let mut sessions = state.sessions.lock().unwrap();
    if let Some(sess) = sessions.get_mut(&id) {
        sess.stdin
            .write_all(data.as_bytes())
            .map_err(|e| e.to_string())?;
        sess.stdin.flush().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Resize the remote PTY via a control frame the bridge intercepts on stdin.
#[tauri::command]
pub fn pty_resize(app: tauri::AppHandle, id: String, cols: u16, rows: u16) {
    let state = app.state::<PtyState>();
    let mut sessions = state.sessions.lock().unwrap();
    if let Some(sess) = sessions.get_mut(&id) {
        let frame = format!("\x1eR{}x{}\n", cols.max(1), rows.max(1));
        let _ = sess.stdin.write_all(frame.as_bytes());
        let _ = sess.stdin.flush();
    }
}

#[tauri::command]
pub fn pty_kill(app: tauri::AppHandle, id: String) {
    if let Some(mut sess) = app.state::<PtyState>().sessions.lock().unwrap().remove(&id) {
        let _ = sess.child.kill();
    }
}

pub fn init(app: &tauri::App) {
    app.manage(PtyState::default());
}
