import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Popover } from "./Popover";
import { createFixtureDesktopAPI } from "../lib/fixtures";
import type { DesktopAPI } from "../lib/desktop-api";
import { UpdateController, type UpdateBackend } from "../lib/update-controller";

function renderPopover(api: DesktopAPI = createFixtureDesktopAPI()) {
  return render(<Popover api={api} />);
}

describe("Popover", () => {
  it("renders the brand, aggregate status, and metric cards for the default server", async () => {
    renderPopover();
    expect(screen.getByText("Neo Desktop")).toBeInTheDocument();

    // production has a stopped app → warning aggregate.
    await waitFor(() =>
      expect(screen.getByRole("status", { name: /Status:/ })).toHaveTextContent(
        "Warning",
      ),
    );

    for (const label of ["CPU", "RAM", "Disk", "Latency"]) {
      expect(screen.getByRole("group", { name: label })).toBeInTheDocument();
    }
    expect(screen.getByText("Reachable")).toBeInTheDocument();
  });

  it("caps the popover findings list at three", async () => {
    // Build an API whose selected server has four findings.
    const base = createFixtureDesktopAPI();
    const api: DesktopAPI = {
      ...base,
      runDiagnostics: async () =>
        Array.from({ length: 4 }, (_, i) => ({
          id: `f${i}`,
          rule: "app_state",
          severity: "warning" as const,
          summary: `Finding ${i}`,
          evidence: [],
          firstObservedAt: "2026-07-18T09:00:00Z",
          lastObservedAt: "2026-07-18T09:00:00Z",
        })),
    };
    renderPopover(api);
    const list = await screen.findByRole("list", { name: "Findings" });
    // 3 shown + 1 "+N more" affordance.
    expect(within(list).getByText("+1 more in dashboard")).toBeInTheDocument();
    expect(within(list).getByText("Finding 0")).toBeInTheDocument();
    expect(within(list).queryByText("Finding 3")).not.toBeInTheDocument();
  });

  it("switches servers and reflects an unreachable, critical server", async () => {
    const user = userEvent.setup();
    renderPopover();
    await screen.findByText("Reachable");

    await user.selectOptions(screen.getByLabelText("Select server"), "edge");

    await waitFor(() =>
      expect(screen.getByText("Unreachable")).toBeInTheDocument(),
    );
    expect(screen.getByRole("status", { name: /Status:/ })).toHaveTextContent(
      "Critical",
    );
  });

  it("marks an offline server's data as stale", async () => {
    const user = userEvent.setup();
    renderPopover();
    await screen.findByText("Reachable");

    // edge is unreachable in the fixtures → its cached data is stale.
    await user.selectOptions(screen.getByLabelText("Select server"), "edge");
    await waitFor(() => expect(screen.getByText("Stale")).toBeInTheDocument());
  });

  it("manual refresh re-requests the snapshot", async () => {
    const user = userEvent.setup();
    const base = createFixtureDesktopAPI();
    let snapshotCalls = 0;
    const api: DesktopAPI = {
      ...base,
      getSnapshot: (server) => {
        snapshotCalls += 1;
        return base.getSnapshot(server);
      },
    };
    renderPopover(api);
    await screen.findByText("Reachable");
    const initial = snapshotCalls;

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(snapshotCalls).toBeGreaterThan(initial));
  });

  it("shows a useful empty state when no servers are configured", async () => {
    const base = createFixtureDesktopAPI();
    renderPopover({
      ...base,
      listServers: async () => [],
    });

    expect(
      await screen.findByRole("region", { name: "No configured servers" }),
    ).toHaveTextContent("Add a server with the Neo CLI, then refresh.");
    expect(screen.getByLabelText("Select server")).toBeDisabled();
  });

  it("prompts for an available update and defers on Later", async () => {
    const user = userEvent.setup();
    const backend: UpdateBackend = {
      check: vi.fn().mockResolvedValue({
        version: "0.2.0",
        notes: "Adds things.",
        downloadAndInstall: vi.fn(),
      }),
      relaunch: vi.fn(),
    };
    // Immediate first check so the test doesn't wait out the startup delay.
    const controller = new UpdateController(backend, { startupDelayMs: 0 });
    render(<Popover api={createFixtureDesktopAPI()} updater={controller} />);

    const banner = await screen.findByRole("status", { name: "Update available" });
    expect(within(banner).getByText(/0\.2\.0 is available/)).toBeInTheDocument();
    expect(within(banner).getByText("Adds things.")).toBeInTheDocument();

    await user.click(within(banner).getByRole("button", { name: "Later" }));
    expect(
      screen.queryByRole("status", { name: "Update available" }),
    ).not.toBeInTheDocument();
  });
});
