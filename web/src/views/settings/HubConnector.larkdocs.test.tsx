// @vitest-environment jsdom
// lark-cli's watched documents on its connector page: adding a link and the "every comment on documents I own" switch
// save the whole options object, so the chat options stay.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HubConnector } from "../../lib/connectors";

const saved = vi.hoisted(() => [] as unknown[]);
const lark: HubConnector = {
  id: "larkcli", name: "飞书 · 我的消息", status: "ok", account: "示例用户", enabled: true, capabilities: ["read"],
  options: { inbox_all_chats: ["oc_example"], documents: [], wiki_spaces: [], owned_all_comments: false, triage_rules: "" },
};
vi.mock("../../lib/connectors", async (original) => ({
  ...(await original<typeof import("../../lib/connectors")>()),
  listConnectors: async () => [lark],
  setConnector: async (_id: string, body: { options?: unknown }) => {
    saved.push(body.options);
    return { ...lark, options: body.options };
  },
}));
import { ConnectorDetail } from "./HubConnector";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  saved.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<ConnectorDetail id="larkcli" onBack={() => {}} />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("lark-cli's watched documents", () => {
  it("adds a document link and keeps the chat options", async () => {
    expect(host.textContent).toContain("关注的文档");
    const input = host.querySelector<HTMLInputElement>('input[aria-label="文档链接"]')!;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
      set.call(input, "https://example.feishu.cn/docx/doc_example");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => input.form!.requestSubmit());
    expect(saved).toEqual([{ inbox_all_chats: ["oc_example"], documents: ["https://example.feishu.cn/docx/doc_example"], wiki_spaces: [], owned_all_comments: false, triage_rules: "" }]);
    expect(host.textContent).toContain("https://example.feishu.cn/docx/doc_example");
  });
});
