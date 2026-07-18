import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopService, type ServiceDeps, type TimerHandle } from "./desktop-service";
import {
  BridgeError,
  type AggregateStatus,
  type AppSummary,
  type DesktopNotification,
  type Finding,
  type ServerSnapshot,
  type ServerSummary,
} from "./protocol";
import type { DesktopAPI } from "./desktop-api";

// --- deterministic time + scheduler ---------------------------------------

interface FakeTimer {
  id: number;
  due: number;
  fn: () => void;
}

/** Virtual clock + scheduler so every timing rule is asserted deterministically. */
class FakeScheduler {
  now = 0;
  private seq = 0;
  private timers: FakeTimer[] = [];

  clock = (): number => this.now;
  setTimer = (fn: () => void, ms: number): TimerHandle => {
    const id = ++this.seq;
    this.timers.push({ id, due: this.now + ms, fn });
    return id;
  };
  clearTimer = (h: TimerHandle): void => {
    this.timers = this.timers.filter((t) => t.id !== h);
  };

  /** Advance virtual time, firing due timers in order, draining async work. */
  async advance(ms: number): Promise<void> {
    const target = this.now + ms;
    for (;;) {
      const next = this.timers
        .filter((t) => t.due <= target)
        .sort((a, b) => a.due - b.due)[0];
      if (!next) break;
      this.timers = this.timers.filter((t) => t.id !== next.id);
      this.now = next.due;
      next.fn();
      await flush();
    }
    this.now = target;
    await flush();
  }

  /** Earliest scheduled delay from now, for asserting cadence. */
  nextDelay(): number | null {
    if (this.timers.length === 0) return null;
    return Math.min(...this.timers.map((t) => t.due)) - this.now;
  }
}

/** Drain the microtask + macrotask queue so injected-promise polls settle. */
const flush = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

// --- controllable API ------------------------------------------------------

function snap(server: ServerSummary, reachable: boolean): ServerSnapshot {
  return {
    server,
    reachable,
    observedAt: "2026-07-18T09:00:00Z",
    latencyMs: 20,
    cpuPercent: 10,
    ramUsedBytes: 1,
    ramTotalBytes: 2,
    diskUsedBytes: 1,
    diskTotalBytes: 2,
    uptimeSeconds: 1,
    apps: { running: 1, stopped: 0, total: 1 },
    services: { running: 1, stopped: 0, total: 1 },
  };
}

function app(id: string, state: AppSummary["state"]): AppSummary {
  return { id, name: id, image: "img:1", state, kind: "app" };
}

function finding(
  id: string,
  severity: Finding["severity"],
  rule = "test",
): Finding {
  return {
    id,
    rule,
    severity,
    summary:
      rule === "server_reachability"
        ? "Server was unreachable"
        : `${id} finding`,
    evidence: [],
    firstObservedAt: "2026-07-18T09:00:00Z",
    lastObservedAt: "2026-07-18T09:00:00Z",
  };
}

interface ApiControl {
  api: DesktopAPI;
  snapshotCalls: Record<string, number>;
  peakConcurrency: number;
  setReachable(server: string, v: boolean): void;
  setApps(server: string, apps: AppSummary[]): void;
  setFindings(server: string, f: Finding[]): void;
  setError(server: string, e: BridgeError | null): void;
  /** When gated, snapshot calls park until release() is called. */
  gate(on: boolean): void;
  release(): void;
}

