import { useMemo, useState } from "react";
import type { ServerSummary } from "../../lib/protocol";
import {
  getObservability,
  type ObservabilityLog,
  type VersionInfo,
} from "../../lib/observability";
import {
  buildDiagnosticBundle,
  bundleFilename,
  serializeBundle,
} from "../../lib/diagnostic-bundle";
import { exportDiagnosticBundle } from "../../lib/host";

export interface DiagnosticBundlePanelProps {
  servers: ServerSummary[];
  /** Recent captured log lines the user may opt to include. */
  serverLogs?: string[];
  /** Test seams — production uses the shared log, real clock, and host writer. */
  observability?: ObservabilityLog;
  versions?: VersionInfo | null;
  now?: () => string;
  exportBundle?: (filename: string, content: string) => Promise<string | null>;
}

type Status =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "saved"; path: string | null }
  | { kind: "error"; message: string };

/**
 * The "Export Diagnostic Bundle" surface (plan "Observability and support
 * bundle"). It previews the exact, already-redacted document before anything is
 * written, and only includes server logs when the user explicitly opts in after
 * seeing that preview. The bundle body is built by the pure, unit-tested
 * builder; this component only orchestrates preview → confirm → write.
 */
export function DiagnosticBundlePanel({
  servers,
  serverLogs,
  observability,
  versions,
  now,
  exportBundle,
}: DiagnosticBundlePanelProps) {
  const obs = observability ?? getObservability();
  const write = exportBundle ?? exportDiagnosticBundle;
  const clock = now ?? (() => new Date().toISOString());

  const [previewing, setPreviewing] = useState(false);
  const [includeLogs, setIncludeLogs] = useState(false);
  const [status, setStatus] = useState<Status>({ kind: "idle" });
  // Frozen at the moment "Preview" is clicked so the previewed text is exactly
  // what gets written (the observability log keeps growing in the background).
  const [generatedAt, setGeneratedAt] = useState<string>("");

  const bundle = useMemo(() => {
    if (!previewing) return null;
    return buildDiagnosticBundle({
      versions: versions ?? obs.getVersions(),
      servers,
      events: obs.snapshot(),
      serverLogs,
      generatedAt,
      options: { includeServerLogs: includeLogs },
    });
    // Rebuild only when the preview opens or the opt-in toggles; the frozen
    // generatedAt keeps the snapshot stable across those changes.
  }, [previewing, includeLogs, generatedAt, servers, serverLogs, versions, obs]);

  const preview = bundle ? serializeBundle(bundle) : "";

  function openPreview() {
    setGeneratedAt(clock());
    setStatus({ kind: "idle" });
    setPreviewing(true);
  }

  function closePreview() {
    setPreviewing(false);
    setIncludeLogs(false);
  }

  async function doExport() {
    if (!bundle) return;
    setStatus({ kind: "saving" });
    try {
      const path = await write(bundleFilename(bundle.generatedAt), preview);
      setStatus({ kind: "saved", path });
      setPreviewing(false);
    } catch (err) {
      setStatus({
        kind: "error",
        message: err instanceof Error ? err.message : String(err),
      });
    }
  }

  return (
    <section className="panel" aria-label="Support">
      <h2 className="panel__title">Support</h2>
      <p className="panel__hint">
        Export a redacted diagnostic bundle to share with support. Private keys,
        passwords, license keys, and app environment values are never included.
      </p>
      <button type="button" className="btn btn--ghost" onClick={openPreview}>
        Export Diagnostic Bundle…
      </button>

      {status.kind === "saved" ? (
        <p className="panel__hint" role="status">
          {status.path
            ? `Saved to ${status.path}`
            : "Bundle ready (no file written outside the desktop app)."}
        </p>
      ) : null}
      {status.kind === "error" ? (
        <p className="popover__error" role="alert">
          Export failed: {status.message}
        </p>
      ) : null}

      {previewing && bundle ? (
        <div
          className="action-dialog__scrim"
          role="presentation"
          onClick={closePreview}
        >
          <div
            className="action-dialog action-dialog--wide"
            role="dialog"
            aria-modal="true"
            aria-label="Diagnostic bundle preview"
            onClick={(e) => e.stopPropagation()}
          >
            <h2 className="action-dialog__title">Diagnostic bundle preview</h2>
            <p className="action-dialog__note">
              This is exactly what will be written. Review it before exporting.
            </p>

            <ul className="bundle-excludes" aria-label="Excluded from the bundle">
              {bundle.excludes.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>

            <label className="action-dialog__remember">
              <input
                type="checkbox"
                checked={includeLogs}
                onChange={(e) => setIncludeLogs(e.target.checked)}
              />
              <span>Include recent server logs (redacted line by line)</span>
            </label>

            <textarea
              className="bundle-preview"
              readOnly
              aria-label="Diagnostic bundle contents"
              value={preview}
              rows={14}
            />

            <div className="action-dialog__actions">
              <button type="button" className="btn btn--ghost" onClick={closePreview}>
                Cancel
              </button>
              <button
                type="button"
                className="btn btn--primary"
                onClick={doExport}
                disabled={status.kind === "saving"}
              >
                {status.kind === "saving" ? "Exporting…" : "Export"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
