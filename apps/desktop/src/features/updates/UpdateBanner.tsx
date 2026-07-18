import type { UpdateControllerState } from "../../lib/update-controller";

/**
 * Update prompt rendered at the top of the popover. Follows the plan's Phase 6
 * update behavior: shows the target version and release notes, asks before any
 * download starts, and offers "Later" (deferral without re-prompting — the
 * controller remembers the version for the session). Renders nothing while the
 * controller is idle, so the tray stays quiet between silent checks.
 */
export function UpdateBanner({
  state,
  onInstall,
  onDefer,
  onDismissError,
}: {
  state: UpdateControllerState;
  onInstall: () => void;
  onDefer: () => void;
  onDismissError: () => void;
}) {
  if (state.phase === "idle") return null;

  if (state.phase === "error") {
    return (
      <div className="update-banner update-banner--error" role="alert">
        <p className="update-banner__title">Update failed</p>
        <p className="update-banner__notes">{state.error}</p>
        <div className="update-banner__actions">
          <button type="button" className="btn btn--ghost btn--small" onClick={onDismissError}>
            Dismiss
          </button>
        </div>
      </div>
    );
  }

  const version = state.update?.version ?? "";
  const notes = state.update?.notes ?? "";

  return (
    <div className="update-banner" role="status" aria-label="Update available">
      <p className="update-banner__title">
        {state.phase === "installing"
          ? `Installing Neo Desktop ${version}…`
          : state.phase === "restarting"
            ? "Restarting to finish the update…"
            : `Neo Desktop ${version} is available`}
      </p>
      {state.phase === "prompt" && notes ? (
        <p className="update-banner__notes">{notes}</p>
      ) : null}
      {state.phase === "prompt" ? (
        <div className="update-banner__actions">
          <button type="button" className="btn btn--ghost btn--small" onClick={onDefer}>
            Later
          </button>
          <button type="button" className="btn btn--primary btn--small" onClick={onInstall}>
            Install and restart
          </button>
        </div>
      ) : null}
    </div>
  );
}
