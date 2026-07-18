import type { ActionDialogState } from "../../lib/action-controller";
import { actionLabel } from "../../lib/actions";

export interface AppActionDialogProps {
  server: string;
  dialog: ActionDialogState;
  onConfirm: () => void;
  onCancel: () => void;
  onDismiss: () => void;
  onRememberChange: (value: boolean) => void;
  /** Reveal the workload's logs after a failure (plan: "a link to relevant logs
   * after failure"). */
  onViewLogs: (app: string) => void;
}

/**
 * The confirmation + progress + result dialog for one lifecycle action. It
 * shows, per plan "Every state-changing action shows": the target server and
 * application, the exact action, the availability impact, progress, the final
 * result, and — on failure — a link to the relevant logs.
 */
export function AppActionDialog({
  server,
  dialog,
  onConfirm,
  onCancel,
  onDismiss,
  onRememberChange,
  onViewLogs,
}: AppActionDialogProps) {
  const { app, action, phase, safety } = dialog;
  const label = actionLabel(action);
  const title = `${label} ${app}`;

  return (
    <div
      className="action-dialog__scrim"
      role="presentation"
      onClick={() => {
        if (phase !== "running") onDismiss();
      }}
    >
      <div
        className="action-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        data-phase={phase}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="action-dialog__title">{title}</h2>

        <dl className="action-dialog__meta">
          <div className="action-dialog__row">
            <dt>Server</dt>
            <dd>{server}</dd>
          </div>
          <div className="action-dialog__row">
            <dt>Application</dt>
            <dd>{app}</dd>
          </div>
          <div className="action-dialog__row">
            <dt>Action</dt>
            <dd>{label}</dd>
          </div>
        </dl>

        <p className="action-dialog__impact" data-safety={safety}>
          {dialog.impact}
        </p>

        {phase === "confirm" ? (
          <ConfirmPhase
            dialog={dialog}
            onConfirm={onConfirm}
            onDismiss={onDismiss}
            onRememberChange={onRememberChange}
            label={label}
          />
        ) : null}

        {phase === "running" ? (
          <div className="action-dialog__body">
            <p className="action-dialog__progress" role="status">
              {label}ing {app}…
            </p>
            <div className="action-dialog__actions">
              <button type="button" className="btn btn--ghost" onClick={onCancel}>
                Cancel
              </button>
            </div>
          </div>
        ) : null}

        {phase === "done" ? (
          <DonePhase dialog={dialog} onDismiss={onDismiss} onViewLogs={onViewLogs} />
        ) : null}
      </div>
    </div>
  );
}

function ConfirmPhase({
  dialog,
  onConfirm,
  onDismiss,
  onRememberChange,
  label,
}: {
  dialog: ActionDialogState;
  onConfirm: () => void;
  onDismiss: () => void;
  onRememberChange: (value: boolean) => void;
  label: string;
}) {
  return (
    <div className="action-dialog__body">
      {dialog.canRemember ? (
        <label className="action-dialog__remember">
          <input
            type="checkbox"
            checked={dialog.remember}
            onChange={(e) => onRememberChange(e.target.checked)}
          />
          <span>Don’t ask again for {label.toLowerCase()}</span>
        </label>
      ) : (
        <p className="action-dialog__note">
          This action affects availability and is always confirmed.
        </p>
      )}
      <div className="action-dialog__actions">
        <button type="button" className="btn btn--ghost" onClick={onDismiss}>
          Cancel
        </button>
        <button type="button" className="btn btn--primary" onClick={onConfirm}>
          {label}
        </button>
      </div>
    </div>
  );
}

function DonePhase({
  dialog,
  onDismiss,
  onViewLogs,
}: {
  dialog: ActionDialogState;
  onDismiss: () => void;
  onViewLogs: (app: string) => void;
}) {
  const failed = dialog.error !== null || dialog.result?.status === "failed";
  const cancelled = dialog.result === null && dialog.error?.code === "operation_cancelled";
  const status = cancelled ? "cancelled" : failed ? "failed" : "succeeded";
  const message =
    dialog.result?.summary ?? dialog.error?.message ?? "The action has finished.";

  return (
    <div className="action-dialog__body">
      <p className={`action-dialog__result action-dialog__result--${status}`} role="status">
        {message}
      </p>

      {dialog.result && dialog.result.changes.length > 0 ? (
        <ul className="action-dialog__changes">
          {dialog.result.changes.map((c) => (
            <li key={c.target}>
              <span className="action-dialog__change-target">{c.target}</span>:{" "}
              {c.from} → {c.to}
            </li>
          ))}
        </ul>
      ) : null}

      <div className="action-dialog__actions">
        {failed || cancelled ? (
          <button
            type="button"
            className="btn btn--ghost"
            onClick={() => onViewLogs(dialog.app)}
          >
            View logs
          </button>
        ) : null}
        <button type="button" className="btn btn--primary" onClick={onDismiss}>
          Close
        </button>
      </div>
    </div>
  );
}
