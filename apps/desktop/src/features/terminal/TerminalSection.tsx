import { useEffect, useReducer, useState } from "react";
import { terminals } from "../../lib/terminals";
import { TerminalPanel } from "./TerminalPanel";

interface AppRef {
  id: string;
  name: string;
}

// Self-contained terminal panel for the management window: open server shells
// or container shells (over `neo-bridge pty`), switch tabs, and maximize.
// Needs only the selected server NAME and the apps on it — no edits to the rest
// of the UI or the bridge transport.
export function TerminalSection({ server, apps }: { server?: string; apps: AppRef[] }) {
  const [, force] = useReducer((x) => x + 1, 0);
  const [full, setFull] = useState(false);
  const [appSel, setAppSel] = useState("");

  useEffect(() => terminals.subscribe(force), []);

  const tabs = terminals.tabs;
  const activeId = terminals.activeId;

  function openServer() {
    if (server && !terminals.atLimit()) terminals.openServer(server);
  }
  function openContainer() {
    if (server && appSel && !terminals.atLimit()) terminals.openContainer(server, appSel);
  }

  return (
    <section className={`panel panel--wide terminal-panel ${full ? "terminal-panel--full" : ""}`}>
      <div className="panel__heading">
        <h2 className="panel__title">Terminal</h2>
        <div className="terminal-panel__controls">
          <button className="btn btn--small" onClick={openServer} disabled={!server || terminals.atLimit()}>
            ＋ Server shell
          </button>
          <select
            className="terminal-panel__appsel"
            value={appSel}
            onChange={(e) => setAppSel(e.target.value)}
            disabled={!apps.length}
          >
            <option value="">Container…</option>
            {apps.map((a) => (
              <option key={a.id} value={a.name}>
                {a.name}
              </option>
            ))}
          </select>
          <button
            className="btn btn--small"
            onClick={openContainer}
            disabled={!server || !appSel || terminals.atLimit()}
          >
            Open shell
          </button>
          <button className="btn btn--small" onClick={() => setFull((v) => !v)} title={full ? "Restore" : "Full screen"}>
            {full ? "❐" : "⤢"}
          </button>
        </div>
      </div>

      {tabs.length > 0 && (
        <div className="term-tabs">
          {tabs.map((t) => (
            <button
              key={t.id}
              className={`term-tab ${t.id === activeId ? "active" : ""}`}
              onClick={() => terminals.setActive(t.id)}
            >
              <span className={`tab-dot ${t.spec.kind}`} />
              <span className="tab-title">{t.title}</span>
              <span
                className="tab-close"
                onClick={(e) => {
                  e.stopPropagation();
                  terminals.close(t.id);
                }}
              >
                ✕
              </span>
            </button>
          ))}
        </div>
      )}

      <div className="term-stack">
        {tabs.length === 0 ? (
          <div className="term-empty">
            {server ? "Open a server or container shell above." : "Select a server first."}
          </div>
        ) : (
          tabs.map((t) => <TerminalPanel key={t.id} tab={t} active={t.id === activeId} />)
        )}
      </div>
    </section>
  );
}
