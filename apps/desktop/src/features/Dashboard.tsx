import { useEffect, useReducer, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "../lib/api";
import { aggregateTrayState } from "../lib/desktop-api";
import { terminals } from "../lib/terminals";
import { toast, type Toast } from "../lib/toast";
import { TerminalPanel } from "./Terminal";
import { Modal } from "../components/Modal";
import {
  GlobeIcon,
  LogsIcon,
  MoreIcon,
  PlayIcon,
  RestartIcon,
  StopIcon,
  TerminalIcon,
  TrashIcon,
} from "../components/icons";
import type { AppSummary, Finding, ServerSnapshot, ServerSummary } from "../lib/protocol";

const pct = (u: number, t: number) => (t > 0 ? Math.round((u / t) * 100) : 0);
const gib = (b: number) => (b ? (b / 1024 ** 3).toFixed(1) : "0");

async function startDrag(e: React.MouseEvent) {
  if (e.button !== 0) return;
  if ((e.target as HTMLElement).closest("button,input,a,.term,.term-tab")) return;
  try {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    await getCurrentWindow().startDragging();
  } catch {
    /* browser preview */
  }
}

export function Dashboard() {
  const [servers, setServers] = useState<ServerSummary[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [err, setErr] = useState("");
  const [termOpen, setTermOpen] = useState(false);
  const [termFull, setTermFull] = useState(false);

  useEffect(() => {
    api
      .listServers()
      .then((s) => {
        setServers(s);
        setSelected(s.find((x) => x.current)?.name ?? s[0]?.name ?? "");
      })
      .catch((e) => setErr(String(e)));
    terminals.onReveal(() => setTermOpen(true));
  }, []);

  const current = servers.find((s) => s.name === selected);

  function toggleTerm() {
    setTermOpen((o) => {
      const next = !o;
      if (next && terminals.tabs.length === 0 && current) {
        terminals.openServer({ server: current.name, host: current.host, port: current.port, keyPath: current.keyPath });
      }
      return next;
    });
  }

  return (
    <div className="win">
      <aside className="sidebar">
        <div className="side-drag" onMouseDown={startDrag} />
        <div className="side-group-title">Servers</div>
        <nav className="side-list">
          {servers.map((s) => (
            <SideRow key={s.name} server={s} active={s.name === selected} onClick={() => setSelected(s.name)} />
          ))}
          {servers.length === 0 && !err && <div className="side-empty">No servers</div>}
        </nav>
        {err && <div className="side-empty err">{err}</div>}
      </aside>

      <main className={`detail ${termOpen && termFull ? "term-max" : ""}`}>
        {current ? (
          <Detail key={current.name} server={current} termOpen={termOpen} onToggleTerm={toggleTerm} />
        ) : (
          <div className="detail-empty" onMouseDown={startDrag}>
            Select a server
          </div>
        )}

        <TermDrawer
          current={current}
          open={termOpen}
          full={termFull}
          onToggleFull={() => setTermFull((v) => !v)}
          onClose={() => { setTermOpen(false); setTermFull(false); }}
        />
      </main>

      <Toaster />
    </div>
  );
}

function TermDrawer({
  current,
  open,
  full,
  onToggleFull,
  onClose,
}: {
  current: ServerSummary | undefined;
  open: boolean;
  full: boolean;
  onToggleFull: () => void;
  onClose: () => void;
}) {
  const [, force] = useReducer((x) => x + 1, 0);
  useEffect(() => terminals.subscribe(force), []);
  const tabs = terminals.tabs;
  const activeId = terminals.activeId;

  function addServer() {
    if (current && !terminals.atLimit()) {
      terminals.openServer({ server: current.name, host: current.host, port: current.port, keyPath: current.keyPath });
    }
  }

  return (
    <div className={`term-drawer ${open ? "open" : ""} ${full ? "max" : ""}`}>
      <div className="term-head" onMouseDown={startDrag} onDoubleClick={() => open && onToggleFull()}>
        <div className="term-tabs">
          {tabs.map((t) => (
            <button
              key={t.id}
              className={`term-tab ${t.id === activeId ? "active" : ""}`}
              onClick={() => terminals.setActive(t.id)}
            >
              <span className={`tab-dot ${t.spec.kind}`} />
              <span className="tab-title">{t.title}</span>
              <span className="tab-close" onClick={(e) => { e.stopPropagation(); terminals.close(t.id); }}>✕</span>
            </button>
          ))}
          <button className="term-add" onClick={addServer} disabled={terminals.atLimit() || !current} title="New server terminal">
            ＋
          </button>
        </div>
        <span className="term-head-btns">
          <button className="mini" onClick={onToggleFull} title={full ? "Restore" : "Full screen"}>{full ? "❐" : "⤢"}</button>
          <button className="mini" onClick={onClose} title="Hide terminal">▾</button>
        </span>
      </div>
      <div className="term-stack">
        {tabs.length === 0 ? (
          <div className="term-empty">No terminal open. Click ＋ for a server shell, or an app's “Container shell”.</div>
        ) : (
          tabs.map((t) => <TerminalPanel key={t.id} tab={t} active={t.id === activeId} />)
        )}
      </div>
    </div>
  );
}

function Toaster() {
  const [items, setItems] = useState<Toast[]>([]);
  useEffect(() => toast.subscribe(setItems), []);
  return (
    <div className="toaster">
      {items.map((t) => (
        <div key={t.id} className={`toast ${t.kind}`}>
          {t.msg}
        </div>
      ))}
    </div>
  );
}

function SideRow({ server, active, onClick }: { server: ServerSummary; active: boolean; onClick: () => void }) {
  return (
    <button className={`side-row ${active ? "active" : ""}`} onClick={onClick}>
      <span className="state-dot healthy" />
      <span className="side-name">{server.name}</span>
      {server.current && <span className="side-current" title="current server" />}
    </button>
  );
}

function Detail({
  server,
  termOpen,
  onToggleTerm,
}: {
  server: ServerSummary;
  termOpen: boolean;
  onToggleTerm: () => void;
}) {
  const [snap, setSnap] = useState<ServerSnapshot | null>(null);
  const [apps, setApps] = useState<AppSummary[]>([]);
  const [findings, setFindings] = useState<Finding[]>([]);
  const [loading, setLoading] = useState(true);

  async function load() {
    setLoading(true);
    try {
      const s = await api.getSnapshot(server.name);
      setSnap(s);
      if (s.reachable) {
        const [a, f] = await Promise.all([api.listApps(server.name), api.runDiagnostics(server.name)]);
        setApps(a ?? []);
        setFindings(f ?? []);
      } else {
        setApps([]);
        setFindings([]);
      }
    } catch {
      setSnap((prev) => prev ?? ({ server: server.name, reachable: false } as ServerSnapshot));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name]);

  const state = snap ? aggregateTrayState([snap], findings) : "unknown";

  return (
    <>
      <div className="toolbar" onMouseDown={startDrag}>
        <div className="toolbar-title">
          <span className={`state-dot ${state}`} />
          <h1>{server.name}</h1>
          <span className="toolbar-host">{server.host}</span>
        </div>
        <button className={`tbtn ${termOpen ? "on" : ""}`} onClick={onToggleTerm} title="SSH terminal into this server">
          <TerminalIcon width={14} height={14} /> Terminal
        </button>
        <button className="mini" disabled={loading} onClick={load} title="Refresh">
          {loading ? "…" : "↻"}
        </button>
      </div>

      <div className="detail-body">
        {loading && !snap ? (
          <div className="detail-empty">Connecting over SSH…</div>
        ) : snap && snap.reachable ? (
          <>
            <div className="tiles">
              <Tile label="CPU" value={`${Math.round(snap.cpuPercent)}%`} bar={snap.cpuPercent} />
              <Tile label="Memory" value={`${pct(snap.ramUsedBytes, snap.ramTotalBytes)}%`} bar={pct(snap.ramUsedBytes, snap.ramTotalBytes)} sub={`${gib(snap.ramUsedBytes)} / ${gib(snap.ramTotalBytes)} GB`} />
              <Tile label="Disk" value={`${pct(snap.diskUsedBytes, snap.diskTotalBytes)}%`} bar={pct(snap.diskUsedBytes, snap.diskTotalBytes)} sub={`${gib(snap.diskUsedBytes)} / ${gib(snap.diskTotalBytes)} GB`} />
              <Tile label="Uptime" value={`${Math.floor(snap.uptimeSeconds / 86400)}d`} sub={`${snap.latencyMs} ms ping`} />
            </div>

            {findings.length > 0 && (
              <section className="group">
                <div className="group-title">Alerts</div>
                <div className="group-box">
                  {findings.map((f) => (
                    <div key={f.id} className={`finding-row ${f.severity}`}>
                      <span className="fdot" />
                      <span>{f.summary}</span>
                    </div>
                  ))}
                </div>
              </section>
            )}

            <section className="group">
              <div className="group-title">
                Applications
                <span className="group-count">{snap.apps.running}/{snap.apps.total} running</span>
              </div>
              <div className="group-box">
                {apps.length === 0 ? (
                  <div className="group-empty">No apps installed</div>
                ) : (
                  apps.map((a) => <AppRow key={a.name} app={a} srv={server} onChanged={load} />)
                )}
              </div>
            </section>
          </>
        ) : (
          <div className="detail-empty">Server unreachable</div>
        )}
      </div>
    </>
  );
}

type Confirm = { action: "restart" | "stop" | "remove"; danger?: boolean } | null;

function AppRow({ app, srv, onChanged }: { app: AppSummary; srv: ServerSummary; onChanged: () => void }) {
  const [status, setStatus] = useState(app.status);
  const [busy, setBusy] = useState(false);
  const [menu, setMenu] = useState(false);
  const [menuPos, setMenuPos] = useState<{ top: number; left: number } | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [logsOpen, setLogsOpen] = useState(false);
  const [domainOpen, setDomainOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const moreRef = useRef<HTMLButtonElement>(null);
  const running = status === "running";

  function openMenu() {
    const r = moreRef.current?.getBoundingClientRect();
    if (r) setMenuPos({ top: r.bottom + 4, left: Math.max(8, r.right - 186) });
    setMenu(true);
  }

  useEffect(() => {
    if (!menu) return;
    const away = (e: MouseEvent) => {
      const t = e.target as Node;
      if (menuRef.current?.contains(t) || moreRef.current?.contains(t)) return;
      setMenu(false);
    };
    window.addEventListener("mousedown", away);
    return () => window.removeEventListener("mousedown", away);
  }, [menu]);

  async function act(action: "restart" | "stop" | "start" | "remove") {
    setBusy(true);
    setConfirm(null);
    try {
      const r = (await api.runAppAction({ server: srv.name, app: app.name, action: action as never })) as unknown as { status?: string };
      const past = action === "stop" ? "stopped" : action === "start" ? "started" : action === "remove" ? "removed" : "restarted";
      if (action !== "remove") setStatus((r.status as typeof status) ?? (action === "stop" ? "stopped" : "running"));
      toast.show(`${app.name} ${past}`, "ok");
      onChanged();
    } catch (e) {
      toast.show(`${app.name}: ${String(e).replace(/^Error:\s*/, "")}`, "err");
    } finally {
      setBusy(false);
    }
  }

  function containerShell() {
    setMenu(false);
    terminals.openContainer({ server: srv.name, host: srv.host, port: srv.port, keyPath: srv.keyPath, app: app.name });
  }

  return (
    <div className="table-row">
      <span className={`app-dot ${status}`} />
      <span className="table-name">{app.name}</span>
      <span className="table-sub">{app.domain || app.image}</span>
      <span className={`table-status ${status}`}>{status}</span>
      <span className="row-actions">
        {busy ? (
          <span className="row-spin" />
        ) : running ? (
          <>
            <button className="act" onClick={() => setConfirm({ action: "restart" })} title="Restart">
              <RestartIcon />
            </button>
            <button className="act" onClick={() => setConfirm({ action: "stop" })} title="Stop">
              <StopIcon />
            </button>
          </>
        ) : (
          <button className="act" onClick={() => act("start")} title="Start">
            <PlayIcon />
          </button>
        )}
        <button className="act" ref={moreRef} onClick={() => (menu ? setMenu(false) : openMenu())} title="More">
          <MoreIcon />
        </button>
        {menu && menuPos &&
          createPortal(
            <div className="menu-pop" ref={menuRef} style={{ position: "fixed", top: menuPos.top, left: menuPos.left, right: "auto" }}>
              <button onClick={() => { setMenu(false); setLogsOpen(true); }}>
                <LogsIcon width={14} height={14} /> View logs
              </button>
              <button onClick={() => { setMenu(false); setDomainOpen(true); }}>
                <GlobeIcon width={14} height={14} /> Set domain / HTTPS
              </button>
              <button onClick={containerShell}>
                <TerminalIcon width={14} height={14} /> Container shell
              </button>
              <button className="danger" onClick={() => { setMenu(false); setConfirm({ action: "remove", danger: true }); }}>
                <TrashIcon width={14} height={14} /> Remove
              </button>
            </div>,
            document.body
          )}
      </span>

      {confirm && (
        <ConfirmDialog
          appName={app.name}
          action={confirm.action}
          danger={confirm.danger}
          onCancel={() => setConfirm(null)}
          onConfirm={() => act(confirm.action)}
        />
      )}
      {logsOpen && <LogsModal server={srv.name} app={app.name} onClose={() => setLogsOpen(false)} />}
      {domainOpen && (
        <DomainDialog
          server={srv.name}
          app={app.name}
          current={app.domain}
          onClose={() => setDomainOpen(false)}
          onSaved={(d) => { toast.show(`${app.name} → ${d}`, "ok"); setDomainOpen(false); onChanged(); }}
        />
      )}
    </div>
  );
}

function ConfirmDialog({
  appName,
  action,
  danger,
  onCancel,
  onConfirm,
}: {
  appName: string;
  action: string;
  danger?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const verb = action[0].toUpperCase() + action.slice(1);
  return (
    <Modal
      title={`${verb} ${appName}?`}
      onClose={onCancel}
      footer={
        <>
          <button className="btn" onClick={onCancel}>Cancel</button>
          <button className={`btn ${danger ? "danger" : "primary"}`} onClick={onConfirm}>{verb}</button>
        </>
      }
    >
      {action === "remove" ? (
        <p className="dialog-text">
          This stops and removes the <b>{appName}</b> container and its proxy route. Volumes (data) are kept.
        </p>
      ) : (
        <p className="dialog-text">
          {verb} the <b>{appName}</b> container now?
        </p>
      )}
    </Modal>
  );
}

function LogsModal({ server, app, onClose }: { server: string; app: string; onClose: () => void }) {
  const [logs, setLogs] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const preRef = useRef<HTMLPreElement>(null);

  async function load() {
    setLogs(null);
    setErr("");
    try {
      const l = await api.getLogs(server, app);
      setLogs(l || "(no output)");
    } catch (e) {
      setErr(String(e).replace(/^Error:\s*/, ""));
    }
  }
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (preRef.current) preRef.current.scrollTop = preRef.current.scrollHeight;
  }, [logs]);

  return (
    <Modal
      title={`Logs — ${app}`}
      wide
      onClose={onClose}
      footer={
        <>
          <span className="muted small" style={{ flex: 1 }}>last 500 lines</span>
          <button className="btn" onClick={load}>Refresh</button>
          <button className="btn primary" onClick={onClose}>Done</button>
        </>
      }
    >
      {err ? (
        <div className="offline">{err}</div>
      ) : logs === null ? (
        <div className="loading">loading…</div>
      ) : (
        <pre className="logs" ref={preRef}>{logs}</pre>
      )}
    </Modal>
  );
}

function DomainDialog({
  server,
  app,
  current,
  onClose,
  onSaved,
}: {
  server: string;
  app: string;
  current: string;
  onClose: () => void;
  onSaved: (domain: string) => void;
}) {
  const [domain, setDomain] = useState(current || "");
  const [https, setHttps] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function save() {
    const d = domain.trim();
    if (!d) return;
    setBusy(true);
    setErr("");
    try {
      await api.setDomain({ server, app, domain: d, https });
      onSaved(d);
    } catch (e) {
      setErr(String(e).replace(/^Error:\s*/, ""));
      setBusy(false);
    }
  }

  return (
    <Modal
      title={`Domain — ${app}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={busy || !domain.trim()} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </button>
        </>
      }
    >
      <label className="field">
        <span className="field-label">Domain</span>
        <input
          className="input"
          value={domain}
          autoFocus
          placeholder="app.example.com"
          onChange={(e) => setDomain(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && save()}
        />
      </label>
      <label className="check">
        <input type="checkbox" checked={https} onChange={(e) => setHttps(e.target.checked)} />
        <span>HTTPS (auto-provision certificate via Let's Encrypt)</span>
      </label>
      <p className="dialog-text muted small">Point this domain's DNS A record at the server first.</p>
      {err && <div className="offline">{err}</div>}
    </Modal>
  );
}

function Tile({ label, value, bar, sub }: { label: string; value: string; bar?: number; sub?: string }) {
  const level = bar == null ? "ok" : bar >= 90 ? "critical" : bar >= 75 ? "warning" : "ok";
  return (
    <div className="tile">
      <div className="tile-label">{label}</div>
      <div className="tile-val">{value}</div>
      {bar != null && (
        <div className="bar">
          <div className={`bar-fill ${level}`} style={{ width: `${Math.min(100, bar)}%` }} />
        </div>
      )}
      {sub && <div className="tile-sub">{sub}</div>}
    </div>
  );
}
