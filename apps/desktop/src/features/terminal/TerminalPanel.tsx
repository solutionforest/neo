import { useEffect, useRef } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import type { TermTab } from "../../lib/terminals";

// One PTY session per tab, driven by the Rust `pty_*` commands (which spawn
// `neo-bridge pty …`). Output streams over per-session events; xterm and the
// remote PTY are kept the same size via `pty_resize`.
export function TerminalPanel({ tab, active }: { tab: TermTab; active: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const syncRef = useRef<() => void>(() => {});

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const term = new XTerm({
      fontFamily: '"SF Mono", Menlo, Monaco, "Cascadia Code", monospace',
      fontSize: 12,
      cursorBlink: true,
      allowProposedApi: true,
      theme: {
        background: "#00000000",
        foreground: "#e6e6e6",
        cursor: "#0a84ff",
        selectionBackground: "rgba(10,132,255,0.35)",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);

    let cleanup: Array<() => void> = [];
    let disposed = false;

    (async () => {
      const sync = () => {
        try {
          fit.fit();
        } catch {
          return;
        }
        if (term.cols > 0 && term.rows > 0) {
          invoke("pty_resize", { id: tab.id, cols: term.cols, rows: term.rows });
        }
      };
      syncRef.current = sync;

      await new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r())));
      if (disposed) return;
      try {
        fit.fit();
      } catch {
        /* ignore */
      }

      const { spec } = tab;
      term.writeln(
        `\x1b[90mConnecting to ${spec.server}${spec.kind === "container" ? ` → ${spec.app}` : ""}…\x1b[0m`
      );

      const unlisten = await listen<string>(`pty://data/${tab.id}`, (e) => term.write(e.payload));
      const unexit = await listen(`pty://exit/${tab.id}`, () =>
        term.writeln("\r\n\x1b[90m[disconnected]\x1b[0m")
      );
      const onData = term.onData((d) => invoke("pty_write", { id: tab.id, data: d }));

      try {
        await invoke("pty_spawn", {
          id: tab.id,
          server: spec.server,
          container: spec.kind === "container" ? spec.app : null,
          cols: term.cols || 80,
          rows: term.rows || 24,
        });
      } catch (e) {
        term.writeln(`\r\n\x1b[31mfailed to start: ${String(e)}\x1b[0m`);
      }

      const ro = new ResizeObserver(() => sync());
      ro.observe(el);
      setTimeout(sync, 120);

      cleanup = [
        () => unlisten(),
        () => unexit(),
        () => onData.dispose(),
        () => ro.disconnect(),
        () => invoke("pty_kill", { id: tab.id }),
      ];
    })();

    return () => {
      disposed = true;
      cleanup.forEach((fn) => fn());
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  useEffect(() => {
    if (active) requestAnimationFrame(() => syncRef.current());
  }, [active]);

  return <div className={`term ${active ? "" : "term-hidden"}`} ref={ref} />;
}
