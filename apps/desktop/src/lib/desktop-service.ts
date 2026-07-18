// The desktop application service — the SINGLE owner of periodic refresh.
//
// The plan (Phase 4, "Polling policy") requires exactly one layer to own the
// refresh timers so that opening several windows never multiplies SSH load. No
// React component starts its own interval; every window subscribes to this
// service instead (see getSharedService / useDesktopService). It:
//
//   * refreshes the selected server every 30s while a window is visible and
//     immediately when the popover opens;
//   * refreshes other servers every 120s;
//   * caps concurrent SSH snapshots at 3;
//   * adds up to 10% jitter so many clients do not poll in lockstep;
//   * backs an unreachable server off through 30/60/120/300s;
//   * lets a manual refresh bypass backoff once, while debouncing rapid clicks;
//   * caches the last successful snapshot and exposes its age when offline;
//   * aggregates every server into one tray state; and
//   * emits transition-only notifications with dedup and a per-finding cooldown,
//     staying silent until the initial scan of every server has completed.
//
// Everything time- or randomness-dependent is injected (clock, timers, random)
// so the behavior above is covered by deterministic unit tests.

import type { DesktopAPI } from "./desktop-api";
import {
  aggregateAll,
  aggregateStatus,
  BridgeError,
  type AggregateStatus,
  type AppSummary,
  type DesktopNotification,
  type Finding,
  type ServerSnapshot,
  type ServerSummary,
} from "./protocol";

/** Tunable policy. Defaults come straight from the plan's "Polling policy". */
export interface PollingConfig {
  selectedIntervalMs: number;
  otherIntervalMs: number;
  maxConcurrent: number;
  jitterRatio: number;
  /** Backoff ladder applied to an unreachable server, by consecutive failure. */
  backoffMs: number[];
  /** Per-finding notification cooldown. */
  notificationCooldownMs: number;
  /** Rapid manual refreshes inside this window collapse to one. */
  manualDebounceMs: number;
}

export const DEFAULT_POLLING_CONFIG: PollingConfig = {
  selectedIntervalMs: 30_000,
  otherIntervalMs: 120_000,
  maxConcurrent: 3,
  jitterRatio: 0.1,
  backoffMs: [30_000, 60_000, 120_000, 300_000],
  notificationCooldownMs: 300_000,
  manualDebounceMs: 1_000,
};

/** An opaque timer handle so tests can supply a virtual scheduler. */
export type TimerHandle = unknown;

export interface ServiceDeps {
  api: DesktopAPI;
  /** Current time in epoch milliseconds. */
  clock: () => number;
  setTimer: (fn: () => void, ms: number) => TimerHandle;
  clearTimer: (handle: TimerHandle) => void;
  /** [0,1) source for jitter; defaults to Math.random. */
  random?: () => number;
  /** Push the aggregate tray state to the native shell. */
  onTray?: (state: AggregateStatus, detail: TrayDetail) => void;
  /** Deliver a native notification. */
  onNotify?: (n: DesktopNotification) => void;
  /**
   * Whether this instance owns the periodic refresh timers. The popover passes
   * true; an on-demand window (management) passes false so it loads once and
   * refreshes manually without adding a second polling loop — this is how
   * "opening several windows does not multiply polling" is honored across
   * separate webview realms. Defaults to true.
   */
  periodic?: boolean;
  config?: Partial<PollingConfig>;
}

export interface TrayDetail {
  reachable: number;
  total: number;
  /** Human summary for the tray tooltip. */
  summary: string;
}

/** Per-server view state exposed to the UI. */
export interface ServerRuntime {
  server: ServerSummary;
  /** Last SUCCESSFUL snapshot (the cache), even while currently offline. */
  snapshot: ServerSnapshot | null;
  apps: AppSummary[];
  findings: Finding[];
  status: AggregateStatus;
  /** True when the most recent poll failed — the snapshot above is stale. */
  stale: boolean;
  /** Epoch ms of the last successful poll, for computing displayed age. */
  lastSuccessAt: number | null;
  error: BridgeError | null;
  /** Whether a poll is currently in flight for this server. */
  refreshing: boolean;
}

/** The full snapshot of service state handed to subscribers. */
export interface ServiceState {
  loading: boolean;
  servers: ServerRuntime[];
  selected: string;
  aggregate: AggregateStatus;
  initialScanComplete: boolean;
  error: string | null;
}

interface ServerCell extends ServerRuntime {
  consecutiveFailures: number;
  /** True once this server has been polled at least once (baseline captured). */
  scanned: boolean;
  timer: TimerHandle | null;
  /** Findings active at the previous poll, including their prior severity. */
  prevFindings: Map<string, Finding>;
}

type Listener = (state: ServiceState) => void;

