// The lifecycle-action controller — the single owner of the confirmation flow,
// duplicate-click protection, cancellation, and the local action history.
//
// It is framework-agnostic and subscribe-based (like DesktopService) so every
// rule from plan "Phase 5 acceptance criteria" is covered by deterministic unit
// tests: the right confirmation per safety class, "duplicate clicks cannot start
// the same action twice concurrently", cancellation via operation.cancel, an
// immediate refresh after an action, and a secret-free history.

import type { DesktopAPI } from "./desktop-api";
import {
  BridgeError,
  type AppAction,
  type OperationResult,
  type SafetyClass,
} from "./protocol";
import {
  ActionHistory,
  availabilityImpact,
  canRemember,
  confirmEveryTime,
  entryFromResult,
  makeOperationId,
  safetyClassOf,
  type ActionHistoryEntry,
} from "./actions";

export type ActionPhase = "confirm" | "running" | "done";

export interface ActionDialogState {
  app: string;
  action: AppAction;
  safety: SafetyClass;
  /** One-line availability impact shown before confirmation. */
  impact: string;
  phase: ActionPhase;
  /** Whether a "don't ask again" option applies (reversible actions only). */
  canRemember: boolean;
  /** Current value of the remember checkbox (only meaningful in "confirm"). */
  remember: boolean;
  /** The in-flight/settled operation id, for cancellation and display. */
  operationId: string | null;
  result: OperationResult | null;
  error: BridgeError | null;
}

export interface ActionControllerState {
  dialog: ActionDialogState | null;
  /** Apps with an action in flight — their buttons are disabled so a duplicate
   * click cannot start a second concurrent action. */
  runningApps: string[];
  history: ActionHistoryEntry[];
}

export interface ActionControllerDeps {
  api: DesktopAPI;
  server: string;
  /** Epoch ms source, injected for deterministic ids and timestamps. */
  clock?: () => number;
  /** Shared history store (persisted across server switches). */
  history?: ActionHistory;
  /** Called after every settled action so the UI can refresh state immediately
   * (plan: "The app refreshes server state immediately after an action"). */
  onSettled?: (result: OperationResult | null) => void;
}

type Listener = (state: ActionControllerState) => void;

export class ActionController {
  private readonly api: DesktopAPI;
  private server: string;
  private readonly clock: () => number;
  private readonly history: ActionHistory;
  private readonly onSettled?: (result: OperationResult | null) => void;

  private dialog: ActionDialogState | null = null;
  private running = new Set<string>();
  private remembered = new Set<AppAction>();
  private seq = 0;

  private listeners = new Set<Listener>();

  constructor(deps: ActionControllerDeps) {
    this.api = deps.api;
    this.server = deps.server;
    this.clock = deps.clock ?? (() => Date.now());
    this.history = deps.history ?? new ActionHistory();
    this.onSettled = deps.onSettled;
  }

