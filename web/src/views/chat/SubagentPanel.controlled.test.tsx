// @vitest-environment jsdom
// Controlled panels supply child/parent rows, machines, interactions, transcript items, actions and chat body without touching default singletons; default callers keep every fallback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Interaction, Item, Machine, Session } from "../../lib/types";
import type { SubagentPanelProps } from "./SubagentPanel";

const mocks = vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia ??= () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver ??= class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  return {
    useChildRow: vi.fn(),
    useStore: vi.fn(),
    getSession: vi.fn(),
    useSidePanelBack: vi.fn(),
    useSharedTranscript: vi.fn(),
    useBackgroundChat: vi.fn(),
    apiOp: vi.fn(),
    stopMessageFeedback: vi.fn(),
    openExpanded: vi.fn(),
    openFromPage: vi.fn(),
    toast: vi.fn(),
    chatView: vi.fn(),
  };
});

vi.mock("../../lib/use-child-sessions", () => ({ useChildRow: mocks.useChildRow }));
vi.mock("../../lib/store", () => ({ useStore: mocks.useStore, getSession: mocks.getSession }));
vi.mock("../../lib/panel", () => ({ useSidePanelBack: mocks.useSidePanelBack }));
vi.mock("./transcript-share", () => ({ useSharedTranscript: mocks.useSharedTranscript }));
vi.mock("../../lib/background-panel", () => ({ useBackgroundChat: mocks.useBackgroundChat }));
vi.mock("../../lib/ops", () => ({ apiOp: mocks.apiOp }));
vi.mock("../../lib/message-send", () => ({ stopMessageFeedback: mocks.stopMessageFeedback }));
vi.mock("./expanded", () => ({ openExpanded: mocks.openExpanded }));
vi.mock("../../lib/handoff", () => ({ openFromPage: mocks.openFromPage }));
vi.mock("../../ui/atoms", async (orig) => {
  const actual = await orig<typeof import("../../ui/atoms")>();
  return { ...actual, toast: mocks.toast };
});
vi.mock("./ChatView", () => ({
  ChatView: (props: { mid: string; sid: string; embedded?: boolean }) => {
    mocks.chatView(props);
    return <div data-default-chat-view={`${props.mid}/${props.sid}`} />;
  },
}));

const { SubagentPanel } = await import("./SubagentPanel");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [
  { id: "host-a", name: "Host A", online: true },
  { id: "host-b", name: "Host B", online: true },
];

const parentSession: Session = {
  machine_id: "host-a",
  session_id: "parent-1",
  title: "Parent session",
  cwd: "/workspace/example",
  agent: "claude",
  status: "working",
  state: "working",
  subagents: [
    { id: "sa-1", session_id: "parent-1@sa-1", description: "Embedded check", running: false, tool_use_id: "call-emb" },
  ],
};