export class DesktopService {
  private readonly api: DesktopAPI;
  private readonly clock: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => TimerHandle;
  private readonly clearTimer: (handle: TimerHandle) => void;
  private readonly random: () => number;
  private readonly onTray?: (state: AggregateStatus, detail: TrayDetail) => void;
  private readonly onNotify?: (n: DesktopNotification) => void;
  private readonly periodic: boolean;
  private readonly cfg: PollingConfig;

  private cells = new Map<string, ServerCell>();
  private order: string[] = [];
  private selected = "";
  private visible = false;
  private started = false;
  private loading = true;
  private error: string | null = null;
  private initialScanComplete = false;

  private inFlight = 0;
  private pollQueue: string[] = [];
  private queued = new Set<string>();

  private lastManualAt = new Map<string, number>();
  private lastNotifiedAt = new Map<string, number>();

  private listeners = new Set<Listener>();

  constructor(deps: ServiceDeps) {
    this.api = deps.api;
    this.clock = deps.clock;
    this.setTimer = deps.setTimer;
    this.clearTimer = deps.clearTimer;
    this.random = deps.random ?? Math.random;
    this.onTray = deps.onTray;
    this.onNotify = deps.onNotify;
    this.periodic = deps.periodic ?? true;
    this.cfg = { ...DEFAULT_POLLING_CONFIG, ...deps.config };
  }

  // --- lifecycle ----------------------------------------------------------

  /** Load the configured servers, run the initial scan, and start the timers. */
  async start(): Promise<void> {
    if (this.started) return;
    this.started = true;
    try {
      const servers = await this.api.listServers();
      this.order = servers.map((s) => s.id);
      for (const s of servers) this.cells.set(s.id, newCell(s));
      if (!this.selected) {
        this.selected = servers.find((s) => s.current)?.id ?? servers[0]?.id ?? "";
      }
    } catch (err) {
      this.error = errorMessage(err);
      this.loading = false;
      this.emit();
      return;
    }

    this.loading = false;
    if (this.order.length === 0) {
      this.initialScanComplete = true;
      this.pushTray();
    }
    this.emit();

    // Initial scan: poll every server once (concurrency-capped). Notifications
    // stay suppressed until this completes (plan: "no notifications until the
    // initial scan completes").
    for (const id of this.order) this.enqueuePoll(id);
  }

