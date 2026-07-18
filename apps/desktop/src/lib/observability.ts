// Local observability: a bounded, in-memory record of what the desktop app is
// doing, kept entirely on-device (plan "Observability and support bundle").
//
// It exists so a user can export a support bundle that explains a problem
// WITHOUT ever capturing a secret. The plan enumerates exactly what to record:
//
//   * Desktop, bridge, protocol, OS, and architecture versions.
//   * Bridge start/stop/restart events.
//   * Request method, duration, and error code — but NOT sensitive parameters.
//   * Poll scheduling and cache age.
//   * Notification transitions.
//   * Update checks and signature failures.
//
// The log is a fixed-size ring buffer so a long-running tray process can never
// grow memory without bound. It is deliberately dumb about payloads: callers
// pass only non-sensitive scalar fields (method names, durations, stable error
// codes, counts, ages). The diagnostic-bundle builder applies a second
// redaction pass as defense in depth — see diagnostic-bundle.ts.

/** The categories of event the plan asks the desktop to record. */
export type ObservabilityCategory =
  | "bridge" // sidecar lifecycle: ready / error / unavailable / restart
  | "request" // one bridge method call: method, duration, error code
  | "poll" // poll scheduling and cache age
  | "notification" // transition notifications delivered to the OS
  | "update" // update checks, installs, and signature failures
  | "lifecycle"; // sleep/wake, online/offline, VPN reconnect refreshes

/** A scalar field value. Objects/arrays are intentionally disallowed at the type
 * level so a caller cannot accidentally log a nested secret-bearing structure. */
export type FieldValue = string | number | boolean | null;

export interface ObservabilityEvent {
  /** Epoch milliseconds when the event was recorded. */
  at: number;
  category: ObservabilityCategory;
  /** A short, human-readable, secret-free summary. */
  message: string;
  /** Non-sensitive structured detail (method, durationMs, code, ageMs, …). */
  fields: Record<string, FieldValue>;
}

/** The version surface recorded once the bridge handshake completes. Mirrors the
 * fields the About panel already shows (see Management.tsx). */
export interface VersionInfo {
  desktopVersion: string;
  bridgeVersion: string;
  coreVersion: string;
  protocolVersion: number;
  commit: string;
  platform: string;
  arch: string;
  activation: string;
}

export interface ObservabilityOptions {
  /** Ring-buffer capacity. Older events are dropped once exceeded. */
  capacity?: number;
  /** Injectable clock so tests get deterministic timestamps. */
  clock?: () => number;
}

const DEFAULT_CAPACITY = 500;

export class ObservabilityLog {
  private readonly capacity: number;
  private readonly clock: () => number;
  private events: ObservabilityEvent[] = [];
  private versions: VersionInfo | null = null;

  constructor(options: ObservabilityOptions = {}) {
    this.capacity = Math.max(1, options.capacity ?? DEFAULT_CAPACITY);
    this.clock = options.clock ?? (() => Date.now());
  }

  /** Record versions once the bridge answers `bridge.hello`. */
  setVersions(info: VersionInfo): void {
    this.versions = info;
    this.record("bridge", "handshake complete", {
      protocolVersion: info.protocolVersion,
      bridgeVersion: info.bridgeVersion,
      platform: `${info.platform}/${info.arch}`,
    });
  }

  getVersions(): VersionInfo | null {
    return this.versions;
  }

  /** Low-level append. Prefer the typed helpers below. */
  record(
    category: ObservabilityCategory,
    message: string,
    fields: Record<string, FieldValue> = {},
  ): void {
    this.events.push({ at: this.clock(), category, message, fields });
    if (this.events.length > this.capacity) {
      this.events.splice(0, this.events.length - this.capacity);
    }
  }

  // --- typed helpers ------------------------------------------------------

  /** Bridge sidecar lifecycle transition (ready / error / unavailable / restart). */
  recordBridge(state: "ready" | "error" | "unavailable" | "restart", detail?: string): void {
    this.record("bridge", `bridge ${state}`, detail ? { detail } : {});
  }

  /**
   * One completed bridge request. Only the method, duration, and stable error
   * code are recorded — never the parameters, which may name servers, apps, or
   * carry other operational detail the plan wants kept out of the log.
   */
  recordRequest(method: string, durationMs: number, code: string | null): void {
    this.record("request", code ? `${method} failed` : `${method} ok`, {
      method,
      durationMs: Math.round(durationMs),
      code,
    });
  }

  /** A poll was scheduled for a server after `delayMs`. */
  recordPollScheduled(delayMs: number): void {
    this.record("poll", "poll scheduled", { delayMs: Math.round(delayMs) });
  }

  /** A poll settled. `ageMs` is how stale the displayed cache now is (0 when the
   * poll succeeded); `ok` reflects reachability. */
  recordPollResult(ok: boolean, ageMs: number | null): void {
    this.record("poll", ok ? "poll ok" : "poll stale", {
      ok,
      cacheAgeMs: ageMs == null ? null : Math.round(ageMs),
    });
  }

  /** A transition notification was delivered to the OS. `key` identifies the
   * finding, not the occurrence — it is a stable dedup key, not a secret. */
  recordNotification(key: string, severity: string): void {
    this.record("notification", "notification delivered", { key, severity });
  }

  /** An update check ran. */
  recordUpdateCheck(available: boolean, version: string | null): void {
    this.record("update", available ? "update available" : "up to date", {
      available,
      version,
    });
  }

  /** An update install failed. A signature-verification failure is flagged so a
   * support bundle makes a fail-closed updater obvious. */
  recordUpdateFailure(message: string): void {
    this.record("update", "update failed", {
      signatureFailure: isSignatureFailure(message),
      // The message is a plugin/status string, not a user parameter; still, keep
      // only a short, bounded slice of it.
      detail: message.slice(0, 200),
    });
  }

  /** A sleep/wake, online/offline, or reconnect transition triggered a refresh. */
  recordLifecycle(trigger: string): void {
    this.record("lifecycle", `refresh on ${trigger}`, { trigger });
  }

  // --- read model ---------------------------------------------------------

  /** A copy of the retained events, oldest first. */
  snapshot(): ObservabilityEvent[] {
    return this.events.map((e) => ({ ...e, fields: { ...e.fields } }));
  }

  size(): number {
    return this.events.length;
  }

  clear(): void {
    this.events = [];
  }
}

/** A message names a signature/verification failure. The Tauri updater fails
 * closed on a bad signature; surfacing that in the bundle helps support. */
function isSignatureFailure(message: string): boolean {
  return /signature|verif|untrusted|public key/i.test(message);
}

// A single process-wide log. The popover and management windows are separate
// webview realms, so each gets its own instance; that is fine — a bundle is
// exported from whichever window the user is in, and both windows drive the
// same underlying bridge.
let shared: ObservabilityLog | null = null;

/** The process-wide observability log, created lazily. */
export function getObservability(): ObservabilityLog {
  if (!shared) shared = new ObservabilityLog();
  return shared;
}

/** Test seam: replace (or reset) the shared instance. */
export function setObservability(log: ObservabilityLog | null): void {
  shared = log;
}
