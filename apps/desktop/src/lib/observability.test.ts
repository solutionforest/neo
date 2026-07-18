import { describe, expect, it } from "vitest";
import { ObservabilityLog, type VersionInfo } from "./observability";

function fixedClock(): () => number {
  let t = 1_000;
  return () => (t += 1);
}

const VERSIONS: VersionInfo = {
  desktopVersion: "0.1.0",
  bridgeVersion: "0.1.0",
  coreVersion: "0.2.0",
  protocolVersion: 1,
  commit: "abc123",
  platform: "darwin",
  arch: "arm64",
  activation: "active",
};

describe("ObservabilityLog", () => {
  it("records request method, duration, and code but never parameters", () => {
    const log = new ObservabilityLog({ clock: fixedClock() });
    log.recordRequest("server_snapshot", 42.7, null);
    log.recordRequest("app_action", 10, "ssh_auth_failed");

    const events = log.snapshot();
    expect(events).toHaveLength(2);
    expect(events[0]).toMatchObject({
      category: "request",
      fields: { method: "server_snapshot", durationMs: 43, code: null },
    });
    expect(events[1].fields).toMatchObject({
      method: "app_action",
      code: "ssh_auth_failed",
    });
    // No field should carry a server id or other parameter.
    for (const e of events) {
      expect(Object.keys(e.fields).sort()).toEqual(["code", "durationMs", "method"]);
    }
  });

  it("records bridge lifecycle, poll scheduling, cache age, and notifications", () => {
    const log = new ObservabilityLog({ clock: fixedClock() });
    log.recordBridge("restart", "attempt 2/3");
    log.recordPollScheduled(30_000);
    log.recordPollResult(false, 65_000);
    log.recordNotification("srv:finding:disk", "critical");

    const cats = log.snapshot().map((e) => e.category);
    expect(cats).toEqual(["bridge", "poll", "poll", "notification"]);
    expect(log.snapshot()[2].fields).toMatchObject({ ok: false, cacheAgeMs: 65_000 });
  });

  it("flags signature failures in update failures", () => {
    const log = new ObservabilityLog({ clock: fixedClock() });
    log.recordUpdateFailure("signature verification failed for the artifact");
    log.recordUpdateFailure("network unreachable");

    const [sig, net] = log.snapshot();
    expect(sig.fields.signatureFailure).toBe(true);
    expect(net.fields.signatureFailure).toBe(false);
  });

  it("stores versions and emits a handshake event", () => {
    const log = new ObservabilityLog({ clock: fixedClock() });
    log.setVersions(VERSIONS);
    expect(log.getVersions()).toEqual(VERSIONS);
    expect(log.snapshot()[0].category).toBe("bridge");
  });

  it("bounds memory to the ring-buffer capacity, dropping oldest", () => {
    const log = new ObservabilityLog({ capacity: 3, clock: fixedClock() });
    for (let i = 0; i < 10; i++) log.recordLifecycle(`t${i}`);
    const events = log.snapshot();
    expect(events).toHaveLength(3);
    expect(events.map((e) => e.fields.trigger)).toEqual(["t7", "t8", "t9"]);
  });

  it("returns defensive copies from snapshot", () => {
    const log = new ObservabilityLog({ clock: fixedClock() });
    log.recordLifecycle("wake");
    const snap = log.snapshot();
    snap[0].fields.trigger = "mutated";
    expect(log.snapshot()[0].fields.trigger).toBe("wake");
  });
});
