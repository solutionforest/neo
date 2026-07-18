import { beforeEach, describe, expect, it, vi } from "vitest";
import { LogController, type FlushScheduler } from "./log-controller";
import type { DesktopAPI } from "./desktop-api";
import { BridgeError, type LogHandlers, type LogSubscription } from "./protocol";

// A controllable fake transport: capture the handlers so a test can push lines
// and a close event by hand, and record whether close() was called.
function fakeApi() {
  let handlers: LogHandlers | null = null;
  const closed = { count: 0 };
  const sub: LogSubscription = {
    id: "sub-1",
    close: async () => {
      closed.count++;
    },
  };
  const api = {
    subscribeLogs: vi.fn(async (_input, h: LogHandlers) => {
      handlers = h;
      return sub;
    }),
  } as unknown as DesktopAPI;
  return {
    api,
    closed,
    emit: (lines: string[]) => handlers!.onLines(lines),
    close: (reason: Parameters<NonNullable<LogHandlers["onClosed"]>>[0], err?: BridgeError) =>
      handlers!.onClosed?.(reason, err),
  };
}

/** A manual flush scheduler so batching is observable without real timers. */
function manualScheduler(): FlushScheduler & { flush: () => void } {
  const queue: Array<() => void> = [];
  const schedule: FlushScheduler = (run) => queue.push(run);
  return Object.assign(schedule, {
    flush: () => {
      const pending = queue.splice(0);
      for (const fn of pending) fn();
    },
  });
}

describe("LogController", () => {
  let onChange: ReturnType<typeof vi.fn>;
  let scheduler: ReturnType<typeof manualScheduler>;

  beforeEach(() => {
    onChange = vi.fn();
    scheduler = manualScheduler();
  });

  function make(fake: ReturnType<typeof fakeApi>, maxLines?: number) {
    return new LogController({
      api: fake.api,
      input: { server: "prod", target: "ghost", follow: true },
      onChange,
      schedule: scheduler,
      maxLines,
    });
  }

  it("batches multiple bursts into a single change notification", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();

    fake.emit(["a", "b"]);
    fake.emit(["c"]);
    // Nothing rendered yet — the flush is coalesced.
    expect(onChange).not.toHaveBeenCalled();

    scheduler.flush();
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(c.getLines().map((l) => l.text)).toEqual(["a", "b", "c"]);
  });

  it("bounds history to maxLines, dropping the oldest", async () => {
    const fake = fakeApi();
    const c = make(fake, 3);
    await c.start();

    fake.emit(["1", "2", "3", "4", "5"]);
    scheduler.flush();

    expect(c.getLines().map((l) => l.text)).toEqual(["3", "4", "5"]);
  });

  it("assigns stable, monotonic keys across the cap", async () => {
    const fake = fakeApi();
    const c = make(fake, 2);
    await c.start();

    fake.emit(["1", "2", "3"]);
    scheduler.flush();
    const seqs = c.getLines().map((l) => l.seq);
    expect(seqs).toEqual([1, 2]); // 0 was dropped; keys never reused
  });

  it("holds lines while paused and flushes them on resume", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();

    fake.emit(["before"]);
    scheduler.flush();

    c.pause();
    fake.emit(["held-1", "held-2"]);
    scheduler.flush();

    // The visible view is frozen; the held lines are counted, not shown.
    expect(c.getLines().map((l) => l.text)).toEqual(["before"]);
    expect(c.pendingCount()).toBe(2);
    expect(c.isPaused()).toBe(true);

    c.resume();
    scheduler.flush();
    expect(c.getLines().map((l) => l.text)).toEqual(["before", "held-1", "held-2"]);
    expect(c.pendingCount()).toBe(0);
  });

  it("clears visible and held lines", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();

    fake.emit(["x"]);
    scheduler.flush();
    c.pause();
    fake.emit(["y"]);
    c.clear();
    scheduler.flush();

    expect(c.getLines()).toHaveLength(0);
    expect(c.pendingCount()).toBe(0);
  });

  it("records a terminal error close with its stable code", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();

    fake.close("error", new BridgeError("app_not_found", "gone"));
    scheduler.flush();

    expect(c.isStreamClosed()).toBe(true);
    expect(c.getCloseReason()).toBe("error");
    expect(c.getError()?.code).toBe("app_not_found");
  });

  it("cancels the subscription on close", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();
    await c.close();
    expect(fake.closed.count).toBe(1);
  });

  it("cancels a subscription that resolved after disposal", async () => {
    const fake = fakeApi();
    const c = make(fake);
    // Dispose before start resolves: the late subscription must be closed.
    const starting = c.start();
    await c.close();
    await starting;
    expect(fake.closed.count).toBe(1);
  });

  it("does not notify after disposal", async () => {
    const fake = fakeApi();
    const c = make(fake);
    await c.start();
    await c.close();
    fake.emit(["late"]);
    scheduler.flush();
    expect(onChange).not.toHaveBeenCalled();
  });
});