const makeChild = (mid: string, sid: string, title: string, extra: Partial<Session> = {}): Session => ({
  machine_id: mid,
  session_id: sid,
  title,
  cwd: "/workspace/example",
  agent: "codex",
  status: "working",
  state: "working",
  folded_under: "parent-1",
  folded_under_machine_id: "host-a",
  is_subagent: true,
  capabilities: { read: true, send: true, interrupt: true },
  ...extra,
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<SubagentPanelProps> = {}) => {
  await act(async () => {
    root.render(
      <SubagentPanel
        mid="host-b"
        sid="child-1"
        parentMid="host-a"
        parentSid="parent-1"
        onClose={vi.fn()}
        {...props}
      />,
    );
  });
};

const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const stopButton = () => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(t("chat-sessionoverview.stop")));
const moreButton = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-chatview.more_actions")}"]`);

const expectNoDefaultSingletons = () => {
  expect(mocks.useChildRow).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.useSidePanelBack).not.toHaveBeenCalled();
  expect(mocks.useSharedTranscript).not.toHaveBeenCalled();
  expect(mocks.useBackgroundChat).not.toHaveBeenCalled();
  expect(mocks.apiOp).not.toHaveBeenCalled();
  expect(mocks.stopMessageFeedback).not.toHaveBeenCalled();
  expect(mocks.openExpanded).not.toHaveBeenCalled();
  expect(mocks.openFromPage).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
  expect(mocks.chatView).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.useChildRow.mockReturnValue(undefined);
  mocks.useStore.mockImplementation((selector: (s: { machines: Machine[]; interactions: Interaction[] }) => unknown) =>
    selector({ machines, interactions: [] }),
  );
  mocks.getSession.mockReturnValue(undefined);
  mocks.useSidePanelBack.mockReturnValue(undefined);
  mocks.useSharedTranscript.mockReturnValue(null);
  mocks.useBackgroundChat.mockReturnValue(undefined);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("SubagentPanel with controlled props", () => {
  it("renders controlled data, actions, back button, menu items and custom chat renderer without calling default singletons", async () => {
    const onClose = vi.fn();
    const onBack = vi.fn();
    const onShowInChat = vi.fn();
    const onOpenAsPage = vi.fn();
    const onStop = vi.fn(async () => {});
    const onNotify = vi.fn();
    const renderChat = vi.fn(({ mid, sid, embedded }) => (
      <div data-custom-subagent-chat={`${mid}/${sid}/${String(embedded)}`} />
    ));

    await mount({
      child: makeChild("host-b", "child-1", "Run integration checks"),
      parent: parentSession,
      machines,
      interactions: [],
      parentItems: [],
      call: "call-remote",
      from: "card",
      onClose,
      onBack,
      canShowInChat: true,
      onShowInChat,
      onOpenAsPage,
      onStop,
      onNotify,
      renderChat,
    });

    expect(host.querySelector("h2")?.textContent).toBe("Run integration checks");
    expect(host.querySelector("[data-subagent-provenance]")?.textContent).toBe("Host B · Codex");
    expect(host.querySelector('[data-custom-subagent-chat="host-b/child-1/true"]')).not.toBeNull();
    expect(renderChat).toHaveBeenCalledWith({ mid: "host-b", sid: "child-1", embedded: true });

    // Back action delegates to onBack when not from overview.
    const backBtn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("sidepanel.back")}"]`)!;
    act(() => backBtn.click());
    expect(onBack).toHaveBeenCalledTimes(1);

    // Stop action delegates to onStop and notifies via onNotify.
    await act(async () => {
      stopButton()!.click();
      await Promise.resolve();
    });
    expect(onStop).toHaveBeenCalledWith({ mid: "host-b", sid: "child-1" });
    expect(onNotify).toHaveBeenCalledWith(t("chat-chatview.interrupted"), undefined);

    // Menu delegates Show in chat and Open as page to controlled callbacks.
    act(() => moreButton()!.click());
    const menuItems = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    expect(menuItems).toHaveLength(2);
    act(() => menuItems[0]!.click());
    expect(onShowInChat).toHaveBeenCalledWith("call-remote");

    act(() => moreButton()!.click());
    const menuItems2 = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    act(() => menuItems2[1]!.click());
    expect(onOpenAsPage).toHaveBeenCalledWith({ mid: "host-b", sid: "child-1" });
    expect(onClose).toHaveBeenCalledTimes(1);

    // Passing null hides Stop and omits the menu button when all menu items are disabled.
    await mount({
      child: null,
      parent: parentSession,
      machines,
      interactions: [],
      parentItems: [
        { id: "r1#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "call-emb", is_error: true, text: "boom" } as Item,
      ],
      mid: "host-a",
      sid: "parent-1@sa-1",
      from: "overview",
      onClose,
      onShowInChat: null,
      onOpenAsPage: null,
      onStop: null,
      onNotify,
      renderChat,
    });
    expect(host.querySelector("[data-subagent-panel-status]")?.getAttribute("data-subagent-panel-status")).toBe("failed");
    expect(stopButton()).toBeUndefined();
    expect(moreButton()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("loads child sessions via loadChild with AbortSignal, session-match guard, and ABA race protection", async () => {
    let resolveA1!: (s: Session | null) => void;
    let resolveB!: (s: Session | null) => void;
    let resolveA2!: (s: Session | null) => void;
    const signals: AbortSignal[] = [];
    const loadChild = vi.fn((_mid: string, _sid: string, signal?: AbortSignal) => {
      if (signal) signals.push(signal);
      const n = signals.length;
      if (n === 1) return new Promise<Session | null>((resolve) => { resolveA1 = resolve; });
      if (n === 2) return new Promise<Session | null>((resolve) => { resolveB = resolve; });
      if (n === 3) return new Promise<Session | null>((resolve) => { resolveA2 = resolve; });
      return Promise.resolve(makeChild("host-b", "wrong-sid", "Mismatched child"));
    });

    const common: Partial<SubagentPanelProps> = {
      parent: parentSession,
      machines,
      interactions: [],
      parentItems: [],
      from: "overview",
      onShowInChat: null,
      onOpenAsPage: null,
      onStop: vi.fn(),
      onNotify: vi.fn(),
      renderChat: ({ mid, sid }) => <div data-body={`${mid}/${sid}`} />,
      loadChild,
    };

    // Start on A -> request #1 pending.
    await mount({ ...common, mid: "host-b", sid: "child-a" });
    expect(loadChild).toHaveBeenCalledTimes(1);
    expect(signals[0].aborted).toBe(false);

    // Switch to B -> request #1 aborted, request #2 starts.
    await mount({ ...common, mid: "host-b", sid: "child-b" });
    expect(signals[0].aborted).toBe(true);
    expect(loadChild).toHaveBeenCalledTimes(2);

    // ABA: switch back to A -> request #2 aborted, request #3 starts.
    await mount({ ...common, mid: "host-b", sid: "child-a" });
    expect(signals[1].aborted).toBe(true);
    expect(loadChild).toHaveBeenCalledTimes(3);

    // Stale A1 and B responses settle late -> must be ignored.
    await act(async () => {
      resolveA1(makeChild("host-b", "child-a", "Stale A1 title"));
      resolveB(makeChild("host-b", "child-b", "Stale B title"));
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("h2")?.textContent).toBe("child-a");

    // Fresh A2 resolves -> rendered.
    await act(async () => {
      resolveA2(makeChild("host-b", "child-a", "Fresh A2 title"));
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector("h2")?.textContent).toBe("Fresh A2 title");

    // Mismatched (machine_id, session_id) payload returned by loader is rejected.
    await mount({ ...common, mid: "host-b", sid: "child-c" });
    await flush();
    expect(host.querySelector("h2")?.textContent).toBe("child-c");
    expectNoDefaultSingletons();
  });

  it("isolates in-flight stopSubagent across session switches and ABA transitions without leaking busy or notifications", async () => {
    let rejectA1!: (err: Error) => void;
    let resolveA2!: () => void;
    const onStop = vi.fn(() => {
      if (onStop.mock.calls.length === 1) return new Promise<void>((_res, rej) => { rejectA1 = rej; });
      return new Promise<void>((res) => { resolveA2 = res; });
    });
    const onNotify = vi.fn();
    const childA = makeChild("host-b", "child-a", "Child A");
    const childB = makeChild("host-b", "child-b", "Child B");
    const common: Partial<SubagentPanelProps> = {
      parent: parentSession,
      machines,
      interactions: [],
      parentItems: [],
      from: "overview",
      onShowInChat: null,
      onOpenAsPage: null,
      onStop,
      onNotify,
      renderChat: ({ mid, sid }) => <div data-body={`${mid}/${sid}`} />,
    };

    await mount({ ...common, mid: "host-b", sid: "child-a", child: childA });
    act(() => stopButton()!.click());
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(stopButton()!.disabled).toBe(true);

    // Switch to child B while child A stop is in flight: child B is NOT busy/disabled.
    await mount({ ...common, mid: "host-b", sid: "child-b", child: childB });
    expect(stopButton()!.disabled).toBe(false);

    // ABA: switch back to child A -> fresh state, NOT permanently locked; start stop #2.
    await mount({ ...common, mid: "host-b", sid: "child-a", child: childA });
    expect(stopButton()!.disabled).toBe(false);
    act(() => stopButton()!.click());
    expect(onStop).toHaveBeenCalledTimes(2);
    expect(stopButton()!.disabled).toBe(true);

    // Stale A1 rejects late: must NOT notify or clear A2's busy lock.
    await act(async () => {
      rejectA1(new Error("stale timeout"));
      await Promise.resolve();
    });
    await flush();
    expect(onNotify).not.toHaveBeenCalled();
    expect(stopButton()!.disabled).toBe(true);

    // Active A2 resolves: notifies once and unlocks Stop.
    await act(async () => {
      resolveA2();
      await Promise.resolve();
    });
    await flush();
    expect(onNotify).toHaveBeenCalledTimes(1);
    expect(onNotify).toHaveBeenCalledWith(t("chat-chatview.interrupted"), undefined);
    expect(stopButton()!.disabled).toBe(false);
    expectNoDefaultSingletons();
  });
});

describe("SubagentPanel default compatibility", () => {
  it("falls back to store, transcript, side-panel back, background chat, apiOp, openFromPage and ChatView when controlled props are omitted", async () => {
    const child = makeChild("host-b", "child-1", "Default stored child");
    const backFn = vi.fn();
    const jumpToCall = vi.fn();
    const onClose = vi.fn();

    mocks.useChildRow.mockReturnValue(undefined);
    mocks.getSession.mockImplementation((mid: string, sid: string) => {
      if (mid === "host-b" && sid === "child-1") return child;
      if (mid === "host-a" && sid === "parent-1") return parentSession;
      return undefined;
    });
    mocks.useSidePanelBack.mockReturnValue(backFn);
    mocks.useSharedTranscript.mockReturnValue({ items: [] });
    mocks.useBackgroundChat.mockReturnValue({ stopLoop: vi.fn(), jumpToCall, hasCall: () => true });
    mocks.apiOp.mockResolvedValue({ state: "ok" });

    await mount({ call: "call-1", from: "card", onClose });

    expect(mocks.useChildRow).toHaveBeenCalledWith("host-b", "child-1");
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.getSession).toHaveBeenCalledWith("host-b", "child-1");
    expect(mocks.getSession).toHaveBeenCalledWith("host-a", "parent-1");
    expect(mocks.useSharedTranscript).toHaveBeenCalledWith("host-a", "parent-1");
    expect(mocks.useSidePanelBack).toHaveBeenCalled();
    expect(mocks.useBackgroundChat).toHaveBeenCalledWith("host-a", "parent-1");
    expect(mocks.chatView).toHaveBeenCalledWith({ mid: "host-b", sid: "child-1", embedded: true });

    const backBtn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("sidepanel.back")}"]`)!;
    act(() => backBtn.click());
    expect(backFn).toHaveBeenCalledTimes(1);

    await act(async () => {
      stopButton()!.click();
      await Promise.resolve();
    });
    expect(mocks.apiOp).toHaveBeenCalledWith(expect.stringContaining("/sessions/host-b/child-1/interrupt"), expect.objectContaining({ method: "POST" }));
    expect(mocks.toast).toHaveBeenCalledWith(t("chat-chatview.interrupted"), undefined);
    expect(mocks.stopMessageFeedback).toHaveBeenCalledWith("host-b", "child-1");

    act(() => moreButton()!.click());
    const menuItems = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    act(() => menuItems[0]!.click());
    expect(mocks.openExpanded).toHaveBeenCalledWith(["host-a/parent-1:task:call-1"]);
    expect(jumpToCall).toHaveBeenCalledWith("call-1");

    act(() => moreButton()!.click());
    const menuItems2 = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    act(() => menuItems2[1]!.click());
    expect(mocks.openFromPage).toHaveBeenCalledWith({ name: "session", mid: "host-b", sid: "child-1", standalone: true });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
