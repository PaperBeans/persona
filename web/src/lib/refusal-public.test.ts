// #2528: a refusal record kept or shown by the web is its public projection only. A cached page an older version
// wrote with the record's whole line in it is never hydrated, and what is written now carries none of it.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { hasPrivateRefusal, publicRefusalItem } from "./refusal-public";
import type { Item } from "./types";

const MARKER = "HIDDEN-MARKER-2528-cache";
const line = `{"type":"system","subtype":"model_refusal_fallback","syntheticHidden":{"nested":{"deeper":"${MARKER}"}}}`;
const refusal = { subtype: "model_refusal_fallback", original_model: "primary-model", fallback_model: "fallback-model", category: "synthetic-policy", scope: "session", explanation: "Upstream refused the request", text: "Synthetic refusal words" };
const legacy = { id: "r1", seq: 3, role: "system", kind: "notice", text: line, model_refusal: { ...refusal, raw: line } } as unknown as Item;
const user = { id: "u1", seq: 1, role: "user", kind: "text", text: "hi" } as Item;

describe("publicRefusalItem", () => {
  it("keeps the public fields and the refusal's words, drops the line", () => {
    const it = publicRefusalItem(legacy);
    expect(JSON.stringify(it)).not.toContain(MARKER);
    expect(it.model_refusal).toEqual(refusal);
    expect(it.text).toBe("Synthetic refusal words");
    expect(hasPrivateRefusal(legacy)).toBe(true);
    expect(hasPrivateRefusal(it)).toBe(false);
    expect(publicRefusalItem(user)).toBe(user);
  });
  it("falls back to the explanation, then the subtype, for the text", () => {
    expect(publicRefusalItem({ ...legacy, model_refusal: { subtype: "model_refusal_future", explanation: "e" } } as Item).text).toBe("e");
    expect(publicRefusalItem({ ...legacy, model_refusal: { subtype: "model_refusal_future" } } as Item).text).toBe("model_refusal_future");
  });
});

describe("the feed cache", () => {
  let cache: typeof import("./feed-cache");
  let stored: Map<string, string>;
  beforeEach(async () => {
    vi.resetModules();
    stored = new Map([["persona.token", "jd_synthetic_credential"]]);
    vi.stubGlobal("localStorage", {
      get length() { return stored.size; },
      key: (index: number) => [...stored.keys()][index] ?? null,
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => stored.set(key, value),
      removeItem: (key: string) => stored.delete(key),
    });
    vi.stubGlobal("location", { origin: "https://hub.example.com" });
    cache = await import("./feed-cache");
  });
  afterEach(() => vi.unstubAllGlobals());
  it("never hydrates a page an older version wrote with a refusal record's line", () => {
    const scope = cache.feedCacheScope();
    const key = scope!.slice(0, scope!.lastIndexOf("."));
    stored.set(key, JSON.stringify({ machineID: "m", sessionID: "s", items: [user, legacy], hasMore: false }));
    expect(cache.readFeedCache(scope)).toBeNull();
    expect(stored.has(key)).toBe(false); // dropped, not kept for later
  });
  it("writes only the public projection, and reads it back whole", () => {
    const scope = cache.feedCacheScope();
    expect(cache.writeFeedCache(scope, { machineID: "m", sessionID: "s", items: [user, legacy], hasMore: false })).toBe(true);
    expect(JSON.stringify([...stored])).not.toContain(MARKER);
    const back = cache.readFeedCache(scope);
    expect(back?.items[1].model_refusal).toEqual(refusal);
    expect(back?.items[1].text).toBe("Synthetic refusal words");
  });
});
