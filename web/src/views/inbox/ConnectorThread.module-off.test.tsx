// @vitest-environment jsdom
// The larkcli connector is another Core read path for an existing Lark chat. Its reply box follows the same module
// boundary as LarkThread: off means readable history without a composer or reply action; on means the old send flow.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectModuleHub, registerModule, resetModulesForTest, type ModuleDescriptor } from "../../core/module-host";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../../core/module-switch";
import { initI18n, t } from "../../i18n";
import type { ThreadPage } from "../../lib/connectors";
import type { InboxItem } from "../../lib/inbox";
import { resetManagerChoice } from "../../lib/manager-choice";
import { getState, reset, subscribe } from "../../lib/store";
import { ConnectorBody } from "./ConnectorThread";
import { LarkWriteGate } from "./LarkThread";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

const transport = vi.hoisted(() => ({ load: vi.fn(), reply: vi.fn() }));
vi.mock("../../lib/connectors", async (original) => ({
  ...(await original<typeof import("../../lib/connectors")>()),
  loadThread: transport.load,
  replyInThread: transport.reply,
}));

const descriptor: ModuleDescriptor = {
  id: "lark",
  feature: "module.lark",
  title: () => "Lark",
  description: () => "Lark module",
  load: async () => ({}),
};
const page: ThreadPage = {
  chat: { id: "g", name: "Old group", kind: "group", reply: "example_send" },
  messages: [{ id: "1", at: "2026-11-20T09:00:00Z", sender: { name: "Alice" }, text: "Historical note" }],
};
const item = {
  id: "larkcli:1",
  source: "larkcli",
  kind: "group",
  account: "larkcli",
  account_name: "Lark",
  title: "Old group",
  preview: "",
  count: 1,
  connector: { thread: "primary.group" },
} as InboxItem;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const render = async () => {
  await act(async () => root.render(<LarkWriteGate><ConnectorBody item={item} /></LarkWriteGate>));
  await flush();
};
const replyButtons = () => [...host.querySelectorAll("button")].filter((b) => b.textContent?.trim() === t("inbox-connectorthread.reply"));

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetModulesForTest();
  forgetModuleSwitches();
  reset();
  connectModuleHub(getState, subscribe);
  resetManagerChoice();
  transport.load.mockReset().mockResolvedValue(page);
  transport.reply.mockReset().mockResolvedValue({ ok: true });
  Element.prototype.scrollIntoView = vi.fn();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetManagerChoice();
});

describe("larkcli history with the module on", () => {
  beforeEach(() => registerModule(descriptor));

  it("keeps replies working", async () => {
    await render();
    expect(host.textContent).toContain("Historical note");
    expect(host.querySelector("textarea")).not.toBeNull();
    expect(replyButtons()).toHaveLength(1);

    await act(async () => replyButtons()[0]!.click());
    const box = host.querySelector<HTMLTextAreaElement>("textarea")!;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box, "A reply");
    box.dispatchEvent(new Event("input", { bubbles: true }));
    await act(async () => box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    await flush();
    expect(transport.reply).toHaveBeenCalledExactlyOnceWith("larkcli", {
      thread: "primary.group", action: "example_send", text: "A reply", reply_to: "1",
    }, undefined);
  });
});

describe.each(["switched off", "absent"] as const)("larkcli history with the module %s", (mode) => {
  beforeEach(() => {
    if (mode === "switched off") {
      registerModule(descriptor);
      setModuleSwitchedOff("lark", true);
    }
  });

  it("keeps history readable without a reply entry", async () => {
    await render();
    expect(host.textContent).toContain("Historical note");
    expect(host.querySelector("textarea")).toBeNull();
    expect(replyButtons()).toEqual([]);
    expect(transport.reply).not.toHaveBeenCalled();
  });
});
