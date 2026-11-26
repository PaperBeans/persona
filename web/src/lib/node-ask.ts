// The two small questions the palette's node commands ask (lib/node-palette.ts): a name for a rename, a yes before a
// close. views/NodePrompt.tsx draws them from the sidebar, so this store is on every first screen; the commands that ask
// load with the palette.

export type NodeAsk =
  | { kind: "text"; title: string; label: string; value: string; resolve: (v: string | null) => void }
  | { kind: "confirm"; title: string; body: string; label: string; resolve: (v: boolean) => void };

let asking: NodeAsk | null = null;
const askListeners = new Set<() => void>();
const setAsk = (a: NodeAsk | null) => {
  asking = a;
  askListeners.forEach((f) => f());
};
export const getAsk = () => asking;
export const subscribeAsk = (f: () => void) => {
  askListeners.add(f);
  return () => void askListeners.delete(f);
};
export const answerAsk = () => setAsk(null);
export const askText = (title: string, label: string, value: string) => new Promise<string | null>((resolve) => setAsk({ kind: "text", title, label, value, resolve }));
export const askConfirm = (title: string, body: string, label: string) => new Promise<boolean>((resolve) => setAsk({ kind: "confirm", title, body, label, resolve }));
