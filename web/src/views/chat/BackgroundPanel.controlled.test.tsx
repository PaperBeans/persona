// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(),
  getSession: vi.fn(),
  credentialOwner: vi.fn(),
  subscribeCredentialOwner: vi.fn(),
  useBackgroundChat: vi.fn(),
  openPanel: vi.fn(),
  copyText: vi.fn(),
  commandDetail: vi.fn(),
}));

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  window.matchMedia = ((query: string) => ({
    matches: query.includes("reduce"),
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

vi.mock("../../lib/store", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...actual,
    useStore: (selector: () => unknown) => mocks.useStore(selector),
    getSession: (mid: string, sid: string) => mocks.getSession(mid, sid),
  };
});

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    credentialOwner: () => mocks.credentialOwner(),
    subscribeCredentialOwner: (listener: () => void) => mocks.subscribeCredentialOwner(listener),
  };
});

vi.mock("../../lib/background-panel", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/background-panel")>();
  return {
    ...actual,
    useBackgroundChat: (mid: string, sid: string) => mocks.useBackgroundChat(mid, sid),
  };
});

vi.mock("../../lib/panel", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/panel")>();
  return {
    ...actual,
    openPanel: (panel: unknown, options?: unknown) => mocks.openPanel(panel, options),
  };
});

vi.mock("../../ui/atoms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../ui/atoms")>();
  return {
    ...actual,
    copyText: (text: string) => mocks.copyText(text),
  };
});

vi.mock("./BackgroundCommandDetail", () => ({
  BackgroundCommandDetail: (props: {
    task: { id: string };
    running: boolean;
    onBack: () => void;
    onOpenFile: () => void;
  }) => {
    mocks.commandDetail(props);
    return (
      <div data-default-command-detail={props.task.id} data-running={String(props.running)}>
        <button type="button" data-default-detail-back onClick={props.onBack}>back</button>
        <button type="button" data-default-detail-open-file onClick={props.onOpenFile}>open</button>
      </div>
    );
  },
}));

import { t } from "../../i18n";
import type { BackgroundChat, BackgroundPanel as BackgroundPanelObject } from "../../lib/background-panel";
import type { BackgroundTask, LoopInfo, Session, Workflow } from "../../lib/types";
import { BackgroundList, BackgroundPanel, type BackgroundCommandDetailViewProps, type BackgroundPanelProps } from "./BackgroundPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (seconds: number) => new Date(T0 + seconds * 1000).toISOString();

const sampleLoop: LoopInfo = {
  kind: "interval",
  prompt: "/loop 15m check pipeline",
  interval_seconds: 900,
  next_at: at(300),
};

const sampleWorkflow: Workflow = {
  task_id: "wf-1",
  name: "Verify build",
  summary: "Check build outputs",
  status: "running",
  running: true,
  tool_use_id: "call-wf-1",
  phases: ["Verify"],
  phase: "Verify",
  phase_stats: [{ title: "Verify", started: 2, done: 1, running: 1 }],
  agents_started: 2,
  agents_done: 1,
  agents_running: 1,
  started_at: at(-120),
  agents: [
    { id: "wf-1-a1", session_id: "s@wf-1-a1", label: "verify:1", phase: "Verify", running: false, status: "done", started_at: at(-100), ended_at: at(-50) },
  ],
};

const sampleTask: BackgroundTask = {
  id: "cmd-1",
  kind: "shell",
  description: "Run checks",
  command: "npm run check",
  output_file: "/tmp/example-output.log",
  started_at: at(-45),
};

const makeSession = (over: Partial<Session> = {}): Session => ({
  machine_id: "example-machine",
  session_id: "example-session",
  cwd: "/example/work",
  manager: "manager-a",
  status: "working",
  state: "working",
  loop: sampleLoop,
  workflows: [sampleWorkflow],
  background: [sampleTask],
  ...over,
} as Session);

let host: HTMLDivElement;
let root: Root;
const onClose = vi.fn();

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
  vi.setSystemTime(T0);
  vi.clearAllMocks();
  mocks.useStore.mockImplementation((selector: () => unknown) => selector());
  mocks.subscribeCredentialOwner.mockImplementation(() => () => {});
  mocks.credentialOwner.mockReturnValue("default-owner");
  mocks.copyText.mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

