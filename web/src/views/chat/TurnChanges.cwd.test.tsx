// @vitest-environment jsdom
// The changed-files card reads the session's folder from the store (#5157): a session row that comes in after the
// transcript (a chat opened from a link, a reload) turns the full paths into ones relative to the folder, without the
// exchange being drawn again for another reason.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
import { applyCompleteSessions, reset } from "../../lib/store";
import { emptyTaskIndex } from "../../lib/tasks";
import { segments } from "../../lib/turns";
import type { Item, Session } from "../../lib/types";
import { ChatContext } from "./chat-context";
import { SegmentView } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  reset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

const items: Item[] = [
  { id: "u1#0", seq: 1, role: "user", kind: "text", text: "rename it" },
  { id: "e1#0", seq: 2, role: "assistant", kind: "tool_use", tool_use_id: "c1", tool_name: "Edit", tool_input: { file_path: "/code/app/src/a.ts", old_string: "a", new_string: "b" } },
  { id: "r1#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "c1", text: "ok" },
  { id: "a1#0", seq: 3, role: "assistant", kind: "text", text: "Renamed." },
];

describe("the card and the session's folder", () => {
  it("shows paths relative to the folder once the session row comes in", () => {
    const seg = segments(items)[0];
    act(() =>
      root.render(
        <ChatContext.Provider value={{ tasks: emptyTaskIndex(), subagents: [], scope: "t:", lineStyle: "agents" }}>
          <SegmentView seg={seg} live={false} mid="mac" sid="s1" expandFold={async () => {}} />
        </ChatContext.Provider>,
      ),
    );
    const rows = () => [...host.querySelectorAll<HTMLElement>("[data-changed-file]")].map((r) => r.dataset.changedFile);
    expect(rows()).toEqual(["/code/app/src/a.ts"]);
    act(() => applyCompleteSessions([{ machine_id: "mac", session_id: "s1", title: "s1", cwd: "/code/app", status: "idle", state: "idle" } as Session]));
    expect(rows()).toEqual(["src/a.ts"]);
  });
});
