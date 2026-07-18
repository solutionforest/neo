import { useEffect, useMemo, useRef, useState } from "react";
import type { DesktopAPI } from "../lib/desktop-api";
import {
  aggregateStatus,
  type AggregateStatus,
  type AppSummary,
  type Finding,
  type ServerSnapshot,
  type ServerSummary,
} from "../lib/protocol";
import {
  DesktopService,
  type ServerRuntime,
  type ServiceState,
} from "../lib/desktop-service";
import { notify, setTrayState } from "../lib/host";

export interface ServerData {
  loading: boolean;
  error: string | null;
  servers: ServerSummary[];
  selected: string;
  snapshot: ServerSnapshot | null;
  apps: AppSummary[];
  findings: Finding[];
  lastRefreshed: string | null;
  /** True when the selected server's cached snapshot is stale (offline). */
  stale: boolean;
  /** Aggregate tray state across ALL configured servers. */
  aggregate: AggregateStatus;
  select: (server: string) => void;
  refresh: () => void;
}

export interface UseServerDataOptions {
  /**
   * When true (the popover), this hook's service becomes the polling owner: it
   * runs the periodic timers and drives the native tray state + notifications.
   * The management window passes false so it loads once and refreshes manually,
   * never adding a second polling loop or double-driving the tray — the plan
   * requires exactly one owner of periodic refresh.
   */
  ownsTray?: boolean;
}

/**
 * Subscribes the component to a DesktopService, which is the single owner of
 * periodic refresh (plan Phase 4). No timer lives in React: the hook maps the
 * service's per-server runtime for the selected server into the legacy
 * ServerData shape and forwards user intent (select / manual refresh).
 */
export function useServerData(
  api: DesktopAPI,
  options: UseServerDataOptions = {},
): ServerData {
  const { ownsTray = false } = options;

  const service = useMemo(
    () =>
      new DesktopService({
        api,
        clock: () => Date.now(),
        setTimer: (fn, ms) => window.setTimeout(fn, ms),
        clearTimer: (h) => window.clearTimeout(h as number),
        random: Math.random,
        // Only the tray owner runs periodic timers and pushes to the shell.
        periodic: ownsTray,
        onTray: ownsTray ? (state, detail) => void setTrayState(state, detail) : undefined,
        onNotify: ownsTray ? (n) => void notify(n) : undefined,
      }),
    [api, ownsTray],
  );

  const [state, setState] = useState<ServiceState>(() => service.getState());
  // Keep the latest service in a ref so returned callbacks stay referentially
  // stable across renders.
  const serviceRef = useRef(service);
  serviceRef.current = service;

  useEffect(() => {
    const unsubscribe = service.subscribe(setState);
    void service.start();
    // Rendering this window means it is visible; for the popover this refreshes
    // the selected server immediately (plan "Immediately refresh ... when the
    // popover opens").
    service.setVisible(true);
    return () => {
      service.setVisible(false);
      unsubscribe();
      service.stop();
    };
  }, [service]);

  const selectedRuntime = findSelected(state);

  return useMemo<ServerData>(
    () => ({
      loading: selectedRuntime
        ? selectedRuntime.refreshing && !selectedRuntime.snapshot
        : state.loading,
      error: selectedRuntime?.error?.message ?? state.error,
      servers: state.servers.map((s) => s.server),
      selected: state.selected,
      snapshot: selectedRuntime?.snapshot ?? null,
      apps: selectedRuntime?.apps ?? [],
      findings: selectedRuntime?.findings ?? [],
      lastRefreshed: selectedRuntime?.snapshot?.observedAt ?? null,
      stale: selectedRuntime?.stale ?? false,
      aggregate: state.aggregate,
      select: (server) => serviceRef.current.select(server),
      refresh: () => serviceRef.current.manualRefresh(),
    }),
    [state, selectedRuntime],
  );
}

function findSelected(state: ServiceState): ServerRuntime | undefined {
  return state.servers.find((s) => s.server.id === state.selected);
}

/** The SELECTED server's status (the popover header badge). For the whole-tray
 * rollup across every server use ServerData.aggregate instead. */
export function statusFor(data: ServerData): AggregateStatus {
  return aggregateStatus(data.snapshot, data.findings);
}
