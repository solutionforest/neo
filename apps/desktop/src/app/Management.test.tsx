import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
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
});
