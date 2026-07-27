// Manages the open terminal tabs (server shells + container shells). Each tab
// maps to one `neo-bridge pty` session keyed by id. The bridge resolves SSH
// auth from ~/.neo, so a tab only needs the server name (+ app for containers).
export interface TermSpec {
  kind: "server" | "container";
  server: string; // server name
  app?: string; // container app name (kind === "container")
}

export interface TermTab {
  id: string;
  title: string;
  spec: TermSpec;
}

const MAX = 6;
let tabs: TermTab[] = [];
let activeId = "";
let counter = 0;
const subs = new Set<() => void>();
let reveal: (() => void) | null = null;

function emit() {
  subs.forEach((f) => f());
}

export const terminals = {
  MAX,
  get tabs() {
    return tabs;
  },
  get activeId() {
    return activeId;
  },
  atLimit() {
    return tabs.length >= MAX;
  },
  subscribe(f: () => void): () => void {
    subs.add(f);
    return () => {
      subs.delete(f);
    };
  },
  onReveal(cb: () => void) {
    reveal = cb;
  },
  setActive(id: string) {
    activeId = id;
    emit();
  },
  openServer(server: string): string | null {
    if (tabs.length >= MAX) return null;
    const id = `t${++counter}`;
    tabs = [...tabs, { id, title: server, spec: { kind: "server", server } }];
    activeId = id;
    reveal?.();
    emit();
    return id;
  },
  openContainer(server: string, app: string): string | null {
    if (tabs.length >= MAX) return null;
    const id = `t${++counter}`;
    tabs = [...tabs, { id, title: app, spec: { kind: "container", server, app } }];
    activeId = id;
    reveal?.();
    emit();
    return id;
  },
  close(id: string) {
    tabs = tabs.filter((t) => t.id !== id);
    if (activeId === id) activeId = tabs[tabs.length - 1]?.id ?? "";
    emit();
  },
};
