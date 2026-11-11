// @vitest-environment jsdom
// Where a Feishu chat opens: an unread one at its first unread message under the New messages line, a link or an @me
// at its message lit up, and "First unread" / "You were mentioned" go back to them once the window has moved on.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxItem, LarkMessage } from "../../lib/inbox";
import { chatOfItem, LarkThread } from "./LarkThread";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const msg = (n: number): LarkMessage => ({ id: `m${n}`, position: String(n), sender_name: "示例同事", created_at: "2026-11-01T09:00:00Z", text: `消息 ${n}` });
const range = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => msg(a + i));

let host: HTMLDivElement;
let root: Root;
const reads: string[] = [];
const flush = async (n = 8) => {
  for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  reads.length = 0;
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } });
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("CSS", { escape: (v: string) => v });
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.scrollTo = vi.fn();
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    let body: unknown = {};
    if (/\/conversations\/f1\/messages\?/.test(url) && (init?.method ?? "GET") === "GET") {
      reads.push(url);
      const q = new URL(url, "http://hub").searchParams;
      const around = q.get("around");
      const before = Number(q.get("before"));
      // a long chat (1..200): the window around a position, else the newest page; jsdom has no layout, so the list
      // always sits at its top and would page all the way back: an older page comes back empty, which stops paging
      body = before
        ? { messages: [], has_older: true, has_newer: true }
        : around
        ? { messages: range(Math.max(1, Number(around) - 5), Number(around) + 5), has_older: true, has_newer: true, older_cursor: String(Math.max(1, Number(around) - 5)), newer_cursor: String(Number(around) + 5) }
        : { messages: range(181, 200), has_older: true, has_newer: false, older_cursor: "181" };
    } else if (url.includes("/read-states")) body = { states: [] };
    return { ok: true, status: 200, text: async () => JSON.stringify(body), headers: new Headers() };
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const around = (url: string) => new URL(url, "http://hub").searchParams.get("around");
const row = (id: string) => host.querySelector<HTMLElement>(`[data-mid="${id}"]`)!;
const lit = (id: string) => Boolean(row(id).querySelector('span[style*="pin-flash"]'));
const pill = (testId: string) => host.querySelector<HTMLButtonElement>(`[data-testid="${testId}"]`);

describe("opening a Feishu chat", () => {
  it("opens an unread chat around its first unread message, under the New messages line, without lighting it", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群", group: true, unreadFrom: "50" }} />));
    await flush();
    expect(around(reads[0])).toBe("50");
    const line = [...host.querySelectorAll("div")].find((d) => d.textContent === "以下是新消息");
    expect(line?.parentElement?.querySelector("[data-mid]")?.getAttribute("data-mid")).toBe("m50");
    expect(lit("m50")).toBe(false);
  });

  it("opens a chat with nothing unread at the newest", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群" }} />));
    await flush();
    expect(around(reads[0])).toBeNull();
    expect(host.textContent).not.toContain("以下是新消息");
  });

  it("opens a link or an @me at its message and lights it up", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群", focus: "120", unreadFrom: "40" }} />));
    await flush();
    expect(around(reads[0])).toBe("120");
    expect(lit("m120")).toBe(true);
  });

  it("offers First unread while the window starts after it, and reads the window around it when pressed", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群", focus: "120", unreadFrom: "40" }} />));
    await flush();
    expect(pill("lark-jump-unread")?.textContent).toBe("第一条未读");
    await act(async () => pill("lark-jump-unread")!.click());
    await flush();
    expect(reads.map(around)).toContain("40");
    expect(row("m40")).toBeTruthy();
  });

  it("offers You were mentioned for an unread @me before the window, and lights it when it lands", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群", unreadFrom: "150", mention: "60" }} />));
    await flush();
    expect(pill("lark-jump-mention")?.textContent).toBe("有人@你");
    await act(async () => pill("lark-jump-mention")!.click());
    await flush();
    expect(reads.map(around)).toContain("60");
    expect(lit("m60")).toBe(true);
  });

  it("keeps You were mentioned for an @me below the window too, until it has been on screen", async () => {
    act(() => root.render(<LarkThread chat={{ feed: "f1", title: "群", unreadFrom: "50", mention: "180" }} />));
    await flush();
    expect(pill("lark-jump-unread")).toBeNull();
    await act(async () => pill("lark-jump-mention")!.click());
    await flush();
    expect(reads.map(around)).toContain("180");
    expect(lit("m180")).toBe(true);
  });
});

describe("an inbox item's chat", () => {
  const item = (lark: Partial<NonNullable<InboxItem["lark"]>>, done = false) =>
    ({ id: "i1", title: "群", done_at: done ? "2026-11-01T10:00:00Z" : null, lark: { feed_id: "f1", is_group: true, ...lark } }) as unknown as InboxItem;

  it("lands on its @me with the New messages line at its first new message", () => {
    expect(chatOfItem(item({ start_position: "40", mention_position: "44" }))).toMatchObject({ focus: "44", unreadFrom: "40", mention: "44" });
    expect(chatOfItem(item({ start_position: "40" }))).toMatchObject({ focus: "40", unreadFrom: "40" });
  });
});
