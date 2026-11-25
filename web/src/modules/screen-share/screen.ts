// The live screen's page, /screen/<machine id> (docs/extensions/computer-use.md "Live view"): which machine, and which token.
// The app opens it in its web view with its own device token in the fragment (#token=…&embed=1): that one stays in
// this page's memory, is never stored and never traded for a browser token (the web's sign-in link would mint one per
// open). A signed-in browser uses its own.

import { BASE, stripBase } from "../../lib/base";
import { decodeURIComponentSafe } from "../../lib/uri";
import { getToken } from "../../lib/api";
import { t } from "../../i18n";

/** `name`: the machine as the app calls it (Mac Studio), shown until the hub says; else its id. `keep`: the app that
 * opened it remembers the Mac's sign-in (#1322: "phone" | "mac"), "" in a browser. */
export type ScreenTarget = { mid: string; token: string; embedded: boolean; name: string; keep: "" | "phone" | "mac"; unlock: boolean };

/** /screen/<mid> → mid; anything else is not the live screen. */
export function screenMachine(pathname: string): string | null {
  const p = pathname.split("/").filter(Boolean);
  return p[0] === "screen" && p[1] ? decodeURIComponentSafe(p[1]) : null;
}

/** The page's machine and token, the fragment's token taken out of the address bar's text (returned as rest). */
export function screenTarget(href: string, stored: () => string = getToken): { target: ScreenTarget | null; rest: string } {
  const u = new URL(href);
  const mid = screenMachine(stripBase(u.pathname));
  const hash = new URLSearchParams(u.hash.replace(/^#/, ""));
  const token = hash.get("token")?.trim() ?? "";
  const embedded = hash.get("embed") === "1";
  const name = hash.get("name")?.trim() || "";
  const kept = hash.get("keep");
  const keep = embedded && (kept === "phone" || kept === "mac") ? kept : "";
  hash.delete("token");
  hash.delete("embed");
  hash.delete("name");
  hash.delete("keep");
  const unlock = keep !== "" && hash.get("unlock") === "1"; // one-tap unlock (#1507 a-2): only where the app keeps the sign-in
  hash.delete("unlock");
  const rest = u.pathname + u.search + (hash.toString() ? "#" + hash.toString() : "");
  if (!mid) return { target: null, rest };
  return { target: { mid, token: token || stored(), embedded, name: name || mid, keep, unlock }, rest };
}

/** The WebSocket that carries the Mac's VNC (its port 5900) through the hub's port relay (docs/operations/ports.md). */
export function screenSocket(location: { protocol: string; host: string }, mid: string, token: string): string {
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  return `${scheme}://${location.host}${BASE}/api/v1/machines/${encodeURIComponent(mid)}/ports/5900/connect?token=${encodeURIComponent(token)}`;
}

/** The Mac account's short name, from its home directory (/Users/<name>): what Screen Sharing's sign-in asks for. */
export function accountOf(home: string | undefined): string {
  const m = /^\/Users\/([^/]+)\/?$/.exec(home ?? "");
  return m ? m[1] : "";
}

/** The sign-in's "Remember" box: where the app keeps the account and password. */
export function keepLabel(keep: ScreenTarget["keep"]): string {
  return keep === "mac" ? t("screen-share-screen.keep_mac") : t("screen-share-screen.keep_phone");
}

/** What the page tells the app that opened it (its WKScriptMessageHandler "screen"); nothing in a browser. */
export function toApp(message: Record<string, unknown>): void {
  const w = window as unknown as { webkit?: { messageHandlers?: { screen?: { postMessage(m: unknown): void } } } };
  w.webkit?.messageHandlers?.screen?.postMessage(message);
}

/** The keysyms that type text on a Mac's lock screen (one-tap unlock): printable Latin-1 as itself, anything else as X11's
 * Unicode keysym (0x01000000 + its code point), then Return. */
export function keysymsOf(text: string): number[] {
  const out: number[] = [];
  for (const ch of text) {
    const cp = ch.codePointAt(0) ?? 0;
    out.push((cp >= 0x20 && cp <= 0x7e) || (cp >= 0xa0 && cp <= 0xff) ? cp : 0x01000000 + cp);
  }
  out.push(0xff0d);
  return out;
}
