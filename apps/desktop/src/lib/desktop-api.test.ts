import { afterEach, describe, expect, it } from "vitest";
import { createDesktopAPI, isTauri } from "./desktop-api";

describe("transport selection", () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
  });

  it("reports not running under Tauri in a plain jsdom window", () => {
    expect(isTauri()).toBe(false);
  });

  it("falls back to the fixture provider outside Tauri", async () => {
    const api = await createDesktopAPI();
    const servers = await api.listServers();
    expect(servers.length).toBeGreaterThan(0);
  });

  it("uses fixtures under Tauri when the bridge is not opted in", async () => {
    // Simulate the Tauri webview but without VITE_USE_BRIDGE=true: slice 1 must
    // still render fixture data rather than reaching for a non-existent bridge.
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    expect(isTauri()).toBe(true);
    const api = await createDesktopAPI();
    const servers = await api.listServers();
    expect(servers.length).toBeGreaterThan(0);
  });
});
