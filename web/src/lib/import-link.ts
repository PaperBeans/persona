// A provider import link opened in this page (#5420): `<web address>/settings?tab=models#import=<base64url JSON>`, a
// model provider's settings as cindy's v1 import JSON (lib/provider-link.ts builds them). main.tsx takes it out of the
// address bar as the page starts, before it waits for anything (the language's strings, App), since it may hold a key:
// out of the Back list, bookmarks and links copied from the address bar; App.tsx takes one pasted over a page already
// open. The text waits here, in memory only (never storage, logs or error reports), until Settings › Model providers
// opens Add on it and sends it to the hub, which reads and checks every part of it (POST
// /api/v1/launchers/import/parse); a reload for a sign-in in front of the hub puts it back first (lib/edge-auth.ts).
// Kept apart from lib/provider-link.ts: this is on the first screen, and the link builder is not.
import { BASE, stripBase } from "./base";

/** The link in `href`, when its fragment names `import`: `text`, the whole link as given (the hub reads it), and `rest`,
 *  where the address bar goes instead: the settings page on its Model providers tab, without the link's `import` and
 *  `v`. Anything else in the fragment stays for whoever takes it (a sign-in link's `#token=`, App.tsx). */
export function takeImportLink(href: string): { text: string; rest: string } | null {
  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return null;
  }
  const hash = new URLSearchParams(u.hash.replace(/^#/, ""));
  if (!hash.has("import")) return null;
  hash.delete("import");
  hash.delete("v");
  const left = hash.toString() ? `#${hash.toString()}` : "";
  if (stripBase(u.pathname).replace(/\/+$/, "") !== "/settings") return { text: href, rest: `${BASE}/settings?tab=models${left}` };
  // the providers are on the Model providers tab: a link to another one would leave the link waiting unseen
  const q = new URLSearchParams(u.search);
  if (q.get("tab") === "models") return { text: href, rest: u.pathname + u.search + left };
  q.set("tab", "models");
  return { text: href, rest: `${u.pathname}?${q.toString()}${left}` };
}

/** Takes a link out of the address bar as the page starts (main.tsx) and holds it. */
export function takeImportLinkOnStart() {
  if (typeof window === "undefined") return;
  const importing = takeImportLink(location.href);
  if (!importing) return;
  history.replaceState(history.state, "", importing.rest);
  holdImportLink(importing.text);
}

let held: string | null = null;
const listeners = new Set<(text: string) => void>();

/** Keeps the link opened until Settings › Model providers takes it, telling it when it is open already. */
export function holdImportLink(text: string) {
  held = text;
  listeners.forEach((l) => l(text));
}

/** Calls `fn` with each link held (a page already open on the providers opened with one); returns the stop. */
export function onImportLink(fn: (text: string) => void): () => void {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}

/** The link waiting, if any; reading leaves it (React's StrictMode runs a state's initializer twice). */
export const importLinkHeld = (): string | null => held;

/** Forgets it: the page that opened it has it now. */
export function dropImportLink() {
  held = null;
}

/** Puts a link still waiting back in the address bar, for a reload that is about to lose it (a sign-in in front of
 *  the hub, lib/edge-auth.ts): the reloaded page opens it again. */
export function restoreImportLink() {
  if (held !== null && typeof history !== "undefined") history.replaceState(history.state, "", held);
}