function makeApi(servers: ServerSummary[]): ApiControl {
  const reachable: Record<string, boolean> = {};
  const apps: Record<string, AppSummary[]> = {};
  const findings: Record<string, Finding[]> = {};
  const errors: Record<string, BridgeError | null> = {};
  const snapshotCalls: Record<string, number> = {};
  for (const s of servers) {
    reachable[s.id] = true;
    apps[s.id] = [];
    findings[s.id] = [];
    errors[s.id] = null;
    snapshotCalls[s.id] = 0;
  }

  const ctrl: ApiControl = {
    snapshotCalls,
    peakConcurrency: 0,
    setReachable: (s, v) => (reachable[s] = v),
    setApps: (s, a) => (apps[s] = a),
    setFindings: (s, f) => (findings[s] = f),
    setError: (s, e) => (errors[s] = e),
    gate: (on) => (gated = on),
    release: () => {
      const w = waiters;
      waiters = [];
      w.forEach((r) => r());
    },
    api: undefined as unknown as DesktopAPI,
  };

  let gated = false;
  let waiters: Array<() => void> = [];
  let live = 0;

  const barrier = async (): Promise<void> => {
    if (!gated) return;
    await new Promise<void>((resolve) => waiters.push(resolve));
  };

  ctrl.api = {
    hello: async () => ({
      protocolVersion: 1,
      bridgeVersion: "t",
      desktopVersion: "t",
      coreVersion: "t",
      platform: "test",
      arch: "test",
      activation: "active",
    }),
    listServers: async () => servers.slice(),
    getSnapshot: async (server: string) => {
      snapshotCalls[server] = (snapshotCalls[server] ?? 0) + 1;
      live++;
      ctrl.peakConcurrency = Math.max(ctrl.peakConcurrency, live);
      try {
        await barrier();
        const err = errors[server];
        if (err) throw err;
        const s = servers.find((x) => x.id === server)!;
        return snap(s, reachable[server]);
      } finally {
        live--;
      }
    },
    listApps: async (server: string) => apps[server] ?? [],
    runDiagnostics: async (server: string) => findings[server] ?? [],
    runAppAction: async () => ({
      operationId: "op",
      status: "succeeded",
      startedAt: "t",
      summary: "ok",
      changes: [],
    }),
    cancelOperation: async () => ({ found: false }),
    subscribeLogs: async () => ({ id: "noop", close: async () => {} }),
  };

  return ctrl;
}

function servers(...ids: string[]): ServerSummary[] {
  return ids.map((id, i) => ({
    id,
    name: id,
    host: `root@10.0.0.${i + 1}`,
    current: i === 0,
  }));
}

interface Harness {
  service: DesktopService;
  sched: FakeScheduler;
  ctrl: ApiControl;
  trayStates: AggregateStatus[];
  notes: DesktopNotification[];
}

function makeService(list: ServerSummary[], cfg?: ServiceDeps["config"], randomValue = 0): Harness {
  const sched = new FakeScheduler();
  const ctrl = makeApi(list);
  const trayStates: AggregateStatus[] = [];
  const notes: DesktopNotification[] = [];
  const service = new DesktopService({
    api: ctrl.api,
    clock: sched.clock,
    setTimer: sched.setTimer,
    clearTimer: sched.clearTimer,
    random: () => randomValue,
    onTray: (s) => trayStates.push(s),
    onNotify: (n) => notes.push(n),
    config: cfg,
  });
  return { service, sched, ctrl, trayStates, notes };
}

// --- tests -----------------------------------------------------------------

describe("DesktopService polling", () => {
  it("runs an initial scan of every configured server", async () => {
    const h = makeService(servers("a", "b", "c"));
    await h.service.start();
    await flush();
    expect(h.ctrl.snapshotCalls).toEqual({ a: 1, b: 1, c: 1 });
    expect(h.service.getState().initialScanComplete).toBe(true);
  });

  it("caps concurrent SSH snapshots at three", async () => {
    const h = makeService(servers("a", "b", "c", "d", "e"));
    h.ctrl.gate(true);
    await h.service.start();
    await flush();
    // Five servers want to poll at once, but only three may be in flight.
    expect(h.ctrl.peakConcurrency).toBe(3);
    h.ctrl.release();
    await flush();
    h.ctrl.release(); // drain the two that were queued
    await flush();
    expect(Object.values(h.ctrl.snapshotCalls)).toEqual([1, 1, 1, 1, 1]);
    expect(h.ctrl.peakConcurrency).toBe(3);
  });

  it("polls the selected visible server every 30s and others every 120s", async () => {
    const h = makeService(servers("a", "b"));
    await h.service.start();
    await flush();
    h.service.setVisible(true); // a is selected + visible → 30s cadence
    await flush();
    const before = { ...h.ctrl.snapshotCalls };

    await h.sched.advance(30_000);
    expect(h.ctrl.snapshotCalls.a).toBeGreaterThan(before.a); // selected refreshed
    // b (unselected) should not refresh at 30s — its cadence is 120s.
    expect(h.ctrl.snapshotCalls.b).toBe(before.b);

    await h.sched.advance(90_000); // now at 120s total
    expect(h.ctrl.snapshotCalls.b).toBeGreaterThan(before.b);
  });

  it("keeps polling while all windows are hidden", async () => {
    const h = makeService(servers("a"));
    await h.service.start();
    await flush();
    h.service.setVisible(false);
    const before = h.ctrl.snapshotCalls.a;
    // Hidden: cadence falls back to 120s but must still fire.
    await h.sched.advance(120_000);
    expect(h.ctrl.snapshotCalls.a).toBeGreaterThan(before);
  });

  it("adds at most 10% jitter to the cadence", async () => {
    const h = makeService(servers("a"), undefined, 1); // random()=1 → full jitter
    await h.service.start();
    await flush();
    h.service.setVisible(true);
    await flush();
    // Selected+visible base 30s, +10% → 33s scheduled next.
    expect(h.sched.nextDelay()).toBe(33_000);
  });

  it("backs an unreachable server off through 30/60/120/300s", async () => {
    const h = makeService(servers("a"), undefined, 0);
    h.ctrl.setError("a", new BridgeError("ssh_unreachable", "down", true));
    await h.service.start();
    await flush();
    // After the first failure the next poll is scheduled 30s out.
    expect(h.sched.nextDelay()).toBe(30_000);
    await h.sched.advance(30_000);
    expect(h.sched.nextDelay()).toBe(60_000);
    await h.sched.advance(60_000);
    expect(h.sched.nextDelay()).toBe(120_000);
    await h.sched.advance(120_000);
    expect(h.sched.nextDelay()).toBe(300_000);
    await h.sched.advance(300_000);
    expect(h.sched.nextDelay()).toBe(300_000); // saturates at 300s
  });

  it("recovers to the normal cadence after the server comes back", async () => {
    const h = makeService(servers("a"), undefined, 0);
    h.ctrl.setError("a", new BridgeError("ssh_unreachable", "down", true));
    await h.service.start();
    await flush();
    expect(h.sched.nextDelay()).toBe(30_000); // backoff
    h.ctrl.setError("a", null);
    h.service.setVisible(true);
    await h.sched.advance(30_000); // backed-off poll fires, succeeds
    expect(h.sched.nextDelay()).toBe(30_000); // selected+visible cadence, not backoff
  });
});

