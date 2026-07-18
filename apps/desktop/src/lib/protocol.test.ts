import { describe, expect, it } from "vitest";
import {
  aggregateStatus,
  BridgeError,
  ERROR_CODES,
  PROTOCOL_VERSION,
  type Finding,
  type ServerSnapshot,
} from "./protocol";

function snapshot(reachable: boolean): ServerSnapshot {
  return {
    server: { id: "s", name: "s", host: "root@h", current: true },
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

function finding(severity: Finding["severity"]): Finding {
  return {
    id: severity,
    rule: "test",
    severity,
    summary: "s",
    evidence: [],
    firstObservedAt: "2026-07-18T09:00:00Z",
    lastObservedAt: "2026-07-18T09:00:00Z",
  };
}

describe("aggregateStatus", () => {
  it("is unknown with no snapshot", () => {
    expect(aggregateStatus(null)).toBe("unknown");
    expect(aggregateStatus(undefined)).toBe("unknown");
  });

  it("is critical when unreachable regardless of findings", () => {
    expect(aggregateStatus(snapshot(false), [])).toBe("critical");
    expect(aggregateStatus(snapshot(false), [finding("info")])).toBe("critical");
  });

  it("is critical when any finding is critical", () => {
    expect(aggregateStatus(snapshot(true), [finding("critical")])).toBe("critical");
    expect(
      aggregateStatus(snapshot(true), [finding("warning"), finding("critical")]),
    ).toBe("critical");
  });

  it("is warning when a warning finding exists but none critical", () => {
    expect(aggregateStatus(snapshot(true), [finding("warning")])).toBe("warning");
  });

  it("is healthy when reachable with only info findings", () => {
    expect(aggregateStatus(snapshot(true), [])).toBe("healthy");
    expect(aggregateStatus(snapshot(true), [finding("info")])).toBe("healthy");
  });
});

describe("protocol contract", () => {
  // These must stay in lockstep with the Go bridge (cmd/neo-bridge/protocol.go)
  // and the Rust shell (src-tauri/src/bridge.rs). See TestErrorCodesContract.
  it("pins the protocol version", () => {
    expect(PROTOCOL_VERSION).toBe(1);
  });

  it("mirrors the Go error-code set exactly", () => {
    expect([...ERROR_CODES]).toEqual([
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
    ]);
  });
});

describe("BridgeError", () => {
  it("carries a stable code and defaults", () => {
    const err = new BridgeError("ssh_unreachable", "nope", true);
    expect(err).toBeInstanceOf(Error);
    expect(err.code).toBe("ssh_unreachable");
    expect(err.retryable).toBe(true);
    expect(err.details).toEqual({});
  });
});
