// Detects the laptop scenarios from the plan's manual matrix — sleep/wake,
// offline/online, and VPN reconnects — and turns each into ONE debounced refresh
// (plan Phase 4 acceptance: "Sleep/wake and network reconnect trigger a
// debounced refresh").
//
// The web platform has no direct "woke from sleep" event, so wake is inferred:
// a self-scheduling heartbeat expects to fire every `heartbeatMs`; when the
// machine suspends, timers freeze, so on resume the observed gap is far larger
// than expected. That gap is the wake signal. Network transitions come from the
// host binding the browser's `online` event (and the popover regaining focus).
//
// A burst of transitions (wake → online → focus, common when a lid opens on a
// VPN) collapses into a single refresh via a trailing debounce, so the app never
// hammers SSH with several back-to-back scans. Everything is injected (clock,
// timers) so the behavior is covered by deterministic unit tests.

export type TimerHandle = unknown;

export type RefreshTrigger = "wake" | "online" | "foreground";

export interface LifecycleMonitorDeps {
  clock: () => number;
  setTimer: (fn: () => void, ms: number) => TimerHandle;
  clearTimer: (handle: TimerHandle) => void;
  /** Called once per collapsed burst with the most recent trigger. */
  onRefresh: (trigger: RefreshTrigger) => void;
  /** Trailing debounce window; a burst inside it yields one refresh. */
  debounceMs?: number;
  /** Heartbeat cadence used to infer a sleep gap. */
  heartbeatMs?: number;
  /** Observed gap beyond this (over the expected cadence) counts as a wake. */
  gapThresholdMs?: number;
}

export const DEFAULT_DEBOUNCE_MS = 2_000;
export const DEFAULT_HEARTBEAT_MS = 10_000;
/** Two missed heartbeats: enough to distinguish a real suspend from timer jitter. */
export const DEFAULT_GAP_THRESHOLD_MS = 20_000;

export class LifecycleMonitor {
  private readonly clock: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => TimerHandle;
  private readonly clearTimer: (handle: TimerHandle) => void;
  private readonly onRefresh: (trigger: RefreshTrigger) => void;
  private readonly debounceMs: number;
  private readonly heartbeatMs: number;
  private readonly gapThresholdMs: number;

  private heartbeat: TimerHandle | null = null;
  private debounce: TimerHandle | null = null;
  private pending: RefreshTrigger | null = null;
  private expectedAt = 0;
  private started = false;

  constructor(deps: LifecycleMonitorDeps) {
    this.clock = deps.clock;
    this.setTimer = deps.setTimer;
    this.clearTimer = deps.clearTimer;
    this.onRefresh = deps.onRefresh;
    this.debounceMs = deps.debounceMs ?? DEFAULT_DEBOUNCE_MS;
    this.heartbeatMs = deps.heartbeatMs ?? DEFAULT_HEARTBEAT_MS;
    this.gapThresholdMs = deps.gapThresholdMs ?? DEFAULT_GAP_THRESHOLD_MS;
  }

  /** Begin the sleep-detection heartbeat. Idempotent. */
  start(): void {
    if (this.started) return;
    this.started = true;
    this.armHeartbeat();
  }

  /** Stop all timers and drop any pending refresh. */
  stop(): void {
    this.started = false;
    if (this.heartbeat != null) this.clearTimer(this.heartbeat);
    if (this.debounce != null) this.clearTimer(this.debounce);
    this.heartbeat = null;
    this.debounce = null;
    this.pending = null;
  }

  /** The host observed the network coming back (browser `online`, VPN up). */
  online(): void {
    this.trigger("online");
  }

  /** The popover/window returned to the foreground. */
  foreground(): void {
    this.trigger("foreground");
  }

  private armHeartbeat(): void {
    this.expectedAt = this.clock() + this.heartbeatMs;
    this.heartbeat = this.setTimer(() => {
      const drift = this.clock() - this.expectedAt;
      // A large positive drift means the timer was frozen — the machine slept.
      if (drift > this.gapThresholdMs) this.trigger("wake");
      if (this.started) this.armHeartbeat();
    }, this.heartbeatMs);
  }

  /** Collapse a burst of transitions into one trailing refresh. */
  private trigger(reason: RefreshTrigger): void {
    if (!this.started) return;
    this.pending = reason;
    if (this.debounce != null) this.clearTimer(this.debounce);
    this.debounce = this.setTimer(() => {
      this.debounce = null;
      const trigger = this.pending ?? reason;
      this.pending = null;
      this.onRefresh(trigger);
    }, this.debounceMs);
  }
}
