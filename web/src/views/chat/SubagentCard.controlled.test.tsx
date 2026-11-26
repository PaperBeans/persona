// @vitest-environment jsdom
// Controlled panels supply subagent metadata, tasks, expansion, panel open and full-text loading without touching default singletons; default callers keep every fallback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { emptyTaskIndex } from "../../lib/tasks";
import type { Item, Subagent, TaskNote } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import type { SubagentCardProps } from "./SubagentCard";

const mocks = vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  return {
    useChatContext: vi.fn(),
    useExpanded: vi.fn(),
    openPanel: vi.fn(),
    useFullText: vi.fn(),
  };
});

vi.mock("./chat-context", () => ({ useChatContext: mocks.useChatContext }));
vi.mock("./expanded", () => ({ useExpanded: mocks.useExpanded }));
vi.mock("../../lib/panel", () => ({ openPanel: mocks.openPanel }));
vi.mock("./FullText", () => ({ useFullText: mocks.useFullText }));

const { SubagentCard } = await import("./SubagentCard");
const { subagentCardKey } = await import("../../lib/tasks");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-18T10:00:00Z");
const at = (seconds: number) => new Date(T0 + seconds * 1000).toISOString();

const makeStep = (
  id: string,
  callId: string,
  input: Record<string, unknown>,
  result?: Partial<Item>,
): ToolStep => ({
  kind: "tool",
  id,
  use: {
    id,
    seq: 1,
    role: "assistant",
    kind: "tool_use",
    tool_name: "Agent",
    tool_use_id: callId,
    tool_input: input,
    ts: at(0),
  },
  result: result
    ? {
        id: `${id}-res`,
        seq: 2,
        role: "user",
        kind: "tool_result",
        tool_use_id: callId,
        ts: at(45),
        ...result,
      }
    : undefined,
  images: [],
});

const makeSubagent = (extra: Partial<Subagent> = {}): Subagent => ({
  id: "sa-example",
  session_id: "s-example@sa-example",
  tool_use_id: "call-example",
  running: false,
  started_at: at(0),
  updated_at: at(45),
  model: "claude-haiku-4-5-20251001",
  tokens: 12_400,
  steps: 6,
  last_tool: { id: "t1", name: "Bash", summary: "Run unit checks" },
  ...extra,
});

const defaultInput = {
  description: "Verify auth middleware",
  prompt: "Inspect session refresh handling and summarize edge cases.",
};

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<SubagentCardProps> = {}) => {
  await act(async () => {
    root.render(
      <SubagentCard
        step={makeStep("u1#0", "call-example", defaultInput, { text: "All middleware checks passed." })}
        status="completed"
        mid="example-host"
        sid="s-example"
        {...props}
      />,
    );
  });
};

