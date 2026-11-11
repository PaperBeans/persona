// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { FeedCache } from "./feed-cache";

type Tab = {
  api: typeof import("./api");
  auth: typeof import("./auth");
  cache: typeof import("./feed-cache");
  files: typeof import("./file-ops");
};
const page: FeedCache = { machineID: "synthetic-machine", sessionID: "synthetic-session", items: [{ id: "item", seq: 1, role: "user", kind: "text", text: "Synthetic text" }], hasMore: false };
async function tab(): Promise<Tab> {
  vi.resetModules();
  return { api: await import("./api"), auth: await import("./auth"), cache: await import("./feed-cache"), files: await import("./file-ops") };
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("persona.token", "jd_synthetic-a");
  localStorage.setItem("persona.tokenId", "synthetic-device");
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ machines: [] })));
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });

it("publishes one same-token pair per tab and fences stale feeds before the storage event", async () => {
  const a = await tab(), b = await tab();
  const aBefore = a.api.credentialOwner(), bBefore = b.api.credentialOwner();
  const oldA = a.cache.feedCacheScope(), oldB = b.cache.feedCacheScope();
  expect(a.cache.writeFeedCache(oldA, page)).toBe(true);
  b.cache.writeMemoryPage(oldB, page);
  const changedA = vi.fn(), changedB = vi.fn();
  const offA = a.api.subscribeCredentialOwner(changedA), offB = b.api.subscribeCredentialOwner(changedB);
  try {
    await a.auth.pair("jd_synthetic-a");
    expect(a.api.credentialOwner()).not.toBe(aBefore);
    expect(changedA).toHaveBeenCalledOnce();
    expect(a.cache.writeFeedCache(oldA, page)).toBe(false);
    // This is another tab reading synchronously, before its queued storage event is delivered.
    expect(b.cache.writeFeedCache(oldB, page)).toBe(false);
    expect(b.cache.readMemoryPage(oldB, page.machineID, page.sessionID)).toBeNull();
    expect(b.api.credentialOwner()).not.toBe(bBefore);
    expect(changedB).toHaveBeenCalledOnce();
    window.dispatchEvent(new StorageEvent("storage", { key: "persona.credentialEpoch", storageArea: localStorage }));
    expect(changedA).toHaveBeenCalledOnce();
    expect(changedB).toHaveBeenCalledOnce();
    expect(b.cache.readFeedCache()).toBeNull();
    expect(b.cache.writeFeedCache(b.cache.feedCacheScope(), page)).toBe(true);
  } finally { offA(); offB(); }
});

it("publishes once for changed tokens and leaves an idempotent setToken alone", async () => {
  const a = await tab(); a.api.credentialOwner();
  const changed = vi.fn(), off = a.api.onCredentialChange(changed);
  try {
    a.api.setToken("jd_synthetic-a");
    expect(changed).not.toHaveBeenCalled();
    await a.auth.pair("jd_synthetic-b");
    expect(changed).toHaveBeenCalledOnce();
    const first = a.api.credentialOwner();
    await a.auth.pair("jd_synthetic-a");
    expect(a.api.credentialOwner()).not.toBe(first);
    expect(changed).toHaveBeenCalledTimes(2);
    a.api.setToken("jd_synthetic-a");
    expect(changed).toHaveBeenCalledTimes(2);
  } finally { off(); }
});

it("retires a same-token sign-in through the old-hub 404 fallback", async () => {
  const a = await tab(); a.api.setToken("synthetic-legacy-token");
  const before = a.api.credentialOwner(), changed = vi.fn(), off = a.api.onCredentialChange(changed);
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ machines: [] })).mockResolvedValueOnce(Response.json({ error: "missing" }, { status: 404 }));
  try {
    await a.auth.pair("synthetic-legacy-token");
    expect(a.api.credentialOwner()).not.toBe(before);
    expect(changed).toHaveBeenCalledOnce();
  } finally { off(); }
});

