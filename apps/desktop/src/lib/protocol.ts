// Shared protocol types for the Neo Desktop frontend.
//
// These mirror the Go `neo-bridge` wire contract described in
// plans/2026-07-18-neo-desktop-tray-application.md. In slice 1 they are only
// consumed by the fixture provider and UI; slice 2 wires them to the real
// bridge transport. Keep field names in sync with the Go `internal/operations`
// types when that package lands.

export const PROTOCOL_VERSION = 1 as const;

/** Stable, machine-facing error codes. The UI branches on these, never on the
 * English `message`. Mirrors the "Stable error codes" table in the plan. */
export const ERROR_CODES = [
  "invalid_request",
  "protocol_mismatch",
  "not_activated",
  "server_not_found",
  "app_not_found",
  "ssh_unknown_host",
  "ssh_auth_failed",
  "ssh_unreachable",
  "remote_state_invalid",
  "operation_timeout",
  "operation_cancelled",
  "action_not_allowed",
  "internal_error",
] as const;

export type ErrorCode = (typeof ERROR_CODES)[number];

/** A bridge error surfaced to the UI. Carries a stable `code` for branching. */
export class BridgeError extends Error {
  readonly code: ErrorCode;
  readonly retryable: boolean;
  readonly details: Record<string, unknown>;

  constructor(
    code: ErrorCode,
    message: string,
    retryable = false,
    details: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = "BridgeError";
    this.code = code;
    this.retryable = retryable;
    this.details = details;
  }
}

export interface BridgeHello {
  protocolVersion: number;
  bridgeVersion: string;
  desktopVersion: string;
  coreVersion: string;
  platform: string;
  arch: string;
  /** Activation status. The key itself is never exposed to the frontend.
   * "unknown" is reported by the walking-skeleton bridge until real license
   * state is wired in a later slice. */
  activation: "active" | "inactive" | "grace" | "unknown";
}

export interface ServerSummary {
  id: string;
  name: string;
  host: string;
  current: boolean;
}

/** Aggregate rollup shown on the tray and popover header. */
export type AggregateStatus = "healthy" | "warning" | "critical" | "unknown";

export type FindingSeverity = "info" | "warning" | "critical";

export interface Evidence {
  label: string;
  value: string;
}

export interface Finding {
  id: string;
  rule: string;
  severity: FindingSeverity;
  summary: string;
  evidence: Evidence[];
  recommendedFixId?: string;
  firstObservedAt: string;
  lastObservedAt: string;
}

export interface WorkloadCounts {
  running: number;
  stopped: number;
  total: number;
}

export interface ServerSnapshot {
  server: ServerSummary;
  reachable: boolean;
  observedAt: string;
  /** A metric that the server could not report (a missing platform command)
   * arrives as `null`, never a misleading `0` — mirrors the nil pointers in the
   * Go `internal/operations.Snapshot`. The UI must treat `null` as "unavailable". */
  latencyMs: number;
  cpuPercent: number | null;
  ramUsedBytes: number | null;
  ramTotalBytes: number | null;
  diskUsedBytes: number | null;
  diskTotalBytes: number | null;
  uptimeSeconds: number | null;
  apps: WorkloadCounts;
  services: WorkloadCounts;
  containers?: ContainerStat[];
}

/** Live per-container resource usage. Each numeric field is nullable for the
 * same "unavailable, not zero" reason as ServerSnapshot's metrics. */
export interface ContainerStat {
  name: string;
  cpuPercent: number | null;
  memUsedBytes: number | null;
  memLimitBytes: number | null;
}

export type AppState = "running" | "stopped" | "restarting" | "unhealthy";

export interface AppSummary {
  id: string;
  name: string;
  image: string;
  state: AppState;
  kind: "app" | "worker" | "sidecar" | "service";
}

/** Allowlisted lifecycle actions. This union is the ENTIRE allowlist —
 * destructive operations (remove, update, restore, firewall, database changes)
 * are intentionally absent and can never be requested (plan "Fix safety
 * classes"). Mirrors the Go allowlist in internal/operations/actions.go. */
export type AppAction = "start" | "stop" | "restart";

/** Every allowlisted action, ordered — the single source of truth the UI maps
 * over. Kept in lockstep with the Go `allowedActions` map. */
export const APP_ACTIONS = ["start", "stop", "restart"] as const;

/** Safety class of an action (plan "Fix safety classes"). Drives the
 * confirmation UX. Mirrors SafetyClass in internal/operations/actions.go. */
export type SafetyClass = "reversible" | "availability";

