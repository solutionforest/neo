// Bridges GUI action buttons and the integrated terminal: a button calls
// terminalBus.run("neo ..."), which is typed into the live PTY session.
type Handler = (cmd: string) => void;

let handler: Handler | null = null;
let onShow: (() => void) | null = null;
const queue: string[] = [];

export const terminalBus = {
  register(h: Handler): () => void {
    handler = h;
    queue.splice(0).forEach(h);
    return () => {
      if (handler === h) handler = null;
    };
  },
  onReveal(cb: () => void) {
    onShow = cb;
  },
  run(cmd: string) {
    onShow?.();
    if (handler) handler(cmd);
    else queue.push(cmd);
  },
};
