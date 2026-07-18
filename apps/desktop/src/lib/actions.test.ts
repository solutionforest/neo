import { describe, expect, it } from "vitest";
import {
  ActionHistory,
  availabilityImpact,
  canRemember,
  confirmEveryTime,
  entryFromResult,
  makeOperationId,
  safetyClassOf,
  type HistoryStorage,
} from "./actions";
import type { OperationResult } from "./protocol";

describe("action helpers", () => {
  it("classifies actions by safety", () => {
    expect(safetyClassOf("start")).toBe("reversible");
    expect(safetyClassOf("restart")).toBe("reversible");
    expect(safetyClassOf("stop")).toBe("availability");
  });

  it("only confirms-every-time for availability-affecting actions", () => {
    expect(confirmEveryTime("stop")).toBe(true);
    expect(confirmEveryTime("start")).toBe(false);
    expect(canRemember("restart")).toBe(true);
    expect(canRemember("stop")).toBe(false);
  });

  it("describes the availability impact per action", () => {
    expect(availabilityImpact("stop", "ghost")).toContain("unavailable");
    expect(availabilityImpact("start", "ghost")).toContain("available");
  });

  it("generates a deterministic, collision-free operation id", () => {
    const a = makeOperationId("start", "web", 1, 100);
    const b = makeOperationId("start", "web", 2, 100);
    expect(a).not.toBe(b);
    expect(a).toContain("web");
    expect(a).toContain("start");
  });
});

describe("entryFromResult", () => {
  it("carries only workload identifiers and states (no secrets)", () => {
    const result: OperationResult = {
      operationId: "op-1",
      status: "succeeded",
      startedAt: "2026-07-18T09:00:00Z",
      finishedAt: "2026-07-18T09:00:01Z",
      summary: "started web",
      changes: [{ target: "web", from: "stopped", to: "running" }],
    };
    const e = entryFromResult("production", "web", "start", result);
    expect(e).toMatchObject({
      operationId: "op-1",
      server: "production",
      app: "web",
      action: "start",
      status: "succeeded",
      at: "2026-07-18T09:00:01Z",
    });
    // Only target/from/to fields — nothing that could be a secret.
    expect(Object.keys(e.changes[0]).sort()).toEqual(["from", "target", "to"]);
  });
});

class MemStorage implements HistoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) {
    return this.m.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
}

describe("ActionHistory", () => {
  const entry = (id: string) => ({
    operationId: id,
    server: "production",
    app: "web",
    action: "start" as const,
    status: "succeeded" as const,
    summary: "started web",
    at: "2026-07-18T09:00:00Z",
    changes: [],
  });

  it("keeps entries newest-first and persists them", () => {
    const store = new MemStorage();
    const h = new ActionHistory(store);
    h.add(entry("a"));
    h.add(entry("b"));
    expect(h.list().map((e) => e.operationId)).toEqual(["b", "a"]);

    // A fresh instance restores from storage.
    const h2 = new ActionHistory(store);
    expect(h2.list().map((e) => e.operationId)).toEqual(["b", "a"]);
  });

  it("caps the history so it cannot grow without bound", () => {
    const h = new ActionHistory(undefined, 3);
    for (const id of ["a", "b", "c", "d", "e"]) h.add(entry(id));
    expect(h.list().map((e) => e.operationId)).toEqual(["e", "d", "c"]);
  });
});
