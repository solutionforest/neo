// Tiny toast store for GUI action feedback (restart succeeded, etc.).
export type ToastKind = "ok" | "err" | "info";
export interface Toast {
  id: number;
  kind: ToastKind;
  msg: string;
}

let items: Toast[] = [];
let seq = 1;
const subs = new Set<(t: Toast[]) => void>();

function emit() {
  subs.forEach((f) => f(items));
}

export const toast = {
  show(msg: string, kind: ToastKind = "info") {
    const id = seq++;
    items = [...items, { id, kind, msg }];
    emit();
    setTimeout(() => {
      items = items.filter((t) => t.id !== id);
      emit();
    }, 3200);
  },
  subscribe(f: (t: Toast[]) => void): () => void {
    subs.add(f);
    f(items);
    return () => {
      subs.delete(f);
    };
  },
};