it.each(["probe", "exchange"])("does not publish after a rejected %s", async (stage) => {
  const a = await tab(); const before = a.api.credentialOwner(), marker = localStorage.getItem("persona.credentialEpoch");
  const changed = vi.fn(), unauthorized = vi.fn(), off = a.api.onCredentialChange(changed);
  a.api.handleUnauthorized(unauthorized);
  vi.mocked(fetch).mockReset();
  if (stage === "exchange") vi.mocked(fetch).mockResolvedValueOnce(Response.json({ machines: [] }));
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ error: "invalid token" }, { status: 401 }));
  try {
    await expect(a.auth.pair("synthetic-probe-token")).rejects.toMatchObject({ status: 401 });
    expect(a.api.credentialOwner()).toBe(before);
    expect(localStorage.getItem("persona.credentialEpoch")).toBe(marker);
    expect(changed).not.toHaveBeenCalled();
    expect(unauthorized).not.toHaveBeenCalled();
  } finally { off(); }
});

it("still retires this tab and its cache when writing the marker fails", async () => {
  const a = await tab(), b = await tab();
  const beforeA = a.api.credentialOwner(), beforeB = b.api.credentialOwner();
  const old = a.cache.feedCacheScope(), changed = vi.fn(), off = a.api.onCredentialChange(changed);
  const set = Storage.prototype.setItem;
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
    if (key === "persona.credentialEpoch") throw new Error("storage unavailable");
    return set.call(this, key, value);
  });
  try {
    await expect(a.auth.pair("jd_synthetic-a")).resolves.toBeUndefined();
    expect(a.api.credentialOwner()).not.toBe(beforeA);
    expect(changed).toHaveBeenCalledOnce();
    expect(a.cache.writeFeedCache(old, page)).toBe(false);
    expect(b.api.credentialOwner()).toBe(beforeB); // no shared marker can be delivered
  } finally { off(); }
});

it("fences work across the token-to-epoch transition between tabs", async () => {
  class SyntheticXHR {
    static sent: SyntheticXHR[] = [];
    headers: Record<string, string> = {};
    upload: { onprogress?: () => void } = {};
    aborted = false;
    status = 200;
    responseText = JSON.stringify({ path: "/work/example.txt", name: "example.txt", size: 1 });
    onload?: () => void; onerror?: () => void; onabort?: () => void;
    open() {}
    setRequestHeader(key: string, value: string) { this.headers[key] = value; }
    send() { SyntheticXHR.sent.push(this); }
    abort() { this.aborted = true; this.onabort?.(); }
    getResponseHeader() { return null; }
  }
  vi.stubGlobal("XMLHttpRequest", SyntheticXHR);
  const a = await tab(), b = await tab();
  a.api.credentialOwner();
  const oldOwner = b.api.credentialOwner(), events = vi.fn(), stop = b.api.onCredentialChange(events);
  const file = new File(["x"], "example.txt");
  const old = b.files.uploadToDir("synthetic-machine", file, { dir: "/work", credential: { owner: oldOwner, token: "jd_synthetic-a" } }).catch((error: unknown) => error);
  const realSet = Storage.prototype.setItem;
  let intermediate: Promise<unknown> | undefined;
  let intermediateOwner = "";
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
    realSet.call(this, key, value);
    if (key === "persona.token" && value === "jd_synthetic-b" && !intermediate) {
      intermediateOwner = b.api.credentialOwner();
      intermediate = b.files.uploadToDir("synthetic-machine", file, { dir: "/work", credential: { owner: intermediateOwner, token: b.api.getToken() } }).catch((error: unknown) => error);
    }
  });
  try {
    await a.auth.pair("jd_synthetic-b");
    expect(SyntheticXHR.sent).toHaveLength(2);
    expect(SyntheticXHR.sent[0].aborted).toBe(true);
    expect(await old).toMatchObject({ name: "AbortError" });
    const settledOwner = b.api.credentialOwner();
    expect(settledOwner).not.toBe(intermediateOwner);
    expect(SyntheticXHR.sent[1].aborted).toBe(true);
    expect(await intermediate).toMatchObject({ name: "AbortError" });
    expect(events).toHaveBeenCalledTimes(2);
    const fresh = b.files.uploadToDir("synthetic-machine", file, { dir: "/work", credential: { owner: settledOwner, token: b.api.getToken() } });
    expect(SyntheticXHR.sent[2].headers.Authorization).toBe("Bearer jd_synthetic-b");
    SyntheticXHR.sent[2].onload?.();
    await expect(fresh).resolves.toMatchObject({ path: "/work/example.txt" });
  } finally { stop(); }
});
