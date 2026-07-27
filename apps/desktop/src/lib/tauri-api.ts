// Real DesktopAPI: proxies to the neo-bridge sidecar through the typed Tauri
// `bridge` command. Each call runs one bridge invocation (read-only).

import type {
  AppSummary,
  BridgeHello,
  DesktopAPI,
  Finding,
  ServerSnapshot,
  ServerSummary,
} from "./protocol";

interface Envelope<T> {
  version: number;
  result?: T;
  error?: { code: string; message: string };
}

async function call<T>(method: string, params?: Record<string, unknown>): Promise<T> {
  const { invoke } = await import("@tauri-apps/api/core");
  const raw = await invoke<string>("bridge", {
    method,
    params: params ? JSON.stringify(params) : undefined,
  });
  const env = JSON.parse(raw) as Envelope<T>;
  if (env.error) throw new Error(`${env.error.code}: ${env.error.message}`);
  return env.result as T;
}

export const tauriApi: DesktopAPI = {
  hello: () => call<BridgeHello>("bridge.hello"),
  listServers: () => call<ServerSummary[]>("server.list"),
  getSnapshot: (server) => call<ServerSnapshot>("server.snapshot", { server }),
  listApps: (server) => call<AppSummary[]>("app.list", { server }),
  runAppAction: (input) => call("app.action", input as unknown as Record<string, unknown>),
  runDiagnostics: (server) => call<Finding[]>("diagnostics.run", { server }),
  getLogs: async (server, app) => (await call<{ logs: string }>("app.logs", { server, app })).logs,
  setDomain: (input) => call("app.domain", input as unknown as Record<string, unknown>),
  getSshKey: async (server) => (await call<{ keyPath: string }>("server.sshkey", { server })).keyPath,
};
