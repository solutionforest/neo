import { useEffect, useState } from "react";
import type { DesktopAPI } from "../lib/desktop-api";
import type { BridgeHello } from "../lib/protocol";
import {
  formatBytes,
  formatLatency,
  formatPercent,
  formatRelativeTime,
  usagePercent,
} from "../lib/format";
import { actionLabel } from "../lib/actions";
import type { AppAction, AppState, AppSummary } from "../lib/protocol";
import { NeoLogo } from "../components/NeoLogo";
import { StatusBadge } from "../components/StatusBadge";
import { MetricCard, type MetricTone } from "../components/MetricCard";
import { Icon } from "../components/Icon";
import { ServerSelector } from "../features/servers/ServerSelector";
import { FindingsList } from "../features/diagnostics/FindingsList";
import { LogViewer } from "../features/logs/LogViewer";
import { AppActionDialog } from "../features/actions/AppActionDialog";
import { ActionHistoryList } from "../features/actions/ActionHistoryList";
import { DiagnosticBundlePanel } from "../features/diagnostics/DiagnosticBundlePanel";
import { statusFor, useServerData } from "./useServerData";
import { useAppActions } from "./useAppActions";

/** Which lifecycle buttons to offer for a workload's current state. A stopped
 * app can only be started; a running one can be restarted or stopped. */
function actionsForState(state: AppState): AppAction[] {
  return state === "stopped" ? ["start"] : ["restart", "stop"];
}

function metricTone(
  value: number | null | undefined,
  warning: number,
  critical: number,
): MetricTone {
  if (value == null) return "normal";
  if (value >= critical) return "critical";
  if (value >= warning) return "warning";
  return "normal";
}

export function Management({ api }: { api: DesktopAPI }) {
  const data = useServerData(api);
  const status = statusFor(data);
  const { snapshot } = data;

  // The action controller refreshes the selected server immediately after any
  // action settles (plan: "The app refreshes server state immediately").
  const actions = useAppActions({
    api,
    server: data.selected,
    onSettled: () => data.refresh(),
  });
  const { dialog } = actions.state;
  const ramPct = snapshot
    ? usagePercent(snapshot.ramUsedBytes, snapshot.ramTotalBytes)
    : 0;
  const diskPct = snapshot
    ? usagePercent(snapshot.diskUsedBytes, snapshot.diskTotalBytes)
    : 0;

  // The failure "View logs" link selects that workload in the viewer below.
  const [logsTarget, setLogsTarget] = useState<string | undefined>(undefined);

  return (
    <main className="management" data-status={status} aria-busy={data.loading}>
      <header className="management__header">
        <div className="popover__brand">
          <NeoLogo size={32} />
          <span className="management__brand-copy">
            <span className="management__title">Neo Desktop</span>
            <span className="management__subtitle">Server operations</span>
          </span>
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
            <Icon name="refresh" size={14} className={data.loading ? "icon--spinning" : ""} />
            {data.loading ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </header>

      {data.error ? (
        <div className="management__error" role="alert">
          <Icon name="warning" size={16} />
          <span>{data.error}</span>
        </div>
      ) : null}

      <div className="management__grid">
        <section className="panel panel--wide overview-panel" aria-label="Overview">
          <div className="panel__heading">
            <div>
              <h1 className="panel__title">Server health</h1>
              <p className="panel__description">Live capacity and reachability for {data.selected || "your selected server"}.</p>
            </div>
            <StatusBadge status={status} />
          </div>
          <div className="overview-panel__body">
            <div className={`overview-state overview-state--${snapshot ? (snapshot.reachable ? "reachable" : "offline") : "unknown"}`}>
              <span className="overview-state__icon" aria-hidden="true">
                <Icon name={snapshot?.reachable ? "check" : snapshot ? "close" : "activity"} size={22} />
              </span>
              <span className="overview-state__copy">
                <strong>{snapshot ? (snapshot.reachable ? "Server is reachable" : "Server is offline") : "Checking server"}</strong>
                <span>
                  {data.stale
                    ? `Showing cached data${data.lastRefreshed ? ` from ${formatRelativeTime(data.lastRefreshed)}` : ""}`
                    : data.lastRefreshed
                      ? `Updated ${formatRelativeTime(data.lastRefreshed)}`
                      : "Waiting for the first snapshot"}
                </span>
              </span>
            </div>
            <div className="management__metrics" aria-label="Server metrics">
              <MetricCard label="CPU" value={snapshot ? formatPercent(snapshot.cpuPercent) : "—"} percent={snapshot?.cpuPercent} tone={snapshot ? metricTone(snapshot.cpuPercent, 80, 95) : "normal"} icon="cpu" />
              <MetricCard label="Memory" value={snapshot ? formatPercent(ramPct) : "—"} detail={snapshot ? `${formatBytes(snapshot.ramUsedBytes)} of ${formatBytes(snapshot.ramTotalBytes)}` : undefined} percent={ramPct} tone={snapshot ? metricTone(ramPct, 80, 95) : "normal"} icon="memory" />
              <MetricCard label="Disk" value={snapshot ? formatPercent(diskPct) : "—"} detail={snapshot ? `${formatBytes(snapshot.diskUsedBytes)} of ${formatBytes(snapshot.diskTotalBytes)}` : undefined} percent={diskPct} tone={snapshot ? metricTone(diskPct, 75, 90) : "normal"} icon="disk" />
              <MetricCard label="Latency" value={snapshot?.reachable ? formatLatency(snapshot.latencyMs) : "—"} tone={snapshot ? metricTone(snapshot.latencyMs, 750, 2000) : "normal"} icon="latency" />
            </div>
          </div>
        </section>

        <section className="panel panel--wide panel--workloads" aria-label="Applications">
          <div className="panel__heading">
            <div>
              <h2 className="panel__title">Applications & services</h2>
              <p className="panel__description">Inspect workload state and run allowlisted lifecycle actions.</p>
            </div>
            <span className="panel__count">{data.apps.length} workloads</span>
          </div>
          {data.apps.length === 0 ? (
            <div className="empty-state empty-state--compact">
              <span className="empty-state__icon" aria-hidden="true"><Icon name="apps" size={20} /></span>
              <strong>No applications on this server</strong>
              <span>Workloads appear here after the next successful refresh.</span>
            </div>
          ) : (
            <div className="table-scroll" tabIndex={0} aria-label="Scrollable application list">
              <table className="apps-table">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Image</th>
                  <th scope="col">State</th>
                  <th scope="col">Actions</th>
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
                        <span className="app-state__glyph" aria-hidden="true">
                          {app.state === "running" ? <Icon name="check" size={11} /> : <Icon name="warning" size={11} />}
                        </span>
                        {app.state}
                      </span>
                    </td>
                    <td>
                      <AppActionButtons app={app} actions={actions} />
                    </td>
                  </tr>
                ))}
              </tbody>
              </table>
            </div>
          )}
        </section>

        <section className="panel" aria-label="Findings">
          <div className="panel__heading">
            <h2 className="panel__title">Findings</h2>
            <span className="panel__count">{data.findings.length} active</span>
          </div>
          <FindingsList findings={data.findings} />
        </section>

        <section className="panel" aria-label="Action history">
          <h2 className="panel__title">Action history</h2>
          <ActionHistoryList entries={actions.state.history} limit={10} />
        </section>

        <AboutPanel api={api} />

        <DiagnosticBundlePanel servers={data.servers} />

        <section className="panel panel--wide" aria-label="Logs">
          <div className="panel__heading">
            <div>
              <h2 className="panel__title">Live logs</h2>
              <p className="panel__description">Search the bounded local history or follow new output.</p>
            </div>
            <Icon name="logs" size={18} />
          </div>
          <LogViewer
            // Remount when a failure asks to show a specific workload's logs, so
            // the viewer re-selects that target.
            key={logsTarget ?? "default"}
            api={api}
            server={data.selected}
            targets={data.apps}
            follow
            variant="full"
            initialTarget={logsTarget}
          />
        </section>
      </div>

      {dialog ? (
        <AppActionDialog
          server={data.selected}
          dialog={dialog}
          onConfirm={actions.confirm}
          onCancel={actions.cancel}
          onDismiss={actions.dismiss}
          onRememberChange={actions.setRemember}
          onViewLogs={(app) => {
            setLogsTarget(app);
            actions.dismiss();
          }}
        />
      ) : null}
    </main>
  );
}

