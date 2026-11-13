// @vitest-environment jsdom
// What Persona sent to a task's agent (#1542): a folded line on the left, not the user's bubble, that opens to the whole
// text with Copy; the user's own word stays a bubble.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type FeedItem } from "../../lib/feed";
import { closePanel } from "../../lib/panel";
import { refresh } from "../../lib/store";
import { SentLine } from "./Sent";
import { FeedLineView } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HANDOFF = "把后台同步的内存降下来。\n\n完成标准：\n- 峰值内存降一半以上";
const now = Date.now();
const iso = (ms: number) => new Date(now + ms).toISOString();
const TODO = { id: "todo-1", title: "降低内存占用", created_at: iso(-60_000), updated_at: iso(-60_000) };
const FEED: FeedItem[] = [
  { id: "f-card", conversation: "c1", at: iso(-60_000), kind: "task", ref: TODO.id },
  { id: "f-handoff", conversation: "c1", at: iso(-59_990), kind: "routed", ref: TODO.id, body: { text: HANDOFF, from: "assistant" } },
  { id: "f-rework", conversation: "c1", at: iso(-10_000), kind: "routed", ref: TODO.id, body: { text: "（督办：返工）\n补截图", from: "assistant", interrupted: true } },
];

let host: HTMLDivElement;
let root: Root;
const click = (el: Element | null) => act(() => (el as HTMLElement).click());

beforeEach(async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve(
        new Response(JSON.stringify(String(url).includes("/todos") ? { todos: [TODO] } : String(url).includes("/feed") ? { items: FEED } : {}), {
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  await refresh();
  closePanel();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePanel();
  vi.unstubAllGlobals();
});

describe("Persona's messages to agents (#1542)", () => {
  it("are a folded line, not the user's bubble; the user's own word stays one", () => {
    const say = (body: Record<string, unknown>) =>
      renderToStaticMarkup(<FeedLineView item={{ id: "f3", conversation: "c1", at: iso(0), kind: "routed", ref: "todo_x", body }} />);
    const persona = say({ text: HANDOFF, from: "assistant" });
    expect(persona).toContain('data-testid="sent-line"');
    expect(persona).toContain("发给「");
    expect(persona).toContain("· 把后台同步的内存降下来。");
    expect(persona).not.toContain("峰值内存降一半以上");
    expect(persona).not.toContain("items-end");
    const user = say({ text: "顺便看看日志" });
    expect(user).not.toContain("sent-line");
    expect(user).toContain("items-end");
    expect(user).toContain("对「");
    expect(say({ text: "先停", from: "assistant", interrupted: true })).toContain("打断「");
  });

  it("opens to the whole text with Copy", () => {
    act(() => root.render(<SentLine item={FEED[1]} title="降低内存占用" />));
    const toggle = host.querySelector('[aria-expanded="false"]');
    expect(host.textContent).not.toContain("峰值内存降一半以上");
    click(toggle);
    expect(host.querySelector('[aria-expanded="true"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="sent-detail"]')?.textContent).toContain("峰值内存降一半以上");
    expect(host.textContent).toContain("复制");
  });
});
