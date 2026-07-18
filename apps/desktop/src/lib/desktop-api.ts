// The single interface the React UI uses to talk to Neo. Two implementations
// exist: a fixture provider (tests, Storybook-style dev, first visual shell)
// and the Tauri transport (production). The UI never imports either concrete
// implementation directly — it depends only on this interface.

import type {
  AppActionInput,
  AppSummary,
  BridgeHello,
  Finding,
  LogHandlers,
  LogSubscribeInput,
  LogSubscription,
  OperationResult,
  ServerSnapshot,
  ServerSummary,
} from "./protocol";

export interface DesktopAPI {
  hello(): Promise<BridgeHello>;
  listServers(): Promise<ServerSummary[]>;
  getSnapshot(server: string): Promise<ServerSnapshot>;
  listApps(server: string): Promise<AppSummary[]>;
  runAppAction(input: AppActionInput): Promise<OperationResult>;
  /**
   * Cancel an in-flight lifecycle action by its operation id. Idempotent:
   * cancelling an already-finished operation resolves harmlessly. Returns
   * whether a live operation was found.
   */
  cancelOperation(operationId: string): Promise<{ found: boolean }>;
  runDiagnostics(server: string): Promise<Finding[]>;
  /**
   * Start a log stream. Lines arrive (batched) via `handlers.onLines`; the
   * returned handle's `close()` cancels the stream. The bridge owns
   * cancellation and backpressure — the caller only decides when to stop.
   */
  subscribeLogs(
    input: LogSubscribeInput,
    handlers: LogHandlers,
  ): Promise<LogSubscription>;
}

/**
 * Select the transport.
 *
 * Slice 1 has no bridge yet, so the tray must render fixture data even inside
 * the Tauri webview. The real bridge transport is only used when running under
 * Tauri AND explicitly opted in via `VITE_USE_BRIDGE=true` — that flag flips on
 * in slice 2 once the sidecar exists. Everything else (tests, `vite dev` in a
 * browser) uses fixtures. Tauri detection: the webview injects
 * `__TAURI_INTERNALS__` on `window`.
 */
export async function createDesktopAPI(): Promise<DesktopAPI> {
  if (isTauri() && useBridge()) {
    const { createTauriDesktopAPI } = await import("./tauri-api");
    return createTauriDesktopAPI();
  }
  const { createFixtureDesktopAPI } = await import("./fixtures");
  // Browser-only visual review can pin a deterministic fixture with
  // `?server=edge`; production Tauri builds continue to use the bridge.
  const initialServer = typeof window === "undefined"
    ? undefined
    : new URLSearchParams(window.location.search).get("server") ?? undefined;
  return createFixtureDesktopAPI({ initialServer });
}

function useBridge(): boolean {
  return import.meta.env?.VITE_USE_BRIDGE === "true";
}

export function isTauri(): boolean {
  return (
    typeof window !== "undefined" &&
    "__TAURI_INTERNALS__" in (window as unknown as Record<string, unknown>)
  );
}
