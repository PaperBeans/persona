// A sign-in layer in front of the hub (a gateway or proxy with its own login) that has expired answers the hub's own
// requests with a redirect or its login page. The service worker already hands such a page load to the browser
// (lib/sw-shell.ts); this is the running page's side: an /api call, or the probe after the socket fails, that sees one
// reloads the page once so the layer takes over and sends the browser to its sign-in. Never a retry loop: one reload per
// RELOAD_GUARD_MS, and a plain network failure (offline) is not an interception.
import { BASE } from "./base";
import { restoreImportLink } from "./import-link";

export const RELOAD_GUARD_MS = 60_000;
const KEY = "persona.edge-reload";

/** True for what an intercepting layer answers with instead of the hub: a (manual-mode) redirect, a followed redirect,
 *  status 0, or an HTML page where the hub only speaks JSON. */
export function intercepted(r: Pick<Response, "type" | "redirected" | "status" | "headers">): boolean {
  if (r.type === "opaqueredirect" || r.redirected || r.status === 0 || (r.status >= 300 && r.status < 400)) return true;
  return [200, 401, 403, 407, 511].includes(r.status) && (r.headers?.get("Content-Type") ?? "").toLowerCase().startsWith("text/html");
}

/** Reloads the page once (within the guard window) so the layer in front can redirect to its sign-in. */
export function reloadForSignIn(now = Date.now(), storage: Pick<Storage, "getItem" | "setItem"> | undefined = safeStorage(), reload: () => void = () => location.reload()): boolean {
  const last = Number(storage?.getItem(KEY) ?? 0);
  if (last && now - last < RELOAD_GUARD_MS) return false;
  try {
    storage?.setItem(KEY, String(now));
  } catch {
    return false; // no way to guard it: never risk a loop
  }
  // a provider import link not opened yet would be lost: the reloaded page opens it again (#5420)
  restoreImportLink();
  reload();
  return true;
}

function safeStorage(): Storage | undefined {
  try {
    return typeof sessionStorage === "undefined" ? undefined : sessionStorage;
  } catch {
    return undefined;
  }
}

async function readProbeError(r: Response): Promise<string> {
  try {
    const ct = (r.headers?.get("Content-Type") ?? "").toLowerCase();
    if (ct.includes("application/json")) {
      const body = (await r.json()) as { error?: unknown; message?: unknown };
      const msg = typeof body?.error === "string" ? body.error : typeof body?.message === "string" ? body.message : "";
      if (msg.trim()) return msg.trim();
    }
  } catch {
    /* fall through to HTTP status */
  }
  return r.statusText ? `HTTP ${r.status} ${r.statusText}` : `HTTP ${r.status}`;
}

let activeProbe: { signal?: AbortSignal; isCurrent?: () => boolean } | null = null;
/** After the live socket failed: one request to the hub that follows no redirect. An interception reloads the page;
 *  a network failure changes nothing. When the hub itself answered (anything below 500: a proxy's 502/503/504 for a
 *  hub that is down is not the hub), it refused the socket rather than being out of reach, and `answered` runs
 *  (store.ts asks whether this browser's credential still works); its promise is awaited. When a 5xx response carries
 *  an error detail, `onServerError` surfaces it so the connection pill shows why the upgrade failed. Passing `signal`
 *  or `isCurrent` fences stale probes (on WS hello, disconnect, or credential switch) so late responses never trigger
 *  `reloadForSignIn`, `answered`, or `onServerError`, and never block a subsequent disconnect episode from probing. */
export async function probeEdge(
  fetcher: typeof fetch = fetch,
  answered?: () => Promise<unknown>,
  onServerError?: (reason: string) => void,
  signal?: AbortSignal,
  isCurrent?: () => boolean,
): Promise<boolean> {
  const stale = () => Boolean(signal?.aborted) || isCurrent?.() === false;
  if (stale()) return false;
  if (activeProbe && !activeProbe.signal?.aborted && activeProbe.isCurrent?.() !== false) return false;
  const entry = { signal, isCurrent };
  activeProbe = entry;
  try {
    const r = await fetcher(`${BASE}/api/v1/health`, { redirect: "manual", cache: "no-store", ...(signal ? { signal } : {}) });
    if (stale()) return false;
    if (intercepted(r)) return reloadForSignIn();
    if (r.status < 500) await answered?.();
    else {
      const reason = await readProbeError(r);
      if (!stale()) onServerError?.(reason);
    }
    return false;
  } catch {
    return false;
  } finally {
    if (activeProbe === entry) activeProbe = null;
  }
}