/** Action → safety class. `start`/`restart` are reversible (one confirmation,
 * optionally remembered); `stop` is availability-affecting (confirm every
 * time). */
export const ACTION_SAFETY: Record<AppAction, SafetyClass> = {
  start: "reversible",
  restart: "reversible",
  stop: "availability",
};

export interface AppActionInput {
  server: string;
  app: string;
  action: AppAction;
  /** Client-generated id so the caller can `operation.cancel` this action while
   * it is still in flight. Optional: the bridge generates one when omitted. */
  operationId?: string;
}

export interface Change {
  target: string;
  from: string;
  to: string;
}

export interface OperationResult {
  operationId: string;
  status: "succeeded" | "failed" | "cancelled";
  startedAt: string;
  finishedAt?: string;
  summary: string;
  changes: Change[];
}

/**
 * Roll a snapshot plus its findings into a single aggregate status.
 * Unreachable or any critical finding → critical; any warning → warning;
 * missing/loading data → unknown; otherwise healthy.
 */
export function aggregateStatus(
  snapshot: ServerSnapshot | null | undefined,
  findings: Finding[] = [],
): AggregateStatus {
  if (!snapshot) return "unknown";
  if (!snapshot.reachable) return "critical";
  if (findings.some((f) => f.severity === "critical")) return "critical";
  if (findings.some((f) => f.severity === "warning")) return "warning";
  return "healthy";
}

/** Severity order used to combine many servers' statuses; higher wins. */
const AGGREGATE_RANK: Record<AggregateStatus, number> = {
  critical: 3,
  warning: 2,
  unknown: 1,
  healthy: 0,
};

/**
 * Combine every configured server's status into the single value shown on the
 * tray. The worst status wins (plan "Tray state"): any critical → critical, else
 * any warning → warning, else any unknown (a server still starting up, mid-refresh,
 * or with a stale cache) → unknown, otherwise healthy. With no configured servers
 * the tray is unknown, not healthy — there is nothing to vouch for.
 */
export function aggregateAll(statuses: AggregateStatus[]): AggregateStatus {
  if (statuses.length === 0) return "unknown";
  return statuses.reduce<AggregateStatus>(
    (worst, s) => (AGGREGATE_RANK[s] > AGGREGATE_RANK[worst] ? s : worst),
    "healthy",
  );
}

// --- Log streaming ---------------------------------------------------------

/** Default recent backlog and hard tail cap, mirroring the Go bridge's
 * `DefaultLogTail` / `MaxLogTail` (internal/operations/logs.go). Kept here so the
 * UI can label the default and never request more than the bridge will serve. */
export const DEFAULT_LOG_TAIL = 200 as const;
export const MAX_LOG_TAIL = 5_000 as const;

/** Apply the bridge's tail policy locally: a non-positive request means "use the
 * default recent backlog", and anything above the cap is clamped down. Mirrors
 * `ClampTail` in internal/operations/logs.go. */
export function clampTail(tail: number | undefined): number {
  if (tail === undefined || tail <= 0) return DEFAULT_LOG_TAIL;
  return Math.min(tail, MAX_LOG_TAIL);
}

/** Parameters for a `logs.subscribe` request. `target` is an AppSummary id. */
export interface LogSubscribeInput {
  server: string;
  target: string;
  /** Recent backlog to load. Omitted → DEFAULT_LOG_TAIL; clamped to MAX_LOG_TAIL
   * by the bridge. */
  tail?: number;
  /** Keep streaming new lines after the backlog (follow mode). */
  follow?: boolean;
}

/** Why a log stream ended. `cancelled` is the normal result of unsubscribing;
 * `error` carries a stable BridgeError code. */
export type LogClosedReason = "eof" | "cancelled" | "error";

/** Callbacks a log subscriber provides. `onLines` receives an already-batched
 * chunk of lines (the bridge coalesces high-volume output). */
export interface LogHandlers {
  onLines: (lines: string[]) => void;
  onClosed?: (reason: LogClosedReason, error?: BridgeError) => void;
}

/** A live log subscription handle. `close` cancels the bridge stream and stops
 * delivering to the handlers. */
export interface LogSubscription {
  readonly id: string;
  close: () => Promise<void>;
}

/** A transition-triggered desktop notification. `key` deduplicates repeats. */
export interface DesktopNotification {
  /** Stable dedup/cooldown key: identifies the finding, not the occurrence. */
  key: string;
  title: string;
  body: string;
  severity: FindingSeverity;
  server: string;
}
