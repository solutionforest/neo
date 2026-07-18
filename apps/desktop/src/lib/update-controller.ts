// The updater controller — the single owner of the plan's "Update behavior"
// rules (Phase 6): check silently after startup and every six hours, prompt
// before downloading, show release notes and the target version, allow deferral
// without re-prompting during the same session, and treat bridge + desktop as
// one indivisible unit (the bridge ships inside the bundle, so installing the
// desktop update is the only way anything updates).
//
// It is framework-agnostic and subscribe-based (like ActionController) so every
// rule is covered by deterministic unit tests. Signature verification is NOT
// done here: the Tauri updater plugin verifies the artifact signature against
// the public key baked into tauri.conf.json before install, and a bad signature
// fails closed inside downloadAndInstall().

export interface AvailableUpdate {
  /** Target version, e.g. "0.2.0". */
  version: string;
  /** Release notes (the updater manifest's `notes` field). */
  notes: string;
  /** Download, verify the signature, and install. Rejects on any failure. */
  downloadAndInstall(): Promise<void>;
}

export interface UpdateBackend {
  /** One silent update check. Resolves null when already up to date. */
  check(): Promise<AvailableUpdate | null>;
  /** Relaunch the app to finish an installed update. */
  relaunch(): Promise<void>;
}

/** Optional sink for update observability (checks + signature failures). Kept as
 * a narrow interface so tests inject a spy and production passes the shared log. */
export interface UpdateObserver {
  recordUpdateCheck(available: boolean, version: string | null): void;
  recordUpdateFailure(message: string): void;
}

export type UpdatePhase = "idle" | "prompt" | "installing" | "restarting" | "error";

export interface UpdateControllerState {
  phase: UpdatePhase;
  /** The pending update while phase is prompt/installing/restarting. */
  update: AvailableUpdate | null;
  /** Human-readable failure while phase is "error". */
  error: string | null;
}

export interface UpdateControllerOptions {
  /** Delay before the first silent check, so startup stays quiet. */
  startupDelayMs?: number;
  /** Interval between silent checks (plan: every six hours). */
  intervalMs?: number;
  /** Records update checks and signature failures for the support bundle. */
  observer?: UpdateObserver;
}

export const DEFAULT_STARTUP_DELAY_MS = 20_000;
export const DEFAULT_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;

type Listener = (state: UpdateControllerState) => void;

export class UpdateController {
  private backend: UpdateBackend;
  private startupDelayMs: number;
  private intervalMs: number;
  private observer?: UpdateObserver;

  private state: UpdateControllerState = { phase: "idle", update: null, error: null };
  private listeners = new Set<Listener>();
  /** Versions the user deferred this session — never re-prompted (plan: "allow
   * deferral, but do not repeatedly prompt during the same session"). */
  private deferred = new Set<string>();
  private startupTimer: ReturnType<typeof setTimeout> | null = null;
  private intervalTimer: ReturnType<typeof setInterval> | null = null;
  private checking = false;

  constructor(backend: UpdateBackend, options: UpdateControllerOptions = {}) {
    this.backend = backend;
    this.startupDelayMs = options.startupDelayMs ?? DEFAULT_STARTUP_DELAY_MS;
    this.intervalMs = options.intervalMs ?? DEFAULT_CHECK_INTERVAL_MS;
    this.observer = options.observer;
  }

  getState(): UpdateControllerState {
    return this.state;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Begin the silent check schedule: once shortly after startup, then every
   * six hours. Idempotent. */
  start(): void {
    if (this.startupTimer || this.intervalTimer) return;
    this.startupTimer = setTimeout(() => {
      this.startupTimer = null;
      void this.checkSilently();
      this.intervalTimer = setInterval(() => void this.checkSilently(), this.intervalMs);
    }, this.startupDelayMs);
  }

  stop(): void {
    if (this.startupTimer) clearTimeout(this.startupTimer);
    if (this.intervalTimer) clearInterval(this.intervalTimer);
    this.startupTimer = null;
    this.intervalTimer = null;
  }

  /**
   * One silent check. Failures (offline, no release yet, missing updater
   * config) are swallowed — a background check must never surface errors or
   * interrupt the user. A found update moves to "prompt" unless that version
   * was already deferred this session or another flow is active.
   */
  async checkSilently(): Promise<void> {
    if (this.checking || this.state.phase !== "idle") return;
    this.checking = true;
    try {
      const update = await this.backend.check();
      this.observer?.recordUpdateCheck(update !== null, update?.version ?? null);
      if (
        update &&
        this.state.phase === "idle" &&
        !this.deferred.has(update.version)
      ) {
        this.setState({ phase: "prompt", update, error: null });
      }
    } catch (err) {
      console.warn("silent update check failed", err);
    } finally {
      this.checking = false;
    }
  }

  /** User chose "Later": drop the prompt and never re-prompt for this version
   * during this session. */
  defer(): void {
    if (this.state.phase !== "prompt" || !this.state.update) return;
    this.deferred.add(this.state.update.version);
    this.setState({ phase: "idle", update: null, error: null });
  }

  /** User confirmed: download + verify + install, then relaunch. The plugin
   * fails closed on a bad signature, which lands here as the error phase. */
  async install(): Promise<void> {
    const { update } = this.state;
    if (this.state.phase !== "prompt" || !update) return;
    this.setState({ phase: "installing", update, error: null });
    try {
      await update.downloadAndInstall();
      this.setState({ phase: "restarting", update, error: null });
      await this.backend.relaunch();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      // A bad updater signature fails closed inside downloadAndInstall and lands
      // here; recording it makes a fail-closed updater visible in the bundle.
      this.observer?.recordUpdateFailure(message);
      this.setState({ phase: "error", update: null, error: message });
    }
  }

  /** Dismiss a failed install. The next scheduled check may prompt again. */
  dismissError(): void {
    if (this.state.phase !== "error") return;
    this.setState({ phase: "idle", update: null, error: null });
  }

  private setState(next: UpdateControllerState): void {
    this.state = next;
    for (const listener of this.listeners) listener(next);
  }
}

/**
 * Production backend on top of the Tauri updater/process plugins. Loaded
 * dynamically so tests and plain-browser dev never import Tauri modules.
 * Outside Tauri every check resolves null (no update UI is ever shown).
 */
export function createTauriUpdateBackend(): UpdateBackend {
  return {
    async check() {
      const { check } = await import("@tauri-apps/plugin-updater");
      const update = await check();
      if (!update) return null;
      return {
        version: update.version,
        notes: update.body ?? "",
        downloadAndInstall: () => update.downloadAndInstall(),
      };
    },
    async relaunch() {
      const { relaunch } = await import("@tauri-apps/plugin-process");
      await relaunch();
    },
  };
}
