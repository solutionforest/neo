// Shared types between the desktop UI and the (future) neo-bridge sidecar.
// Phase 1 uses a fixture implementation; the transport is swapped in later.

export type Severity = "info" | "warning" | "critical";
export type TrayState = "healthy" | "warning" | "critical" | "unknown";

export interface BridgeHello {
  protocolVersion: number;
  bridgeVersion: string;
  cliCore: string;
  platform: string;
  activated: boolean;
}

export interface ServerSummary {
  name: string;
  host: string;
  port: number;
  keyPath: string;
  current: boolean;
}

export interface WorkloadCounts {
  total: number;
  running: number;
}

export interface ServerSnapshot {
  server: string;
  reachable: boolean;
  observedAt: string; // ISO
  latencyMs: number;
  cpuPercent: number;
  ramUsedBytes: number;
  ramTotalBytes: number;
  diskUsedBytes: number;
  diskTotalBytes: number;
  uptimeSeconds: number;
  apps: WorkloadCounts;
  services: WorkloadCounts;
}

export interface AppSummary {
  name: string;
  domain: string;
  status: "running" | "stopped" | "restarting" | "unhealthy";
  image: string;
}

export interface Finding {
  id: string;
  rule: string;
  severity: Severity;
  summary: string;
  lastObservedAt: string;
}

export interface AppActionInput {
  server: string;
  app: string;
  action: "start" | "stop" | "restart";
}

export interface OperationResult {
  operationId: string;
  status: "succeeded" | "failed";
  summary: string;
}

export interface DomainInput {
  server: string;
  app: string;
  domain: string;
  https: boolean;
}

export interface DesktopAPI {
  hello(): Promise<BridgeHello>;
  listServers(): Promise<ServerSummary[]>;
  getSnapshot(server: string): Promise<ServerSnapshot>;
  listApps(server: string): Promise<AppSummary[]>;
  runAppAction(input: AppActionInput): Promise<OperationResult>;
  runDiagnostics(server: string): Promise<Finding[]>;
  getLogs(server: string, app: string): Promise<string>;
  setDomain(input: DomainInput): Promise<{ status?: string }>;
  getSshKey(server: string): Promise<string>;
}
