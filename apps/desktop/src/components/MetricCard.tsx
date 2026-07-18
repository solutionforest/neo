export type MetricTone = "normal" | "warning" | "critical";

export interface MetricCardProps {
  label: string;
  value: string;
  /** Optional secondary line, e.g. "6.1 GB / 16 GB". */
  detail?: string;
  /** Optional 0–100 gauge. `null` (an unavailable metric) hides the gauge. */
  percent?: number | null;
  tone?: MetricTone;
}

export function MetricCard({
  label,
  value,
  detail,
  percent,
  tone = "normal",
}: MetricCardProps) {
  return (
    <div className={`metric-card metric-card--${tone}`} role="group" aria-label={label}>
      <div className="metric-card__label">{label}</div>
      <div className="metric-card__value">{value}</div>
      {detail ? <div className="metric-card__detail">{detail}</div> : null}
      {percent != null ? (
        <div
          className="metric-card__gauge"
          role="meter"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(percent)}
          aria-label={`${label} usage`}
        >
          <span
            className="metric-card__gauge-fill"
            style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
          />
        </div>
      ) : null}
    </div>
  );
}