  // --- store ---------------------------------------------------------------

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    fn(this.getState());
    return () => this.listeners.delete(fn);
  }

  getState(): ActionControllerState {
    return {
      dialog: this.dialog,
      runningApps: [...this.running],
      history: this.history.list(),
    };
  }

  /** Update the target server (the user switched selection). */
  setServer(server: string): void {
    this.server = server;
  }

  private emit(): void {
    const state = this.getState();
    for (const fn of this.listeners) fn(state);
  }

  // --- user intent ---------------------------------------------------------

  /** A user asked to run `action` on `app`. Opens the confirmation dialog,
   * unless the action is reversible and its confirmation was remembered — then
   * it runs immediately. A duplicate request while the app is busy is ignored. */
  request(app: string, action: AppAction): void {
    if (this.running.has(app)) return; // duplicate-click guard
    if (this.dialog && this.dialog.phase !== "done") return; // one dialog at a time

    if (canRemember(action) && this.remembered.has(action)) {
      void this.execute(app, action, false);
      return;
    }

    this.dialog = {
      app,
      action,
      safety: safetyClassOf(action),
      impact: availabilityImpact(action, app),
      phase: "confirm",
      canRemember: canRemember(action),
      remember: false,
      operationId: null,
      result: null,
      error: null,
    };
    this.emit();
  }

  /** Toggle the "don't ask again" checkbox in the confirmation dialog. */
  setRemember(remember: boolean): void {
    if (!this.dialog || this.dialog.phase !== "confirm") return;
    // Availability-affecting actions can never be remembered away.
    if (!this.dialog.canRemember) return;
    this.dialog = { ...this.dialog, remember };
    this.emit();
  }

  /** Confirm the pending action. Availability-affecting actions (stop) always
   * reach here — their confirmation can never be remembered away. */
  confirm(): void {
    const d = this.dialog;
    if (!d || d.phase !== "confirm") return;
    if (d.remember && canRemember(d.action) && !confirmEveryTime(d.action)) {
      this.remembered.add(d.action);
    }
    void this.execute(d.app, d.action, true);
  }

  /** Cancel the in-flight action (operation.cancel). The in-flight
   * runAppAction promise then rejects with operation_cancelled, which the
   * controller records and shows. */
  cancel(): void {
    const d = this.dialog;
    if (!d || d.phase !== "running" || !d.operationId) return;
    void this.api.cancelOperation(d.operationId).catch(() => {
      // Best-effort: the action also stops on shutdown/window close.
    });
  }

  /** Dismiss the dialog. Not allowed while an action is running — the user must
   * cancel or wait — so a running action always has visible progress. */
  dismiss(): void {
    if (!this.dialog || this.dialog.phase === "running") return;
    this.dialog = null;
    this.emit();
  }

  // --- execution -----------------------------------------------------------

  private async execute(app: string, action: AppAction, hasDialog: boolean): Promise<void> {
    if (this.running.has(app)) return; // final guard against a double start
    this.running.add(app);

    this.seq += 1;
    const operationId = makeOperationId(action, app, this.seq, this.clock());
    const server = this.server;

    if (hasDialog && this.dialog) {
      this.dialog = { ...this.dialog, phase: "running", operationId, result: null, error: null };
    }
    this.emit();

    let settledResult: OperationResult | null = null;
    try {
      const result = await this.api.runAppAction({ server, app, action, operationId });
      this.history.add(entryFromResult(server, app, action, result));
      settledResult = result;
      this.showDone(app, action, operationId, result, null, hasDialog);
    } catch (err) {
      const be =
        err instanceof BridgeError
          ? err
          : new BridgeError("internal_error", err instanceof Error ? err.message : String(err));
      const status = be.code === "operation_cancelled" ? "cancelled" : "failed";
      this.history.add({
        operationId,
        server,
        app,
        action,
        status,
        summary: be.message,
        at: new Date(this.clock()).toISOString(),
        changes: [],
      });
      // Always surface a failure/cancellation, even if the confirmation was
      // remembered and no dialog was open — errors must never be silent.
      this.showDone(app, action, operationId, null, be, true);
    } finally {
      this.running.delete(app);
      this.emit();
      this.onSettled?.(settledResult);
    }
  }

  /** Move the dialog (or open one) into the terminal "done" phase for this
   * operation, unless a newer dialog has since taken its place. */
  private showDone(
    app: string,
    action: AppAction,
    operationId: string,
    result: OperationResult | null,
    error: BridgeError | null,
    open: boolean,
  ): void {
    if (!open) return;
    // Only update if the dialog still belongs to this operation (or none is
    // open and we need to surface an error).
    const belongs =
      this.dialog &&
      this.dialog.app === app &&
      this.dialog.action === action &&
      (this.dialog.operationId === operationId || this.dialog.operationId === null);
    if (this.dialog && !belongs) return;

    this.dialog = {
      app,
      action,
      safety: safetyClassOf(action),
      impact: availabilityImpact(action, app),
      phase: "done",
      canRemember: canRemember(action),
      remember: this.dialog?.remember ?? false,
      operationId,
      result,
      error,
    };
    this.emit();
  }
}
