// Manages the open terminal tabs (server shells + container shells).
export interface TermSpec {
  kind: "server" | "container";
  server: string; // server name (for key lookup)
  host: string; // user@ip
  port: number;
  keyPath: string;
  app?: string; // container name (kind === "container")
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
  openServer(spec: Omit<TermSpec, "kind">): string | null {
    if (tabs.length >= MAX) return null;
    const id = `t${++counter}`;
    tabs = [...tabs, { id, title: spec.server, spec: { ...spec, kind: "server" } }];
    activeId = id;
    reveal?.();
    emit();
    return id;
  },
  openContainer(spec: Omit<TermSpec, "kind">): string | null {
    if (tabs.length >= MAX) return null;
    const id = `t${++counter}`;
    tabs = [...tabs, { id, title: spec.app ?? "shell", spec: { ...spec, kind: "container" } }];
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