const head = () => host.querySelector<HTMLButtonElement>("[data-subagent-card] button")!;
const viewFull = () => host.querySelector<HTMLButtonElement>("[data-subagent-view-full]");
const fullTextBtn = () => host.querySelector<HTMLButtonElement>("[data-full-text]");
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const expectNoDefaultSingletons = () => {
  expect(mocks.useChatContext).not.toHaveBeenCalled();
  expect(mocks.useExpanded).not.toHaveBeenCalled();
  expect(mocks.openPanel).not.toHaveBeenCalled();
  expect(mocks.useFullText).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.useChatContext.mockReturnValue({
    subagents: [],
    tasks: emptyTaskIndex(),
    lineStyle: undefined,
    harness: undefined,
    scope: "default:",
  });
  mocks.useExpanded.mockReturnValue([false, vi.fn()]);
  mocks.useFullText.mockImplementation((item: Item | undefined) => ({
    text: item?.text ?? "",
    more: null,
    getText: async () => item?.text ?? "",
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("SubagentCard with controlled props", () => {
  it("renders explicit metadata, notes, reports, harness, expansion and view-full callback without invoking default singletons", async () => {
    const onExpandedChange = vi.fn();
    const onOpenFull = vi.fn();
    const loadFullText = vi.fn(async (item: Item) => item.text ?? "");
    const notes: TaskNote[] = [{ kind: "agent", status: "completed", output_file: "/workspace/example/out.log" }];
    const reports: Item[] = [
      { id: "rep#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "call-example", text: "Latest controlled report." },
    ];

    await mount({
      subagent: makeSubagent(),
      notes,
      reports,
      lineStyle: "agents",
      harness: "Codex",
      expanded: false,
      onExpandedChange,
      onOpenFull,
      loadFullText,
    });

    expect(host.textContent).toContain("Verify auth middleware");
    expect(host.textContent).toContain("Codex");
    expect(host.querySelector("[data-task-model]")?.textContent).toBe("Haiku 4.5");
    expect(head().getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-subagent-details]")).toBeNull();

    act(() => head().click());
    expect(onExpandedChange).toHaveBeenCalledWith(true);

    await mount({
      subagent: makeSubagent(),
      notes,
      reports,
      lineStyle: "agents",
      harness: "Codex",
      expanded: true,
      onExpandedChange,
      onOpenFull,
      loadFullText,
    });

    expect(head().getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector("[data-subagent-prompt]")?.textContent).toBe(defaultInput.prompt);
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Latest controlled report.");
    expect(host.querySelector("[data-subagent-last-tool]")?.textContent).toBe(t("chat-subagentcard.last_tool", { tool: "Bash" }));
    expect(host.querySelector("[data-subagent-output]")?.textContent).toBe(t("chat-subagentcard.output_file", { path: "/workspace/example/out.log" }));

    act(() => viewFull()!.click());
    expect(onOpenFull).toHaveBeenCalledWith({
      kind: "subagent",
      mid: "example-host",
      sid: "s-example@sa-example",
      parentMid: "example-host",
      parentSid: "s-example",
      call: "call-example",
      from: "card",
    });

    // Assistant style skips harness without needing ChatContext, and onOpenFull={null} hides View full.
    await mount({
      subagent: makeSubagent(),
      notes,
      reports,
      lineStyle: "assistant",
      expanded: true,
      onExpandedChange,
      onOpenFull: null,
      loadFullText,
    });
    expect(host.textContent).not.toContain("Codex");
    expect(host.textContent).not.toContain("Claude Code");
    expect(viewFull()).toBeNull();

    // Explicit null subagent and null harness fall back to default harness without reading ChatContext.
    await mount({
      subagent: null,
      notes: [],
      reports: [],
      lineStyle: "agents",
      harness: null,
      expanded: false,
      onExpandedChange,
      onOpenFull,
      loadFullText,
    });
    expect(host.textContent).toContain("Claude Code");
    expect(host.querySelector("[data-task-model]")).toBeNull();
    expectNoDefaultSingletons();
  });

  it("loads clipped results via loadFullText, supports failure retry, and prevents cross-session / ABA loading lock and stale overwrites", async () => {
    let resolveA1!: (text: string) => void;
    let rejectA2!: (err: Error) => void;
    let resolveA3!: (text: string) => void;
    const loadFullText = vi.fn((item: Item) => {
      const count = loadFullText.mock.calls.length;
      if (count === 1) return new Promise<string>((resolve) => { resolveA1 = resolve; });
      if (count === 2) return new Promise<string>((_resolve, reject) => { rejectA2 = reject; });
      if (count === 3) return new Promise<string>((resolve) => { resolveA3 = resolve; });
      return Promise.resolve(`Unexpected ${item.id}`);
    });

    const stepA = makeStep("u1#0", "call-a", defaultInput, {
      text: "Clipped A preview\n… [truncated]",
      full_size: 4096,
    });
    const stepB = makeStep("u1#0", "call-a", defaultInput, {
      text: "Clipped B preview\n… [truncated]",
      full_size: 4096,
    });
    const baseControlled: Partial<SubagentCardProps> = {
      subagent: makeSubagent({ id: "sa-a", session_id: "s-a@sa-a", tool_use_id: "call-a" }),
      notes: [],
      reports: [],
      lineStyle: "agents",
      harness: "Claude Code",
      expanded: true,
      onExpandedChange: vi.fn(),
      onOpenFull: vi.fn(),
      loadFullText,
    };

    // Mount session A and start loading full text (request #1 stays in flight).
    await mount({ ...baseControlled, step: stepA, mid: "m-a", sid: "s-a" });
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("idle");
    act(() => fullTextBtn()!.click());
    expect(loadFullText).toHaveBeenCalledTimes(1);
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("loading");

    // Switch to session B (same item id and full_size, different mid/sid): must NOT inherit A's loading state.
    await mount({ ...baseControlled, step: stepB, mid: "m-b", sid: "s-b" });
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullTextBtn()?.disabled).toBe(false);

    // ABA: switch back to session A while request #1 is STILL pending.
    // Must be idle (never permanently stuck in loading) and allow a fresh request (#2).
    await mount({ ...baseControlled, step: stepA, mid: "m-a", sid: "s-a" });
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullTextBtn()?.disabled).toBe(false);

    act(() => fullTextBtn()!.click());
    expect(loadFullText).toHaveBeenCalledTimes(2);
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("loading");

    // Stale request #1 resolves late while request #2 is active: must be ignored and must not clear loading.
    await act(async () => {
      resolveA1("STALE A1 RESULT");
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("[data-subagent-summary]")?.textContent).not.toContain("STALE A1 RESULT");
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("loading");

    // Request #2 fails -> button shows failed retry state; retry starts request #3 -> resolves fresh result.
    await act(async () => {
      rejectA2(new Error("offline"));
      await Promise.resolve();
    });
    await flush();
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("failed");

    act(() => fullTextBtn()!.click());
    expect(loadFullText).toHaveBeenCalledTimes(3);
    expect(fullTextBtn()?.getAttribute("data-full-text")).toBe("loading");

    await act(async () => {
      resolveA3("Fresh full report for session A.");
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Fresh full report for session A.");
    expect(fullTextBtn()).toBeNull();
    expectNoDefaultSingletons();
  });
});

describe("SubagentCard default compatibility", () => {
  it("falls back to ChatContext, useExpanded, openPanel and useFullText when controlled props are omitted", async () => {
    const setOpen = vi.fn();
    const meta = makeSubagent();
    const notes = new Map<string, TaskNote[]>([["call-example", [{ kind: "agent", status: "completed", output_file: "/workspace/default.out" }]]]);
    mocks.useChatContext.mockReturnValue({
      subagents: [meta],
      tasks: { ...emptyTaskIndex(), notes },
      lineStyle: "agents",
      harness: "Grok",
      scope: "t:",
    });
    mocks.useExpanded.mockReturnValue([true, setOpen]);
    mocks.useFullText.mockReturnValue({
      text: "Default full text output.",
      more: <span data-default-more>more</span>,
      getText: async () => "Default full text output.",
    });

    await mount({ scope: "t:" });

    expect(mocks.useChatContext).toHaveBeenCalled();
    expect(mocks.useExpanded).toHaveBeenCalledWith(subagentCardKey("t:", "call-example"));
    expect(mocks.useFullText).toHaveBeenCalledWith(expect.objectContaining({ id: "u1#0-res" }), "example-host", "s-example");
    expect(host.textContent).toContain("Grok");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Default full text output.");
    expect(host.querySelector("[data-default-more]")).not.toBeNull();

    act(() => head().click());
    expect(setOpen).toHaveBeenCalledWith(expect.any(Function));

    act(() => viewFull()!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "subagent",
      mid: "example-host",
      sid: "s-example@sa-example",
      parentMid: "example-host",
      parentSid: "s-example",
      call: "call-example",
      from: "card",
    });
  });
});
