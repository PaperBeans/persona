// @vitest-environment jsdom
// The Messages page after a reconnect (#4629, moved with the page into the lark module, #13404): the `lark` pushes missed while away are replayed only when the hub resumes the stream. After a hello it could
// not resume, the open chat reads its newest page again, the chat list, a chat's Pin list and the message display
// settings read again (as for a push naming the chat, or saying the settings changed); a resumed hello reads nothing.
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
import { setLarkHome } from "./home";
import { Messages } from "./Messages";

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


const count = (part: string) => reads.filter((u) => u.includes(part)).length;
const wait = async (ms: number) => {
  await act(async () => void vi.advanceTimersByTime(ms));
  await flush();
};

describe("飞书的列表在重连后", () => {
  it("hub 没能续传时会话列表、账号未读和消息显示设置重读，续传时不读", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    setLarkHome(true); // the "Messages" page is on in this browser; a hub that names no features has Feishu
    await act(async () => root.render(<Messages onToggleSidebar={() => {}} />));
    await flush();
    const chats = count("/lark/conversations?");
    const accounts = count("/lark/accounts");
    const display = count("/lark/message-display");
    expect(chats).toBeGreaterThan(0);
    expect(display).toBeGreaterThan(0);
    expect(accounts).toBeGreaterThan(0);
    await emit("hello", { resumed: true });
    await wait(1000);
    expect(count("/lark/conversations?")).toBe(chats);
    expect(count("/lark/message-display")).toBe(display);
    await emit("hello", { resumed: false });
    await wait(1000);
    expect(count("/lark/conversations?")).toBe(chats + 1);
    expect(count("/lark/accounts")).toBe(accounts + 1);
    // the message display settings ("Keep recalled messages", "Edit history") too: a push saying they changed is not replayed either
    expect(count("/lark/message-display")).toBe(display + 1);
  });
});
