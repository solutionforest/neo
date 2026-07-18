//! `neo-bridge` sidecar supervision.
//!
//! Rust owns exactly one bridge child process and speaks the versioned
//! newline-delimited JSON protocol to it over stdio (see the Go side in
//! `cmd/neo-bridge` and the plan's "Phase 2"). Responsibilities:
//!
//!   * Start a single bundled `neo-bridge` sidecar (never a PATH executable).
//!   * Perform a `bridge.hello` handshake and reject an incompatible protocol.
//!   * Correlate responses to requests by id.
//!   * Route streaming events to the frontend.
//!   * Restart at most [`MAX_RESTARTS`] times after unexpected exits, with
//!     exponential backoff, then surface a clear error.
//!   * Terminate the child when the desktop app exits.
//!
//! The webview never gets shell access: the sidecar is spawned here in Rust and
//! no `shell:`/`process:` spawn capability is granted to the frontend. The only
//! surface JavaScript sees is the allowlisted, typed commands in this module.

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::Serialize;
use serde_json::{json, Map, Value};
use tauri::{AppHandle, Emitter, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tokio::sync::oneshot;

/// Protocol version this desktop build speaks. Mirrors `PROTOCOL_VERSION` on the
/// TypeScript side (`src/lib/protocol.ts`) and `ProtocolVersion` in the Go
/// bridge (`cmd/neo-bridge/protocol.go`). Bump all three together.
pub const PROTOCOL_VERSION: u32 = 1;

/// The bundled sidecar's base name; Tauri resolves it to
/// `neo-bridge-<target-triple>` from `externalBin`.
const SIDECAR_NAME: &str = "neo-bridge";

/// Restarts allowed after unexpected exits before we give up and surface an
/// error. This is a lifetime budget, not per-crash: the plan requires "at most
/// three" restarts.
const MAX_RESTARTS: u32 = 3;

/// How long a single request may wait for its correlated response.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(30);

/// Frontend event names. Slice 2 emits lifecycle events and forwards protocol
/// stream events; windows subscribe in later slices.
const EVENT_READY: &str = "bridge://ready";
const EVENT_ERROR: &str = "bridge://error";
const EVENT_UNAVAILABLE: &str = "bridge://unavailable";
const EVENT_STREAM: &str = "bridge://event";

/// Structured error handed back to the webview. Its shape matches
/// `RawBridgeError` in `src/lib/tauri-api.ts`, so the frontend can branch on the
/// stable `code` and never parse the message.
#[derive(Debug, Clone, Serialize)]
pub struct BridgeErrorPayload {
    pub code: String,
    pub message: String,
    pub retryable: bool,
    pub details: Map<String, Value>,
}

impl BridgeErrorPayload {
    fn new(code: &str, message: impl Into<String>) -> Self {
        Self {
            code: code.to_string(),
            message: message.into(),
            retryable: false,
            details: Map::new(),
        }
    }

    fn internal(message: impl Into<String>) -> Self {
        Self::new("internal_error", message)
    }
}

/// A pending request's completion channel. Carries either the `result` value or
/// a structured error parsed from the bridge's `error` object.
type Reply = Result<Value, BridgeErrorPayload>;

/// Supervises the single `neo-bridge` child process and its protocol stream.
pub struct BridgeManager {
    app: AppHandle,
    /// stdin of the live child, if one is running.
    child: Mutex<Option<CommandChild>>,
    /// in-flight requests awaiting their correlated response, keyed by id.
    pending: Mutex<HashMap<String, oneshot::Sender<Reply>>>,
    next_id: AtomicU64,
    /// set once during app shutdown so the supervisor stops restarting.
    shutting_down: AtomicBool,
}

impl BridgeManager {
    pub fn new(app: AppHandle) -> Self {
        Self {
            app,
            child: Mutex::new(None),
            pending: Mutex::new(HashMap::new()),
            next_id: AtomicU64::new(1),
            shutting_down: AtomicBool::new(false),
        }
    }

    /// Launch the supervisor loop. Returns immediately; the loop runs on the
    /// async runtime, (re)spawning the sidecar as needed.
    pub fn start(self: &Arc<Self>) {
        let this = Arc::clone(self);
        tauri::async_runtime::spawn(async move { this.supervise().await });
    }

    /// Ask the running bridge to shut down and terminate the child. Synchronous
    /// and best-effort so it can run from the app's `Exit` event.
    pub fn shutdown(&self) {
        self.shutting_down.store(true, Ordering::SeqCst);
        // Best-effort graceful request; ignore failures — we kill next anyway.
        if let Ok(mut guard) = self.child.lock() {
            if let Some(child) = guard.as_mut() {
                let line = serde_json::to_string(&json!({
                    "version": PROTOCOL_VERSION,
                    "id": "shutdown",
                    "method": "bridge.shutdown",
                }))
                .unwrap_or_default();
                let _ = child.write(format!("{line}\n").as_bytes());
            }
        }
        self.kill_child();
    }

    async fn supervise(self: Arc<Self>) {
        let mut restarts: u32 = 0;
        loop {
            if self.shutting_down.load(Ordering::SeqCst) {
                break;
            }

            match self.spawn_once() {
                Ok((mut rx, child)) => {
                    if let Ok(mut guard) = self.child.lock() {
                        *guard = Some(child);
                    }

                    // Handshake concurrently: it issues bridge.hello, whose
                    // reply is delivered by the read loop below.
                    let hs = Arc::clone(&self);
                    tauri::async_runtime::spawn(async move {
                        match hs.handshake().await {
                            Ok(()) => {
                                let _ = hs.app.emit(EVENT_READY, json!({"ready": true}));
                            }
                            Err(err) => {
                                log::error!("bridge handshake failed: {err}");
                                let _ = hs.app.emit(EVENT_ERROR, json!({"message": err}));
                            }
                        }
                    });

                    // Drain the child's output until the process exits.
                    self.read_loop(&mut rx).await;

                    self.clear_child();
                    self.fail_all_pending();
                }
                Err(err) => {
                    log::error!("failed to spawn {SIDECAR_NAME}: {err}");
                    let _ = self.app.emit(EVENT_ERROR, json!({"message": err}));
                }
            }

            if self.shutting_down.load(Ordering::SeqCst) {
                break;
            }

            restarts += 1;
            if restarts > MAX_RESTARTS {
                log::error!("bridge restart budget exhausted ({MAX_RESTARTS} restarts)");
                let _ = self.app.emit(
                    EVENT_UNAVAILABLE,
                    json!({"message": "neo-bridge could not be kept running"}),
                );
                break;
            }

            // Exponential backoff: 200ms, 400ms, 800ms.
            let backoff = Duration::from_millis(200 * 2u64.pow(restarts - 1));
            log::warn!("restarting bridge (attempt {restarts}/{MAX_RESTARTS}) after {backoff:?}");
            tokio::time::sleep(backoff).await;
        }
    }

    fn spawn_once(&self) -> Result<(tauri::async_runtime::Receiver<CommandEvent>, CommandChild), String> {
        let command = self
            .app
            .shell()
            .sidecar(SIDECAR_NAME)
            .map_err(|e| format!("resolve sidecar: {e}"))?;
        command.spawn().map_err(|e| format!("spawn sidecar: {e}"))
    }

    async fn read_loop(&self, rx: &mut tauri::async_runtime::Receiver<CommandEvent>) {
        // Bytes may arrive in arbitrary chunks; reassemble complete NDJSON lines.
        let mut buf: Vec<u8> = Vec::new();
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(chunk) => {
                    buf.extend_from_slice(&chunk);
                    while let Some(pos) = buf.iter().position(|&b| b == b'\n') {
                        let line: Vec<u8> = buf.drain(..=pos).collect();
                        let trimmed = &line[..line.len().saturating_sub(1)];
                        self.handle_message(trimmed);
                    }
                }
                CommandEvent::Stderr(chunk) => {
                    // The bridge logs structured JSON to stderr. Forward at debug
                    // for diagnostics; it can never corrupt the protocol stream.
                    let text = String::from_utf8_lossy(&chunk);
                    log::debug!("[neo-bridge] {}", text.trim_end());
                }
                CommandEvent::Error(err) => log::error!("neo-bridge process error: {err}"),
                CommandEvent::Terminated(payload) => {
                    log::warn!("neo-bridge terminated: code={:?}", payload.code)
                }
                _ => {}
            }
        }
    }

    /// Parse one protocol line and either route an event or complete a pending
    /// request. Malformed lines are logged and dropped.
    fn handle_message(&self, line: &[u8]) {
        if line.iter().all(|b| b.is_ascii_whitespace()) {
            return;
        }
        let value: Value = match serde_json::from_slice(line) {
            Ok(v) => v,
            Err(e) => {
                log::warn!("dropping unparseable bridge line: {e}");
                return;
            }
        };

        if value.get("event").and_then(Value::as_str).is_some() {
            let _ = self.app.emit(EVENT_STREAM, value);
            return;
        }

        let Some(id) = value.get("id").and_then(Value::as_str) else {
            log::warn!("bridge message without id or event: {value}");
            return;
        };

        let sender = self.pending.lock().ok().and_then(|mut p| p.remove(id));
        let Some(sender) = sender else {
            log::warn!("bridge reply for unknown request id {id}");
            return;
        };

        let reply: Reply = match value.get("error") {
            Some(err) => Err(parse_error(err)),
            None => Ok(value.get("result").cloned().unwrap_or(Value::Null)),
        };
        let _ = sender.send(reply);
    }

    /// Send a request and await its correlated response (or a timeout).
    pub async fn request(&self, method: &str, params: Value) -> Reply {
        let id = format!("req-{}", self.next_id.fetch_add(1, Ordering::SeqCst));
        let (tx, rx) = oneshot::channel::<Reply>();

        // Register before writing so a fast reply can never race ahead of us.
        match self.pending.lock() {
            Ok(mut p) => {
                p.insert(id.clone(), tx);
            }
            Err(_) => return Err(BridgeErrorPayload::internal("bridge state poisoned")),
        }

        let mut request = Map::new();
        request.insert("version".into(), json!(PROTOCOL_VERSION));
        request.insert("id".into(), Value::String(id.clone()));
        request.insert("method".into(), Value::String(method.to_string()));
        if !params.is_null() {
            request.insert("params".into(), params);
        }
        let line = format!("{}\n", Value::Object(request));

        // Write to stdin, dropping the guard before we await.
        {
            let mut guard = match self.child.lock() {
                Ok(g) => g,
                Err(_) => {
                    self.drop_pending(&id);
                    return Err(BridgeErrorPayload::internal("bridge state poisoned"));
                }
            };
            match guard.as_mut() {
                Some(child) => {
                    if let Err(e) = child.write(line.as_bytes()) {
                        drop(guard);
                        self.drop_pending(&id);
                        return Err(BridgeErrorPayload::internal(format!(
                            "write to bridge failed: {e}"
                        )));
                    }
                }
                None => {
                    drop(guard);
                    self.drop_pending(&id);
                    return Err(BridgeErrorPayload::new(
                        "internal_error",
                        "bridge is not running",
                    ));
                }
            }
        }

        match tokio::time::timeout(REQUEST_TIMEOUT, rx).await {
            Ok(Ok(reply)) => reply,
            Ok(Err(_canceled)) => Err(BridgeErrorPayload::internal("bridge reply channel closed")),
            Err(_elapsed) => {
                self.drop_pending(&id);
                Err(BridgeErrorPayload::new(
                    "operation_timeout",
                    "bridge request timed out",
                ))
            }
        }
    }

    /// Handshake: verify the bridge speaks a compatible protocol version before
    /// any live data is shown. A mismatch is fatal — stop supervising.
    async fn handshake(&self) -> Result<(), String> {
        let hello = self
            .request("bridge.hello", Value::Null)
            .await
            .map_err(|e| format!("{}: {}", e.code, e.message))?;

        let version = hello
            .get("protocolVersion")
            .and_then(Value::as_u64)
            .unwrap_or(0) as u32;

        if version != PROTOCOL_VERSION {
            self.shutting_down.store(true, Ordering::SeqCst);
            self.kill_child();
            return Err(format!(
                "incompatible protocol version {version} (desktop speaks {PROTOCOL_VERSION})"
            ));
        }
        Ok(())
    }

    fn drop_pending(&self, id: &str) {
        if let Ok(mut p) = self.pending.lock() {
            p.remove(id);
        }
    }

    /// Complete every in-flight request with an error so awaiting commands do
    /// not hang after the process dies.
    fn fail_all_pending(&self) {
        if let Ok(mut p) = self.pending.lock() {
            for (_, tx) in p.drain() {
                let _ = tx.send(Err(BridgeErrorPayload::new(
                    "internal_error",
                    "bridge exited before responding",
                )));
            }
        }
    }

    fn clear_child(&self) {
        if let Ok(mut guard) = self.child.lock() {
            *guard = None;
        }
    }

    fn kill_child(&self) {
        if let Ok(mut guard) = self.child.lock() {
            if let Some(child) = guard.take() {
                let _ = child.kill();
            }
        }
    }
}