  /** Stop all timers and drop subscribers' scheduled work. */
  stop(): void {
    for (const cell of this.cells.values()) {
      if (cell.timer != null) this.clearTimer(cell.timer);
      cell.timer = null;
    }
    this.started = false;
  }

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    fn(this.snapshotState());
    return () => this.listeners.delete(fn);
  }

  getState(): ServiceState {
    return this.snapshotState();
  }

  // --- user intent --------------------------------------------------------

  /** Change the selected server and refresh it immediately. */
  select(id: string): void {
    if (id === this.selected) return;
    this.selected = id;
    this.emit();
    if (this.cells.has(id)) this.enqueuePoll(id, { immediate: true });
    this.reschedule(id);
  }

  /** Report window visibility. Becoming visible refreshes the selected server. */
  setVisible(visible: boolean): void {
    const wasVisible = this.visible;
    this.visible = visible;
    // The selected server's cadence depends on visibility, so re-time it.
    if (this.selected) this.reschedule(this.selected);
    if (visible && !wasVisible && this.selected) {
      this.enqueuePoll(this.selected, { immediate: true });
    }
  }

  /** The popover opened: make it visible and refresh the selected server now. */
  popoverOpened(): void {
    this.setVisible(true);
  }

  /**
   * Manual refresh. Bypasses backoff for one poll, but repeated clicks inside
   * `manualDebounceMs` collapse to a single request (plan: "Manual refresh
   * bypasses backoff once, but repeated clicks are debounced").
   */
  manualRefresh(id?: string): void {
    const target = id ?? this.selected;
    if (!target || !this.cells.has(target)) return;
    const now = this.clock();
    const last = this.lastManualAt.get(target) ?? -Infinity;
    if (now - last < this.cfg.manualDebounceMs) return; // debounced
    this.lastManualAt.set(target, now);
    this.enqueuePoll(target, { immediate: true, bypassBackoff: true });
  }

  // --- polling engine -----------------------------------------------------

  private enqueuePoll(id: string, opts: { immediate?: boolean; bypassBackoff?: boolean } = {}): void {
    const cell = this.cells.get(id);
    if (!cell) return;
    if (opts.bypassBackoff) cell.consecutiveFailures = 0;
    // A poll already running or queued for this server is enough; do not stack.
    if (this.queued.has(id) || cell.refreshing) return;

    // Cancel any pending timer — this poll supersedes it.
    if (cell.timer != null) {
      this.clearTimer(cell.timer);
      cell.timer = null;
    }

    this.queued.add(id);
    this.pollQueue.push(id);
    this.pump();
  }

  /** Start queued polls up to the concurrency cap. */
  private pump(): void {
    while (this.inFlight < this.cfg.maxConcurrent && this.pollQueue.length > 0) {
      const id = this.pollQueue.shift()!;
      this.queued.delete(id);
      void this.runPoll(id);
    }
  }

  private async runPoll(id: string): Promise<void> {
    const cell = this.cells.get(id);
    if (!cell) return;
    this.inFlight++;
    cell.refreshing = true;
    this.emit();

    try {
      const [snapshotResult, appsResult, findingsResult] = await Promise.allSettled([
        this.api.getSnapshot(id),
        this.api.listApps(id),
        this.api.runDiagnostics(id),
      ]);

      // Diagnostics remain useful when server.snapshot fails: the reachability
      // rule intentionally turns two failed attempts into a finding. Preserve
      // a previous finding set only when diagnostics itself was unavailable.
      const findings =
        findingsResult.status === "fulfilled" ? findingsResult.value : cell.findings;
      if (snapshotResult.status === "fulfilled" && appsResult.status === "fulfilled") {
        this.applySuccess(cell, snapshotResult.value, appsResult.value, findings);
      } else {
        const error =
          snapshotResult.status === "rejected"
            ? snapshotResult.reason
            : appsResult.status === "rejected"
              ? appsResult.reason
              : findingsResult.status === "rejected"
                ? findingsResult.reason
                : new Error("poll failed");
        this.applyFailure(cell, error, findings);
      }
    } catch (err) {
      this.applyFailure(cell, err, cell.findings);
    } finally {
      cell.refreshing = false;
      this.inFlight--;
      this.reschedule(id);
      this.afterPoll();
      this.emit();
      // Let any queued server take the freed slot.
      this.pump();
    }
  }

  private applySuccess(
    cell: ServerCell,
    snapshot: ServerSnapshot,
    apps: AppSummary[],
    findings: Finding[],
  ): void {
    const reachable = snapshot.reachable;
    // Detect transitions against the previous poll BEFORE overwriting baseline.
    this.detectTransitions(cell, { reachable, apps, findings });

    cell.snapshot = snapshot;
    cell.apps = apps;
    cell.findings = findings;
    cell.error = null;
    cell.stale = !reachable;
    if (reachable) cell.lastSuccessAt = this.clock();
    cell.consecutiveFailures = reachable ? 0 : cell.consecutiveFailures + 1;
    cell.status = aggregateStatus(snapshot, findings);
    this.markScanned(cell);
  }

  private applyFailure(cell: ServerCell, err: unknown, findings: Finding[]): void {
    const bridgeErr = err instanceof BridgeError ? err : null;
    // A failed poll means the server is unreachable now; detect that transition
    // (the cached snapshot, if any, stays for display but is marked stale).
    this.detectTransitions(cell, { reachable: false, apps: cell.apps, findings, failed: true });

    cell.error = bridgeErr;
    cell.findings = findings;
    cell.stale = true;
    cell.consecutiveFailures += 1;
    cell.status = "critical"; // unreachable server
    this.markScanned(cell);
  }

  private markScanned(cell: ServerCell): void {
    if (cell.scanned) return;
    cell.scanned = true;
    if (!this.initialScanComplete && this.order.every((id) => this.cells.get(id)?.scanned)) {
      this.initialScanComplete = true;
    }
  }

  private afterPoll(): void {
    this.pushTray();
  }

  /** (Re)arm the periodic timer for one server according to its cadence. */
  private reschedule(id: string): void {
    const cell = this.cells.get(id);
    if (!cell || !this.started || !this.periodic) return;
    if (cell.timer != null) {
      this.clearTimer(cell.timer);
      cell.timer = null;
    }
    const delay = this.nextDelay(id, cell);
    cell.timer = this.setTimer(() => {
      cell.timer = null;
      this.enqueuePoll(id);
    }, delay);
  }

  /** Compute the next poll delay: backoff if failing, otherwise the cadence for
   * this server (30s selected-while-visible, else 120s), plus up to 10% jitter. */
  private nextDelay(id: string, cell: ServerCell): number {
    let base: number;
    if (cell.consecutiveFailures > 0) {
      const ladder = this.cfg.backoffMs;
      base = ladder[Math.min(cell.consecutiveFailures - 1, ladder.length - 1)];
    } else if (id === this.selected && this.visible) {
      base = this.cfg.selectedIntervalMs;
    } else {
      base = this.cfg.otherIntervalMs;
    }
    return Math.round(base * (1 + this.random() * this.cfg.jitterRatio));
  }

  // --- notifications ------------------------------------------------------

  private detectTransitions(
    cell: ServerCell,
    next: { reachable: boolean; apps: AppSummary[]; findings: Finding[]; failed?: boolean },
  ): void {
    const notes: DesktopNotification[] = [];
    const name = cell.server.name;

    // Finding transitions are the source of notification truth. This prevents
    // resource/reachability alerts from firing before their rule persistence
    // is satisfied and catches warning→critical escalation for a stable ID.
    const current = new Map(next.findings.map((finding) => [finding.id, finding]));
    for (const f of next.findings) {
      const previous = cell.prevFindings.get(f.id);
      const newlyCritical =
        f.severity === "critical" && previous?.severity !== "critical";
      const newlyBadWorkload =
        (f.rule === "app_state" || f.rule === "service_state") && previous === undefined;
      if (newlyCritical || newlyBadWorkload) {
        notes.push({
          key: `${cell.server.id}:finding:${f.id}`,
          title: `${name}: ${f.summary}`,
          body: findingEvidence(f),
          severity: f.severity,
          server: cell.server.id,
        });
      }
    }

    // A reachability finding disappearing is the persisted recovery transition.
    for (const previous of cell.prevFindings.values()) {
      if (previous.rule === "server_reachability" && !current.has(previous.id)) {
        notes.push({
          key: `${cell.server.id}:finding:${previous.id}`,
          title: `${name} recovered`,
          body: `${name} is reachable again.`,
          severity: "info",
          server: cell.server.id,
        });
      }
    }

    // Update baselines for the next comparison.
    cell.prevFindings = current;

    // Suppress everything until the initial scan of ALL servers has completed:
    // the first observation only establishes the baseline.
    if (!this.initialScanComplete) return;
    for (const note of notes) this.deliver(note);
  }

  /** Deliver a notification unless an identical one fired within the cooldown. */
  private deliver(note: DesktopNotification): void {
    const now = this.clock();
    const last = this.lastNotifiedAt.get(note.key);
    if (last !== undefined && now - last < this.cfg.notificationCooldownMs) {
      return; // deduped within cooldown
    }
    this.lastNotifiedAt.set(note.key, now);
    this.onNotify?.(note);
  }

  // --- tray + store -------------------------------------------------------

  private pushTray(): void {
    if (!this.onTray) return;
    const { aggregate, detail } = this.aggregate();
    this.onTray(aggregate, detail);
  }

  private aggregate(): { aggregate: AggregateStatus; detail: TrayDetail } {
    const cells = this.order.map((id) => this.cells.get(id)!).filter(Boolean);
    const statuses = cells.map((c) => this.statusOf(c));
    const aggregate = aggregateAll(statuses);
    const reachable = cells.filter((c) => !c.stale && c.snapshot?.reachable).length;
    return {
      aggregate,
      detail: {
        reachable,
        total: cells.length,
        summary: traySummary(aggregate, reachable, cells.length),
      },
    };
  }

  private statusOf(cell: ServerCell): AggregateStatus {
    if (!cell.scanned && !cell.snapshot) return "unknown";
    return cell.status;
  }

  private snapshotState(): ServiceState {
    const servers = this.order
      .map((id) => this.cells.get(id))
      .filter((c): c is ServerCell => Boolean(c))
      .map(toRuntime);
    return {
      loading: this.loading,
      servers,
      selected: this.selected,
      aggregate: this.aggregate().aggregate,
      initialScanComplete: this.initialScanComplete,
      error: this.error,
    };
  }

  private emit(): void {
    const state = this.snapshotState();
    for (const fn of this.listeners) fn(state);
  }
}

