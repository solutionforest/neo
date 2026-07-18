import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MetricCard } from "./MetricCard";

describe("MetricCard", () => {
  it("exposes warning meaning and meter value without relying on color", () => {
    render(
      <MetricCard
        label="Disk"
        value="82%"
        percent={82}
        tone="warning"
        icon="disk"
      />,
    );

    expect(screen.getByLabelText("warning level")).toHaveTextContent("▲");
    expect(screen.getByRole("meter", { name: "Disk usage" })).toHaveAttribute(
      "aria-valuenow",
      "82",
    );
  });

  it("clamps a visual gauge while preserving the reported meter value", () => {
    const { container } = render(
      <MetricCard label="CPU" value="120%" percent={120} icon="cpu" />,
    );

    expect(screen.getByRole("meter")).toHaveAttribute("aria-valuenow", "120");
    expect(container.querySelector<HTMLElement>(".metric-card__gauge-fill")?.style.width)
      .toBe("100%");
  });
});
