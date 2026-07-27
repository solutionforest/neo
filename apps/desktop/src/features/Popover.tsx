import { useEffect, useState, type ReactNode } from "react";
import { api } from "../lib/api";
import { aggregateTrayState } from "../lib/desktop-api";
import type {
  AppSummary,
  BridgeHello,
  Finding,
  ServerSnapshot,
  ServerSummary,
  TrayState,
} from "../lib/protocol";

type View = "home" | "servers" | "apps";

async function invokeCmd(cmd: string) {
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    await invoke(cmd);
  } catch {
    /* browser preview */
  }
}

const pct = (u: number, t: number) => (t > 0 ? Math.round((u / t) * 100) : 0);

export function Popover() {
  const [hello, setHello] = useState<BridgeHello | null>(null);
  const [servers, setServers] = useState<ServerSummary[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [err, setErr] = useState<string>("");
  const [view, setView] = useState<View>("home");

  useEffect(() => {
    (async () => {
      try {
        const [h, s] = await Promise.all([api.hello(), api.listServers()]);
        setHello(h);
        setServers(s);
        setSelected(s.find((x) => x.current)?.name ?? s[0]?.name ?? "");
      } catch (e) {
        setErr(String(e));
      }
    })();
  }, []);

  const current = servers.find((s) => s.name === selected);

  return (
    <div className="popover">
      <header className="pop-head">
        <div className="brand">
          <span className="logo">⚡</span>
          <span>Neo</span>
        </div>
        <div className="head-right">
          {hello && !hello.activated ? (
            <span className="badge bad">activate</span>
          ) : (
            hello && <span className="ver">{hello.cliCore}</span>
          )}
        </div>
      </header>

      {err && <div className="offline">{err}</div>}

      {view === "home" && current && (
        <StatusHome server={current} onSwitch={() => setView("servers")} onAllApps={() => setView("apps")} />
      )}
      {view === "home" && !current && !err && <div className="loading">No servers.</div>}

      {view === "servers" && (
        <ServersView
          servers={servers}
          selected={selected}
          onPick={(name) => {
            setSelected(name);
            setView("home");
          }}
          onBack={() => setView("home")}
        />
      )}
      {view === "apps" && <AppsView server={selected} onBack={() => setView("home")} />}

      <footer className="pop-foot">
        <button className="btn primary" onClick={() => invokeCmd("open_dashboard")}>
          Open Dashboard
        </button>
        <button className="btn ghost icon" onClick={() => invokeCmd("quit_app")} title="Quit">
          ⏻
        </button>
      </footer>
    </div>
  );
}

function StatusHome({
  server,
  onSwitch,
  onAllApps,
}: {
  server: ServerSummary;
  onSwitch: () => void;
  onAllApps: () => void;
}) {
  const [snap, setSnap] = useState<ServerSnapshot | null>(null);
  const [apps, setApps] = useState<AppSummary[]>([]);
  const [findings, setFindings] = useState<Finding[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  async function load() {
    setLoading(true);
    setError("");
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
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name]);

  const state: TrayState = snap ? aggregateTrayState([snap], findings) : "unknown";
  const status = loading
    ? "connecting…"
    : error || (snap && !snap.reachable)
    ? "offline"
    : snap
    ? `${snap.latencyMs}ms`
    : "";

  return (
    <div className="view">
      <button className="server-pill" onClick={onSwitch} title="Switch server">
        <span className={`state-dot ${state}`} />
        <span className="pill-name">{server.name}</span>
        <span className="pill-status">{status}</span>
        <span className="chev">⇅</span>
      </button>

      {snap && snap.reachable ? (
        <>
          <div className="gauges">
            <Gauge label="CPU" v={Math.round(snap.cpuPercent)} />
            <Gauge label="RAM" v={pct(snap.ramUsedBytes, snap.ramTotalBytes)} />
            <Gauge label="DSK" v={pct(snap.diskUsedBytes, snap.diskTotalBytes)} />
            <div className="up">
              <span className="muted xsmall">UP</span>
              <span className="up-val">{Math.floor(snap.uptimeSeconds / 86400)}d</span>
            </div>
          </div>

          {findings.slice(0, 1).map((f) => (
            <div key={f.id} className={`finding ${f.severity}`}>
              <span className="fdot" /> {f.summary}
            </div>
          ))}

          <button className="sec-head" onClick={onAllApps}>
            <span className="sec-label">Apps</span>
            <span className="sec-count">{snap.apps.running}/{snap.apps.total}</span>
            <span className="chev">›</span>
          </button>
          <div className="list tight">
            {apps.length === 0 ? (
              <div className="group-empty">No apps</div>
            ) : (
              apps.slice(0, 3).map((a) => (
                <div key={a.name} className="app-row">
                  <span className={`app-dot ${a.status}`} />
                  <span className="app-name">{a.name}</span>
                  <span className="muted xsmall">{a.domain || a.status}</span>
                </div>
              ))
            )}
            {apps.length > 3 && (
              <button className="more" onClick={onAllApps}>
                +{apps.length - 3} more
              </button>
            )}
          </div>
        </>
      ) : loading ? (
        <div className="loading">connecting over SSH…</div>
      ) : (
        <div className="offline">
          {error || "unreachable"}
          <button className="mini" style={{ marginLeft: 8 }} onClick={load}>↻</button>
        </div>
      )}
    </div>
  );
}

// Compact horizontal gauge: label + inline bar + %.
function Gauge({ label, v }: { label: string; v: number }) {
  const level = v >= 90 ? "critical" : v >= 75 ? "warning" : "ok";
  return (
    <div className="gauge">
      <span className="muted xsmall">{label}</span>
      <div className="bar">
        <div className={`bar-fill ${level}`} style={{ width: `${Math.min(100, v)}%` }} />
      </div>
      <span className="gauge-val">{v}%</span>
    </div>
  );
}

function BackBar({ title, onBack, right }: { title: string; onBack: () => void; right?: ReactNode }) {
  return (
    <div className="backbar">
      <button className="back" onClick={onBack}>‹</button>
      <span className="backtitle">{title}</span>
      <span className="backright">{right}</span>
    </div>
  );
}

function ServersView({
  servers,
  selected,
  onPick,
  onBack,
}: {
  servers: ServerSummary[];
  selected: string;
  onPick: (n: string) => void;
  onBack: () => void;
}) {
  return (
    <div className="view">
      <BackBar title="Servers" onBack={onBack} />
      <div className="list">
        {servers.map((s) => (
          <button key={s.name} className={`list-row ${s.name === selected ? "active" : ""}`} onClick={() => onPick(s.name)}>
            <span className="pill-name">{s.name}</span>
            <span className="muted xsmall">{s.host}</span>
            {s.current && <span className="badge ok">current</span>}
          </button>
        ))}
      </div>
    </div>
  );
}

function AppsView({ server, onBack }: { server: string; onBack: () => void }) {
  const [apps, setApps] = useState<AppSummary[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    (async () => {
      try {
        setApps(await api.listApps(server));
      } catch (e) {
        setError(String(e));
      }
    })();
  }, [server]);

  return (
    <div className="view">
      <BackBar title="Apps" onBack={onBack} />
      {error ? (
        <div className="offline">{error}</div>
      ) : apps === null ? (
        <div className="loading">loading…</div>
      ) : apps.length === 0 ? (
        <div className="group-empty">No apps installed.</div>
      ) : (
        <div className="list tight">
          {apps.map((a) => (
            <div key={a.name} className="app-row">
              <span className={`app-dot ${a.status}`} />
              <span className="app-name">{a.name}</span>
              <span className="muted xsmall">{a.domain || a.status}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
