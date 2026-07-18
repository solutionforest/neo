import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Popover } from "./Popover";
import { createFixtureDesktopAPI } from "../lib/fixtures";
import type { DesktopAPI } from "../lib/desktop-api";

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
});
