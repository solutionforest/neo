import { useCallback, useEffect, useState } from "react";
import type { DesktopAPI } from "../lib/desktop-api";
import {
  aggregateStatus,
  type AppSummary,
  type Finding,
  type ServerSnapshot,
  type ServerSummary,
} from "../lib/protocol";

export interface ServerData {
  loading: boolean;
  error: string | null;
  servers: ServerSummary[];
  selected: string;
  snapshot: ServerSnapshot | null;
  apps: AppSummary[];
  findings: Finding[];
  lastRefreshed: string | null;
  select: (server: string) => void;
  refresh: () => void;
}

/**
 * Loads the configured servers once, then the selected server's snapshot, apps,
 * and findings. Periodic polling is intentionally NOT here — slice 4 gives the
 * desktop application service sole ownership of refresh timers. This hook only
 * refreshes on mount, on server switch, and on explicit refresh().
 */
export function useServerData(api: DesktopAPI): ServerData {
  const [servers, setServers] = useState<ServerSummary[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [snapshot, setSnapshot] = useState<ServerSnapshot | null>(null);
  const [apps, setApps] = useState<AppSummary[]>([]);
  const [findings, setFindings] = useState<Finding[]>([]);
  const [lastRefreshed, setLastRefreshed] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  // Load the server list and pick an initial selection (the current server).
  useEffect(() => {
    let cancelled = false;
    api
      .listServers()
      .then((list) => {
        if (cancelled) return;
        setServers(list);
        setSelected((prev) => prev || list.find((s) => s.current)?.id || list[0]?.id || "");
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      });
    return () => {
      cancelled = true;
    };
  }, [api]);

  // Load the selected server's data whenever selection or refresh tick changes.
  useEffect(() => {
    if (!selected) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    Promise.all([
      api.getSnapshot(selected),
      api.listApps(selected),
      api.runDiagnostics(selected),
    ])
      .then(([snap, appList, findingList]) => {
        if (cancelled) return;
        setSnapshot(snap);
        setApps(appList);
        setFindings(findingList);
        setLastRefreshed(snap.observedAt);
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [api, selected, tick]);

  const select = useCallback((server: string) => {
    setSnapshot(null);
    setApps([]);
    setFindings([]);
    setSelected(server);
  }, []);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  return {
    loading,
    error,
    servers,
    selected,
    snapshot,
    apps,
    findings,
    lastRefreshed,
    select,
    refresh,
  };
}

export function statusFor(data: ServerData) {
  return aggregateStatus(data.snapshot, data.findings);
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
