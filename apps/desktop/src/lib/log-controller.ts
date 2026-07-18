// The frontend-side owner of one log stream's in-memory state.
//
// The bridge already batches and bounds the stream; this controller adds the
// frontend half the plan calls for (Phase 3 "Log streaming" + frontend testing
// list: "Log batching, pause, clear, and bounded in-memory history"):
//
//   * Bounded history — never keeps more than `maxLines`, dropping the oldest so
//     a long-running follow stream cannot grow memory without limit.
//   * Batching — coalesces bursts of incoming lines into a single change
//     notification (via an injectable scheduler) so the UI re-renders once per
//     tick, not once per line.
//   * Pause — freezes the visible view while still counting how many lines
//     arrived, then flushes them on resume.
//   * Clear — empties the visible history (and any held-while-paused lines).
//
// It is framework-agnostic and fully deterministic under test: time, batching,
// and the transport are all injected.

import type { DesktopAPI } from "./desktop-api";
import {
  clampTail,
  type BridgeError,
  type LogClosedReason,
  type LogSubscribeInput,
  type LogSubscription,
} from "./protocol";

/** One rendered log line with a stable, monotonic key for React lists. */
export interface LogLine {
  seq: number;
  text: string;
}

/** Schedules a coalesced flush. Defaults to a microtask; tests inject a manual
 * one so batching is observable without timers. */
export type FlushScheduler = (run: () => void) => void;

export interface LogControllerOptions {
  api: DesktopAPI;
  input: LogSubscribeInput;
  /** Called whenever the visible state changes (drives a React re-render). */
  onChange: () => void;
  /** Hard cap on retained lines. Defaults to MAX_LOG_TAIL. */
  maxLines?: number;
  /** Override the flush scheduler (tests). */
  schedule?: FlushScheduler;
}

const DEFAULT_MAX_LINES = 5_000;

export class LogController {
  private readonly api: DesktopAPI;
  private readonly input: LogSubscribeInput;
  private readonly onChange: () => void;
  private readonly schedule: FlushScheduler;
  readonly maxLines: number;

  private lines: LogLine[] = [];
  /** Lines received while paused, withheld from the visible view until resume. */
  private held: LogLine[] = [];
  private seq = 0;

  private paused = false;
  private streamClosed = false;
  private closeReason: LogClosedReason | null = null;
  private error: BridgeError | null = null;

  private sub: LogSubscription | null = null;
  private starting = false;
  private disposed = false;

  private notifyScheduled = false;

  constructor(opts: LogControllerOptions) {
    this.api = opts.api;
    this.input = opts.input;
    this.onChange = opts.onChange;
    this.maxLines = opts.maxLines ?? clampMax(opts.input.tail, DEFAULT_MAX_LINES);
    this.schedule = opts.schedule ?? ((run) => queueMicrotask(run));
  }

  /** Open the stream. Safe to call once; a second call is a no-op. */
  async start(): Promise<void> {
    if (this.starting || this.sub || this.disposed) return;
    this.starting = true;
    try {
      const sub = await this.api.subscribeLogs(this.input, {
        onLines: (lines) => this.ingest(lines),
        onClosed: (reason, error) => this.onClosed(reason, error),
      });
      if (this.disposed) {
        // Disposed while awaiting the subscription — cancel it immediately.
        void sub.close();
        return;
      }
      this.sub = sub;
    } catch (err) {
      this.error = err as BridgeError;
      this.streamClosed = true;
      this.closeReason = "error";
      this.notify();
    } finally {
      this.starting = false;
    }
  }

  /** Cancel the stream and release the subscription. Idempotent. */
  async close(): Promise<void> {
    this.disposed = true;
    const sub = this.sub;
    this.sub = null;
    if (sub) await sub.close();
  }

  // --- incoming -----------------------------------------------------------

  private ingest(incoming: string[]): void {
    if (incoming.length === 0) return;
    const mapped = incoming.map((text) => ({ seq: this.seq++, text }));
    if (this.paused) {
      this.held.push(...mapped);
      cap(this.held, this.maxLines);
    } else {
      this.lines.push(...mapped);
      cap(this.lines, this.maxLines);
    }
    this.notify();
  }

  private onClosed(reason: LogClosedReason, error?: BridgeError): void {
    this.streamClosed = true;
    this.closeReason = reason;
    this.error = error ?? null;
    this.notify();
  }

  // --- user intent --------------------------------------------------------

  pause(): void {
    if (this.paused) return;
    this.paused = true;
    this.notify();
  }

  resume(): void {
    if (!this.paused) return;
    this.paused = false;
    if (this.held.length > 0) {
      this.lines.push(...this.held);
      this.held = [];
      cap(this.lines, this.maxLines);
    }
    this.notify();
  }

  togglePause(): void {
    this.paused ? this.resume() : this.pause();
  }

  clear(): void {
    this.lines = [];
    this.held = [];
    this.notify();
  }

  // --- read model ---------------------------------------------------------

  getLines(): LogLine[] {
    return this.lines;
  }

  isPaused(): boolean {
    return this.paused;
  }

  /** Lines buffered while paused (the "N new lines" affordance). */
  pendingCount(): number {
    return this.held.length;
  }

  isStreamClosed(): boolean {
    return this.streamClosed;
  }

  getCloseReason(): LogClosedReason | null {
    return this.closeReason;
  }

  getError(): BridgeError | null {
    return this.error;
  }

  // --- batching -----------------------------------------------------------

  private notify(): void {
    if (this.notifyScheduled) return;
    this.notifyScheduled = true;
    this.schedule(() => {
      this.notifyScheduled = false;
      if (!this.disposed) this.onChange();
    });
  }
}

/** Drop the oldest entries so `buf` holds at most `max` lines (in place). */
function cap(buf: LogLine[], max: number): void {
  if (buf.length > max) buf.splice(0, buf.length - max);
}

/** The retained history should comfortably exceed the requested backlog; pick
 * the larger of the default cap and the (clamped) requested tail. */
function clampMax(tail: number | undefined, fallback: number): number {
  return Math.max(fallback, clampTail(tail));
}
