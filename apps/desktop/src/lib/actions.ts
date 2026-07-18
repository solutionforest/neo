// Lifecycle-action helpers shared by the action dialog and its controller: the
// confirmation policy per safety class, human copy for the dialog, client-side
// operation-id generation, and a local action history that stores NO secrets.
//
// See plan "Fix safety classes" and "Phase 5 acceptance criteria". The backend
// (internal/operations/actions.go) is the source of truth for the allowlist and
// safety classes; this module mirrors it for the UI and never widens it.

import {
  ACTION_SAFETY,
  type AppAction,
  type Change,
  type OperationResult,
  type SafetyClass,
} from "./protocol";

export function safetyClassOf(action: AppAction): SafetyClass {
  return ACTION_SAFETY[action];
}

/** Availability-affecting actions (stop) must be confirmed every time; the
 * confirmation can never be remembered away (plan "Fix safety classes"). */
export function confirmEveryTime(action: AppAction): boolean {
  return safetyClassOf(action) === "availability";
}

/** Reversible actions (start/restart) may offer a "don't ask again" option. */
export function canRemember(action: AppAction): boolean {
  return safetyClassOf(action) === "reversible";
}

/** Present-tense verb for buttons ("Start", "Stop", "Restart"). */
export function actionLabel(action: AppAction): string {
  return action.charAt(0).toUpperCase() + action.slice(1);
}

/** One-line description of the availability impact, shown in the dialog so the
 * user understands the consequence before confirming. */
export function availabilityImpact(action: AppAction, app: string): string {
  switch (action) {
    case "stop":
      return `“${app}” will be stopped and unavailable until it is started again.`;
    case "restart":
      return `“${app}” will be briefly unavailable while it restarts.`;
    case "start":
      return `“${app}” will be started and become available.`;
  }
}

/** Monotonic-ish client operation id. Uniqueness comes from a per-call counter
 * combined with the injected clock, so two rapid clicks never collide and the
 * value is deterministic under test. Never uses Math.random. */
export function makeOperationId(
  action: AppAction,
  app: string,
  seq: number,
  now: number,
): string {
  return `op-${app}-${action}-${now}-${seq}`;
}

/** A recorded action outcome. It carries only workload identifiers and states —
 * never env values, passwords, keys, license keys, or raw logs (plan: "Store a
 * local action history without … secrets"). Safe by construction. */
export interface ActionHistoryEntry {
  operationId: string;
  server: string;
  app: string;
  action: AppAction;
  status: "succeeded" | "failed" | "cancelled";
  summary: string;
  /** ISO timestamp the action finished (or was recorded). */
  at: string;
  changes: Change[];
}

/** Build a history entry from an operation result. `summary` from the bridge is
 * a short, secret-free sentence; changes carry only target/from/to states. */
export function entryFromResult(
  server: string,
  app: string,
  action: AppAction,
  result: OperationResult,
): ActionHistoryEntry {
  return {
    operationId: result.operationId,
    server,
    app,
    action,
    status: result.status,
    summary: result.summary,
    at: result.finishedAt ?? result.startedAt,
    changes: result.changes ?? [],
  };
}

/** Minimal storage surface (a subset of the Web Storage API) so the history can
 * persist across launches yet be driven by an in-memory fake in tests. */
export interface HistoryStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

const HISTORY_KEY = "neo.actionHistory.v1";
const HISTORY_LIMIT = 50;

/** A bounded, newest-first local action history. Entries are secret-free by
 * construction (see ActionHistoryEntry). Persistence is optional: without a
 * storage the history is in-memory only. */
export class ActionHistory {
  private entries: ActionHistoryEntry[] = [];

  constructor(
    private readonly storage?: HistoryStorage,
    private readonly limit = HISTORY_LIMIT,
  ) {
    this.entries = this.load();
  }

  list(): ActionHistoryEntry[] {
    return this.entries.slice();
  }

  /** Prepend an entry (newest first) and persist, dropping the oldest past the
   * limit so the log cannot grow without bound. */
  add(entry: ActionHistoryEntry): void {
    this.entries = [entry, ...this.entries].slice(0, this.limit);
    this.persist();
  }

  private load(): ActionHistoryEntry[] {
    if (!this.storage) return [];
    try {
      const raw = this.storage.getItem(HISTORY_KEY);
      if (!raw) return [];
      const parsed = JSON.parse(raw) as unknown;
      if (!Array.isArray(parsed)) return [];
      return parsed.slice(0, this.limit) as ActionHistoryEntry[];
    } catch {
      return [];
    }
  }

  private persist(): void {
    if (!this.storage) return;
    try {
      this.storage.setItem(HISTORY_KEY, JSON.stringify(this.entries));
    } catch {
      // Persistence is best-effort; an in-memory history still works.
    }
  }
}
