// Small, dependency-free formatting helpers shared across the UI.

const KiB = 1024;

// Metric values are nullable: the bridge sends `null` for a metric the server
// could not report (never a misleading 0). Every helper treats null/undefined
// like a non-finite value and renders the "unavailable" fallback.
type Metric = number | null | undefined;

/** Human-readable bytes, e.g. 6.1 GB. Binary units, one decimal above KB. */
export function formatBytes(bytes: Metric): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let value = bytes;
  let unit = 0;
  while (value >= KiB && unit < units.length - 1) {
    value /= KiB;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : 1;
  return `${value.toFixed(digits)} ${units[unit]}`;
}

/** Percentage of used/total, clamped to [0, 100]; 0 when total is missing/0. */
export function usagePercent(used: Metric, total: Metric): number {
  if (used == null || total == null || !Number.isFinite(total) || total <= 0) return 0;
  return Math.min(100, Math.max(0, (used / total) * 100));
}

export function formatPercent(value: Metric): string {
  if (value == null || !Number.isFinite(value)) return "—";
  return `${value.toFixed(value >= 10 ? 0 : 1)}%`;
}

export function formatLatency(ms: Metric): string {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return "—";
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  return `${Math.round(ms)} ms`;
}

/**
 * Coarse relative time ("just now", "3m ago", "2h ago", "4d ago").
 * `now` is injectable so tests are deterministic.
 */
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "unknown";
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 45) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}
