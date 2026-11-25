// @vitest-environment jsdom
// #4629: the `lark` pushes missed while away are replayed only when the hub resumes the stream. After a hello it could
// not resume, the open chat reads its newest page again, the chat list, a chat's Pin list and the message display
// settings read again (as for a push naming the chat, or saying the settings changed); a resumed hello reads nothing.
// The Messages page's own lists: src/modules/lark/Messages.reconnect.test.tsx.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LarkMessage } from "../../lib/inbox";

type Listener = (msg: Record<string, unknown>) => void;
const { listeners } = vi.hoisted(() => ({ listeners: new Map<string, Set<(msg: Record<string, unknown>) => void>>() }));
vi.mock("../../lib/store", async (original) => {
  const real = await original<typeof import("../../lib/store")>();
  return {
    ...real,
    onHubEvent: (type: string, l: Listener) => {
      if (!listeners.has(type)) listeners.set(type, new Set());
      listeners.get(type)!.add(l);
      return () => void listeners.get(type)?.delete(l);
    },
  };
});
import { LarkPinsDialog } from "./LarkPins";
import { LarkThread } from "./LarkThread";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const msg = (id: string): LarkMessage => ({
  id, position: id.replace(/\D/g, ""), sender_name: "示例同事", created_at: "2026-11-01T09:00:00Z", text: `消息 ${id}`,
});

let host: HTMLDivElement;
let root: Root;
let newest: LarkMessage[];
const pageReads: string[] = [];
const reads: string[] = [];
const flush = async (n = 8) => {
  for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); });
};
const emit = async (type: string, m: Record<string, unknown>) => {
  await act(async () => listeners.get(type)?.forEach((l) => l({ type, ...m })));
  await flush();
};

class FakeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}

beforeEach(() => {
  pageReads.length = 0;
  reads.length = 0;
  newest = [msg("m1")];
  vi.stubGlobal("IntersectionObserver", FakeObserver);
  Element.prototype.scrollIntoView = vi.fn();
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    let body: unknown = { error: "not found" };
    let status = 404;
    reads.push(url);
    if (url.includes("/conversations/f1/messages?")) {
      pageReads.push(url);
      body = { messages: newest };
      status = 200;
    } else if (url.includes("/conversations/f1/pins?")) {
      body = { account_id: "a1", items: [], total: 0 };
      status = 200;
    } else if (url.includes("/lark/conversations?") || url.includes("/lark/pinned")) {
      body = { conversations: [] };
      status = 200;
    } else if (url.includes("/lark/accounts")) {
      body = { accounts: [] };
      status = 200;
    } else if (url.includes("/read-states")) {
      body = { states: [] };
      status = 200;
    }
    return { ok: status < 400, status, text: async () => JSON.stringify(body), headers: new Headers() };
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const shown = () => [...host.querySelectorAll("[data-mid]")].map((e) => e.getAttribute("data-mid"));

describe("飞书会话页在重连后", () => {
  it("hub 没能续传时重读最新一页，断线期间的新消息出现", async () => {
    await act(async () => root.render(<LarkThread chat={{ feed: "f1", title: "示例" }} />));
    await flush();
    expect(shown()).toEqual(["m1"]);
    expect(pageReads).toHaveLength(1);

    // the messages that came while the page was away; their `lark` push is not replayed
    newest = [msg("m1"), msg("m2")];
    await emit("hello", { resumed: false, event_stream: "s2", event_seq: 9 });
    expect(pageReads).toHaveLength(2);
    expect(shown()).toEqual(["m1", "m2"]);
  });

  it("续传的 hello 不重读：漏掉的推送会补发", async () => {
    await act(async () => root.render(<LarkThread chat={{ feed: "f1", title: "示例" }} />));
    await flush();
    await emit("hello", { resumed: true, event_stream: "s1", event_seq: 9 });
    expect(pageReads).toHaveLength(1);
  });
});

const count = (part: string) => reads.filter((u) => u.includes(part)).length;
const wait = async (ms: number) => {
  await act(async () => void vi.advanceTimersByTime(ms));
  await flush();
};

describe("飞书的列表在重连后", () => {
  it("hub 没能续传时 Pin 列表重读，续传时不读", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    await act(async () => root.render(<LarkPinsDialog feed="f1" topic={false} onClose={() => {}} onOpen={() => {}} />));
    await flush();
    const first = count("/conversations/f1/pins?");
    expect(first).toBeGreaterThan(0);
    await emit("hello", { resumed: true });
    await wait(1000);
    expect(count("/conversations/f1/pins?")).toBe(first);
    await emit("hello", { resumed: false });
    await wait(1000);
    expect(count("/conversations/f1/pins?")).toBe(first + 1);
  });

});
