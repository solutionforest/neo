import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Management } from "./Management";
import { createFixtureDesktopAPI } from "../lib/fixtures";

describe("Management window", () => {
  it("renders the overview and the full application table", async () => {
    render(<Management api={createFixtureDesktopAPI()} />);

    const apps = await screen.findByRole("table");
    // production fixture has 5 apps (one stopped).
    const rows = within(apps).getAllByRole("row");
    expect(rows).toHaveLength(1 + 5); // header + 5

    expect(screen.getByRole("region", { name: "Overview" })).toBeInTheDocument();
    await waitFor(() =>
      expect(within(apps).getByText("listmonk")).toBeInTheDocument(),
    );
  });

  it("runs a lifecycle action through the confirmation dialog and records history", async () => {
    render(<Management api={createFixtureDesktopAPI()} />);

    const apps = await screen.findByRole("table");
    // listmonk is stopped in the fixture, so it offers a Start button.
    const row = (await within(apps).findByText("listmonk")).closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: "Start" }));

    // Confirmation dialog for the reversible start action.
    const dialog = await screen.findByRole("dialog", { name: "Start listmonk" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Start" }));

    // The action completes and the result summary is shown.
    await waitFor(() =>
      expect(screen.getByText(/start listmonk on production/)).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    // The action was recorded in the local history.
    const history = screen.getByRole("region", { name: "Action history" });
    await waitFor(() =>
      expect(within(history).getAllByText("Start listmonk").length).toBeGreaterThan(0),
    );
  });

  it("shows the bridge.hello version surface in the About panel", async () => {
    render(<Management api={createFixtureDesktopAPI()} />);

    const about = screen.getByRole("region", { name: "About" });
    // Fixture hello: desktop 0.1.0, bridge 0.1.0-fixture / core 0.0.0-dev,
    // commit fixture0000, protocol v1.
    await waitFor(() => expect(within(about).getByText("0.1.0")).toBeInTheDocument());
    expect(within(about).getByText("0.1.0-fixture (core 0.0.0-dev)")).toBeInTheDocument();
    expect(within(about).getByText("fixture0000")).toBeInTheDocument();
    expect(within(about).getByText("v1")).toBeInTheDocument();
  });
});
