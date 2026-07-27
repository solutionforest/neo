import { useEffect, useRef } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import type { TermTab } from "../lib/terminals";

// One session per tab. Rust `pty_spawn` runs the bundled `neo-bridge pty …`
// sidecar (remote PTY over neo's SSH auth). We keep xterm and the remote PTY
// the same size by fitting and pushing the dimensions on every layout change.
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
      const core = await import("@tauri-apps/api/core");
      const ev = await import("@tauri-apps/api/event");

      // Fit xterm to the element and push the exact size to the remote PTY.
      const sync = () => {
        try {
          fit.fit();
        } catch {
          return;
        }
        if (term.cols > 0 && term.rows > 0) {
          core.invoke("pty_resize", { id: tab.id, cols: term.cols, rows: term.rows });
        }
      };
      syncRef.current = sync;

      // Wait two frames so the (now un-animated) drawer has its final height,
      // then fit before spawning so the shell draws at the right width.
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

      const unlisten = await ev.listen<string>(`pty://data/${tab.id}`, (e) => term.write(e.payload));
      const unexit = await ev.listen(`pty://exit/${tab.id}`, () => term.writeln("\r\n\x1b[90m[disconnected]\x1b[0m"));
      const onData = term.onData((d) => core.invoke("pty_write", { id: tab.id, data: d }));

      try {
        await core.invoke("pty_spawn", {
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
      // One more sync after the shell has drawn its first prompt.
      setTimeout(sync, 120);

      cleanup = [
        () => unlisten(),
        () => unexit(),
        () => onData.dispose(),
        () => ro.disconnect(),
        () => core.invoke("pty_kill", { id: tab.id }),
      ];
    })();

    return () => {
      disposed = true;
      cleanup.forEach((fn) => fn());
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  // Re-sync when this tab becomes active (it was display:none, so unmeasurable).
  useEffect(() => {
    if (active) requestAnimationFrame(() => syncRef.current());
  }, [active]);

  return <div className={`term ${active ? "" : "term-hidden"}`} ref={ref} />;
}
