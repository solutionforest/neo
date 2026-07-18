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
import { Icon } from "../components/Icon";
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
    <main className="popover" data-status={status} aria-busy={data.loading}>
      <header className="popover__header">
        <div className="popover__brand">
          <NeoLogo size={28} />
          <span className="popover__brand-copy">
            <span className="popover__title">Neo Desktop</span>
            <span className="popover__subtitle">Server monitor</span>
          </span>
        </div>
        <StatusBadge status={status} />
      </header>

      <UpdateBanner
        state={updates.state}
        onInstall={updates.install}
        onDefer={updates.defer}
        onDismissError={updates.dismissError}
      />

      <section className="popover__server-card" aria-label="Selected server">
        <ServerSelector
          servers={data.servers}
          selected={data.selected}
          onSelect={data.select}
          disabled={data.loading}
        />
        <div className="popover__reachability">
          <span
            className={`reachability reachability--${snapshot ? (reachable ? "up" : "down") : "unknown"}`}
            role="status"
          >
            <span className="reachability__icon" aria-hidden="true">
              <Icon name={snapshot ? (reachable ? "check" : "close") : "info"} size={12} />
            </span>
            {snapshot ? (reachable ? "Reachable" : "Unreachable") : "Checking status"}
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
              "Waiting for first update"
            )}
          </span>
        </div>
      </section>

      {data.error ? (
        <div className="popover__error" role="alert">
          <Icon name="warning" size={15} />
          <span>{data.error}</span>
        </div>
      ) : null}

      {data.servers.length === 0 && !data.loading ? (
        <section className="empty-state" aria-label="No configured servers">
          <span className="empty-state__icon" aria-hidden="true"><Icon name="server" size={20} /></span>
          <strong>No servers configured</strong>
          <span>Add a server with the Neo CLI, then refresh.</span>
        </section>
      ) : null}

      <section className="popover__metrics" aria-label="Server metrics">
        <MetricCard
          label="CPU"
          value={snapshot ? formatPercent(snapshot.cpuPercent) : "—"}
          percent={snapshot?.cpuPercent}
          tone={snapshot ? tone(snapshot.cpuPercent, 80, 95) : "normal"}
          icon="cpu"
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
          icon="memory"
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
          icon="disk"
        />
        <MetricCard
          label="Latency"
          value={snapshot && reachable ? formatLatency(snapshot.latencyMs) : "—"}
          tone={snapshot ? tone(snapshot.latencyMs, 750, 2000) : "normal"}
          icon="latency"
        />
      </section>

      <section className="popover__counts" aria-label="Workload counts">
        <div className="count-pill">
          <Icon name="apps" size={14} />
          <span className="count-pill__value">{snapshot?.apps.running ?? 0}</span>
          <span className="count-pill__label">Apps up</span>
        </div>
        <div className="count-pill">
          <Icon name="warning" size={14} />
          <span className="count-pill__value">{snapshot?.apps.stopped ?? 0}</span>
          <span className="count-pill__label">Apps down</span>
        </div>
        <div className="count-pill">
          <Icon name="activity" size={14} />
          <span className="count-pill__value">{snapshot?.services.running ?? 0}</span>
          <span className="count-pill__label">Services up</span>
        </div>
      </section>

      <section className="popover__findings" aria-label="Findings">
        <div className="section-heading">
          <h2>Findings</h2>
          <span>{data.findings.length === 0 ? "All clear" : `${data.findings.length} active`}</span>
        </div>
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
          <span className="popover__logs-label"><Icon name="logs" size={15} />Recent logs</span>
          <Icon name="chevron" size={14} className={logsOpen ? "icon--expanded" : ""} />
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
          <Icon name="refresh" size={14} className={data.loading ? "icon--spinning" : ""} />
          {data.loading ? "Refreshing…" : "Refresh"}
        </button>
        <button
          type="button"
          className="btn btn--primary"
          onClick={() => openManagementWindow()}
        >
          Open Dashboard
          <Icon name="chevron" size={14} />
        </button>
      </footer>
    </main>
  );
}
