// Fixture-backed DesktopAPI. Deterministic data for tests, local UI iteration,
// and the first visual shell — it renders the popover without any SSH server.
// Production builds must not use this; see createDesktopAPI() in desktop-api.ts.

import type { DesktopAPI } from "./desktop-api";
import {
  clampTail,
  type AppActionInput,
  type AppSummary,
  type BridgeHello,
  type Finding,
  type LogHandlers,
  type LogSubscribeInput,
  type LogSubscription,
  type OperationResult,
  type ServerSnapshot,
  type ServerSummary,
} from "./protocol";

const GiB = 1024 * 1024 * 1024;

export const FIXTURE_HELLO: BridgeHello = {
  protocolVersion: 1,
  bridgeVersion: "0.1.0-fixture",
  desktopVersion: "0.1.0",
  coreVersion: "0.0.0-dev",
  platform: "fixture",
  arch: "unknown",
  activation: "active",
};

export const FIXTURE_SERVERS: ServerSummary[] = [
  { id: "production", name: "production", host: "root@10.0.0.10", current: true },
  { id: "staging", name: "staging", host: "root@10.0.0.20", current: false },
  { id: "edge", name: "edge", host: "root@10.0.0.30", current: false },
];

const SNAPSHOTS: Record<string, ServerSnapshot> = {
  production: {
    server: FIXTURE_SERVERS[0],
    reachable: true,
    observedAt: "2026-07-18T09:00:00Z",
    latencyMs: 84,
    cpuPercent: 37.4,
    ramUsedBytes: 6.1 * GiB,
    ramTotalBytes: 16 * GiB,
    diskUsedBytes: 128 * GiB,
    diskTotalBytes: 200 * GiB,
    uptimeSeconds: 1_209_600,
    apps: { running: 4, stopped: 1, total: 5 },
    services: { running: 3, stopped: 0, total: 3 },
  },
  staging: {
    server: FIXTURE_SERVERS[1],
    reachable: true,
    observedAt: "2026-07-18T09:00:00Z",
    latencyMs: 152,
    cpuPercent: 82.9,
    ramUsedBytes: 3.4 * GiB,
    ramTotalBytes: 4 * GiB,
    diskUsedBytes: 41 * GiB,
    diskTotalBytes: 80 * GiB,
    uptimeSeconds: 259_200,
    apps: { running: 2, stopped: 0, total: 2 },
    services: { running: 1, stopped: 1, total: 2 },
  },
  edge: {
    server: FIXTURE_SERVERS[2],
    reachable: false,
    observedAt: "2026-07-18T08:52:00Z",
    latencyMs: 0,
    cpuPercent: 0,
    ramUsedBytes: 0,
    ramTotalBytes: 0,
    diskUsedBytes: 0,
    diskTotalBytes: 0,
    uptimeSeconds: 0,
    apps: { running: 0, stopped: 0, total: 0 },
    services: { running: 0, stopped: 0, total: 0 },
  },
};

const APPS: Record<string, AppSummary[]> = {
  production: [
    { id: "ghost", name: "ghost", image: "ghost:5", state: "running", kind: "app" },
    { id: "umami", name: "umami", image: "umami:latest", state: "running", kind: "app" },
    { id: "gitea", name: "gitea", image: "gitea/gitea:1", state: "running", kind: "app" },
    { id: "plausible", name: "plausible", image: "plausible:2", state: "running", kind: "app" },
    { id: "listmonk", name: "listmonk", image: "listmonk:v3", state: "stopped", kind: "app" },
  ],
  staging: [
    { id: "web", name: "web", image: "acme/web:staging", state: "running", kind: "app" },
    { id: "worker", name: "worker", image: "acme/web:staging", state: "running", kind: "worker" },
  ],
  edge: [],
};

const FINDINGS: Record<string, Finding[]> = {
  production: [
    {
      id: "listmonk-stopped",
      rule: "app_state",
      severity: "warning",
      summary: "Application “listmonk” is stopped",
      evidence: [{ label: "State", value: "stopped" }],
      recommendedFixId: "restart-listmonk",
      firstObservedAt: "2026-07-18T08:40:00Z",
      lastObservedAt: "2026-07-18T09:00:00Z",
    },
  ],
  staging: [
    {
      id: "staging-cpu",
      rule: "cpu_usage",
      severity: "warning",
      summary: "CPU usage is high (82.9%)",
      evidence: [
        { label: "CPU", value: "82.9%" },
        { label: "Threshold", value: "80%" },
      ],
      firstObservedAt: "2026-07-18T08:57:00Z",
      lastObservedAt: "2026-07-18T09:00:00Z",
    },
    {
      id: "staging-redis-stopped",
      rule: "service_state",
      severity: "warning",
      summary: "Service “redis” is stopped",
      evidence: [{ label: "State", value: "stopped" }],
      firstObservedAt: "2026-07-18T08:50:00Z",
      lastObservedAt: "2026-07-18T09:00:00Z",
    },
  ],
  edge: [
    {
      id: "edge-unreachable",
      rule: "server_reachability",
      severity: "critical",
      summary: "Server “edge” is unreachable",
      evidence: [{ label: "Last contact", value: "8 minutes ago" }],
      firstObservedAt: "2026-07-18T08:52:00Z",
      lastObservedAt: "2026-07-18T09:00:00Z",
    },
  ],
};