describe("DesktopService manual refresh", () => {
  it("bypasses backoff for one immediate poll", async () => {
    const h = makeService(servers("a"), undefined, 0);
    h.ctrl.setError("a", new BridgeError("ssh_unreachable", "down", true));
    await h.service.start();
    await flush();
    const before = h.ctrl.snapshotCalls.a;
    h.service.manualRefresh("a");
    await flush();
    expect(h.ctrl.snapshotCalls.a).toBe(before + 1); // fired now, not after 30s
  });

  it("debounces rapid repeated clicks", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush();
    const before = h.ctrl.snapshotCalls.a;
    h.service.manualRefresh("a");
    h.service.manualRefresh("a"); // within the debounce window → ignored
    h.service.manualRefresh("a");
    await flush();
    expect(h.ctrl.snapshotCalls.a).toBe(before + 1);
  });
});

describe("DesktopService cache + staleness", () => {
  it("keeps the last successful snapshot and marks it stale when offline", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush();
    const good = h.service.getState().servers[0];
    expect(good.snapshot).not.toBeNull();
    expect(good.stale).toBe(false);
    const cachedAt = good.lastSuccessAt;

    // Server goes offline: the cached snapshot survives, flagged stale.
    h.ctrl.setError("a", new BridgeError("ssh_unreachable", "down", true));
    h.service.manualRefresh("a");
    await flush();
    const offline = h.service.getState().servers[0];
    expect(offline.snapshot).not.toBeNull(); // last-known data retained
    expect(offline.stale).toBe(true);
    expect(offline.lastSuccessAt).toBe(cachedAt); // age anchored to last success
    expect(offline.status).toBe("critical");
  });
});

describe("DesktopService tray aggregation", () => {
  it("reflects the worst status across all servers", async () => {
    const h = makeService(servers("a", "b"), undefined, 0);
    h.ctrl.setFindings("b", [finding("warn", "warning")]);
    await h.service.start();
    await flush();
    expect(h.service.getState().aggregate).toBe("warning"); // a healthy, b warning

    h.ctrl.setError("a", new BridgeError("ssh_unreachable", "down", true));
    h.service.manualRefresh("a");
    await flush();
    expect(h.service.getState().aggregate).toBe("critical"); // a now unreachable
  });

  it("is unknown with no configured servers", async () => {
    const h = makeService([]);
    await h.service.start();
    await flush();
    expect(h.service.getState().aggregate).toBe("unknown");
    expect(h.trayStates[h.trayStates.length - 1]).toBe("unknown");
  });
});