/** About: the version surface required by the plan's Phase 6 — desktop version,
 * bridge build, Git commit, and protocol version, all reported by
 * `bridge.hello` (the desktop version is injected by the Rust shell). */
function AboutPanel({ api }: { api: DesktopAPI }) {
  const [hello, setHello] = useState<BridgeHello | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .hello()
      .then((h) => {
        if (!cancelled) setHello(h);
      })
      .catch(() => {
        // Best-effort: the panel shows placeholders until the bridge answers.
      });
    return () => {
      cancelled = true;
    };
  }, [api]);

  return (
    <section className="panel panel--quiet" aria-label="About">
      <div className="panel__heading">
        <h2 className="panel__title">About</h2>
        <Icon name="info" size={17} />
      </div>
      <dl className="kv">
        <div className="kv__row">
          <dt>Desktop</dt>
          <dd>{hello?.desktopVersion ?? "—"}</dd>
        </div>
        <div className="kv__row">
          <dt>Bridge build</dt>
          <dd>{hello ? `${hello.bridgeVersion} (core ${hello.coreVersion})` : "—"}</dd>
        </div>
        <div className="kv__row">
          <dt>Commit</dt>
          <dd>{hello?.commit ?? "—"}</dd>
        </div>
        <div className="kv__row">
          <dt>Protocol</dt>
          <dd>{hello ? `v${hello.protocolVersion}` : "—"}</dd>
        </div>
        <div className="kv__row">
          <dt>Platform</dt>
          <dd>{hello ? `${hello.platform}/${hello.arch}` : "—"}</dd>
        </div>
        <div className="kv__row">
          <dt>Activation</dt>
          <dd>{hello?.activation ?? "—"}</dd>
        </div>
      </dl>
    </section>
  );
}

/** The per-row lifecycle buttons. All of a workload's buttons are disabled while
 * one of its actions is in flight, so a duplicate click cannot start a second
 * concurrent action (plan "Phase 5 acceptance criteria"). Only applications are
 * actionable; workers/sidecars/services are managed via the CLI in this beta. */
function AppActionButtons({
  app,
  actions,
}: {
  app: AppSummary;
  actions: ReturnType<typeof useAppActions>;
}) {
  if (app.kind !== "app") {
    return <span className="apps-table__no-action">—</span>;
  }
  const running = actions.isRunning(app.id);
  return (
    <span className="apps-table__actions">
      {actionsForState(app.state).map((action) => (
        <button
          key={action}
          type="button"
          className="btn btn--small"
          disabled={running}
          onClick={() => actions.request(app.id, action)}
        >
          {actionLabel(action)}
        </button>
      ))}
    </span>
  );
}
