// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { actionSummary, type ToolStep, type WorkGroupNode } from "../../lib/work-groups";
import { WorkGroup, type WorkProps } from "./WorkGroup";
import { forgetExpanded, openExpanded } from "./expanded";
import { presentAction } from "./ActionRow";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const tool = (id: string, name: string, input = {}): ToolStep => ({ kind: "tool", id, use: { id, seq: 1, role: "assistant", kind: "tool_use", tool_name: name, tool_use_id: id, tool_input: input }, images: [] });
const group = (id: string, tools: ToolStep[], live = false): WorkGroupNode => ({ kind: "group", id, live, clock: "run", children: tools.map(step => ({ kind: "step", step })) });

it("headers retain first-seen kind order, deduplicate read paths, combine edit kinds, and stop after three kinds", () => {
  const g = group("g", [tool("1", "Bash"), tool("2", "Read", { file_path: "src/example.ts" }), tool("3", "Read", { file_path: "src/example.ts" }), tool("4", "Edit"), tool("5", "Write"), tool("6", "Grep")]);
  expect(actionSummary(g)).toEqual({ text: "运行 1 条命令 · 读取 1 个文件 · 编辑 2 个文件", icon: "terminal" });
  expect(actionSummary(group("g", [tool("1", "Grep"), tool("2", "Read")])).icon).toBe("search");
  expect(actionSummary(group("g", [tool("1", "Edit", { file_path: "src/example.ts" }), tool("2", "Write", { file_path: "src/example.ts" })])).text).toBe("编辑 1 个文件");
  expect(actionSummary(group("g", [tool("1", "CustomTool")])).text).toContain("CustomTool");
  expect(actionSummary({ kind: "group", id: "think", live: false, clock: "run", children: [{ kind: "step", step: { kind: "thinking", id: "th#0", item: { id: "th#0", seq: 1, role: "assistant", kind: "thinking", text: "Checking" } } }] })).toEqual({ text: "", icon: null });
});

it("command rows lead with the full command (including argv), search rows retain the query and target", () => {
  expect(presentAction(tool("1", "exec_command", { command: ["npm", "test"], description: "Run unit checks" }).use)).toEqual({ param: { text: "npm test", title: "Run unit checks" } });
  expect(presentAction(tool("2", "Grep", { pattern: "missing reply", path: "src/example.ts" }).use).param).toEqual({ text: "missing reply · src/example.ts", title: "missing reply · src/example.ts" });
});

it("live runs close when superseded; manual reopen and external jumps work; only the current run previews edits", () => {
  forgetExpanded();
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const edit = tool("edit", "Edit", { file_path: "src/example.ts", old_string: "old", new_string: "new" });
  const props: WorkProps = { mid: "m", sid: "s", scope: "actions:", expandFold: async () => {}, agentStatus: () => "completed" };
  const draw = (current: boolean) => act(() => root.render(<WorkGroup g={{ ...group("outer", [], true), children: [group("first", [edit], current), ...(!current ? [group("second", [tool("cmd", "Bash", { command: "npm test" })], true)] : [])] }} p={props} />));
  const heads = () => [...host.querySelectorAll<HTMLButtonElement>("[data-work-group] > button")];
  try {
    draw(true);
    expect(heads()[0].getAttribute("aria-expanded")).toBe("false");
    act(() => heads()[0].click());
    expect(heads()[1].getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll("[data-live-edit-diff]")).toHaveLength(1);
    expect(host.querySelectorAll(".border-tool-rail")).toHaveLength(1);
    draw(false);
    expect(heads().map(b => b.getAttribute("aria-expanded"))).toEqual(["true", "false", "true"]);
    act(() => heads()[1].click());
    expect(heads()[1].getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector("[data-live-edit-diff]")).toBeNull();
    act(() => heads()[1].click());
    act(() => openExpanded(["actions:first"]));
    expect(host.querySelector('[data-tool-use="edit"]')).not.toBeNull();
  } finally { act(() => root.unmount()); host.remove(); forgetExpanded(); }
});

it("timeline keeps one continuous outer rail across nested groups and narrations, and omits leading icon on thinking-only nested groups (#14866)", () => {
  forgetExpanded();
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const props: WorkProps = { mid: "m", sid: "s", scope: "rail:", expandFold: async () => {}, agentStatus: () => "completed" };
  const thinkingNested: WorkGroupNode = {
    kind: "group",
    id: "nested-thinking",
    live: false,
    clock: "run",
    durationMs: 1000,
    children: [{ kind: "step", step: { kind: "thinking", id: "th#0", item: { id: "th#0", seq: 1, role: "assistant", kind: "thinking", text: "Planning the fix" } } }],
  };
  const toolNested = group("nested-tool", [tool("cmd", "Bash", { command: "npm test" })], false);
  const outer: WorkGroupNode = {
    kind: "group",
    id: "outer-timeline",
    live: false,
    clock: "exchange",
    durationMs: 12_000,
    children: [
      thinkingNested,
      { kind: "step", step: { kind: "text", id: "nar#0", item: { id: "nar#0", seq: 2, role: "assistant", kind: "text", text: "Intermediate narration." } } },
      toolNested,
    ],
  };
  try {
    act(() => root.render(<WorkGroup g={outer} p={props} />));
    const outerHead = host.querySelector<HTMLButtonElement>('[data-group-id="outer-timeline"] > button')!;
    act(() => outerHead.click());

    const rails = host.querySelectorAll(".border-tool-rail");
    expect(rails).toHaveLength(1);
    const outerDetail = host.querySelector<HTMLElement>('[data-group-id="outer-timeline"] [data-work-detail]')!;
    expect(outerDetail.className).toContain("ml-1.5");
    expect(outerDetail.className).toContain("border-l-2");
    expect(outerDetail.className).toContain("border-tool-rail");
    expect(outerDetail.className).toContain("pl-3");
    expect(outerDetail.textContent).toContain("Intermediate narration.");

    const nestedThinkingDetail = host.querySelector<HTMLElement>('[data-group-id="nested-thinking"] [data-work-detail]')!;
    const nestedToolDetail = host.querySelector<HTMLElement>('[data-group-id="nested-tool"] [data-work-detail]')!;
    for (const detail of [nestedThinkingDetail, nestedToolDetail]) {
      expect(detail.className).toContain("pl-3");
      expect(detail.className).not.toContain("border-l-2");
      expect(detail.className).not.toContain("border-tool-rail");
    }

    // Thinking-only nested group renders only the trailing chevron icon, never a leading layers fallback icon.
    const thinkingBtn = host.querySelector<HTMLButtonElement>('[data-group-id="nested-thinking"] > button')!;
    expect(thinkingBtn.querySelectorAll("svg")).toHaveLength(1);
    // Tool-bearing nested group keeps its leading action icon plus the trailing chevron.
    const toolBtn = host.querySelector<HTMLButtonElement>('[data-group-id="nested-tool"] > button')!;
    expect(toolBtn.querySelectorAll("svg")).toHaveLength(2);
  } finally { act(() => root.unmount()); host.remove(); forgetExpanded(); }
});
