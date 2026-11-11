// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getFeedState, loadFeed, type FeedItem } from "./feed";
import { noteConversation, chooseManager, resetManagerChoice } from "./manager-choice";
import { messageWaits } from "./assistant-entries";
import { reset } from "./store";

const conversation = "demo-cursor-conversation";
const oldAt = Date.parse("2026-11-01T00:00:00Z");
const card = (id: string, at: number, dismissed = false): FeedItem => ({ id, conversation, at: new Date(at).toISOString(), kind: "message", ref: "demo-cursor-inbox", body: { draft: "Synthetic progress", ...(dismissed ? { dismissed_at: "2026-11-10T10:00:00Z", dismissal_id: `dismiss-${id}` } : {}) } });
const ok = (body: unknown) => ({ ok: true, status: 200, headers: new Headers(), text: async () => JSON.stringify(body) });

beforeEach(() => {
  reset();
  resetManagerChoice();
  Object.assign(getFeedState(), { feeds: {}, later: {}, busy: {} });
  localStorage.setItem("persona.token", "demo");
});
afterEach(() => { vi.unstubAllGlobals(); resetManagerChoice(); });

describe("feed snapshot cursors", () => {
  it("passes a full updated-old page and reaches newer progress instead of repeating its old timestamps", async () => {
    const previous = card("demo-previous", oldAt + 86_400_000);
    Object.assign(getFeedState(), { feeds: { [conversation]: [previous] } });
    const old = Array.from({ length: 500 }, (_, i) => card(`demo-old-${i}`, oldAt + i * 1000, true));
    const next = card("demo-next-progress", oldAt + 2 * 86_400_000);
    const urls: URL[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const u = new URL(url, "http://example.test");
      if (!u.pathname.endsWith("/feed")) return ok({});
      urls.push(u);
      return ok(u.searchParams.has("cursor") ? { items: [next], next_cursor: "" } : { items: old, next_cursor: "demo/opaque+cursor=" });
    }));
    await loadFeed(conversation);
    expect(urls).toHaveLength(2);
    expect(urls[0].searchParams.get("paginate")).toBe("1");
    expect(urls[0].searchParams.get("since")).toBe(previous.at);
    expect(urls[1].searchParams.get("cursor")).toBe("demo/opaque+cursor=");
    expect(urls[1].searchParams.has("since")).toBe(false);
    const feed = getFeedState().feeds[conversation];
    expect(feed).toHaveLength(502);
    expect(feed.filter((it) => messageWaits(it)).map((it) => it.id)).toEqual([previous.id, next.id]);
  });

  it("stops at an explicit empty cursor even on an exact full page", async () => {
    const old = Array.from({ length: 500 }, (_, i) => card(`demo-exact-${i}`, oldAt + i * 1000, true));
    const fetcher = vi.fn(async () => ok({ items: old, next_cursor: "" }));
    vi.stubGlobal("fetch", fetcher);
    await loadFeed(conversation);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("rejects a repeated cursor rather than requesting the same page forever", async () => {
    const fetcher = vi.fn(async () => ok({ items: [card("demo-repeat", oldAt)], next_cursor: "demo-repeated" }));
    vi.stubGlobal("fetch", fetcher);
    await loadFeed(conversation);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("keeps the original owner on every page after selecting a different assistant", async () => {
    noteConversation(conversation, "demo-page-owner");
    const owners: (string | null)[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      const u = new URL(url, "http://example.test");
      if (!u.pathname.endsWith("/feed")) return ok({});
      owners.push(new Headers(init?.headers).get("X-Persona-Manager"));
      if (owners.length === 1) {
        chooseManager("demo-another-assistant");
        return ok({ items: [card("demo-owned-first", oldAt)], next_cursor: "demo-owned-cursor" });
      }
      return ok({ items: [card("demo-owned-last", oldAt + 1000)], next_cursor: "" });
    }));
    await loadFeed(conversation);
    expect(owners).toEqual(["demo-page-owner", "demo-page-owner"]);
  });
});
