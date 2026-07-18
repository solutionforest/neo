// Tauri transport for the DesktopAPI. Calls typed Rust commands over the Tauri
// IPC bridge, which in turn speaks newline-delimited JSON to the neo-bridge
// sidecar. The frontend never gets shell access — every call is one of the
// named, allowlisted commands below.
//
// Slice 1 note: the Rust `bridge_request` command and the sidecar it fronts do
// not exist yet. This module is scaffolded for slice 2 and is only reachable
// when `VITE_USE_BRIDGE=true` (see createDesktopAPI). It must not be imported by
// the fixture/default path.

import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type { DesktopAPI } from "./desktop-api";
import {
  BridgeError,
  ERROR_CODES,
  type AppActionInput,
  type AppSummary,
  type BridgeHello,
  type ErrorCode,
  type Finding,
  type LogClosedReason,
  type LogHandlers,
  type LogSubscribeInput,
  type LogSubscription,
  type OperationResult,
  type ServerSnapshot,
  type ServerSummary,
} from "./protocol";

/** Tauri event carrying a forwarded bridge stream message (`bridge://event`).
 * The Rust supervisor emits every protocol event under this one channel; the
 * `event`/`subscription` fields let a subscriber pick out its own lines. */
const STREAM_EVENT = "bridge://event";

interface RawStreamEvent {
  event?: string;
  subscription?: string;
  data?: { lines?: string[]; reason?: string; code?: string; message?: string };
}

/** Shape the Rust side returns for a failed bridge call. */
interface RawBridgeError {
  code?: string;
  message?: string;
  retryable?: boolean;
  details?: Record<string, unknown>;
}

function toBridgeError(raw: unknown): BridgeError {
  const e = (raw ?? {}) as RawBridgeError;
  const code: ErrorCode = (ERROR_CODES as readonly string[]).includes(e.code ?? "")
    ? (e.code as ErrorCode)
    : "internal_error";
  return new BridgeError(
    code,
    e.message ?? "bridge request failed",
    e.retryable ?? false,
    e.details ?? {},
  );
}

/** Invoke a typed Tauri command, normalizing failures into BridgeError. */
async function call<T>(command: string, args: Record<string, unknown>): Promise<T> {
  try {
    return await invoke<T>(command, args);
  } catch (raw) {
    throw toBridgeError(raw);
  }
}

export function createTauriDesktopAPI(): DesktopAPI {
  return {
    hello: () => call<BridgeHello>("bridge_hello", {}),
    listServers: () => call<ServerSummary[]>("server_list", {}),
    getSnapshot: (server) => call<ServerSnapshot>("server_snapshot", { server }),
    listApps: (server) => call<AppSummary[]>("app_list", { server }),
    runDiagnostics: (server) => call<Finding[]>("diagnostics_run", { server }),
    runAppAction: (input: AppActionInput) =>
      call<OperationResult>("app_action", { input }),
    cancelOperation: (operationId: string) =>
      call<{ found: boolean }>("operation_cancel", { operationId }),
    subscribeLogs: (input, handlers) => subscribeLogs(input, handlers),
  };
}

/**
 * Start a bridge log stream and route its `bridge://event` messages to the
 * handlers. The listener is attached BEFORE the subscribe request so a fast
 * first batch cannot be missed; events that arrive before the id is known are
 * buffered and replayed once it resolves.
 */
async function subscribeLogs(
  input: LogSubscribeInput,
  handlers: LogHandlers,
): Promise<LogSubscription> {
  let subId: string | null = null;
  let closed = false;
  const pending: RawStreamEvent[] = [];

  const dispatch = (payload: RawStreamEvent): void => {
    if (closed || subId === null || payload.subscription !== subId) return;
    if (payload.event === "logs.line") {
      handlers.onLines(payload.data?.lines ?? []);
    } else if (payload.event === "logs.closed") {
      const reason = (payload.data?.reason ?? "eof") as LogClosedReason;
      const error =
        reason === "error"
          ? toBridgeError({
              code: payload.data?.code,
              message: payload.data?.message,
            })
          : undefined;
      handlers.onClosed?.(reason, error);
    }
  };

  const unlisten: UnlistenFn = await listen<RawStreamEvent>(STREAM_EVENT, (e) => {
    const payload = e.payload;
    if (subId === null) {
      pending.push(payload);
      return;
    }
    dispatch(payload);
  });

  let res: { subscription: string };
  try {
    res = await call<{ subscription: string }>("logs_subscribe", { input });
  } catch (err) {
    unlisten();
    throw err;
  }
  subId = res.subscription;
  // Replay anything that arrived while we were still learning our id.
  for (const p of pending.splice(0)) dispatch(p);

  return {
    id: res.subscription,
    close: async () => {
      if (closed) return;
      closed = true;
      unlisten();
      try {
        await call("logs_unsubscribe", { subscription: res.subscription });
      } catch {
        // Best-effort: the stream is also torn down on window close / shutdown.
      }
    },
  };
}
