import { apiBlob, credentialOwner } from "./api";
import { BASE } from "./base";
import { defaultManager, MANAGER_HEADER, managerHeaderFor } from "./manager-choice";
import { boundedBlobReader } from "./bounded-blob";

const installed = /^\/api\/v1\/extensions\/[A-Za-z0-9_-]{1,64}\/-\/icon\?v=[A-Za-z0-9_.~%+\-]+$/;
const catalog = /^\/api\/v1\/extensions\/-\/catalog\/[A-Za-z0-9_-]{1,64}\/[A-Za-z0-9_-]{1,64}\/icon\?sha256=[a-fA-F0-9]{64}$/;
const iconPath = (value: string) => installed.test(value) || catalog.test(value);

/** Only these hub-owned routes may receive credentials. Public legacy artwork stays anonymous. */
export function extensionLogoSource(value?: string, origin = window.location.origin, base = BASE): { path?: string; publicURL?: string } | null {
  if (!value || value !== value.trim() || value.includes("\\")) return null;
  if (value.startsWith("/")) return iconPath(value) ? { path: value.slice("/api/v1".length) } : null;
  try {
    const url = new URL(value);
    if (url.username || url.password) return null;
    // Check the unnormalized path too: URL parsing removes dot segments, which must never authorize a request.
    const raw = value.match(/^https?:\/\/[^/?#]+(\/[^#]*)$/i)?.[1];
    const path = raw?.startsWith(`${base}/api/v1/`) ? raw.slice(base.length) : null;
    if (url.origin === origin && path && iconPath(path) && !url.hash) return { path: path.slice("/api/v1".length) };
    return url.protocol === "https:" ? { publicURL: url.href } : null;
  } catch { return null; }
}

const maxBytes = 2 * 1024 * 1024;
const cacheBytes = 8 * 1024 * 1024;
const cacheEntries = 128;
const types = new Set(["image/png"]);
const readIconBlob = boundedBlobReader({ maxBytes, mimeTypes: [...types] });
const cached = new Map<string, Blob>();
const pending = new Map<string, { controller: AbortController; users: number; promise: Promise<Blob> }>();
let bytes = 0;

/** Coalesce only catalog logos, partition by credential/manager, cache bounded bytes, never object URLs. */
export function acquireExtensionLogo(path: string, explicitManager?: string): { promise: Promise<Blob>; release: () => void } {
  if (!iconPath(`/api/v1${path}`)) throw new Error("Invalid extension icon path");
  const owner = credentialOwner();
  const resolveManager = () => (explicitManager ?? managerHeaderFor(path)) || defaultManager();
  const manager = resolveManager();
  const key = JSON.stringify([owner, manager, path]);
  const hit = cached.get(key);
  if (hit) {
    cached.delete(key); cached.set(key, hit);
    return { promise: Promise.resolve(hit), release: () => {} };
  }
  let entry = pending.get(key);
  if (!entry) {
    const controller = new AbortController();
    entry = { controller, users: 0, promise: apiBlob(path, {
      // The ordinary API's same-origin credentials also preserve a hub's front-door sign-in cookies.
      // These never go to public artwork or to a followed redirect.
      signal: controller.signal, credentials: "same-origin", redirect: "manual", referrerPolicy: "no-referrer",
      headers: { [MANAGER_HEADER]: manager }, readBlob: readIconBlob,
      isAuthContextCurrent: () => !controller.signal.aborted && credentialOwner() === owner && resolveManager() === manager,
    }).then(blob => {
      if (controller.signal.aborted || credentialOwner() !== owner || resolveManager() !== manager) throw new DOMException("Aborted", "AbortError");
      if (!blob.size || blob.size > maxBytes || !types.has(blob.type.split(";")[0].toLowerCase())) throw new Error("Invalid extension icon");
      cached.set(key, blob); bytes += blob.size;
      while (cached.size > cacheEntries || bytes > cacheBytes) {
        const first = cached.keys().next().value!;
        bytes -= cached.get(first)!.size; cached.delete(first);
      }
      return blob;
    }).finally(() => { if (pending.get(key)?.controller === controller) pending.delete(key); }) };
    pending.set(key, entry);
  }
  entry.users++;
  const acquired = entry;
  let released = false;
  return { promise: acquired.promise, release: () => {
    if (released) return;
    released = true;
    if (--acquired.users === 0 && pending.get(key) === acquired) {
      pending.delete(key); acquired.controller.abort();
    }
  } };
}