/// Build a [`BridgeErrorPayload`] from a bridge `error` object, defaulting to
/// `internal_error` when fields are missing.
fn parse_error(err: &Value) -> BridgeErrorPayload {
    let code = err
        .get("code")
        .and_then(Value::as_str)
        .unwrap_or("internal_error")
        .to_string();
    let message = err
        .get("message")
        .and_then(Value::as_str)
        .unwrap_or("bridge error")
        .to_string();
    let retryable = err.get("retryable").and_then(Value::as_bool).unwrap_or(false);
    let details = err
        .get("details")
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default();
    BridgeErrorPayload {
        code,
        message,
        retryable,
        details,
    }
}

// --- Typed commands exposed to the webview -------------------------------
//
// Each forwards one allowlisted method to the bridge. Methods beyond
// bridge.hello land in later slices; until then the bridge answers with a stable
// error code, which the frontend maps to a `BridgeError`.

#[tauri::command]
pub async fn bridge_hello(state: State<'_, Arc<BridgeManager>>) -> Result<Value, BridgeErrorPayload> {
    let mut hello = state.request("bridge.hello", Value::Null).await?;
    // The desktop app version is known only here in the shell; inject it so the
    // frontend's BridgeHello is complete.
    if let Value::Object(ref mut map) = hello {
        map.insert(
            "desktopVersion".into(),
            Value::String(env!("CARGO_PKG_VERSION").to_string()),
        );
    }
    Ok(hello)
}

