import type { ActionHistoryEntry } from "../../lib/actions";
import { actionLabel } from "../../lib/actions";
import { formatRelativeTime } from "../../lib/format";

export interface ActionHistoryListProps {
  entries: ActionHistoryEntry[];
  /** Cap the rendered rows (newest first). */
  limit?: number;
}

/**
 * The local action history. Entries carry only workload identifiers and states
 * — never secrets (plan: "Store a local action history without environment
 * values, passwords, private keys, license keys, or complete unredacted logs").
 */
export function ActionHistoryList({ entries, limit }: ActionHistoryListProps) {
  const rows = limit ? entries.slice(0, limit) : entries;

  if (rows.length === 0) {
    return <p className="panel__empty">No actions run yet.</p>;
  }

  return (
    <ul className="action-history">
      {rows.map((e) => (
        <li key={e.operationId} className="action-history__item">
          <span
            className={`action-history__status action-history__status--${e.status}`}
            aria-label={e.status}
          />
          <span className="action-history__summary">
            {actionLabel(e.action)} {e.app}
          </span>
          <span className="action-history__server">{e.server}</span>
          <time className="action-history__time" dateTime={e.at}>
            {formatRelativeTime(e.at)}
          </time>
        </li>
      ))}
    </ul>
  );
}
