import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatLatency,
  formatPercent,
  formatRelativeTime,
  usagePercent,
} from "./format";

describe("formatBytes", () => {
  it("formats binary sizes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(16 * 1024 * 1024 * 1024)).toBe("16.0 GB");
  });
  it("guards against invalid input", () => {
    expect(formatBytes(-5)).toBe("0 B");
    expect(formatBytes(NaN)).toBe("0 B");
  });
});

describe("usagePercent", () => {
  it("computes and clamps", () => {
    expect(usagePercent(1, 2)).toBe(50);
    expect(usagePercent(3, 2)).toBe(100);
    expect(usagePercent(1, 0)).toBe(0);
  });
});

describe("formatPercent", () => {
  it("uses one decimal below 10 and none above", () => {
    expect(formatPercent(4.25)).toBe("4.3%");
    expect(formatPercent(82.9)).toBe("83%");
  });
});

describe("formatLatency", () => {
  it("formats ms and seconds", () => {
    expect(formatLatency(0)).toBe("—");
    expect(formatLatency(84)).toBe("84 ms");
    expect(formatLatency(1500)).toBe("1.50 s");
  });
});

describe("formatRelativeTime", () => {
  const now = Date.parse("2026-07-18T09:00:00Z");
  it("bucketizes deltas", () => {
    expect(formatRelativeTime("2026-07-18T08:59:40Z", now)).toBe("just now");
    expect(formatRelativeTime("2026-07-18T08:57:00Z", now)).toBe("3m ago");
    expect(formatRelativeTime("2026-07-18T07:00:00Z", now)).toBe("2h ago");
    expect(formatRelativeTime("2026-07-14T09:00:00Z", now)).toBe("4d ago");
  });
  it("handles bad input", () => {
    expect(formatRelativeTime("not-a-date", now)).toBe("unknown");
  });
});
