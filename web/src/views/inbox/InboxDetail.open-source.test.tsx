// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { InboxItem } from "../../lib/inbox";
import { settleRosterDefault } from "../../lib/manager-choice";
import { InboxDetail } from "./InboxDetail";

vi.mock("../../lib/extension-slots", () => ({ useInboxItemPlace: () => undefined }));
vi.mock("../../lib/translate", () => ({ useTranslatePrefs: () => ({ enabled: false }), hubTranslation: () => undefined }));
vi.mock("./ConnectorThread", async (original) => ({ ...(await original<typeof import("./ConnectorThread")>()), ConnectorBody: () => null }));
settleRosterDefault();
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
beforeEach(() => { host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });

it.each([
  ["https://applink.feishu.cn/client/chat/open?openChatId=oc_example&position=4", true, "open_in_feishu"],
  ["https://applink.feishu.cn/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example&thread_position=8", true, "open_in_feishu"],
  ["https://applink.larksuite.com/client/chat/open?openChatId=oc_example&position=4", true, "open_in_lark"],
  ["https://applink.feishu.cn/client/chat/open?openChatId=oc_example&position=4", false, "open_in_feishu"],
] as const)("opens the exact source link alongside available draft actions: %s, reply=%s", async (url, canReply, key) => {
  const open = vi.spyOn(window, "open").mockReturnValue(null);
  const item: InboxItem = { id: "larkcli:example", source: "larkcli", kind: "dm", account: "larkcli", account_name: "Feishu", preview: "Synthetic message", title: "Example conversation", count: 1, tier: "action", connector: { thread: "larkcli:chat:oc_example", can_reply: canReply, open_url: url } };
  await act(async () => root.render(<InboxDetail item={item} loaded />));
  const buttons = [...host.querySelectorAll("button")];
  const source = buttons.filter(button => button.textContent === t(`inbox-inboxdetail.${key}`));
  expect(source).toHaveLength(1);
  expect(buttons.some(button => button.textContent === t("inbox-inboxdetail.ai_draft"))).toBe(canReply);
  await act(async () => source[0].click());
  expect(open).toHaveBeenCalledExactlyOnceWith(url, "_blank", "noopener,noreferrer");
});

it("keeps AI draft reply without inventing a source link", async () => {
  const item: InboxItem = { id: "larkcli:example", source: "larkcli", kind: "dm", account: "larkcli", account_name: "Feishu", preview: "Synthetic message", title: "Example conversation", count: 1, tier: "action", connector: { thread: "larkcli:chat:oc_example", can_reply: true } };
  await act(async () => root.render(<InboxDetail item={item} loaded />));
  expect(host.textContent).toContain(t("inbox-inboxdetail.ai_draft"));
  expect(host.textContent).not.toContain(t("inbox-inboxdetail.open_in_feishu"));
});
