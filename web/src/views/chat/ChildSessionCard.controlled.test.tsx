// @vitest-environment jsdom
// Controlled panels supply child row, expansion, transcript detail and panel callbacks without touching default singletons; default callers still use every fallback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChildDetail } from "../../lib/child-detail";
import type { Session } from "../../lib/types";
import type { ChildSessionCardProps } from "./ChildSessionCard";

const mocks = vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  return {
    useChildRow: vi.fn(),
    useStore: vi.fn(),
    getSession: vi.fn(),
    useChatContext: vi.fn(),
    useExpanded: vi.fn(),
    readChildDetail: vi.fn(),
    openPanel: vi.fn(),
  };
});

vi.mock("../../lib/use-child-sessions", () => ({ useChildRow: mocks.useChildRow }));
vi.mock("../../lib/store", () => ({ useStore: mocks.useStore, getSession: mocks.getSession }));
vi.mock("./chat-context", () => ({ useChatContext: mocks.useChatContext }));
vi.mock("./expanded", () => ({ useExpanded: mocks.useExpanded }));
vi.mock("../../lib/child-detail", () => ({ readChildDetail: mocks.readChildDetail }));
vi.mock("../../lib/panel", () => ({ openPanel: mocks.openPanel }));

const { ChildSessionCard, childToolName } = await import("./ChildSessionCard");
const { childCardKey } = await import("../../lib/child-cards");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (seconds: number) => new Date(T0 + seconds * 1000).toISOString();

const makeChild = (extra: Partial<Session> = {}): Session => ({
  machine_id: "example-worker",
  session_id: "example-child",
  title: "Review token refresh middleware",
  cwd: "/workspace/example",
  status: "idle",
  state: "unread",
  agent: "codex",
  model: "gpt-5.5",
  effort: "high",
  started_at: at(0),
  updated_at: at(65),
  folded_under: "example-parent",
  last_text: "Fallback summary from the child row.",
  ...extra,
});

