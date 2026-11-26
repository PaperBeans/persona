// @vitest-environment jsdom
// Controlled panels supply ActionRow expansion, badges, cwd, side-panel routing, full-text loading and image rendering
// without touching default-panel singletons; existing default fallbacks remain pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { emptyTaskIndex, type TaskIndex } from "../../lib/tasks";
import type { Item } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import type { ActionRowProps } from "./ActionRow";

const mocks = vi.hoisted(() => ({
  useChatContext: vi.fn(),
  useExpanded: vi.fn(),
  useReveal: vi.fn(),
  getSession: vi.fn(),
  openPanel: vi.fn(),
  useFullText: vi.fn(),
  thumb: vi.fn(),
}));

vi.mock("./chat-context", () => ({ useChatContext: mocks.useChatContext }));
vi.mock("./expanded", () => ({ useExpanded: mocks.useExpanded }));
vi.mock("./search-reveal", () => ({ useReveal: mocks.useReveal }));
vi.mock("../../lib/store", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/store")>()),
  getSession: mocks.getSession,
}));
vi.mock("../../lib/panel", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/panel")>()),
  openPanel: mocks.openPanel,
}));
vi.mock("./FullText", () => ({ useFullText: mocks.useFullText }));
vi.mock("./Steps", () => ({
  Thumb: (props: { item: Item; mid: string; sid: string }) => {
    mocks.thumb(props);
    return <span data-default-thumb={props.item.id} data-mid={props.mid} data-sid={props.sid} />;
  },
}));

const { ActionRow } = await import("./ActionRow");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const makeStep = (
  toolName: string,
  toolInput: Record<string, unknown>,
  extra: { id?: string; call?: string; result?: Partial<Item>; images?: Item[] } = {},
): ToolStep => {
  const id = extra.id ?? "u1#0";
  const call = extra.call ?? "call-1";
  const use: Item = {
    id,
    seq: 1,
    role: "assistant",
    kind: "tool_use",
    tool_name: toolName,
    tool_use_id: call,
    tool_input: toolInput,
  };
  const result: Item | undefined = extra.result
    ? {
        id: `${id}-r`,
        seq: 2,
        role: "user",
        kind: "tool_result",
        tool_use_id: call,
        text: "ok",
        ...extra.result,
      }
    : undefined;
  return { kind: "tool", id, use, result, images: extra.images ?? [] };
};

let host: HTMLDivElement;
let root: Root;

const mount = async (props: ActionRowProps) => {
  await act(async () => root.render(<ActionRow {...props} />));
};
const rowButton = () => host.querySelector<HTMLButtonElement>("[data-action-row] > button")!;
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};
const expectNoDefaultSingletons = () => {
  expect(mocks.useChatContext).not.toHaveBeenCalled();
  expect(mocks.useExpanded).not.toHaveBeenCalled();
  expect(mocks.useReveal).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.openPanel).not.toHaveBeenCalled();
  expect(mocks.useFullText).not.toHaveBeenCalled();
  expect(mocks.thumb).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.useChatContext.mockReturnValue({ tasks: emptyTaskIndex(), scope: "default:" });
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

