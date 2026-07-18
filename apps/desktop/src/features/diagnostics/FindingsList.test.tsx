import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FindingsList } from "./FindingsList";

afterEach(() => vi.useRealTimers());

describe("FindingsList", () => {
  it("shows evidence and the last observation time", () => {
    vi.setSystemTime(new Date("2026-07-18T09:05:00Z"));
    render(
      <FindingsList
        findings={[
          {
            id: "cpu_usage",
            rule: "cpu_usage",
            severity: "warning",
            summary: "CPU usage was observed at 82.0%",
            evidence: [
              { label: "CPU usage", value: "82.0%" },
              { label: "Persistence", value: "3 consecutive samples" },
            ],
            firstObservedAt: "2026-07-18T09:00:00Z",
            lastObservedAt: "2026-07-18T09:02:00Z",
          },
        ]}
      />,
    );

    const finding = screen.getByRole("listitem");
    expect(within(finding).getByText(/CPU usage: 82.0%/)).toBeInTheDocument();
    const observed = within(finding).getByText("Last observed 3m ago");
    expect(observed).toHaveAttribute("datetime", "2026-07-18T09:02:00Z");
  });
});
