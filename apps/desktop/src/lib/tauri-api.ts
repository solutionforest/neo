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
import type { DesktopAPI } from "./desktop-api";
import {
  BridgeError,
  ERROR_CODES,
  type AppActionInput,
  type AppSummary,
  type BridgeHello,
  type ErrorCode,
  type Finding,
  type OperationResult,
  type ServerSnapshot,
  type ServerSummary,
} from "./protocol";

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
  };
}