const makeDetail = (extra: Partial<ChildDetail> = {}): ChildDetail => ({
  prompt: "Check how session refresh handles retries.",
  result: { id: "a#0", seq: 4, role: "assistant", kind: "text", text: "Retries stay bounded and keep tokens in memory." },
  lastTool: "mcp__github__create_issue",
  ...extra,
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<ChildSessionCardProps> = {}) => {
  await act(async () => root.render(
    <ChildSessionCard mid="example-worker" sid="example-child" {...props} />,
  ));
};
const head = () => host.querySelector<HTMLButtonElement>("[data-child-card] button")!;
const viewFull = () => host.querySelector<HTMLButtonElement>("[data-subagent-view-full]");
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};
const expectNoDefaultSingletons = () => {
  expect(mocks.useChildRow).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.useChatContext).not.toHaveBeenCalled();
  expect(mocks.useExpanded).not.toHaveBeenCalled();
  expect(mocks.readChildDetail).not.toHaveBeenCalled();
  expect(mocks.openPanel).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.useChatContext.mockReturnValue({ lineStyle: undefined });
  mocks.useExpanded.mockReturnValue([false, vi.fn()]);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("ChildSessionCard with controlled props", () => {
  it("renders explicit child data and lineStyle without calling default singletons, and renders nothing for null", async () => {
    const child = makeChild({ state: "working", status: "working" });
    await mount({ child, lineStyle: "agents", expanded: false, detail: null, onExpandedChange: vi.fn(), onOpenFull: vi.fn() });

    expect(host.querySelector("[data-child-card]")?.getAttribute("data-child-card")).toBe("running");
    expect(host.querySelector("[data-child-card]")?.getAttribute("data-tool-use")).toBe("child:example-child");
    expect(host.textContent).toContain("Review token refresh middleware");
    expect(host.textContent).toContain("Codex");
    expect(host.querySelector("[data-task-model]")?.textContent).toBe("GPT-5.5 · High");
    expect(head().getAttribute("aria-expanded")).toBe("false");
    expectNoDefaultSingletons();

    await mount({ child, lineStyle: "assistant", expanded: false, detail: null, onExpandedChange: vi.fn(), onOpenFull: vi.fn() });
    expect(host.textContent).not.toContain("Codex");
    expectNoDefaultSingletons();

    await mount({ child: null, lineStyle: "agents", expanded: false, detail: null, onExpandedChange: vi.fn(), onOpenFull: vi.fn() });
    expect(host.innerHTML).toBe("");
    expectNoDefaultSingletons();
  });

  it("handles controlled expansion and explicit detail states without touching expanded memory or readChildDetail", async () => {
    const child = makeChild();
    const onExpandedChange = vi.fn();
    const onOpenFull = vi.fn();
    const loadDetail = vi.fn();

    await mount({ child, lineStyle: "agents", expanded: false, onExpandedChange, detail: null, loadDetail, onOpenFull });
    expect(host.querySelector("[data-subagent-details]")).toBeNull();
    act(() => head().click());
    expect(onExpandedChange).toHaveBeenCalledWith(true);

    await mount({ child, lineStyle: "agents", expanded: true, onExpandedChange, detail: null, loadDetail, onOpenFull });
    expect(host.querySelector("[data-child-details]")?.getAttribute("data-child-details")).toBe("reading");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Fallback summary from the child row.");
    expect(loadDetail).not.toHaveBeenCalled();

    const detail = makeDetail();
    await mount({ child, lineStyle: "agents", expanded: true, onExpandedChange, detail, loadDetail, onOpenFull });
    expect(host.querySelector("[data-child-details]")?.getAttribute("data-child-details")).toBe("read");
    expect(host.querySelector("[data-subagent-prompt]")?.textContent).toBe(detail.prompt);
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Retries stay bounded");
    expect(host.querySelector("[data-subagent-last-tool]")?.textContent).toBe(
      t("chat-subagentcard.last_tool", { tool: childToolName(detail.lastTool!) }),
    );
    expect(loadDetail).not.toHaveBeenCalled();

    act(() => head().click());
    expect(onExpandedChange).toHaveBeenLastCalledWith(false);
    expectNoDefaultSingletons();
  });

  it("loads transcript details lazily with a custom loadDetail callback, preserves details on failure and aborts on unmount", async () => {
    const child = makeChild({ state: "working", status: "working" });
    let resolveFirst!: (value: ChildDetail) => void;
    const signals: AbortSignal[] = [];
    const loadDetail = vi.fn((_mid: string, _sid: string, signal?: AbortSignal) => {
      if (signal) signals.push(signal);
      if (signals.length === 1) return new Promise<ChildDetail>((resolve) => { resolveFirst = resolve; });
      if (signals.length === 2) return Promise.reject(new Error("panel offline"));
      return new Promise<ChildDetail>(() => {});
    });

    await mount({ child, lineStyle: "agents", expanded: false, onExpandedChange: vi.fn(), loadDetail, onOpenFull: vi.fn() });
    expect(loadDetail).not.toHaveBeenCalled();

    await mount({ child, lineStyle: "agents", expanded: true, onExpandedChange: vi.fn(), loadDetail, onOpenFull: vi.fn() });
    expect(loadDetail).toHaveBeenCalledTimes(1);
    expect(loadDetail).toHaveBeenCalledWith("example-worker", "example-child", expect.any(AbortSignal));
    expect(host.querySelector("[data-child-details]")?.getAttribute("data-child-details")).toBe("reading");

    await act(async () => {
      resolveFirst(makeDetail());
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("[data-child-details]")?.getAttribute("data-child-details")).toBe("read");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Retries stay bounded");

    // Re-rendering with a fresh inline loader wrapper does not abort or refetch while rev and running are unchanged.
    await mount({
      child,
      lineStyle: "agents",
      expanded: true,
      onExpandedChange: vi.fn(),
      loadDetail: (m, s, sig) => loadDetail(m, s, sig),
      onOpenFull: vi.fn(),
    });
    expect(loadDetail).toHaveBeenCalledTimes(1);
    expect(signals[0].aborted).toBe(false);

    // Stopping triggers a final read; failure retains the last loaded detail.
    const finished = makeChild({ state: "unread", status: "idle", updated_at: at(90) });
    await mount({ child: finished, lineStyle: "agents", expanded: true, onExpandedChange: vi.fn(), loadDetail, onOpenFull: vi.fn() });
    await flush();
    expect(loadDetail).toHaveBeenCalledTimes(2);
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Retries stay bounded");

    // Another revision starts a pending read whose AbortSignal aborts on unmount.
    await mount({ child: { ...finished, updated_at: at(120) }, lineStyle: "agents", expanded: true, onExpandedChange: vi.fn(), loadDetail, onOpenFull: vi.fn() });
    expect(loadDetail).toHaveBeenCalledTimes(3);
    expect(signals[2].aborted).toBe(false);
    act(() => root.unmount());
    expect(signals[2].aborted).toBe(true);
    root = createRoot(host);
    expectNoDefaultSingletons();
  });

  it("routes View full through onOpenFull for explicit and derived cross-machine parent targets", async () => {
    const onOpenFull = vi.fn();
    const child = makeChild({
      machine_id: "remote-box",
      session_id: "remote-child",
      folded_under_machine_id: "origin-host",
      folded_under: "origin-parent",
    });

    await mount({
      mid: "remote-box",
      sid: "remote-child",
      parentMid: "explicit-host",
      parentSid: "explicit-parent",
      child,
      lineStyle: "agents",
      expanded: true,
      detail: makeDetail(),
      onExpandedChange: vi.fn(),
      onOpenFull,
    });
    expect(host.querySelector("[data-child-card]")?.getAttribute("data-tool-use")).toBe("child:remote-box/remote-child");
    act(() => viewFull()!.click());
    expect(onOpenFull).toHaveBeenCalledWith({
      kind: "subagent",
      mid: "remote-box",
      sid: "remote-child",
      parentMid: "explicit-host",
      parentSid: "explicit-parent",
      from: "card",
    });

    await mount({
      mid: "remote-box",
      sid: "remote-child",
      child,
      lineStyle: "agents",
      expanded: true,
      detail: makeDetail(),
      onExpandedChange: vi.fn(),
      onOpenFull,
    });
    act(() => viewFull()!.click());
    expect(onOpenFull).toHaveBeenLastCalledWith({
      kind: "subagent",
      mid: "remote-box",
      sid: "remote-child",
      parentMid: "origin-host",
      parentSid: "origin-parent",
      from: "card",
    });
    expectNoDefaultSingletons();
  });
});

describe("ChildSessionCard default compatibility", () => {
  it("prefers held rows over store lookups and keeps default context and expansion wiring", async () => {
    const held = makeChild({ title: "Held row title" });
    const setOpen = vi.fn();
    mocks.useChildRow.mockReturnValue(held);
    mocks.useStore.mockImplementation((selector: () => unknown) => selector());
    mocks.useExpanded.mockReturnValue([false, setOpen]);

    await mount({ scope: "t:" });

    expect(mocks.useChildRow).toHaveBeenCalledWith("example-worker", "example-child");
    expect(mocks.useStore).toHaveBeenCalledWith(expect.any(Function));
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.useChatContext).toHaveBeenCalled();
    expect(mocks.useExpanded).toHaveBeenCalledWith(childCardKey("t:", "example-child"));
    expect(host.textContent).toContain("Held row title");

    act(() => head().click());
    expect(setOpen).toHaveBeenCalledWith(expect.any(Function));
    expect(mocks.readChildDetail).not.toHaveBeenCalled();
  });

  it("falls back to store lookup, assistant lineStyle, readChildDetail and openPanel when controlled props are absent", async () => {
    const stored = makeChild({
      machine_id: "remote-box",
      session_id: "remote-child",
      folded_under_machine_id: "origin-host",
      folded_under: "origin-parent",
    });
    mocks.useChildRow.mockReturnValue(undefined);
    mocks.useStore.mockImplementation((selector: () => unknown) => selector());
    mocks.getSession.mockReturnValue(stored);
    mocks.useChatContext.mockReturnValue({ lineStyle: "assistant" });
    mocks.useExpanded.mockReturnValue([true, vi.fn()]);
    mocks.readChildDetail.mockResolvedValue(makeDetail());

    await mount({ mid: "remote-box", sid: "remote-child", parentMid: "origin-host", parentSid: "origin-parent", scope: "t:" });
    await flush();

    expect(mocks.getSession).toHaveBeenCalledWith("remote-box", "remote-child");
    expect(mocks.useExpanded).toHaveBeenCalledWith(childCardKey("t:", "remote-child", "remote-box"));
    expect(host.textContent).not.toContain("Codex");
    expect(mocks.readChildDetail).toHaveBeenCalledWith("remote-box", "remote-child", expect.any(AbortSignal));
    expect(host.querySelector("[data-subagent-prompt]")?.textContent).toBe("Check how session refresh handles retries.");

    act(() => viewFull()!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "subagent",
      mid: "remote-box",
      sid: "remote-child",
      parentMid: "origin-host",
      parentSid: "origin-parent",
      from: "card",
    });
  });
});
