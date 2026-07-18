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

/** Allowlisted lifecycle actions for slice 1's fixture surface. Destructive
 * operations are intentionally absent (see plan "Fix safety classes"). */
export type AppAction = "start" | "stop" | "restart";

export interface AppActionInput {
  server: string;
  app: string;
  action: AppAction;
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
