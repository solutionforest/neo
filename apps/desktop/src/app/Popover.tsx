import { useState } from "react";
import type { DesktopAPI } from "../lib/desktop-api";
import { openManagementWindow } from "../lib/host";
import {
  formatBytes,
  formatLatency,
  formatPercent,
  formatRelativeTime,
  usagePercent,
} from "../lib/format";
import { NeoLogo } from "../components/NeoLogo";
import { StatusBadge } from "../components/StatusBadge";
import { MetricCard, type MetricTone } from "../components/MetricCard";
import { ServerSelector } from "../features/servers/ServerSelector";
import { FindingsList } from "../features/diagnostics/FindingsList";
import { LogViewer } from "../features/logs/LogViewer";
import { UpdateBanner } from "../features/updates/UpdateBanner";
import type { UpdateController } from "../lib/update-controller";
import { statusFor, useServerData } from "./useServerData";
import { useUpdates } from "./useUpdates";

function tone(
  value: number | null | undefined,
  warn: number,
  crit: number,
): MetricTone {
  if (value == null) return "normal"; // unavailable metric — no alarm
  if (value >= crit) return "critical";
  if (value >= warn) return "warning";
  return "normal";
}

export function Popover({
  api,
  updater,
}: {
  api: DesktopAPI;
  /** Test seam: inject an UpdateController; production uses the Tauri backend. */
  updater?: UpdateController;
}) {
  // The popover is the always-alive menu-bar window: it owns the polling timers
  // and drives the native tray state + notifications (plan Phase 4), and it
  // owns the silent update-check schedule (plan Phase 6).
  const data = useServerData(api, { ownsTray: true });
  const updates = useUpdates(updater);
  // Recent logs are collapsed by default so the popover opens no SSH log stream
  // until the user asks — LogViewer only subscribes while mounted.
  const [logsOpen, setLogsOpen] = useState(false);
  const status = statusFor(data);
  const { snapshot } = data;
  const reachable = snapshot?.reachable ?? false;

  const ramPct = snapshot ? usagePercent(snapshot.ramUsedBytes, snapshot.ramTotalBytes) : 0;
  const diskPct = snapshot ? usagePercent(snapshot.diskUsedBytes, snapshot.diskTotalBytes) : 0;

  return (
    <div className="popover" data-status={status}>
      <header className="popover__header">
        <div className="popover__brand">
          <NeoLogo />
          <span className="popover__title">Neo Desktop</span>
        </div>
        <StatusBadge status={status} />
      </header>

      <UpdateBanner
        state={updates.state}
        onInstall={updates.install}
        onDefer={updates.defer}
        onDismissError={updates.dismissError}
      />

      <div className="popover__controls">
        <ServerSelector
          servers={data.servers}
          selected={data.selected}
          onSelect={data.select}
          disabled={data.loading}
        />
      </div>

      <div className="popover__reachability">
        <span
          className={`reachability reachability--${reachable ? "up" : "down"}`}
          role="status"
        >
          {snapshot ? (reachable ? "Reachable" : "Unreachable") : "—"}
        </span>
        <span className="popover__refreshed" data-stale={data.stale || undefined}>
          {data.stale ? (
            <>
              <span className="popover__stale-tag">Stale</span>
              {data.lastRefreshed
                ? ` · last seen ${formatRelativeTime(data.lastRefreshed)}`
                : ""}
            </>
          ) : data.lastRefreshed ? (
            `Updated ${formatRelativeTime(data.lastRefreshed)}`
          ) : (
            "Never updated"
          )}
        </span>
      </div>

      {data.error ? (
        <div className="popover__error" role="alert">
          {data.error}
        </div>
      ) : null}

      <section className="popover__metrics" aria-label="Server metrics">
        <MetricCard
          label="CPU"
          value={snapshot ? formatPercent(snapshot.cpuPercent) : "—"}
          percent={snapshot?.cpuPercent}
          tone={snapshot ? tone(snapshot.cpuPercent, 80, 95) : "normal"}
        />
        <MetricCard
          label="RAM"
          value={snapshot ? formatPercent(ramPct) : "—"}
          detail={
            snapshot
              ? `${formatBytes(snapshot.ramUsedBytes)} / ${formatBytes(snapshot.ramTotalBytes)}`
              : undefined
          }
          percent={ramPct}
          tone={snapshot ? tone(ramPct, 80, 95) : "normal"}
        />
        <MetricCard
          label="Disk"
          value={snapshot ? formatPercent(diskPct) : "—"}
          detail={
            snapshot
              ? `${formatBytes(snapshot.diskUsedBytes)} / ${formatBytes(snapshot.diskTotalBytes)}`
              : undefined
          }
          percent={diskPct}
          tone={snapshot ? tone(diskPct, 75, 90) : "normal"}
        />
        <MetricCard
          label="Latency"
          value={snapshot && reachable ? formatLatency(snapshot.latencyMs) : "—"}
          tone={snapshot ? tone(snapshot.latencyMs, 750, 2000) : "normal"}
        />
      </section>

      <section className="popover__counts" aria-label="Workload counts">
        <div className="count-pill">
          <span className="count-pill__value">{snapshot?.apps.running ?? 0}</span>
          <span className="count-pill__label">Apps up</span>
        </div>
        <div className="count-pill">
          <span className="count-pill__value">{snapshot?.apps.stopped ?? 0}</span>
          <span className="count-pill__label">Apps down</span>
        </div>
        <div className="count-pill">
          <span className="count-pill__value">{snapshot?.services.running ?? 0}</span>
          <span className="count-pill__label">Services up</span>
        </div>
      </section>

      <section className="popover__findings" aria-label="Findings">
        <FindingsList findings={data.findings} limit={3} />
      </section>

      <section className="popover__logs" aria-label="Recent logs">
        <button
          type="button"
          className="popover__logs-toggle"
          onClick={() => setLogsOpen((v) => !v)}
          aria-expanded={logsOpen}
          disabled={data.apps.length === 0}
        >
          <span>Recent logs</span>
          <span aria-hidden="true">{logsOpen ? "▾" : "▸"}</span>
        </button>
        {logsOpen && data.apps.length > 0 ? (
          <LogViewer
            api={api}
            server={data.selected}
            targets={data.apps}
            variant="compact"
          />
        ) : null}
      </section>

      <footer className="popover__actions">
        <button
          type="button"
          className="btn btn--ghost"
          onClick={data.refresh}
          disabled={data.loading}
        >
          {data.loading ? "Refreshing…" : "Refresh"}
        </button>
        <button
          type="button"
          className="btn btn--primary"
          onClick={() => openManagementWindow()}
        >
          Open Dashboard
        </button>
      </footer>
    </div>
  );
}