#[tauri::command]
pub async fn server_list(state: State<'_, Arc<BridgeManager>>) -> Result<Value, BridgeErrorPayload> {
    state.request("server.list", Value::Null).await
}

#[tauri::command]
pub async fn server_snapshot(
    state: State<'_, Arc<BridgeManager>>,
    server: String,
) -> Result<Value, BridgeErrorPayload> {
    state.request("server.snapshot", json!({ "server": server })).await
}

#[tauri::command]
pub async fn app_list(
    state: State<'_, Arc<BridgeManager>>,
    server: String,
) -> Result<Value, BridgeErrorPayload> {
    state.request("app.list", json!({ "server": server })).await
}

#[tauri::command]
pub async fn diagnostics_run(
    state: State<'_, Arc<BridgeManager>>,
    server: String,
) -> Result<Value, BridgeErrorPayload> {
    state.request("diagnostics.run", json!({ "server": server })).await
}

#[tauri::command]
pub async fn app_action(
    state: State<'_, Arc<BridgeManager>>,
    input: Value,
) -> Result<Value, BridgeErrorPayload> {
    state.request("app.action", input).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn protocol_version_starts_at_one() {
        // Bump deliberately alongside the TypeScript and Go definitions.
        assert_eq!(PROTOCOL_VERSION, 1);
    }

    #[test]
    fn parse_error_reads_all_fields() {
        let err = json!({
            "code": "ssh_unreachable",
            "message": "nope",
            "retryable": true,
            "details": {"server": "prod"}
        });
        let payload = parse_error(&err);
        assert_eq!(payload.code, "ssh_unreachable");
        assert_eq!(payload.message, "nope");
        assert!(payload.retryable);
        assert_eq!(payload.details.get("server").unwrap(), "prod");
    }

    #[test]
    fn parse_error_defaults_to_internal() {
        let payload = parse_error(&json!({}));
        assert_eq!(payload.code, "internal_error");
        assert!(!payload.retryable);
        assert!(payload.details.is_empty());
    }
}