describe("DesktopService notifications", () => {
  it("stays silent during the initial scan", async () => {
    const h = makeService(servers("a"));
    h.ctrl.setReachable("a", false); // unreachable from the very first observation
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    await h.service.start();
    await flush();
    expect(h.notes).toHaveLength(0); // first observation is baseline only
  });

  it("notifies on a reachable→unreachable transition after startup", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush(); // baseline: reachable

    h.ctrl.setReachable("a", false);
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    h.service.manualRefresh("a");
    await flush();
    expect(h.notes).toHaveLength(1);
    expect(h.notes[0].title).toContain("unreachable");
    expect(h.notes[0].severity).toBe("critical");
  });

  it("notifies on recovery", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush();
    h.ctrl.setReachable("a", false);
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    h.service.manualRefresh("a");
    await flush();
    h.ctrl.setReachable("a", true);
    h.ctrl.setFindings("a", []);
    h.sched.now += 400_000; // outside the reachability cooldown
    h.service.manualRefresh("a");
    await flush();
    expect(h.notes.map((n) => n.title).some((t) => t.includes("recovered"))).toBe(true);
  });

  it("notifies when an application transitions to stopped", async () => {
    const h = makeService(servers("a"), undefined, 0);
    h.ctrl.setApps("a", [app("web", "running")]);
    await h.service.start();
    await flush();
    h.ctrl.setApps("a", [app("web", "stopped")]);
    h.ctrl.setFindings("a", [finding("app_state:web", "warning", "app_state")]);
    h.service.manualRefresh("a");
    await flush();
    const appNote = h.notes.find((n) => n.key.includes("app_state:web"));
    expect(appNote).toBeDefined();
    expect(appNote!.severity).toBe("warning");
  });

  it("deduplicates repeated notifications within the cooldown", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush();
    h.ctrl.setReachable("a", false);
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    h.service.manualRefresh("a");
    await flush();
    expect(h.notes).toHaveLength(1);

    // Still unreachable on the next poll — no NEW transition, and even a
    // re-transition inside the 5-min window would be suppressed by cooldown.
    h.ctrl.setReachable("a", true);
    h.ctrl.setFindings("a", []);
    h.sched.now += 2_000;
    h.service.manualRefresh("a"); // recovery transition, but within cooldown
    await flush();
    h.ctrl.setReachable("a", false);
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    h.sched.now += 2_000;
    h.service.manualRefresh("a"); // unreachable again, still within cooldown
    await flush();
    expect(h.notes).toHaveLength(1); // deduped by the per-finding cooldown
  });

  it("fires again once the cooldown elapses", async () => {
    const h = makeService(servers("a"), undefined, 0);
    await h.service.start();
    await flush();
    h.ctrl.setReachable("a", false);
    h.ctrl.setFindings("a", [
      finding("server_reachability", "critical", "server_reachability"),
    ]);
    h.service.manualRefresh("a");
    await flush();
    expect(h.notes).toHaveLength(1);

    h.ctrl.setReachable("a", true);
    h.ctrl.setFindings("a", []);
    h.sched.now += 301_000; // past the 5-min cooldown
    h.service.manualRefresh("a");
    await flush();
    expect(h.notes).toHaveLength(2); // recovery allowed after cooldown
  });

  it("notifies when a persisted finding escalates from warning to critical", async () => {
    const h = makeService(servers("a"), undefined, 0);
    h.ctrl.setFindings("a", [finding("cpu_usage", "warning", "cpu_usage")]);
    await h.service.start();
    await flush(); // warning is the initial baseline and is silent

    h.ctrl.setFindings("a", [finding("cpu_usage", "critical", "cpu_usage")]);
    h.service.manualRefresh("a");
    await flush();

    expect(h.notes).toHaveLength(1);
    expect(h.notes[0].key).toContain("cpu_usage");
    expect(h.notes[0].severity).toBe("critical");
  });
});

describe("DesktopService single-instance", () => {
  it("does not multiply polling across multiple subscribers", async () => {
    const h = makeService(servers("a"), undefined, 0);
    // Three windows subscribe to the SAME service.
    const unsub1 = h.service.subscribe(() => {});
    const unsub2 = h.service.subscribe(() => {});
    const unsub3 = h.service.subscribe(() => {});
    await h.service.start();
    await flush();
    h.service.setVisible(true);
    await h.sched.advance(30_000);
    // One initial scan + one cadence poll = 2, regardless of subscriber count.
    expect(h.ctrl.snapshotCalls.a).toBe(2);
    unsub1();
    unsub2();
    unsub3();
  });
});

// Guard against a leaked real timer keeping vitest alive.
beforeEach(() => vi.useRealTimers());
afterEach(() => vi.clearAllTimers?.());
