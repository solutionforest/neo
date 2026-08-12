// Fixture DesktopAPI — lets the tray UI render without a bridge or SSH server.
// Production builds will swap this for the Tauri stdio transport.

import type {
  AppActionInput,
  AppSummary,
  BridgeHello,
  DesktopAPI,
  Finding,
  ServerSnapshot,
  ServerSummary,
  TrayState,
} from "./protocol";

const GiB = 1024 * 1024 * 1024;

const SERVERS: ServerSummary[] = [
  { name: "production", host: "root@159.65.100.42", port: 22, keyPath: "", current: true },
  { name: "staging", host: "ubuntu@95.41.31.91", port: 22, keyPath: "", current: false },
  { name: "edge-eu", host: "root@203.0.113.10", port: 22, keyPath: "", current: false },
];

const SNAPSHOTS: Record<string, ServerSnapshot> = {
  production: {
    server: "production",
    reachable: true,
    observedAt: "2026-07-20T05:40:00Z",
    latencyMs: 84,
    cpuPercent: 34,
    ramUsedBytes: 2.1 * GiB,
    ramTotalBytes: 4 * GiB,
    diskUsedBytes: 31 * GiB,
    diskTotalBytes: 80 * GiB,
    uptimeSeconds: 1_820_400,
    apps: { total: 3, running: 3 },
    services: { total: 2, running: 2 },
  },
  staging: {
    server: "staging",
    reachable: true,
    observedAt: "2026-07-20T05:39:30Z",
    latencyMs: 240,
    cpuPercent: 82,
    ramUsedBytes: 3.6 * GiB,
    ramTotalBytes: 4 * GiB,
    diskUsedBytes: 61 * GiB,
    diskTotalBytes: 80 * GiB,
    uptimeSeconds: 320_000,
    apps: { total: 2, running: 1 },
    services: { total: 1, running: 1 },
  },
  "edge-eu": {
    server: "edge-eu",
    reachable: false,
    observedAt: "2026-07-20T05:35:00Z",
    latencyMs: 0,
    cpuPercent: 0,
    ramUsedBytes: 0,
    ramTotalBytes: 0,
    diskUsedBytes: 0,
    diskTotalBytes: 0,
    uptimeSeconds: 0,
    apps: { total: 4, running: 0 },
    services: { total: 2, running: 0 },
  },
};

const APPS: Record<string, AppSummary[]> = {
  production: [
    { name: "ghost", domain: "blog.mysite.com", status: "running", image: "ghost:5-alpine" },
    { name: "plausible", domain: "analytics.mysite.com", status: "running", image: "plausible/community:v2" },
    { name: "gitea", domain: "git.mysite.com", status: "running", image: "gitea/gitea:1.22" },
  ],
  staging: [
    { name: "api", domain: "api.staging.mysite.com", status: "running", image: "node:20" },
    { name: "worker", domain: "", status: "stopped", image: "node:20" },
  ],
  "edge-eu": [],
};

const FINDINGS: Record<string, Finding[]> = {
  production: [],
  staging: [
    { id: "f1", rule: "cpu", severity: "warning", summary: "CPU at 82% for 3 samples", lastObservedAt: "2026-07-20T05:39:30Z" },
    { id: "f2", rule: "app_state", severity: "warning", summary: "App 'worker' is stopped", lastObservedAt: "2026-07-20T05:39:30Z" },
  ],
  "edge-eu": [
    { id: "f3", rule: "reachability", severity: "critical", summary: "edge-eu is unreachable (2 attempts)", lastObservedAt: "2026-07-20T05:35:00Z" },
  ],
};

const delay = <T,>(v: T, ms = 220) => new Promise<T>((r) => setTimeout(() => r(v), ms));

export const fixtureApi: DesktopAPI = {
  hello: () =>
    delay<BridgeHello>({
      protocolVersion: 1,
      bridgeVersion: "0.1.0-fixture",
      cliCore: "0.22.0",
      platform: "darwin/arm64",
      activated: true,
    }),
  listServers: () => delay(SERVERS),
  getSnapshot: (server) => delay(SNAPSHOTS[server] ?? SNAPSHOTS.production),
  listApps: (server) => delay(APPS[server] ?? []),
  runAppAction: (input: AppActionInput) =>
    delay({ operationId: "op-1", status: "succeeded", summary: `${input.action} ${input.app} ok` }, 500),
  runDiagnostics: (server) => delay(FINDINGS[server] ?? []),
  getLogs: (_server, app) => delay(`[fixture] last logs for ${app}\n2026-07-22 12:00:00 started\n2026-07-22 12:00:01 ready`, 300),
  setDomain: (input) => delay({ status: input.domain }, 400),
  getSshKey: () => delay("", 100),
};

// Aggregate several servers into a single tray state.
export function aggregateTrayState(snapshots: ServerSnapshot[], findings: Finding[]): TrayState {
  if (snapshots.length === 0) return "unknown";
  if (snapshots.some((s) => !s.reachable) || findings.some((f) => f.severity === "critical")) return "critical";
  if (findings.some((f) => f.severity === "warning")) return "warning";
  return "healthy";
}
