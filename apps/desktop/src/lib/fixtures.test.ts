import { describe, expect, it } from "vitest";
import { createFixtureDesktopAPI } from "./fixtures";

describe("fixture DesktopAPI", () => {
  const api = createFixtureDesktopAPI();

  it("returns servers with exactly one current", async () => {
    const servers = await api.listServers();
    expect(servers.length).toBeGreaterThan(0);
    expect(servers.filter((s) => s.current)).toHaveLength(1);
  });

  it("returns a reachable production snapshot", async () => {
    const snap = await api.getSnapshot("production");
    expect(snap.reachable).toBe(true);
    // Metrics are nullable (unavailable → null); the production fixture reports them.
    expect(snap.ramTotalBytes).not.toBeNull();
    expect(snap.ramTotalBytes!).toBeGreaterThan(snap.ramUsedBytes!);
  });

  it("models an unreachable edge server", async () => {
    const snap = await api.getSnapshot("edge");
    expect(snap.reachable).toBe(false);
    const findings = await api.runDiagnostics("edge");
    expect(findings.some((f) => f.severity === "critical")).toBe(true);
  });

  it("rejects unknown servers", async () => {
    await expect(api.getSnapshot("nope")).rejects.toThrow(/unknown fixture server/);
  });

  it("produces a structured operation result for an action", async () => {
    const result = await api.runAppAction({
      server: "production",
      app: "listmonk",
      action: "restart",
    });
    expect(result.status).toBe("succeeded");
    expect(result.changes).not.toHaveLength(0);
  });

  it("returns independent copies (no shared mutable state)", async () => {
    const a = await api.listServers();
    a[0].name = "mutated";
    const b = await api.listServers();
    expect(b[0].name).not.toBe("mutated");
  });

  it("streams recent log lines and closes a non-follow subscription", async () => {
    const lines: string[] = [];
    let closedReason = "";
    const sub = await api.subscribeLogs(
      { server: "production", target: "ghost", follow: false },
      { onLines: (l) => lines.push(...l), onClosed: (reason) => (closedReason = reason) },
    );
    await new Promise((r) => setTimeout(r, 5));
    expect(lines.some((l) => l.includes("Ghost"))).toBe(true);
    expect(closedReason).toBe("eof");
    await sub.close();
  });

  it("caps the requested tail at the loaded backlog", async () => {
    const lines: string[] = [];
    const sub = await api.subscribeLogs(
      { server: "production", target: "ghost", tail: 1 },
      { onLines: (l) => lines.push(...l) },
    );
    await new Promise((r) => setTimeout(r, 5));
    expect(lines).toHaveLength(1); // only the most recent line
    await sub.close();
  });
});
