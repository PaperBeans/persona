// @vitest-environment jsdom
// Existing Lark history in Core, while the optional module is switched off or absent: history, thread replies, card
// links and Pins stay readable, while every Lark write entry is absent. With the module on, the same view stays writable.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectModuleHub, registerModule, resetModulesForTest, type ModuleDescriptor } from "../../core/module-host";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../../core/module-switch";
import { initI18n, t } from "../../i18n";
import type { LarkMessage, LarkPage } from "../../lib/inbox";
import { getState, reset, subscribe } from "../../lib/store";
import { LarkThread, LarkWriteGate } from "./LarkThread";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type FetchCall = { url: string; method: string; body?: unknown };
const calls: FetchCall[] = [];
const descriptor: ModuleDescriptor = {
  id: "lark",
  feature: "module.lark",
  title: () => "Lark",
  description: () => "Lark module",
  load: async () => ({}),
};

const message = (id: string, extra: Partial<LarkMessage> = {}): LarkMessage => ({
  id,
  position: id.replace(/\D/g, "") || "1",
  sender_id: "u1",
  sender_name: "Alice",
  created_at: "2026-11-20T09:00:00Z",
  text: "Historical note",
  is_mine: false,
  can_pin: true,
  thread_reply_count: 1,
  reactions: [{ key: "OK", count: 1, users: ["Alice"] }],
  parts: [
    { kind: "text", text: "Historical note" },
    {
      kind: "card",
      text: "Decision card",
      metadata: {
        body: {
          children: [
            { tag: "button", actionId: "approve", actionMethod: 2, children: [{ tag: "plain_text", text: "Approve" }] },
            { tag: "button", url: "https://example.test/open", children: [{ tag: "plain_text", text: "Open" }] },
          ],
        },
      },
    },
  ],
  ...extra,
});
const page = (messages: LarkMessage[]): LarkPage => ({ messages, has_older: false, has_newer: false });
const reply: LarkMessage = { id: "r1", position: "2", sender_id: "u2", sender_name: "Bob", created_at: "2026-11-20T09:01:00Z", text: "Thread reply" };
const pin = {
  id: "p1",
  message_id: "m1",
  created_at: "2026-11-20T08:00:00Z",
  message: { ...message("m1"), can_unpin: true },
};

let host: HTMLDivElement;
let root: Root;
const flush = async (times = 10) => {
  for (let i = 0; i < times; i++) await act(async () => { await Promise.resolve(); });
};
const buttons = () => [...host.querySelectorAll<HTMLButtonElement>("button")];
const byText = (text: string) => buttons().find((b) => b.textContent?.includes(text)) ?? null;
const byTextInDocument = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(text)) ?? null;
const byTitle = (title: string) => host.querySelector<HTMLButtonElement>(`button[title="${title}"], button[aria-label="${title}"]`);
const writeCalls = () => calls.filter((c) => c.method === "POST" && !c.url.includes("/read"));

function stubHub() {
  calls.length = 0;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, method: init.method ?? "GET", body: init.body ? JSON.parse(String(init.body)) : undefined });
    let body: unknown = { error: "not found" };
    let status = 404;
    if (url.includes("/conversations/f1/messages?")) {
      body = page([message("m1")]);
      status = 200;
    } else if (url.includes("/lark/messages/m1/thread?")) {
      body = page([message("m1"), reply]);
      status = 200;
    } else if (url.includes("/conversations/f1/pins?")) {
      body = { account_id: "acc", total: 1, items: [pin] };
      status = 200;
    } else if (url.includes("/lark/users/u1/chat")) {
      body = { feed_id: "d1" };
      status = 200;
    } else if (url.includes("/lark/users/u1?")) {
      body = { displayName: "Alice" };
      status = 200;
    } else if (url.includes("/read-states")) {
      body = { states: [] };
      status = 200;
    } else if (url.endsWith("/conversations/f1/read")) {
      body = {};
      status = 200;
    }
    return { ok: status < 400, status, text: async () => JSON.stringify(body), headers: new Headers() };
  }));
}

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetModulesForTest();
  forgetModuleSwitches();
  reset();
  connectModuleHub(getState, subscribe);
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver ??= class {
    constructor(private cb: IntersectionObserverCallback) {}
    observe(el: Element) {
      this.cb([{ target: el, isIntersecting: true, intersectionRatio: 1, rootBounds: null, intersectionRect: el.getBoundingClientRect() } as unknown as IntersectionObserverEntry], this as unknown as IntersectionObserver);
    }
    unobserve() {}
    disconnect() {}
    takeRecords() { return []; }
  } as unknown as typeof IntersectionObserver;
  Element.prototype.scrollIntoView = vi.fn();
  vi.stubGlobal("open", vi.fn());
  stubHub();
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

