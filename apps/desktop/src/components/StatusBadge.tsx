import type { AggregateStatus } from "../lib/protocol";

const LABELS: Record<AggregateStatus, string> = {
  healthy: "Healthy",
  warning: "Warning",
  critical: "Critical",
  unknown: "Unknown",
};

// Shape/text — not color alone — distinguishes states, per the plan's tray
// accessibility requirement.
const GLYPHS: Record<AggregateStatus, string> = {
  healthy: "●",
  warning: "▲",
  critical: "✕",
  unknown: "○",
};

export function StatusBadge({ status }: { status: AggregateStatus }) {
  return (
    <span
      className={`status-badge status-badge--${status}`}
      role="status"
      aria-label={`Status: ${LABELS[status]}`}
    >
      <span className="status-badge__glyph" aria-hidden="true">
        {GLYPHS[status]}
      </span>
      {LABELS[status]}
    </span>
  );
}
