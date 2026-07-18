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
import { statusFor, useServerData } from "./useServerData";

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

export function Popover({ api }: { api: DesktopAPI }) {
  const data = useServerData(api);
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
        <span className="popover__refreshed">
          {data.lastRefreshed
            ? `Updated ${formatRelativeTime(data.lastRefreshed)}`
            : "Never updated"}
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