// --- helpers ---------------------------------------------------------------

function newCell(server: ServerSummary): ServerCell {
  return {
    server,
    snapshot: null,
    apps: [],
    findings: [],
    status: "unknown",
    stale: false,
    lastSuccessAt: null,
    error: null,
    refreshing: false,
    consecutiveFailures: 0,
    scanned: false,
    timer: null,
    prevFindings: new Map(),
  };
}

function toRuntime(cell: ServerCell): ServerRuntime {
  return {
    server: cell.server,
    snapshot: cell.snapshot,
    apps: cell.apps,
    findings: cell.findings,
    status: !cell.scanned && !cell.snapshot ? "unknown" : cell.status,
    stale: cell.stale,
    lastSuccessAt: cell.lastSuccessAt,
    error: cell.error,
    refreshing: cell.refreshing,
  };
}

function findingEvidence(finding: Finding): string {
  if (finding.evidence.length === 0) return finding.summary;
  return finding.evidence.map((item) => `${item.label}: ${item.value}`).join(" · ");
}

function traySummary(aggregate: AggregateStatus, reachable: number, total: number): string {
  if (total === 0) return "No servers configured";
  const health =
    aggregate === "healthy"
      ? "All systems healthy"
      : aggregate === "warning"
        ? "Warnings"
        : aggregate === "critical"
          ? "Critical"
          : "Checking…";
  return `${health} · ${reachable}/${total} reachable`;
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
