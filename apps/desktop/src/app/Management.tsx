import type { DesktopAPI } from "../lib/desktop-api";
import {
  formatBytes,
  formatLatency,
  formatPercent,
  formatRelativeTime,
  usagePercent,
} from "../lib/format";
import { NeoLogo } from "../components/NeoLogo";
import { StatusBadge } from "../components/StatusBadge";
import { ServerSelector } from "../features/servers/ServerSelector";
import { FindingsList } from "../features/diagnostics/FindingsList";
import { LogViewer } from "../features/logs/LogViewer";
import { statusFor, useServerData } from "./useServerData";

export function Management({ api }: { api: DesktopAPI }) {
  const data = useServerData(api);
  const status = statusFor(data);
  const { snapshot } = data;

  return (
    <div className="management" data-status={status}>
      <header className="management__header">
        <div className="popover__brand">
          <NeoLogo size={26} />
          <span className="management__title">Neo Desktop</span>
          <StatusBadge status={status} />
        </div>
        <div className="management__toolbar">
          <ServerSelector
            servers={data.servers}
            selected={data.selected}
            onSelect={data.select}
            disabled={data.loading}
          />
          <button
            type="button"
            className="btn btn--ghost"
            onClick={data.refresh}
            disabled={data.loading}
          >
            {data.loading ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </header>

      {data.error ? (
        <div className="management__error" role="alert">
          {data.error}
        </div>
      ) : null}

      <div className="management__grid">
        <section className="panel" aria-label="Overview">
          <h2 className="panel__title">Overview</h2>
          <dl className="kv">
            <div className="kv__row">
              <dt>Reachable</dt>
              <dd>{snapshot ? (snapshot.reachable ? "Yes" : "No") : "—"}</dd>
            </div>
            <div className="kv__row">
              <dt>Last updated</dt>
              <dd>
                {data.lastRefreshed
                  ? formatRelativeTime(data.lastRefreshed)
                  : "Never"}
              </dd>
            </div>
            <div className="kv__row">
              <dt>CPU</dt>
              <dd>{snapshot ? formatPercent(snapshot.cpuPercent) : "—"}</dd>
            </div>
            <div className="kv__row">
              <dt>RAM</dt>
              <dd>
                {snapshot
                  ? `${formatBytes(snapshot.ramUsedBytes)} / ${formatBytes(
                      snapshot.ramTotalBytes,
                    )} (${formatPercent(
                      usagePercent(snapshot.ramUsedBytes, snapshot.ramTotalBytes),
                    )})`
                  : "—"}
              </dd>
            </div>
            <div className="kv__row">
              <dt>Disk</dt>
              <dd>
                {snapshot
                  ? `${formatBytes(snapshot.diskUsedBytes)} / ${formatBytes(
                      snapshot.diskTotalBytes,
                    )} (${formatPercent(
                      usagePercent(snapshot.diskUsedBytes, snapshot.diskTotalBytes),
                    )})`
                  : "—"}
              </dd>
            </div>
            <div className="kv__row">
              <dt>Latency</dt>
              <dd>
                {snapshot && snapshot.reachable
                  ? formatLatency(snapshot.latencyMs)
                  : "—"}
              </dd>
            </div>
          </dl>
        </section>

        <section className="panel" aria-label="Applications">
          <h2 className="panel__title">Applications</h2>
          {data.apps.length === 0 ? (
            <p className="panel__empty">No applications on this server.</p>
          ) : (
            <table className="apps-table">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Image</th>
                  <th scope="col">State</th>
                </tr>
              </thead>
              <tbody>
                {data.apps.map((app) => (
                  <tr key={app.id}>
                    <td>{app.name}</td>
                    <td>{app.kind}</td>
                    <td className="apps-table__image">{app.image}</td>
                    <td>
                      <span className={`app-state app-state--${app.state}`}>
                        {app.state}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>

        <section className="panel panel--wide" aria-label="Findings">
          <h2 className="panel__title">Findings</h2>
          <FindingsList findings={data.findings} />
        </section>

        <section className="panel panel--wide" aria-label="Logs">
          <h2 className="panel__title">Logs</h2>
          <LogViewer
            api={api}
            server={data.selected}
            targets={data.apps}
            follow
            variant="full"
          />
        </section>
      </div>
    </div>
  );
}
