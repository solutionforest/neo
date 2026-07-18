// Builds the "Export Diagnostic Bundle" payload (plan "Observability and support
// bundle"). The bundle is a single redacted JSON document a user can hand to
// support. It contains version/config metadata plus the local observability log,
// and it MUST NOT contain:
//
//   * Private keys or their contents.
//   * Passwords and passphrases.
//   * License keys.
//   * Application environment values.
//   * Full server logs — unless the user explicitly opts in after previewing.
//
// Redaction is applied here as defense in depth even though the observability
// log is already secret-free by construction: a support bundle is the one
// artifact that leaves the machine, so every field passes through a scrubber
// before it is serialized. The builder is pure and synchronous so its redaction
// is exhaustively unit-tested; the file write happens in the Rust shell.

import type { ObservabilityEvent, FieldValue, VersionInfo } from "./observability";
import type { ServerSummary } from "./protocol";

/** What the user chose to include. Server logs are opt-in and off by default. */
export interface BundleOptions {
  /** Include captured recent log lines. Off by default; the plan requires an
   * explicit opt-in after preview before any server log text is added. */
  includeServerLogs?: boolean;
}

export interface BundleInput {
  versions: VersionInfo | null;
  servers: ServerSummary[];
  events: ObservabilityEvent[];
  /** Recent streamed log lines, only emitted when includeServerLogs is true. */
  serverLogs?: string[];
  /** ISO timestamp the bundle was generated (injectable for tests). */
  generatedAt: string;
  options?: BundleOptions;
}

export interface DiagnosticBundle {
  schema: "neo-desktop-diagnostics/v1";
  generatedAt: string;
  redacted: true;
  /** Human-facing note about what is deliberately kept out. */
  excludes: string[];
  versions: VersionInfo | null;
  config: {
    serverCount: number;
    servers: Array<{ name: string; host: string; current: boolean }>;
  };
  events: ObservabilityEvent[];
  /** Present only when the user opted in; redacted line-by-line even then. */
  serverLogs?: string[];
}

/** The redaction placeholder inserted wherever a secret was removed. */
export const REDACTED = "[redacted]";

const ALWAYS_EXCLUDED = [
  "Private keys and their contents",
  "Passwords and passphrases",
  "License keys",
  "Application environment values",
];

/**
 * Assemble the redacted bundle. Pure: same input → same output. The caller then
 * serializes it (serializeBundle) for preview and for writing to disk.
 */
export function buildDiagnosticBundle(input: BundleInput): DiagnosticBundle {
  const includeLogs = input.options?.includeServerLogs === true;

  const excludes = [...ALWAYS_EXCLUDED];
  if (!includeLogs) excludes.push("Full server logs (not included)");

  const bundle: DiagnosticBundle = {
    schema: "neo-desktop-diagnostics/v1",
    generatedAt: input.generatedAt,
    redacted: true,
    excludes,
    versions: input.versions ? redactVersions(input.versions) : null,
    config: {
      serverCount: input.servers.length,
      servers: input.servers.map((s) => ({
        name: redactText(s.name),
        // Keep the login user (useful for support) but mask the host address so
        // the bundle never pins the user's infrastructure to an IP/hostname.
        host: maskHost(s.host),
        current: s.current,
      })),
    },
    events: input.events.map(redactEvent),
  };

  if (includeLogs && input.serverLogs && input.serverLogs.length > 0) {
    bundle.serverLogs = input.serverLogs.map(redactText);
  }

  return bundle;
}

/** Pretty-print the bundle for the preview pane and for the exported file. */
export function serializeBundle(bundle: DiagnosticBundle): string {
  return JSON.stringify(bundle, null, 2);
}

/** A stable, timestamped filename with no secrets. */
export function bundleFilename(generatedAt: string): string {
  const stamp = generatedAt.replace(/[:.]/g, "-");
  return `neo-desktop-diagnostics-${stamp}.json`;
}

// --- redaction -------------------------------------------------------------

function redactVersions(v: VersionInfo): VersionInfo {
  // Versions carry no secrets, but activation must never leak a key; it is a
  // coarse status ("active"/"inactive"/…) by contract — pass it through scrubbed.
  return { ...v, activation: redactText(v.activation) };
}

function redactEvent(event: ObservabilityEvent): ObservabilityEvent {
  const fields: Record<string, FieldValue> = {};
  for (const [key, value] of Object.entries(event.fields)) {
    fields[key] = isSecretKey(key)
      ? REDACTED
      : typeof value === "string"
        ? redactText(value)
        : value;
  }
  return { ...event, message: redactText(event.message), fields };
}

/** Field names that must never carry a value into the bundle. */
function isSecretKey(key: string): boolean {
  return /pass(word|phrase)?|secret|token|licen[sc]e|private[_-]?key|credential|api[_-]?key/i.test(
    key,
  );
}

/**
 * Scrub a free-text value. Removes PEM private-key blocks, `KEY=value` secret
 * assignments (env-style), and long opaque base64/hex blobs that could be key
 * material. Non-secret text passes through unchanged.
 */
export function redactText(text: string): string {
  if (!text) return text;
  let out = text;

  // PEM private-key blocks in any form.
  out = out.replace(
    /-----BEGIN[^-]*PRIVATE KEY-----[\s\S]*?-----END[^-]*PRIVATE KEY-----/g,
    REDACTED,
  );

  // Secret-looking assignments: PASSWORD=..., DB_PASS: ..., --license xxxx,
  // "token": "...". The separator may be `=`, `:`, or whitespace (a CLI flag),
  // and the key may be quoted (JSON). Mask the value, keep the key + separator so
  // the shape stays legible. Over-redaction here is deliberate — the bundle
  // fails closed rather than risk leaking a secret.
  out = out.replace(
    /\b([A-Za-z0-9_]*(?:pass(?:word|phrase)?|secret|token|licen[sc]e|api[_-]?key|private[_-]?key|credential)[A-Za-z0-9_]*)"?(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|\S+)/gi,
    (_m, key: string, sep: string) => `${key}${sep}${REDACTED}`,
  );

  // Long opaque blobs (>= 40 chars of base64/hex) — likely key or token material.
  out = out.replace(/\b[A-Za-z0-9+/=_-]{40,}\b/g, REDACTED);

  return out;
}

/**
 * Mask a `user@host` (or bare host) to keep the login name but drop the address.
 * `root@10.1.2.3` → `root@[redacted]`; `1.2.3.4` → `[redacted]`.
 */
export function maskHost(host: string): string {
  if (!host) return host;
  const at = host.lastIndexOf("@");
  if (at >= 0) return `${host.slice(0, at)}@${REDACTED}`;
  return REDACTED;
}
