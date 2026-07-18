import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LogViewer } from "./LogViewer";
import { createFixtureDesktopAPI } from "../../lib/fixtures";
import type { AppSummary } from "../../lib/protocol";

const targets: AppSummary[] = [
  { id: "ghost", name: "ghost", image: "ghost:5", state: "running", kind: "app" },
  { id: "web", name: "web", image: "acme/web", state: "running", kind: "app" },
];

describe("LogViewer", () => {
  it("streams and renders the recent backlog for the selected workload", async () => {
    render(<LogViewer api={createFixtureDesktopAPI()} server="production" targets={targets} />);
    const body = screen.getByLabelText("Log output");
    await waitFor(() => expect(body).toHaveTextContent("Booting Ghost"));
    await waitFor(() => expect(screen.getByText(/lines$/)).toBeInTheDocument());
  });

  it("filters loaded lines locally without touching the server", async () => {
    const user = userEvent.setup();
    render(<LogViewer api={createFixtureDesktopAPI()} server="production" targets={targets} />);
    const body = screen.getByLabelText("Log output");
    await waitFor(() => expect(body).toHaveTextContent("Booting Ghost"));

    await user.type(screen.getByLabelText("Filter logs"), "assets");
    await waitFor(() => {
      expect(body).toHaveTextContent("screen.css");
      expect(body).not.toHaveTextContent("Booting Ghost");
    });
    // The count shows filtered / total when a filter is active.
    expect(screen.getByText(/\/\s*\d+ lines/)).toBeInTheDocument();
  });

  it("clears the loaded history", async () => {
    const user = userEvent.setup();
    render(<LogViewer api={createFixtureDesktopAPI()} server="production" targets={targets} />);
    const body = screen.getByLabelText("Log output");
    await waitFor(() => expect(body).toHaveTextContent("Booting Ghost"));

    await user.click(screen.getByRole("button", { name: "Clear" }));
    await waitFor(() => expect(body).not.toHaveTextContent("Booting Ghost"));
  });

  it("toggles pause", async () => {
    const user = userEvent.setup();
    render(<LogViewer api={createFixtureDesktopAPI()} server="production" targets={targets} follow />);
    await waitFor(() =>
      expect(screen.getByLabelText("Log output")).toHaveTextContent("Booting Ghost"),
    );

    const pause = screen.getByRole("button", { name: "Pause" });
    await user.click(pause);
    expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
  });

  it("shows an empty state when the server has no workloads", async () => {
    render(<LogViewer api={createFixtureDesktopAPI()} server="production" targets={[]} />);
    expect(screen.getByText("No workloads on this server.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause" })).toBeDisabled();
  });
});