/** Deterministic recent-log lines per workload id, for the log viewer's fixture
 * mode. A target with no fixture gets a short generic backlog. */
const LOG_LINES: Record<string, string[]> = {
  ghost: [
    "[ghost] Booting Ghost 5.x in production",
    "[ghost] Database is connected",
    "[ghost] Ghost is running on http://0.0.0.0:2368",
    "[ghost] GET /  200  12ms",
    "[ghost] GET /assets/built/screen.css  200  3ms",
    "[ghost] GET /ghost/  200  41ms",
  ],
  web: [
    "[web] listening on :3000",
    "[web] GET /health  200  1ms",
    "[web] GET /  200  8ms",
  ],
};

/** Options let tests inject latency or a fixed clock without touching data. */
export interface FixtureOptions {
  /** Artificial per-call delay in ms (default 0 for synchronous tests). */
  latencyMs?: number;
  /** Deterministic timestamp for generated operation results. */
  now?: () => string;
}

export function createFixtureDesktopAPI(options: FixtureOptions = {}): DesktopAPI {
  const { latencyMs = 0, now = () => "2026-07-18T09:00:05Z" } = options;
  const delay = <T>(value: T): Promise<T> =>
    latencyMs > 0
      ? new Promise((r) => setTimeout(() => r(value), latencyMs))
      : Promise.resolve(value);

  const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;

  return {
    hello: () => delay(clone(FIXTURE_HELLO)),
    listServers: () => delay(clone(FIXTURE_SERVERS)),
    getSnapshot: (server) => {
      const snap = SNAPSHOTS[server];
      if (!snap) return Promise.reject(new Error(`unknown fixture server: ${server}`));
      return delay(clone(snap));
    },
    listApps: (server) => delay(clone(APPS[server] ?? [])),
    runDiagnostics: (server) => delay(clone(FINDINGS[server] ?? [])),
    runAppAction: (input: AppActionInput): Promise<OperationResult> => {
      const started = now();
      const result: OperationResult = {
        operationId: `fixture-${input.app}-${input.action}`,
        status: "succeeded",
        startedAt: started,
        finishedAt: started,
        summary: `${input.action} ${input.app} on ${input.server}`,
        changes: [
          {
            target: input.app,
            from: input.action === "start" ? "stopped" : "running",
            to: input.action === "stop" ? "stopped" : "running",
          },
        ],
      };
      return delay(result);
    },
    subscribeLogs: (
      input: LogSubscribeInput,
      handlers: LogHandlers,
    ): Promise<LogSubscription> => {
      const tail = clampTail(input.tail);
      const backlog = (LOG_LINES[input.target] ?? [
        `[${input.target}] no fixture logs; showing a placeholder line`,
      ]).slice(-tail);

      let closed = false;
      const timers: ReturnType<typeof setTimeout>[] = [];

      // Deliver the recent backlog on the next tick so subscribers can attach
      // handlers exactly as they would against the real async bridge.
      timers.push(
        setTimeout(() => {
          if (closed) return;
          handlers.onLines(backlog);
          if (!input.follow) {
            handlers.onClosed?.("eof");
          }
        }, 0),
      );

      // Follow mode: emit a couple of synthetic lines so the live view is not
      // frozen. Bounded and cleared on close so tests never leak timers.
      if (input.follow) {
        for (let i = 1; i <= 2; i++) {
          timers.push(
            setTimeout(() => {
              if (closed) return;
              handlers.onLines([`[${input.target}] heartbeat ${i}`]);
            }, i),
          );
        }
      }

      const sub: LogSubscription = {
        id: `fixture-${input.target}`,
        close: () => {
          if (!closed) {
            closed = true;
            for (const t of timers) clearTimeout(t);
            handlers.onClosed?.("cancelled");
          }
          return Promise.resolve();
        },
      };
      return Promise.resolve(sub);
    },
  };
}
