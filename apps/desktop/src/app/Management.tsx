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

  // The failure "View logs" link selects that workload in the viewer below.
  const [logsTarget, setLogsTarget] = useState<string | undefined>(undefined);

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
          )}
        </section>

        <section className="panel panel--wide" aria-label="Findings">
          <h2 className="panel__title">Findings</h2>
          <FindingsList findings={data.findings} />
        </section>

        <section className="panel" aria-label="Action history">
          <h2 className="panel__title">Action history</h2>
          <ActionHistoryList entries={actions.state.history} limit={10} />
        </section>

        <AboutPanel api={api} />

        <DiagnosticBundlePanel servers={data.servers} />

        <section className="panel panel--wide" aria-label="Logs">
          <h2 className="panel__title">Logs</h2>
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
    </div>
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
    <section className="panel" aria-label="About">
      <h2 className="panel__title">About</h2>
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
