import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  UpdateController,
  type AvailableUpdate,
  type UpdateBackend,
} from "./update-controller";

const STARTUP = 1000;
const INTERVAL = 60_000;

function makeUpdate(version: string, overrides: Partial<AvailableUpdate> = {}): AvailableUpdate {
  return {
    version,
    notes: `Release notes for ${version}`,
    downloadAndInstall: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

function makeBackend(results: Array<AvailableUpdate | null | Error>): UpdateBackend & {
  check: ReturnType<typeof vi.fn>;
  relaunch: ReturnType<typeof vi.fn>;
} {
  let call = 0;
  return {
    check: vi.fn().mockImplementation(() => {
      const result = results[Math.min(call++, results.length - 1)];
      return result instanceof Error ? Promise.reject(result) : Promise.resolve(result);
    }),
    relaunch: vi.fn().mockResolvedValue(undefined),
  };
}

function controller(backend: UpdateBackend) {
  return new UpdateController(backend, { startupDelayMs: STARTUP, intervalMs: INTERVAL });
}

/** Flush pending microtasks so async check results settle under fake timers. */
async function flush(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

describe("UpdateController", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("checks silently after the startup delay, then on the six-hour interval", async () => {
    const backend = makeBackend([null]);
    const c = controller(backend);
    c.start();

    expect(backend.check).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(STARTUP);
    expect(backend.check).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(INTERVAL * 2);
    expect(backend.check).toHaveBeenCalledTimes(3);
    expect(c.getState().phase).toBe("idle");
    c.stop();
  });

  it("start() is idempotent and stop() cancels the schedule", async () => {
    const backend = makeBackend([null]);
    const c = controller(backend);
    c.start();
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);
    expect(backend.check).toHaveBeenCalledTimes(1);

    c.stop();
    await vi.advanceTimersByTimeAsync(INTERVAL * 5);
    expect(backend.check).toHaveBeenCalledTimes(1);
  });

  it("prompts with version and notes when an update is found", async () => {
    const update = makeUpdate("0.2.0");
    const c = controller(makeBackend([update]));
    const seen: string[] = [];
    c.subscribe((s) => seen.push(s.phase));

    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);

    const state = c.getState();
    expect(state.phase).toBe("prompt");
    expect(state.update?.version).toBe("0.2.0");
    expect(state.update?.notes).toContain("0.2.0");
    expect(seen).toContain("prompt");
    // Prompt only — nothing downloads until the user confirms.
    expect(update.downloadAndInstall).not.toHaveBeenCalled();
    c.stop();
  });

  it("does not re-prompt a deferred version during the same session", async () => {
    const c = controller(makeBackend([makeUpdate("0.2.0")]));
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);
    expect(c.getState().phase).toBe("prompt");

    c.defer();
    expect(c.getState().phase).toBe("idle");

    // Later checks keep finding 0.2.0 — the prompt must not come back.
    await vi.advanceTimersByTimeAsync(INTERVAL * 3);
    expect(c.getState().phase).toBe("idle");
    c.stop();
  });

  it("prompts again for a different version after a deferral", async () => {
    const c = controller(makeBackend([makeUpdate("0.2.0"), makeUpdate("0.3.0")]));
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);
    c.defer();

    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(c.getState().phase).toBe("prompt");
    expect(c.getState().update?.version).toBe("0.3.0");
    c.stop();
  });

  it("swallows check failures silently", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const c = controller(makeBackend([new Error("offline"), null]));
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);
    expect(c.getState().phase).toBe("idle");
    expect(c.getState().error).toBeNull();
    // The schedule keeps running after a failure.
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(c.getState().phase).toBe("idle");
    warn.mockRestore();
    c.stop();
  });

  it("install downloads, then relaunches", async () => {
    const update = makeUpdate("0.2.0");
    const backend = makeBackend([update]);
    const c = controller(backend);
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);

    await c.install();
    expect(update.downloadAndInstall).toHaveBeenCalledTimes(1);
    expect(backend.relaunch).toHaveBeenCalledTimes(1);
    expect(c.getState().phase).toBe("restarting");
    c.stop();
  });

  it("a failed install (e.g. bad signature fails closed) lands in the error phase", async () => {
    const update = makeUpdate("0.2.0", {
      downloadAndInstall: vi.fn().mockRejectedValue(new Error("signature verification failed")),
    });
    const backend = makeBackend([update]);
    const c = controller(backend);
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);

    await c.install();
    expect(backend.relaunch).not.toHaveBeenCalled();
    expect(c.getState().phase).toBe("error");
    expect(c.getState().error).toContain("signature");

    c.dismissError();
    expect(c.getState().phase).toBe("idle");
    c.stop();
  });

  it("a background check never clobbers an active prompt", async () => {
    const first = makeUpdate("0.2.0");
    const c = controller(makeBackend([first, makeUpdate("0.3.0")]));
    c.start();
    await vi.advanceTimersByTimeAsync(STARTUP);
    expect(c.getState().update?.version).toBe("0.2.0");

    await vi.advanceTimersByTimeAsync(INTERVAL * 2);
    // Still the original prompt — the user hasn't answered it yet.
    expect(c.getState().phase).toBe("prompt");
    expect(c.getState().update?.version).toBe("0.2.0");
    c.stop();
  });

  it("checkSilently is a no-op while a check is already in flight", async () => {
    let resolveCheck: (u: AvailableUpdate | null) => void = () => {};
    const backend: UpdateBackend = {
      check: vi.fn().mockImplementation(
        () => new Promise<AvailableUpdate | null>((res) => (resolveCheck = res)),
      ),
      relaunch: vi.fn(),
    };
    const c = controller(backend);
    void c.checkSilently();
    void c.checkSilently();
    expect(backend.check).toHaveBeenCalledTimes(1);
    resolveCheck(null);
    await flush();
  });

  it("reports checks and flags signature failures to the observer", async () => {
    const observer = { recordUpdateCheck: vi.fn(), recordUpdateFailure: vi.fn() };
    const badSignature = makeUpdate("0.3.0", {
      downloadAndInstall: vi
        .fn()
        .mockRejectedValue(new Error("signature verification failed")),
    });
    const backend = makeBackend([badSignature]);
    const c = new UpdateController(backend, {
      startupDelayMs: STARTUP,
      intervalMs: INTERVAL,
      observer,
    });

    await c.checkSilently();
    expect(observer.recordUpdateCheck).toHaveBeenCalledWith(true, "0.3.0");

    await c.install();
    expect(observer.recordUpdateFailure).toHaveBeenCalledWith(
      "signature verification failed",
    );
    expect(c.getState().phase).toBe("error");
    c.stop();
  });
});
