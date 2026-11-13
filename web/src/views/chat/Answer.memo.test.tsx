// @vitest-environment jsdom
// A finished answer is not drawn again when its segment re-renders with the same props (#3024): the chat context
// changes with every streamed item, so every SegmentView re-renders, and each finished Answer used to follow.
import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item } from "../../lib/types";

// count how often Answer draws its menu, the first thing it renders, and what it hands the reply bar
const drawn = vi.fn();
vi.mock("./MessageActions", () => ({
  MessageActions: ({ children, ...props }: { children: ReactNode; bar?: boolean; usage?: string; usageMs?: number }) => {
    drawn(props);
    return <>{children}</>;
  },
}));

const { Answer, SegmentView } = await import("./SegmentView");
const { segments } = await import("../../lib/turns");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  drawn.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const item = { id: "a1", kind: "assistant", text: "写完了", ts: "2026-11-05T10:00:00Z" } as unknown as Item;

describe("Answer", () => {
  it("is skipped when its parent re-renders with the same props", () => {
    let bump = () => {};
    function Parent() {
      const [n, setN] = useState(0);
      bump = () => setN((x) => x + 1);
      return (
        <div data-n={n}>
          <Answer item={item} mid="m" sid="s" />
        </div>
      );
    }
    act(() => root.render(<Parent />));
    expect(drawn).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("写完了");
    for (let i = 0; i < 5; i++) act(() => bump());
    expect(drawn).toHaveBeenCalledTimes(1);
  });

  it("is skipped when its exchange is worked out again with the same usage, and hands the bar its usage", () => {
    // each push builds the exchange (and its usage) anew from the same items: the same tokens are the same string
    const items = [
      { id: "u1#0", seq: 1, role: "user", kind: "text", text: "go", ts: "2026-11-05T09:59:00Z" },
      { id: "t2#0", seq: 2, role: "assistant", kind: "tool_use", tool_use_id: "c2", tool_name: "Read", tool_input: { file_path: "/a.ts" }, ts: "2026-11-05T09:59:10Z", usage: [{ model: "claude-opus-5-5", in: 3, out: 40 }] },
      { id: "r3#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "c2", text: "ok", ts: "2026-11-05T09:59:20Z" },
      { ...item, seq: 4, kind: "text", role: "assistant", usage: [{ model: "claude-opus-5-5", out: 10 }] },
    ] as Item[];
    const exchange = () => segments(items)[0];
    const draw = () => act(() => root.render(<SegmentView seg={exchange()} live={false} mid="m" sid="s" expandFold={async () => {}} />));
    // the reply's menu (your message's bubble draws one too, and is not memo'd)
    const replies = () => drawn.mock.calls.map((c) => c[0] as { bar?: boolean; usage?: string; usageMs?: number }).filter((p) => p.bar);
    draw();
    expect(replies()).toHaveLength(1);
    const [props] = replies();
    expect(JSON.parse(props.usage!)).toEqual({ entries: [{ model: "claude-opus-5-5", in: 3, out: 40 }, { model: "claude-opus-5-5", out: 10 }] });
    expect(props.usageMs).toBe(60_000); // the work summary's "Worked for": from the message to the reply
    draw();
    draw();
    expect(replies()).toHaveLength(1);
  });

  it("still redraws when its own props change", () => {
    act(() => root.render(<Answer streaming="写到" mid="m" sid="s" />));
    act(() => root.render(<Answer streaming="写到一半" mid="m" sid="s" />));
    // a streaming answer's Markdown draws its text on its own schedule: the count is what matters here
    expect(drawn).toHaveBeenCalledTimes(2);
    act(() => root.render(<Answer item={{ ...item, text: "改过了" }} mid="m" sid="s" />));
    expect(drawn).toHaveBeenCalledTimes(3);
    expect(host.textContent).toContain("改过了");
  });
});
