import type { Finding } from "../../lib/protocol";

const SEVERITY_GLYPH: Record<Finding["severity"], string> = {
  info: "ℹ",
  warning: "▲",
  critical: "✕",
};

export interface FindingsListProps {
  findings: Finding[];
  /** Cap the number rendered; the popover shows up to three. */
  limit?: number;
}

export function FindingsList({ findings, limit }: FindingsListProps) {
  const shown = limit ? findings.slice(0, limit) : findings;
  const hidden = findings.length - shown.length;

  if (findings.length === 0) {
    return (
      <div className="findings findings--empty" aria-label="Findings">
        No findings — everything looks healthy.
      </div>
    );
  }

  return (
    <ul className="findings" aria-label="Findings">
      {shown.map((f) => (
        <li key={f.id} className={`finding finding--${f.severity}`}>
          <span className="finding__glyph" aria-hidden="true">
            {SEVERITY_GLYPH[f.severity]}
          </span>
          <div className="finding__body">
            <span className="finding__summary">{f.summary}</span>
            {f.evidence.length > 0 ? (
              <span className="finding__evidence">
                {f.evidence.map((e) => `${e.label}: ${e.value}`).join(" · ")}
              </span>
            ) : null}
          </div>
        </li>
      ))}
      {hidden > 0 ? (
        <li className="finding finding--more">+{hidden} more in dashboard</li>
      ) : null}
    </ul>
  );
}
