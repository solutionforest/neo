import { useMemo, useState } from "react";
import type { DesktopAPI } from "../../lib/desktop-api";
import type { AppSummary, LogSubscribeInput } from "../../lib/protocol";
import { useLogStream } from "../../app/useLogStream";

export interface LogViewerProps {
  api: DesktopAPI;
  /** The server whose workload logs are shown. */
  server: string;
  /** Workloads to choose from (apps, workers, sidecars, services). */
  targets: AppSummary[];
  /** Follow mode (live tail). The compact popover view passes false. */
  follow?: boolean;
  /** Recent backlog to request. */
  tail?: number;
  /** "full" (management) or "compact" (popover). */
  variant?: "full" | "compact";
}

/**
 * A log viewer over one selected workload. Search is purely local — the plan
 * keeps server-side grep out of the beta, so we filter the already-loaded lines
 * (Phase 3 "search loaded lines locally"). Pause/Clear and the bounded history
 * are provided by the underlying controller via useLogStream.
 */
export function LogViewer({
  api,
  server,
  targets,
  follow = false,
  tail,
  variant = "full",
}: LogViewerProps) {
  const [target, setTarget] = useState<string>(() => targets[0]?.id ?? "");
  const [query, setQuery] = useState("");

  // Keep a valid selection as the workload list changes across server switches.
  const selected = targets.some((t) => t.id === target) ? target : targets[0]?.id ?? "";

  const input: LogSubscribeInput | null =
    server && selected ? { server, target: selected, follow, tail } : null;

  const stream = useLogStream(api, input);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return stream.lines;
    return stream.lines.filter((l) => l.text.toLowerCase().includes(q));
  }, [stream.lines, query]);

  const hasTargets = targets.length > 0;

  return (
    <div className={`logs logs--${variant}`} data-testid="log-viewer">
      <div className="logs__toolbar">
        <label className="logs__field">
          <span className="logs__field-label">Workload</span>
          <select
            className="logs__select"
            value={selected}
            onChange={(e) => setTarget(e.target.value)}
            disabled={!hasTargets}
            aria-label="Log workload"
          >
            {hasTargets ? (
              targets.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                  {t.kind !== "app" ? ` (${t.kind})` : ""}
                </option>
              ))
            ) : (
              <option value="">No workloads</option>
            )}
          </select>
        </label>

        <input
          type="search"
          className="logs__search"
          placeholder="Filter loaded lines…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Filter logs"
          disabled={!hasTargets}
        />

        <div className="logs__actions">
          <button
            type="button"
            className="btn btn--ghost"
            onClick={() => stream.togglePause()}
            disabled={!hasTargets}
            aria-pressed={stream.paused}
          >
            {stream.paused ? "Resume" : "Pause"}
          </button>
          <button
            type="button"
            className="btn btn--ghost"
            onClick={() => stream.clear()}
            disabled={!hasTargets}
          >
            Clear
          </button>
        </div>
      </div>

      <div className="logs__status" aria-live="polite">
        <span className="logs__count">
          {filtered.length}
          {query ? ` / ${stream.lines.length}` : ""} lines
        </span>
        {stream.paused && stream.pendingCount > 0 ? (
          <button
            type="button"
            className="logs__pending"
            onClick={() => stream.resume()}
          >
            {stream.pendingCount} new line{stream.pendingCount === 1 ? "" : "s"} — resume
          </button>
        ) : null}
        {stream.error ? (
          <span className="logs__error" role="alert">
            {logErrorLabel(stream.error.code)}
          </span>
        ) : stream.streamClosed && stream.closeReason === "eof" ? (
          <span className="logs__ended">stream ended</span>
        ) : null}
      </div>

      <pre className="logs__body" tabIndex={0} aria-label="Log output">
        {!hasTargets ? (
          <span className="logs__empty">No workloads on this server.</span>
        ) : filtered.length === 0 ? (
          <span className="logs__empty">
            {stream.lines.length === 0 ? "Waiting for logs…" : "No lines match the filter."}
          </span>
        ) : (
          filtered.map((line) => (
            <span key={line.seq} className="logs__line">
              {line.text}
            </span>
          ))
        )}
      </pre>
    </div>
  );
}

/** Map a stable bridge error code to a short human label for the log status. */
function logErrorLabel(code: string): string {
  switch (code) {
    case "app_not_found":
      return "workload not found";
    case "server_not_found":
      return "server not configured";
    case "ssh_unreachable":
      return "server unreachable";
    case "ssh_auth_failed":
      return "authentication failed";
    case "ssh_unknown_host":
      return "host key not trusted";
    case "operation_timeout":
      return "timed out";
    case "not_activated":
      return "activation required";
    default:
      return "log stream error";
  }
}
