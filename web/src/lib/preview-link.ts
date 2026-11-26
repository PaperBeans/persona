// What the first screen needs of dev server previews (docs/web/preview.md): the hub feature, a localhost link of a
// reply as a port of the session's computer, and the address a comment card shows. The panel's own logic is
// preview.ts, which loads with views/session/PreviewPanel.tsx; keep this file free of it, or it comes back to every
// first screen.
import { portLink } from "./port-links";

/** The hub feature that says it has previews. */
export const PREVIEW_FEATURE = "preview";

/** What the address field shows for the page's URL: its path when it is on the server being previewed, else the
 *  whole address. */
export function addressOf(url: string, port: number): string {
  try {
    const u = new URL(url);
    if (u.port === String(port) && /^(localhost|127\.0\.0\.1|\[::1\])$/.test(u.hostname)) return u.pathname + u.search + u.hash;
    return url;
  } catch {
    return url;
  }
}

/** The address a localhost link of a reply stands for on the session's computer: its port and path; null for one that
 *  is not a port of that computer (docs/operations/ports.md "Recognizing links in replies"). `hostnames`: that
 *  computer's own name, which counts as well. */
export function localhostLink(url: string, hostnames: readonly string[] = []): { port: number; path: string } | null {
  const l = portLink(/^[a-z][a-z0-9+.-]*:\/\//i.test(url) ? url : `http://${url}`, hostnames);
  return l && { port: l.port, path: l.path };
}