describe("Lark history with the module on", () => {
  beforeEach(() => registerModule(descriptor));

  it("keeps the composer, replies, reactions, Pins and card actions writable", async () => {
    await act(async () => root.render(
      <LarkWriteGate>
        <LarkThread chat={{ feed: "f1", title: "Old group", group: true }} pinsOpen onPinsOpen={() => {}} />
      </LarkWriteGate>,
    ));
    await flush(20);

    expect(host.textContent).toContain("Historical note");
    expect(host.querySelector("textarea")).not.toBeNull();
    expect(byTitle(t("inbox-larkthread.reply"))).not.toBeNull();
    expect(byTitle(t("inbox-larkthread.react"))).not.toBeNull();
    expect(byTitle(t("inbox-larkthread.add_pin"))).not.toBeNull();
    expect(byText("Approve")).not.toBeNull();
    expect(byText("Open")).not.toBeNull();
    expect(document.body.textContent).toContain(t("inbox-larkpins.total", { count: 1 }));
    expect(document.body.querySelector(`button[aria-label="${t("inbox-larkpins.remove")}"]`)).not.toBeNull();

    await act(async () => byTitle(t("inbox-larkthread.reply"))!.click());
    const box = host.querySelector<HTMLTextAreaElement>("textarea")!;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box, "A reply");
    box.dispatchEvent(new Event("input", { bubbles: true }));
    await act(async () => byText(t("inbox-larkthread.send"))!.click());
    await flush();
    expect(writeCalls().some((c) => c.url.includes("/lark/messages/m1/replies") && c.body)).toBe(true);
  });

  it("keeps the profile Message action available", async () => {
    await act(async () => root.render(
      <LarkWriteGate>
        <LarkThread chat={{ feed: "f1", title: "Old group", group: true }} />
      </LarkWriteGate>,
    ));
    await flush();
    await act(async () => byTitle(t("inbox-larkthread.open_profile", { name: "Alice" }))!.click());
    await flush();
    expect(byTextInDocument(t("inbox-larkthread.send_message"))).not.toBeNull();
  });
});

describe.each(["switched off", "absent"] as const)("Lark history with the module %s", (mode) => {
  beforeEach(() => {
    if (mode === "switched off") {
      registerModule(descriptor);
      setModuleSwitchedOff("lark", true);
    }
  });

  it("keeps the history readable without Lark write controls", async () => {
    await act(async () => root.render(
      <LarkWriteGate>
        <LarkThread chat={{ feed: "f1", title: "Old group", group: true }} />
      </LarkWriteGate>,
    ));
    await flush();

    expect(host.textContent).toContain("Historical note");
    expect(host.querySelector("textarea")).toBeNull();
    expect(byTitle(t("inbox-larkthread.reply"))).toBeNull();
    expect(byTitle(t("inbox-larkthread.react"))).toBeNull();
    expect(byTitle(t("inbox-larkthread.add_pin"))).toBeNull();
    expect(byText("Approve")).toBeNull();
    expect(byText("Open")).not.toBeNull();
    expect(host.querySelector("button[aria-pressed]")).toBeNull();
    expect([...host.querySelectorAll<HTMLSpanElement>("span[title]")].some((el) => el.title.includes("Alice"))).toBe(true);

    const replies = byText(t("inbox-larkthread.replies", { count: 1 }))!;
    await act(async () => replies.click());
    await flush();
    expect(host.textContent).toContain("Thread reply");
    expect(host.querySelector("textarea")).toBeNull();

    await act(async () => byTitle(t("inbox-larkthread.open_profile", { name: "Alice" }))!.click());
    await flush();
    expect(byTextInDocument(t("inbox-larkthread.send_message"))).toBeNull();
    expect(writeCalls().some((c) => c.url.includes("/lark/users/u1/chat"))).toBe(false);
    expect(writeCalls()).toEqual([]);
  });

  it("keeps the Pins list readable without a remove control", async () => {
    await act(async () => root.render(
      <LarkWriteGate>
        <LarkThread chat={{ feed: "f1", title: "Old group", group: true }} pinsOpen onPinsOpen={() => {}} />
      </LarkWriteGate>,
    ));
    await flush(20);

    expect(host.textContent).toContain("Historical note");
    expect(document.body.textContent).toContain(t("inbox-larkpins.total", { count: 1 }));
    expect(document.body.querySelector(`button[aria-label="${t("inbox-larkpins.remove")}"]`)).toBeNull();
    expect(document.body.textContent).toContain(t("inbox-larkpins.title"));
    expect(writeCalls()).toEqual([]);
  });
});
