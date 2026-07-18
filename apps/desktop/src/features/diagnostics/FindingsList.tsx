import type { Finding } from "../../lib/protocol";
import { formatRelativeTime } from "../../lib/format";
import { Icon } from "../../components/Icon";

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
        <span className="findings__empty-icon" aria-hidden="true">
          <Icon name="check" size={14} />
        </span>
        <span>No findings — everything looks healthy.</span>
      </div>
    );
  }

  return (
    <ul className="findings" aria-label="Findings">
      {shown.map((f) => (
        <li key={f.id} className={`finding finding--${f.severity}`}>
          <span className="finding__glyph" aria-hidden="true">
            <Icon
              name={f.severity === "info" ? "info" : f.severity === "warning" ? "warning" : "close"}
              size={15}
            />
          </span>
          <div className="finding__body">
            <span className="finding__summary">{f.summary}</span>
            {f.evidence.length > 0 ? (
              <span className="finding__evidence">
                {f.evidence.map((e) => `${e.label}: ${e.value}`).join(" · ")}
              </span>
            ) : null}
            <time className="finding__observed" dateTime={f.lastObservedAt}>
              Last observed {formatRelativeTime(f.lastObservedAt)}
            </time>
          </div>
        </li>
      ))}
      {hidden > 0 ? (
        <li className="finding finding--more">+{hidden} more in dashboard</li>
      ) : null}
    </ul>
  );
}
