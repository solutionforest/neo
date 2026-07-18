import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DiagnosticBundlePanel } from "./DiagnosticBundlePanel";
import { ObservabilityLog, type VersionInfo } from "../../lib/observability";
import type { ServerSummary } from "../../lib/protocol";

const VERSIONS: VersionInfo = {
  desktopVersion: "0.1.0",
  bridgeVersion: "0.1.0",
  coreVersion: "0.2.0",
  protocolVersion: 1,
  commit: "abc123",
  platform: "darwin",
  arch: "arm64",
  activation: "active",
};

const SERVERS: ServerSummary[] = [
  { id: "prod", name: "production", host: "root@203.0.113.7", current: true },
];

function makeLog(): ObservabilityLog {
  const log = new ObservabilityLog({ clock: (() => { let t = 0; return () => (t += 1); })() });
  log.setVersions(VERSIONS);
  log.recordRequest("server_snapshot", 40, null);
  return log;
}

describe("DiagnosticBundlePanel", () => {
  it("previews a redacted bundle and exports on confirm", async () => {
    const user = userEvent.setup();
    const exportBundle = vi.fn().mockResolvedValue("/Users/me/Downloads/bundle.json");
    render(
      <DiagnosticBundlePanel
        servers={SERVERS}
        observability={makeLog()}
        versions={VERSIONS}
        now={() => "2026-07-18T08:00:00.000Z"}
        exportBundle={exportBundle}
      />,
    );

    await user.click(screen.getByRole("button", { name: /export diagnostic bundle/i }));

    const dialog = await screen.findByRole("dialog", { name: /diagnostic bundle preview/i });
    expect(dialog).toBeInTheDocument();

    const preview = screen.getByLabelText(/diagnostic bundle contents/i) as HTMLTextAreaElement;
    // Host address is masked; the raw IP never appears in the preview.
    expect(preview.value).not.toContain("203.0.113.7");
    expect(preview.value).toContain("[redacted]");
    // Exclusions are shown to the user before export.
    expect(screen.getByText("Private keys and their contents")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(exportBundle).toHaveBeenCalledTimes(1));
    const [filename, content] = exportBundle.mock.calls[0];
    expect(filename).toBe("neo-desktop-diagnostics-2026-07-18T08-00-00-000Z.json");
    expect(content).not.toContain("203.0.113.7");
    expect(await screen.findByText(/saved to/i)).toBeInTheDocument();
  });

  it("only includes server logs after the explicit opt-in", async () => {
    const user = userEvent.setup();
    render(
      <DiagnosticBundlePanel
        servers={SERVERS}
        observability={makeLog()}
        versions={VERSIONS}
        serverLogs={["password=topsecret in a log line"]}
        now={() => "2026-07-18T08:00:00.000Z"}
        exportBundle={vi.fn().mockResolvedValue(null)}
      />,
    );

    await user.click(screen.getByRole("button", { name: /export diagnostic bundle/i }));
    const preview = () => screen.getByLabelText(/diagnostic bundle contents/i) as HTMLTextAreaElement;

    // Off by default: no server log content, and the exclusion note is shown.
    expect(preview().value).not.toContain("a log line");
    expect(screen.getByText("Full server logs (not included)")).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: /include recent server logs/i }));

    // Now the log line is present — but still redacted line-by-line.
    expect(preview().value).toContain("a log line");
    expect(preview().value).not.toContain("topsecret");
  });
});
