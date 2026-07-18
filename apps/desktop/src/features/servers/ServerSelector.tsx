import type { ServerSummary } from "../../lib/protocol";

export interface ServerSelectorProps {
  servers: ServerSummary[];
  selected: string;
  onSelect: (server: string) => void;
  disabled?: boolean;
}

export function ServerSelector({
  servers,
  selected,
  onSelect,
  disabled,
}: ServerSelectorProps) {
  return (
    <label className="server-selector">
      <span className="server-selector__label">Server</span>
      <span className="server-selector__control">
      <select
        className="server-selector__select"
        value={selected}
        disabled={disabled || servers.length === 0}
        aria-label="Select server"
        onChange={(e) => onSelect(e.target.value)}
      >
        {servers.length === 0 ? (
          <option value="">No servers configured</option>
        ) : (
          servers.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))
        )}
      </select>
      <span className="server-selector__chevron" aria-hidden="true">⌄</span>
      </span>
    </label>
  );
}