describe("ActionRow with controlled props", () => {
  it("handles controlled expansion, explicit task badges and explicit tasks without default singletons", async () => {
    const onExpandedChange = vi.fn();
    const loadFullText = vi.fn();
    const step = makeStep("Bash", { command: "npm test", description: "Run suite" }, { result: { text: "all green" } });

    await mount({
      step,
      expanded: false,
      onExpandedChange,
      taskBadge: { text: "Background · running", failed: false },
      loadFullText,
    });

    expect(rowButton().getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-inline-detail]")).toBeNull();
    expect(host.textContent).toContain("Background · running");

    act(() => rowButton().click());
    expect(onExpandedChange).toHaveBeenCalledWith(true);
    expect(host.querySelector("[data-inline-detail]")).toBeNull();

    await mount({
      step,
      expanded: true,
      onExpandedChange,
      taskBadge: { text: "Background · failed", failed: true },
      loadFullText,
    });
    expect(rowButton().getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("all green");
    expect(host.querySelector(".bg-red-tint")?.textContent).toBe("Background · failed");

    act(() => rowButton().click());
    expect(onExpandedChange).toHaveBeenLastCalledWith(false);

    const tasks: TaskIndex = {
      ...emptyTaskIndex(),
      started: new Set(["call-1"]),
    };
    await mount({
      step,
      expanded: false,
      onExpandedChange,
      tasks,
      loadFullText,
    });
    expect(host.textContent).toContain(t("lib-tasks.badge_running"));

    await mount({
      step,
      expanded: false,
      onExpandedChange,
      taskBadge: null,
      tasks,
      loadFullText,
    });
    expect(host.textContent).not.toContain(t("lib-tasks.badge_running"));
    expectNoDefaultSingletons();
  });

  it("routes edit and file rows through onOpenPanel with explicit cwd and keeps trace rows inline", async () => {
    const onOpenPanel = vi.fn();
    const onExpandedChange = vi.fn();
    const loadFullText = vi.fn();

    const editStep = makeStep("Edit", { file_path: "src/app.ts", old_string: "old", new_string: "new" });
    await mount({
      step: editStep,
      mid: "m-shell",
      sid: "s-shell",
      cwd: "/workspace/repo",
      taskBadge: null,
      expanded: false,
      onOpenPanel,
      loadFullText,
    });
    act(() => rowButton().click());
    expect(onOpenPanel).toHaveBeenCalledWith({
      kind: "changes",
      mid: "m-shell",
      sid: "s-shell",
      at: { path: "/workspace/repo/src/app.ts", call: "call-1" },
    });

    const readStep = makeStep("Read", { file_path: "/workspace/repo/README.md", offset: 12 });
    await mount({
      step: readStep,
      mid: "m-shell",
      sid: "s-shell",
      cwd: null,
      taskBadge: null,
      expanded: false,
      onOpenPanel,
      loadFullText,
    });
    act(() => rowButton().click());
    expect(onOpenPanel).toHaveBeenLastCalledWith({
      kind: "file",
      mid: "m-shell",
      path: "/workspace/repo/README.md",
      cwd: undefined,
      line: 12,
    });

    await mount({
      step: editStep,
      trace: true,
      taskBadge: null,
      expanded: false,
      onExpandedChange,
      onOpenPanel,
      loadFullText,
    });
    act(() => rowButton().click());
    expect(onExpandedChange).toHaveBeenCalledWith(true);
    expect(onOpenPanel).toHaveBeenCalledTimes(2);
    expectNoDefaultSingletons();
  });

  it("loads clipped results through loadFullText with dedupe, failure retry, sync-throw recovery and stale-load protection", async () => {
    let resolveFirst!: (value: string) => void;
    let calls = 0;
    const loadFullText = vi.fn((_item: Item) => {
      calls += 1;
      if (calls === 1) {
        return new Promise<string>((resolve) => { resolveFirst = resolve; });
      }
      if (calls === 2) {
        throw new Error("sync failure");
      }
      if (calls === 3) {
        return Promise.reject(new Error("async failure"));
      }
      return Promise.resolve("full recovered output");
    });

    const cutStep = makeStep(
      "Bash",
      { command: "cat log.txt" },
      { id: "cut-1", result: { id: "cut-1-r", text: "first line\n…（已截断，共 90000 字节）", full_size: 90_000 } },
    );

    await mount({
      step: cutStep,
      expanded: true,
      taskBadge: null,
      loadFullText,
    });
    const more = () => host.querySelector<HTMLButtonElement>("[data-full-text]");
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("first line");
    expect(host.querySelector("[data-inline-detail]")?.textContent).not.toContain("共 90000 字节");
    expect(more()?.getAttribute("data-full-text")).toBe("idle");

    // Duplicate clicks while pending invoke loadFullText only once.
    act(() => more()!.click());
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(more()?.disabled).toBe(true);
    expect(more()?.getAttribute("aria-busy")).toBe("true");
    act(() => more()!.click());
    await flush();
    expect(loadFullText).toHaveBeenCalledTimes(1);

    // Switch to a different cut step before the first promise settles: stale resolution must be ignored.
    const secondCutStep = makeStep(
      "Bash",
      { command: "cat second.txt" },
      { id: "cut-2", result: { id: "cut-2-r", text: "second preview", full_size: 40_000 } },
    );
    await mount({
      step: secondCutStep,
      expanded: true,
      taskBadge: null,
      loadFullText: (item) => loadFullText(item),
    });
    await act(async () => {
      resolveFirst("stale first output");
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("second preview");
    expect(host.querySelector("[data-inline-detail]")?.textContent).not.toContain("stale first output");

    // Synchronous throw transitions to failed and stays retryable.
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("failed");

    // Async rejection stays failed and retryable.
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("failed");

    // Final retry resolves full output and clears the button.
    act(() => more()!.click());
    await flush();
    expect(loadFullText).toHaveBeenCalledTimes(4);
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("full recovered output");
    expect(more()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("clears abandoned loading state when an in-flight key settles away and returns (path 1: resolve and reject)", async () => {
    const stepA = makeStep(
      "Bash",
      { command: "cat a.log" },
      { id: "race-a", result: { id: "race-a-r", text: "preview A", full_size: 50_000 } },
    );
    const stepB = makeStep(
      "Bash",
      { command: "cat b.log" },
      { id: "race-b", result: { id: "race-b-r", text: "preview B", full_size: 50_000 } },
    );
    const more = () => host.querySelector<HTMLButtonElement>("[data-full-text]");

    // Subcase 1a: keyA resolves while viewing keyB -> returning to keyA must be idle and clickable.
    let resolveA1!: (v: string) => void;
    let rejectA2!: (err: Error) => void;
    let calls = 0;
    const loadFullText = vi.fn((_item: Item) => {
      calls += 1;
      if (calls === 1) return new Promise<string>((resolve) => { resolveA1 = resolve; });
      if (calls === 2) return new Promise<string>((_resolve, reject) => { rejectA2 = reject; });
      return Promise.resolve("recovered A");
    });

    await mount({ step: stepA, expanded: true, taskBadge: null, loadFullText });
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(more()?.disabled).toBe(true);
    expect(loadFullText).toHaveBeenCalledTimes(1);

    await mount({ step: stepB, expanded: true, taskBadge: null, loadFullText });
    await act(async () => {
      resolveA1("settled while away");
      await Promise.resolve();
    });
    await flush();

    // Returning to keyA must NOT be permanently stuck in loading.
    await mount({ step: stepA, expanded: true, taskBadge: null, loadFullText });
    expect(more()?.getAttribute("data-full-text")).toBe("idle");
    expect(more()?.disabled).toBe(false);

    // Subcase 1b: keyA rejects while viewing keyB -> returning to keyA must also be idle and loadable.
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(loadFullText).toHaveBeenCalledTimes(2);

    await mount({ step: stepB, expanded: true, taskBadge: null, loadFullText });
    await act(async () => {
      rejectA2(new Error("failed while away"));
      await Promise.resolve();
    });
    await flush();

    await mount({ step: stepA, expanded: true, taskBadge: null, loadFullText });
    expect(more()?.getAttribute("data-full-text")).toBe("idle");
    expect(more()?.disabled).toBe(false);

    act(() => more()!.click());
    await flush();
    expect(loadFullText).toHaveBeenCalledTimes(3);
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("recovered A");
    expect(more()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("ignores stale ABA settlements so an older keyA request never overwrites or deadlocks a newer keyA request (path 2)", async () => {
    const stepA = makeStep(
      "Bash",
      { command: "cat a.log" },
      { id: "aba-a", result: { id: "aba-a-r", text: "preview A", full_size: 50_000 } },
    );
    const stepB = makeStep(
      "Bash",
      { command: "cat b.log" },
      { id: "aba-b", result: { id: "aba-b-r", text: "preview B", full_size: 50_000 } },
    );
    const more = () => host.querySelector<HTMLButtonElement>("[data-full-text]");

    // Subcase 2a: stale p1 resolves while fresh p3 is in flight -> must not overwrite p3.
    let resolveP1!: (v: string) => void;
    let resolveP3!: (v: string) => void;
    let calls = 0;
    const loadFullTextA = vi.fn((item: Item) => {
      calls += 1;
      if (calls === 1) return new Promise<string>((resolve) => { resolveP1 = resolve; });
      if (item.id === "aba-b-r") return Promise.resolve("full B");
      return new Promise<string>((resolve) => { resolveP3 = resolve; });
    });

    await mount({ step: stepA, expanded: true, taskBadge: null, loadFullText: loadFullTextA });
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");

    // Switch to keyB and finish p2.
    await mount({ step: stepB, expanded: true, taskBadge: null, loadFullText: loadFullTextA });
    act(() => more()!.click());
    await flush();
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("full B");

    // Return to keyA and start fresh p3 while p1 is still pending.
    await mount({ step: stepA, expanded: true, taskBadge: null, loadFullText: loadFullTextA });
    expect(more()?.getAttribute("data-full-text")).toBe("idle");
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(loadFullTextA).toHaveBeenCalledTimes(3);

    // Late p1 resolves -> must be ignored; p3 stays loading.
    await act(async () => {
      resolveP1("stale p1 output");
      await Promise.resolve();
    });
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(host.querySelector("[data-inline-detail]")?.textContent).not.toContain("stale p1 output");

    // Fresh p3 resolves -> wins cleanly.
    await act(async () => {
      resolveP3("fresh p3 output");
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("fresh p3 output");
    expect(more()).toBeNull();

    // Subcase 2b: stale p1 rejects while fresh p3 is in flight -> must not flip to failed or deadlock retry.
    const stepA2 = makeStep(
      "Bash",
      { command: "cat a2.log" },
      { id: "aba-a2", result: { id: "aba-a2-r", text: "preview A2", full_size: 50_000 } },
    );
    const stepB2 = makeStep(
      "Bash",
      { command: "cat b2.log" },
      { id: "aba-b2", result: { id: "aba-b2-r", text: "preview B2", full_size: 50_000 } },
    );
    let rejectStale!: (err: Error) => void;
    let rejectFresh!: (err: Error) => void;
    let calls2 = 0;
    const loadFullTextB = vi.fn((item: Item) => {
      calls2 += 1;
      if (calls2 === 1) return new Promise<string>((_resolve, reject) => { rejectStale = reject; });
      if (item.id === "aba-b2-r") return Promise.resolve("full B2");
      if (calls2 === 3) return new Promise<string>((_resolve, reject) => { rejectFresh = reject; });
      return Promise.resolve("retried p4 output");
    });

    await mount({ step: stepA2, expanded: true, taskBadge: null, loadFullText: loadFullTextB });
    act(() => more()!.click());
    await flush();
    await mount({ step: stepB2, expanded: true, taskBadge: null, loadFullText: loadFullTextB });
    act(() => more()!.click());
    await flush();
    await mount({ step: stepA2, expanded: true, taskBadge: null, loadFullText: loadFullTextB });
    act(() => more()!.click());
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");

    // Stale p1 rejects -> must be ignored; p3 stays loading.
    await act(async () => {
      rejectStale(new Error("stale failure"));
      await Promise.resolve();
    });
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("loading");
    expect(more()?.disabled).toBe(true);

    // Fresh p3 rejects -> transitions to failed and clears pending so retry succeeds immediately.
    await act(async () => {
      rejectFresh(new Error("fresh failure"));
      await Promise.resolve();
    });
    await flush();
    expect(more()?.getAttribute("data-full-text")).toBe("failed");
    expect(more()?.disabled).toBe(false);

    act(() => more()!.click());
    await flush();
    expect(loadFullTextB).toHaveBeenCalledTimes(4);
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("retried p4 output");
    expect(more()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("renders tool images through renderImage without mounting default Thumb", async () => {
    const imgA: Item = { id: "img-a", seq: 3, role: "user", kind: "image" };
    const imgB: Item = { id: "img-b", seq: 4, role: "user", kind: "image" };
    const renderImage = vi.fn((item: Item, index: number) => (
      <span data-custom-image={`${index}:${item.id}`}>{item.id}</span>
    ));
    const step = makeStep("Bash", { command: "screenshot" }, { images: [imgA, imgB] });

    await mount({
      step,
      expanded: false,
      taskBadge: null,
      loadFullText: vi.fn(),
      renderImage,
    });

    expect(renderImage).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[data-custom-image="0:img-a"]')).not.toBeNull();
    expect(host.querySelector('[data-custom-image="1:img-b"]')).not.toBeNull();
    expectNoDefaultSingletons();
  });

  it("supports partial overrides without breaking the remaining default fallbacks", async () => {
    const editStep = makeStep("Edit", { file_path: "src/file.ts", old_string: "a", new_string: "b" });
    await mount({ step: editStep, mid: "m1", sid: "s1", cwd: "/panel/cwd" });
    act(() => rowButton().click());
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "changes",
      mid: "m1",
      sid: "s1",
      at: { path: "/panel/cwd/src/file.ts", call: "call-1" },
    });

    vi.clearAllMocks();
    mocks.getSession.mockReturnValue({ cwd: "/store/cwd" });
    mocks.useChatContext.mockReturnValue({ tasks: emptyTaskIndex(), scope: "s:" });
    mocks.useExpanded.mockReturnValue([false, vi.fn()]);
    const onOpenPanel = vi.fn();
    await mount({ step: editStep, mid: "m1", sid: "s1", onOpenPanel });
    act(() => rowButton().click());
    expect(mocks.getSession).toHaveBeenCalledWith("m1", "s1");
    expect(onOpenPanel).toHaveBeenCalledWith({
      kind: "changes",
      mid: "m1",
      sid: "s1",
      at: { path: "/store/cwd/src/file.ts", call: "call-1" },
    });
    expect(mocks.openPanel).not.toHaveBeenCalled();

    vi.clearAllMocks();
    const setOpen = vi.fn((updater: boolean | ((v: boolean) => boolean)) => (typeof updater === "function" ? updater(false) : updater));
    const onExpandedChange = vi.fn();
    let revealCb: (() => (() => void) | void) | undefined;
    mocks.useChatContext.mockReturnValue({ tasks: emptyTaskIndex(), scope: "s:" });
    mocks.useExpanded.mockReturnValue([false, setOpen]);
    mocks.useReveal.mockImplementation((_scope: string, _ids: readonly string[], open: () => (() => void) | void) => {
      revealCb = open;
      return false;
    });
    const cmdStep = makeStep("Bash", { command: "ls" });
    await mount({ step: cmdStep, mid: "m1", sid: "s1", onExpandedChange });
    act(() => rowButton().click());
    expect(setOpen).toHaveBeenCalledTimes(1);
    expect(onExpandedChange).toHaveBeenCalledWith(true);

    const undo = revealCb?.();
    expect(onExpandedChange).toHaveBeenLastCalledWith(true);
    undo?.();
    expect(onExpandedChange).toHaveBeenLastCalledWith(false);
  });
});

describe("ActionRow default compatibility", () => {
  it("uses ChatContext, expanded memory, search reveal, useFullText and Thumb when optional props are omitted", async () => {
    const tasks: TaskIndex = { ...emptyTaskIndex(), started: new Set(["call-1"]) };
    const setOpen = vi.fn();
    mocks.useChatContext.mockReturnValue({ tasks, scope: "chat-1:" });
    mocks.useExpanded.mockReturnValue([true, setOpen]);
    mocks.useFullText.mockReturnValue({ text: "default output", more: <span data-default-more />, getText: async () => "default output" });

    const img: Item = { id: "img-1", seq: 3, role: "user", kind: "image" };
    const step = makeStep("Bash", { command: "echo hi" }, { result: { text: "default output" }, images: [img] });
    await mount({ step, mid: "m-def", sid: "s-def" });

    expect(mocks.useChatContext).toHaveBeenCalled();
    expect(mocks.useExpanded).toHaveBeenCalledWith("chat-1:act:u1#0");
    expect(mocks.useReveal).toHaveBeenCalledWith("chat-1:", ["u1#0", "u1#0-r"], expect.any(Function));
    expect(mocks.useFullText).toHaveBeenCalledWith(step.result, "m-def", "s-def");
    expect(mocks.thumb).toHaveBeenCalledWith(expect.objectContaining({ item: img, mid: "m-def", sid: "s-def" }));
    expect(host.querySelector('[data-default-thumb="img-1"]')).not.toBeNull();
    expect(host.querySelector("[data-default-more]")).not.toBeNull();
    expect(host.textContent).toContain(t("lib-tasks.badge_running"));

    act(() => rowButton().click());
    expect(setOpen).toHaveBeenCalledWith(expect.any(Function));
  });

  it("reads getSession cwd and calls openPanel for default edit and read rows", async () => {
    mocks.getSession.mockReturnValue({ cwd: "/session/cwd" });
    const editStep = makeStep("Edit", { file_path: "src/index.ts", old_string: "a", new_string: "b" });
    await mount({ step: editStep, mid: "m-def", sid: "s-def" });
    act(() => rowButton().click());
    expect(mocks.getSession).toHaveBeenCalledWith("m-def", "s-def");
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "changes",
      mid: "m-def",
      sid: "s-def",
      at: { path: "/session/cwd/src/index.ts", call: "call-1" },
    });

    const readStep = makeStep("Read", { file_path: "/session/cwd/notes.md", offset: 7 });
    await mount({ step: readStep, mid: "m-def", sid: "s-def" });
    act(() => rowButton().click());
    expect(mocks.openPanel).toHaveBeenLastCalledWith({
      kind: "file",
      mid: "m-def",
      path: "/session/cwd/notes.md",
      cwd: "/session/cwd",
      line: 7,
    });
  });
});