const defaultOpened = (over: Partial<BackgroundPanelObject> = {}): BackgroundPanelObject => ({
  kind: "background",
  mid: "example-machine",
  sid: "example-session",
  ...over,
});

const renderPanel = (props: Partial<BackgroundPanelProps> = {}) => {
  act(() => {
    root.render(
      <BackgroundPanel
        mid="example-machine"
        sid="example-session"
        opened={props.opened ?? defaultOpened()}
        onClose={props.onClose ?? onClose}
        {...props}
      />,
    );
  });
};

const q = (selector: string) => host.querySelector<HTMLElement>(selector);

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
  expect(mocks.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(mocks.useBackgroundChat).not.toHaveBeenCalled();
  expect(mocks.openPanel).not.toHaveBeenCalled();
  expect(mocks.copyText).not.toHaveBeenCalled();
  expect(mocks.commandDetail).not.toHaveBeenCalled();
};

describe("BackgroundPanel controlled mode", () => {
  it("runs list, loop stop, workflow jump, copy, and custom command detail without default singletons", () => {
    const session = makeSession();
    const stopLoop = vi.fn();
    const jumpToCall = vi.fn();
    const onOpenPanel = vi.fn();
    const onCopyCommand = vi.fn();
    const chat: BackgroundChat = { stopLoop, jumpToCall };
    const seenDetails: BackgroundCommandDetailViewProps[] = [];

    renderPanel({
      session,
      owner: "controlled-owner",
      chat,
      onOpenPanel,
      onCopyCommand,
      renderCommandDetail: (detail) => {
        seenDetails.push(detail);
        return (
          <div data-controlled-command-detail={detail.task.id}>
            <button type="button" data-controlled-open-file onClick={detail.onOpenFile}>open</button>
            <button type="button" data-controlled-back onClick={detail.onBack}>back</button>
          </div>
        );
      },
    });

    // Copy command through controlled callback
    const copyButton = q(`[data-bg-task="cmd-1"] button[aria-label="${t("chat-activity.copy_command")}"]`)!;
    act(() => copyButton.click());
    expect(onCopyCommand).toHaveBeenCalledWith("npm run check");

    // Loop stop confirmation calls controlled chat.stopLoop
    act(() => q(`[data-bg-loop] button[aria-label="${t("chat-activity.stop_loop")}"]`)!.click());
    const confirmStop = [...q("[data-bg-loop-confirm]")!.querySelectorAll("button")]
      .find((b) => b.textContent === t("chat-activity.stop_loop"))!;
    act(() => confirmStop.click());
    expect(stopLoop).toHaveBeenCalledTimes(1);

    // Workflow detail and jump use controlled chat.jumpToCall
    act(() => q('[data-bg-run="wf-1"] [data-bg-row-button]')!.click());
    expect(q('[data-background-detail="wf-1"]')).not.toBeNull();
    act(() => q("[data-workflow-jump]")!.click());
    expect(jumpToCall).toHaveBeenCalledWith("call-wf-1");

    // Back from workflow returns to list and notifies onOpenPanel
    act(() => q(`[data-background-detail="wf-1"] button[aria-label="${t("chat-activity.back")}"]`)!.click());
    expect(onOpenPanel).toHaveBeenCalledWith({ kind: "background", mid: "example-machine", sid: "example-session" });

    // Open command task uses renderCommandDetail and controlled file-open + back navigation
    act(() => q('[data-bg-task="cmd-1"] [data-bg-row-button]')!.click());
    expect(onOpenPanel).toHaveBeenLastCalledWith({ kind: "background", mid: "example-machine", sid: "example-session", task: "cmd-1" });
    expect(q('[data-controlled-command-detail="cmd-1"]')).not.toBeNull();
    expect(seenDetails.at(-1)?.cwd).toBe("/example/work");
    expect(seenDetails.at(-1)?.running).toBe(true);

    act(() => q("[data-controlled-open-file]")!.click());
    expect(onOpenPanel).toHaveBeenLastCalledWith({ kind: "file", mid: "example-machine", path: "/tmp/example-output.log", cwd: "/example/work" });

    act(() => q("[data-controlled-back]")!.click());
    expect(q("[data-background-list]")).not.toBeNull();
    expect(onOpenPanel).toHaveBeenLastCalledWith({ kind: "background", mid: "example-machine", sid: "example-session" });
    expectNoDefaultSingletons();
  });

  it("enforces controlled fencing, null chat, and owner/manager retirement without default singletons", () => {
    let liveOwner = "controlled-owner";
    let liveSession: Session | undefined = makeSession();
    const onOpenPanel = vi.fn();
    const onCopyCommand = vi.fn();
    const renderCommandDetail = (detail: BackgroundCommandDetailViewProps) => (
      <div data-controlled-command-detail={detail.task.id}>
        <button type="button" data-controlled-open-file onClick={detail.onOpenFile}>open</button>
      </div>
    );

    renderPanel({
      session: liveSession,
      owner: liveOwner,
      getCurrentOwner: () => liveOwner,
      getCurrentSession: () => liveSession,
      chat: null,
      onOpenPanel,
      onCopyCommand,
      renderCommandDetail,
    });

    // chat: null explicitly hides Stop without consulting useBackgroundChat
    expect(q(`[data-bg-loop] button[aria-label="${t("chat-activity.stop_loop")}"]`)).toBeNull();

    // Stale owner blocks copy and openTask before React commits
    liveOwner = "rotated-owner";
    act(() => q(`[data-bg-task="cmd-1"] button[aria-label="${t("chat-activity.copy_command")}"]`)!.click());
    act(() => q('[data-bg-task="cmd-1"] [data-bg-row-button]')!.click());
    expect(onCopyCommand).not.toHaveBeenCalled();
    expect(onOpenPanel).not.toHaveBeenCalled();
    expect(q("[data-controlled-command-detail]")).toBeNull();

    // Restore live owner, open overview-originated task, then rotate manager to retire it
    liveOwner = "controlled-owner";
    const overviewOpened = defaultOpened({ task: "cmd-1", from: "overview" });
    renderPanel({
      opened: overviewOpened,
      session: liveSession,
      owner: liveOwner,
      getCurrentOwner: () => liveOwner,
      getCurrentSession: () => liveSession,
      chat: null,
      onOpenPanel,
      onCopyCommand,
      renderCommandDetail,
    });
    expect(q('[data-controlled-command-detail="cmd-1"]')).not.toBeNull();

    // Action-time fence blocks openFile when live session manager drifts
    liveSession = makeSession({ manager: "manager-b" });
    act(() => q("[data-controlled-open-file]")!.click());
    expect(onOpenPanel).not.toHaveBeenCalled();

    // Committing the drifted manager with the same opened ask retires the detail and clears overview origin
    renderPanel({
      opened: overviewOpened,
      session: liveSession,
      owner: liveOwner,
      getCurrentOwner: () => liveOwner,
      getCurrentSession: () => liveSession,
      chat: null,
      onOpenPanel,
      onCopyCommand,
      renderCommandDetail,
    });
    expect(q("[data-controlled-command-detail]")).toBeNull();
    expect(onOpenPanel).toHaveBeenCalledWith({ kind: "background", mid: "example-machine", sid: "example-session" });

    // Changing controlled owner closes the panel
    renderPanel({
      session: liveSession,
      owner: "rotated-owner",
      getCurrentOwner: () => "rotated-owner",
      getCurrentSession: () => liveSession,
      chat: null,
      onOpenPanel,
      onCopyCommand,
      renderCommandDetail,
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(q("[data-background-panel]")).toBeNull();
    expectNoDefaultSingletons();
  });

  it("supports standalone controlled BackgroundList without reading default store or registry", () => {
    const stopLoop = vi.fn();
    const onOpenRun = vi.fn();
    const onOpenTask = vi.fn();
    const onCopyCommand = vi.fn();
    const session = makeSession();

    act(() => {
      root.render(
        <BackgroundList
          session={session}
          mid="example-machine"
          sid="example-session"
          owner="list-owner"
          chat={{ stopLoop }}
          onCopyCommand={onCopyCommand}
          onOpenRun={onOpenRun}
          onOpenTask={onOpenTask}
        />,
      );
    });

    act(() => q(`[data-bg-task="cmd-1"] button[aria-label="${t("chat-activity.copy_command")}"]`)!.click());
    expect(onCopyCommand).toHaveBeenCalledWith("npm run check");

    act(() => q(`[data-bg-loop] button[aria-label="${t("chat-activity.stop_loop")}"]`)!.click());
    const confirmStop = [...q("[data-bg-loop-confirm]")!.querySelectorAll("button")]
      .find((b) => b.textContent === t("chat-activity.stop_loop"))!;
    act(() => confirmStop.click());
    expect(stopLoop).toHaveBeenCalledTimes(1);

    act(() => q('[data-bg-run="wf-1"] [data-bg-row-button]')!.click());
    expect(onOpenRun).toHaveBeenCalledWith("wf-1");

    act(() => q('[data-bg-task="cmd-1"] [data-bg-row-button]')!.click());
    expect(onOpenTask).toHaveBeenCalledWith(sampleTask);
    expectNoDefaultSingletons();
  });
});

describe("BackgroundPanel default compatibility", () => {
  it("falls back to default store, credential owner, chat registry, panel navigation, copyText, and command detail", () => {
    const session = makeSession();
    const stopLoop = vi.fn();
    const jumpToCall = vi.fn();
    mocks.getSession.mockReturnValue(session);
    mocks.useBackgroundChat.mockReturnValue({ stopLoop, jumpToCall });

    renderPanel();

    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.getSession).toHaveBeenCalledWith("example-machine", "example-session");
    expect(mocks.subscribeCredentialOwner).toHaveBeenCalled();
    expect(mocks.credentialOwner).toHaveBeenCalled();
    expect(mocks.useBackgroundChat).toHaveBeenCalledWith("example-machine", "example-session");

    act(() => q(`[data-bg-task="cmd-1"] button[aria-label="${t("chat-activity.copy_command")}"]`)!.click());
    expect(mocks.copyText).toHaveBeenCalledWith("npm run check");

    act(() => q('[data-bg-run="wf-1"] [data-bg-row-button]')!.click());
    act(() => q("[data-workflow-jump]")!.click());
    expect(jumpToCall).toHaveBeenCalledWith("call-wf-1");
    act(() => q(`[data-background-detail="wf-1"] button[aria-label="${t("chat-activity.back")}"]`)!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "background", mid: "example-machine", sid: "example-session" }, undefined);

    act(() => q('[data-bg-task="cmd-1"] [data-bg-row-button]')!.click());
    expect(mocks.openPanel).toHaveBeenLastCalledWith({ kind: "background", mid: "example-machine", sid: "example-session", task: "cmd-1" }, undefined);
    expect(q('[data-default-command-detail="cmd-1"]')).not.toBeNull();
    expect(mocks.commandDetail).toHaveBeenCalled();

    act(() => q("[data-default-detail-open-file]")!.click());
    expect(mocks.openPanel).toHaveBeenLastCalledWith({ kind: "file", mid: "example-machine", path: "/tmp/example-output.log", cwd: "/example/work" }, undefined);
  });

  it("keeps standalone BackgroundList default store, credential, registry and clipboard fallbacks", () => {
    const session = makeSession();
    const stopLoop = vi.fn();
    mocks.getSession.mockReturnValue(session);
    mocks.useBackgroundChat.mockReturnValue({ stopLoop });

    act(() => {
      root.render(
        <BackgroundList
          session={session}
          mid="example-machine"
          sid="example-session"
          onOpenRun={vi.fn()}
          onOpenTask={vi.fn()}
        />,
      );
    });

    act(() => q(`[data-bg-task="cmd-1"] button[aria-label="${t("chat-activity.copy_command")}"]`)!.click());
    expect(mocks.credentialOwner).toHaveBeenCalled();
    expect(mocks.getSession).toHaveBeenCalledWith("example-machine", "example-session");
    expect(mocks.copyText).toHaveBeenCalledWith("npm run check");
    expect(mocks.useBackgroundChat).toHaveBeenCalledWith("example-machine", "example-session");
  });
});
