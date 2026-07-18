import type { AggregateStatus } from "../lib/protocol";
import { Icon } from "./Icon";

const LABELS: Record<AggregateStatus, string> = {
  healthy: "Healthy",
  warning: "Warning",
  critical: "Critical",
  unknown: "Unknown",
};

// Shape/text — not color alone — distinguishes states, per the plan's tray
// accessibility requirement.
export function StatusBadge({ status }: { status: AggregateStatus }) {
  return (
    <span
      className={`status-badge status-badge--${status}`}
      role="status"
      aria-label={`Status: ${LABELS[status]}`}
    >
      <span className="status-badge__glyph" aria-hidden="true">
        {status === "healthy" ? <Icon name="check" size={11} /> : null}
        {status === "warning" ? <Icon name="warning" size={11} /> : null}
        {status === "critical" ? <Icon name="close" size={11} /> : null}
        {status === "unknown" ? <Icon name="info" size={11} /> : null}
      </span>
      {LABELS[status]}
    </span>
  );
}
