import { useEffect, useState } from "react";
import { createDesktopAPI, type DesktopAPI } from "../lib/desktop-api";
import { Popover } from "./Popover";
import { Management } from "./Management";

export type WindowKind = "popover" | "management";

export interface AppProps {
  window: WindowKind;
  /** Injectable for tests; production resolves the transport via createDesktopAPI. */
  api?: DesktopAPI;
}

export function App({ window: kind, api: injected }: AppProps) {
  const [api, setApi] = useState<DesktopAPI | null>(injected ?? null);

  useEffect(() => {
    if (injected) return;
    let cancelled = false;
    createDesktopAPI().then((resolved) => {
      if (!cancelled) setApi(resolved);
    });
    return () => {
      cancelled = true;
    };
  }, [injected]);

  if (!api) {
    return (
      <div className="app-loading" role="status">
        Loading…
      </div>
    );
  }

  return kind === "management" ? <Management api={api} /> : <Popover api={api} />;
}
