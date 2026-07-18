import { describe, expect, it, vi } from "vitest";
import { ActionController } from "./action-controller";
import { ActionHistory } from "./actions";
import { BridgeError, type AppActionInput, type OperationResult } from "./protocol";
import type { DesktopAPI } from "./desktop-api";

const flush = () => new Promise((r) => setTimeout(r, 0));

const notImpl = () => Promise.reject(new Error("not implemented"));

/** A DesktopAPI whose runAppAction stays pending until the test resolves it, so
 * in-flight behavior (duplicate guard, cancel) is deterministic. */
function makeApi() {
  const calls: AppActionInput[] = [];
  const cancels: string[] = [];
  let resolveNext: ((r: OperationResult) => void) | null = null;
  let rejectNext: ((e: unknown) => void) | null = null;

  const api: DesktopAPI = {
    hello: notImpl,
    listServers: notImpl,
    getSnapshot: notImpl,
    listApps: notImpl,
    runDiagnostics: notImpl,
    subscribeLogs: notImpl as unknown as DesktopAPI["subscribeLogs"],
    runAppAction: (input) => {
      calls.push(input);
      return new Promise<OperationResult>((res, rej) => {
        resolveNext = res;
        rejectNext = rej;
      });
    },
    cancelOperation: async (id) => {
      cancels.push(id);
      return { found: true };
    },
  };

  return {
    api,
    calls,
    cancels,
    resolve: (r: OperationResult) => resolveNext?.(r),
    reject: (e: unknown) => rejectNext?.(e),
  };
}

function okResult(input: AppActionInput): OperationResult {
  return {
    operationId: input.operationId ?? "op",
    status: "succeeded",
    startedAt: "2026-07-18T09:00:00Z",
    finishedAt: "2026-07-18T09:00:01Z",
    summary: `${input.action} ${input.app}`,
    changes: [{ target: input.app, from: "stopped", to: "running" }],
  };
}

function newController(api: DesktopAPI, onSettled?: () => void) {
  return new ActionController({
    api,
    server: "production",
    clock: () => 1_700_000_000_000,
    history: new ActionHistory(),
    onSettled: onSettled ? () => onSettled() : undefined,
  });
}

describe("ActionController", () => {
  it("opens a confirmation dialog for a reversible action and runs it on confirm", async () => {
    const { api, calls, resolve } = makeApi();
    const settled = vi.fn();
    const c = newController(api, settled);

    c.request("web", "start");
    expect(c.getState().dialog?.phase).toBe("confirm");
    expect(c.getState().dialog?.safety).toBe("reversible");
    expect(c.getState().dialog?.canRemember).toBe(true);
    // Nothing runs until confirmed.
    expect(calls).toHaveLength(0);

    c.confirm();
    expect(c.getState().dialog?.phase).toBe("running");
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({ server: "production", app: "web", action: "start" });
    expect(calls[0].operationId).toBeTruthy();
    expect(c.getState().runningApps).toContain("web");

    resolve(okResult(calls[0]));
    await flush();

    expect(c.getState().dialog?.phase).toBe("done");
    expect(c.getState().dialog?.result?.status).toBe("succeeded");
    expect(c.getState().runningApps).not.toContain("web");
    expect(c.getState().history).toHaveLength(1);
    expect(c.getState().history[0]).toMatchObject({ app: "web", action: "start", status: "succeeded" });
    expect(settled).toHaveBeenCalledTimes(1); // immediate refresh
  });

  it("always confirms an availability-affecting stop and does not offer remember", () => {
    const { api } = makeApi();
    const c = newController(api);
    c.request("web", "stop");
    expect(c.getState().dialog?.safety).toBe("availability");
    expect(c.getState().dialog?.canRemember).toBe(false);
    // Attempting to set remember has no effect for stop.
    c.setRemember(true);
    expect(c.getState().dialog?.remember).toBe(false);
  });

  it("skips the dialog for a reversible action once its confirmation is remembered", async () => {
    const { api, calls, resolve } = makeApi();
    const c = newController(api);

    c.request("web", "restart");
    c.setRemember(true);
    expect(c.getState().dialog?.remember).toBe(true);
    c.confirm();
    resolve(okResult(calls[0]));
    await flush();
    c.dismiss();
    expect(c.getState().dialog).toBeNull();

    // Second time: runs immediately and silently — no dialog is opened.
    c.request("web", "restart");
    expect(c.getState().dialog).toBeNull();
    expect(c.getState().runningApps).toContain("web");
    expect(calls).toHaveLength(2);
  });

  it("prevents a duplicate concurrent action on the same app", () => {
    const { api, calls } = makeApi();
    const c = newController(api);

    c.request("web", "start");
    c.confirm(); // now running (promise pending)
    expect(c.getState().runningApps).toContain("web");

    // A second request while in flight is ignored — no second call.
    c.request("web", "restart");
    expect(calls).toHaveLength(1);
  });

  it("cancels an in-flight action via operation.cancel", async () => {
    const { api, calls, cancels, reject } = makeApi();
    const settled = vi.fn();
    const c = newController(api, settled);

    c.request("web", "start");
    c.confirm();
    const opId = calls[0].operationId!;

    c.cancel();
    expect(cancels).toEqual([opId]);

    // The bridge then rejects the in-flight action with operation_cancelled.
    reject(new BridgeError("operation_cancelled", "the action was cancelled"));
    await flush();

    expect(c.getState().dialog?.phase).toBe("done");
    expect(c.getState().history[0].status).toBe("cancelled");
    expect(settled).toHaveBeenCalledTimes(1);
  });

  it("surfaces a failure and records it in history", async () => {
    const { api, reject } = makeApi();
    const settled = vi.fn();
    const c = newController(api, settled);

    c.request("web", "start");
    c.confirm();
    reject(new BridgeError("internal_error", "docker refused"));
    await flush();

    const d = c.getState().dialog;
    expect(d?.phase).toBe("done");
    expect(d?.error?.code).toBe("internal_error");
    expect(c.getState().history[0].status).toBe("failed");
    expect(settled).toHaveBeenCalledTimes(1); // refresh even on failure
  });

  it("does not allow dismissing a running action", () => {
    const { api } = makeApi();
    const c = newController(api);
    c.request("web", "start");
    c.confirm();
    c.dismiss();
    expect(c.getState().dialog?.phase).toBe("running");
  });
});
