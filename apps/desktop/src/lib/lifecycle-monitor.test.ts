import { describe, expect, it } from "vitest";
import { LifecycleMonitor, type RefreshTrigger } from "./lifecycle-monitor";

/**
 * A virtual scheduler with a wall clock that can be advanced two ways:
 *   - tick(ms): time passes normally and due timers fire (as when awake).
 *   - suspend(ms) + resume(): time jumps forward with timers FROZEN, then the
 *     overdue timers fire at the new wall time — modeling a laptop sleeping and
 *     waking, which is exactly what the monitor must detect.
 */
class FakeScheduler {
  wall = 0;
  private id = 1;
  private timers = new Map<number, { fireAt: number; fn: () => void }>();

  clock = () => this.wall;
  setTimer = (fn: () => void, ms: number): number => {
    const id = this.id++;
    this.timers.set(id, { fireAt: this.wall + ms, fn });
    return id;
  };
  clearTimer = (h: unknown): void => {
    this.timers.delete(h as number);
  };

  private fireDue(upTo: number): void {
    for (;;) {
      let next: [number, { fireAt: number; fn: () => void }] | null = null;
      for (const entry of this.timers) {
        if (entry[1].fireAt <= upTo && (!next || entry[1].fireAt < next[1].fireAt)) {
          next = entry;
        }
      }
      if (!next) return;
      // A timer never moves the clock backwards: on resume, an overdue timer
      // fires at the (already advanced) wall time, exactly as after a real wake.
      this.wall = Math.max(this.wall, next[1].fireAt);
      this.timers.delete(next[0]);
      next[1].fn();
    }
  }

  /** Time passes normally; due timers fire. */
  tick(ms: number): void {
    const target = this.wall + ms;
    this.fireDue(target);
    this.wall = target;
  }

  /** The machine sleeps: the wall clock advances but no timers fire. */
  suspend(ms: number): void {
    this.wall += ms;
  }

  /** The machine wakes: overdue timers now fire at the current wall time. */
  resume(): void {
    this.fireDue(this.wall);
  }
}

function makeMonitor(sched: FakeScheduler, triggers: RefreshTrigger[]) {
  return new LifecycleMonitor({
    clock: sched.clock,
    setTimer: sched.setTimer,
    clearTimer: sched.clearTimer,
    onRefresh: (t) => triggers.push(t),
    debounceMs: 2_000,
    heartbeatMs: 10_000,
    gapThresholdMs: 20_000,
  });
}

describe("LifecycleMonitor", () => {
  it("detects a sleep/wake gap and refreshes once", () => {
    const sched = new FakeScheduler();
    const triggers: RefreshTrigger[] = [];
    const monitor = makeMonitor(sched, triggers);
    monitor.start();

    sched.suspend(60_000); // laptop asleep for a minute
    sched.resume(); // heartbeat fires late → wake inferred
    sched.tick(2_000); // debounce elapses

    expect(triggers).toEqual(["wake"]);
    monitor.stop();
  });

  it("does not treat normal time passing as a wake", () => {
    const sched = new FakeScheduler();
    const triggers: RefreshTrigger[] = [];
    const monitor = makeMonitor(sched, triggers);
    monitor.start();

    sched.tick(10_000); // heartbeat fires on time
    sched.tick(10_000); // and again
    sched.tick(2_000);

    expect(triggers).toEqual([]);
    monitor.stop();
  });

  it("collapses a burst of transitions into a single refresh", () => {
    const sched = new FakeScheduler();
    const triggers: RefreshTrigger[] = [];
    const monitor = makeMonitor(sched, triggers);
    monitor.start();

    monitor.online();
    monitor.foreground();
    monitor.online();
    sched.tick(2_000);

    expect(triggers).toEqual(["online"]); // one refresh, most recent trigger
    monitor.stop();
  });

  it("ignores transitions before start and after stop", () => {
    const sched = new FakeScheduler();
    const triggers: RefreshTrigger[] = [];
    const monitor = makeMonitor(sched, triggers);

    monitor.online(); // not started yet
    monitor.start();
    monitor.online();
    monitor.stop(); // pending debounce cancelled
    sched.tick(5_000);

    expect(triggers).toEqual([]);
  });
});
