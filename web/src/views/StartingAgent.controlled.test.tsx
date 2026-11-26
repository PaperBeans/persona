// @vitest-environment jsdom
// Controlled StartingAgent bypasses store/provider-names/router/delivery/confirm/ShellQuestion singletons, resets
// controlled and uncontrolled edit/retry state across launch/machine/scope switches, and fences async/ABA/getter races.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LocalAttachment } from "../lib/attachments";
import type { AgentLaunch } from "../lib/new-agent";
import type { StartingAgentDeps } from "./StartingAgent";

const mocks = vi.hoisted(() => ({
  deliverLaunch: vi.fn(),
  useProviderNames: vi.fn(() => [] as { name: string; display_name?: string }[]),
  navigate: vi.fn(),
  useStore: vi.fn(),
  putLaunch: vi.fn(),
  releaseAttachments: vi.fn(),
  shellQuestion: vi.fn(() => null),
}));

vi.mock("../lib/new-agent", () => ({
  deliverLaunch: mocks.deliverLaunch,
}));
vi.mock("../lib/provider-names", async (original) => ({
  ...(await original<typeof import("../lib/provider-names")>()),
  useProviderNames: mocks.useProviderNames,
}));
vi.mock("../lib/router", () => ({
  navigate: mocks.navigate,
}));
vi.mock("../lib/store", () => ({
  useStore: mocks.useStore,
  putLaunch: mocks.putLaunch,
}));
vi.mock("../lib/attachments", () => ({
  releaseAttachments: mocks.releaseAttachments,
}));
vi.mock("./ShellQuestionCard", () => ({
  default: mocks.shellQuestion,
}));

const { StartingAgent, StartingAgentView } = await import("./StartingAgent");
const { initI18n, t } = await import("../i18n");

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const makeLaunch = (patch: Partial<AgentLaunch> = {}): AgentLaunch => ({
  id: "launch-1",
  machineId: "mac-1",
  cwd: "/code/app",
  launcher: "relay-1",
  text: "Initial prompt",
  attachments: [],
  phase: "failed",
  error: "Failed to start",
  uploaded: {},
  startOpId: "op-1",
  startPromptOpId: "op-2",
  messageId: "msg-1",
  ...patch,
});

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const buttonByText = (label: string) =>
  [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const setInputValue = async (el: HTMLInputElement | HTMLTextAreaElement, value: string) => {
  const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
};

const expectNoDefaultSingletons = () => {
  expect(mocks.deliverLaunch).not.toHaveBeenCalled();
  expect(mocks.useProviderNames).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.putLaunch).not.toHaveBeenCalled();
  expect(mocks.releaseAttachments).not.toHaveBeenCalled();
  expect(mocks.shellQuestion).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  Object.values(mocks).forEach((fn) => fn.mockReset());
  mocks.useProviderNames.mockReturnValue([]);
  mocks.useStore.mockImplementation((sel: (s: { machines: { id: string; name: string }[] }) => unknown) =>
    sel({ machines: [{ id: "mac-1", name: "Default Mac" }] }),
  );
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("StartingAgent controlled deps, view separation, and race guards", () => {
  it("runs fully controlled via deps without touching store, provider-names, router, delivery, or ShellQuestion singletons", async () => {
    const att: LocalAttachment = { id: "a1", name: "spec.txt", mime: "text/plain", file: new File(["x"], "spec.txt") };
    const putLaunch = vi.fn();
    const deliverLaunch = vi.fn();
    const releaseAttachments = vi.fn();
    const navigate = vi.fn();
    const launch = makeLaunch({
      launcher: "relay-1",
      model: "glm-5",
      attachments: [att],
      shellWaiting: { line: "Continue? [y/n]" },
    });
    const deps: StartingAgentDeps = {
      scopeKey: "acct-1",
      machineName: "Studio Mac",
      providerNames: [{ name: "relay-1", display_name: "Team Relay" }],
      putLaunch,
      deliverLaunch,
      releaseAttachments,
      navigate,
      renderShellQuestion: (l) => <div data-testid="custom-shell-q">{l.id}</div>,
    };

    await act(async () => root.render(<StartingAgent launch={launch} deps={deps} />));
    const headerSub = host.querySelector<HTMLElement>("header p")!;
    expect(headerSub.textContent).toBe("Studio Mac · /code/app · Team Relay · glm-5");
    expect(headerSub.title).toBe("Studio Mac · /code/app · Team Relay (relay-1) · glm-5");
    expect(host.querySelector('[data-testid="custom-shell-q"]')?.textContent).toBe("launch-1");

    // Edit launcher & prompt and click Retry.
    await setInputValue(host.querySelector("input")!, "claude");
    await setInputValue(host.querySelector("textarea")!, "Updated task");
    await act(async () => buttonByText(t("startingagent.retry"))!.click());

    expect(putLaunch).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      id: "launch-1",
      launcher: "claude",
      text: "Updated task",
      model: undefined,
      displayLauncher: undefined,
      displayModel: undefined,
    }));
    expect(deliverLaunch).toHaveBeenCalledExactlyOnceWith("launch-1", undefined);

    // Click "View list" and remove attachment.
    await act(async () => buttonByText(t("startingagent.view_list"))!.click());
    expect(navigate).toHaveBeenCalledWith({ name: "home" });

    const removeBtn = host.querySelector<HTMLButtonElement>('button[aria-label]');
    expect(removeBtn).not.toBeNull();
    await act(async () => removeBtn!.click());
    expect(releaseAttachments).toHaveBeenCalledWith([att]);
    expect(putLaunch).toHaveBeenLastCalledWith(expect.objectContaining({ attachments: [] }));

    // Ready phase auto-navigates through controlled deps.navigate.
    const ready = makeLaunch({ phase: "ready", session: { machine_id: "mac-1", session_id: "sess-9", title: "Started", status: "idle", cwd: "/code/app" } });
    await act(async () => root.render(<StartingAgent launch={ready} deps={deps} />));
    expect(navigate).toHaveBeenCalledWith({ name: "session", mid: "mac-1", sid: "sess-9" }, true);
    expectNoDefaultSingletons();
  });

  it("resets both controlled and uncontrolled edit/retry state via unified setters on launch/scope switch and fences stale/ABA async confirms", async () => {
    const confirm1 = deferred<boolean>();
    const confirm2 = deferred<boolean>();
    const confirmFn = vi.fn()
      .mockImplementationOnce(() => confirm1.promise)
      .mockImplementationOnce(() => confirm2.promise);
    const putLaunch = vi.fn();
    const deliverLaunch = vi.fn();

    const launchA = makeLaunch({ id: "l-a", machineId: "m-a", launcher: "orig-a", text: "text-a", uncertain: true });
    const launchB = makeLaunch({ id: "l-b", machineId: "m-b", launcher: "orig-b", text: "text-b", uncertain: true });

    let switchLaunch!: (l: AgentLaunch) => void;
    let switchScope!: (s: string) => void;
    const retryingHistory: boolean[] = [];

    function ControlledHost() {
      const [scopeKey, setScopeKey] = useState("scope-1");
      const [launch, setLaunch] = useState(launchA);
      const [text, setText] = useState(launchA.text);
      const [launcher, setLauncher] = useState(launchA.launcher);
      const [retrying, setRetrying] = useState(false);
      switchLaunch = setLaunch;
      switchScope = setScopeKey;
      return (
        <StartingAgent
          launch={launch}
          deps={{
            scopeKey,
            machineName: (mid) => `Host-${mid}`,
            providerNames: [],
            confirm: confirmFn,
            putLaunch,
            deliverLaunch,
            navigate: vi.fn(),
            text,
            onTextChange: setText,
            launcher,
            onLauncherChange: setLauncher,
            retrying,
            onRetryingChange: (next) => { retryingHistory.push(next); setRetrying(next); },
          }}
        />
      );
    }

    await act(async () => root.render(<ControlledHost />));
    // Rapid double click triggers only one async confirm.
    await act(async () => {
      buttonByText(t("startingagent.retry"))!.click();
      buttonByText(t("startingagent.retry"))!.click();
    });
    expect(confirmFn).toHaveBeenCalledTimes(1);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // Switch to launchB while launchA's confirm1 is pending -> retrying resets to false via unified setter!
    await act(async () => switchLaunch(launchB));
    await flush();
    expect(retryingHistory.at(-1)).toBe(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);

    // Start retry on launchB -> now confirm2 is pending and button is busy.
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(confirmFn).toHaveBeenCalledTimes(2);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // Stale confirm1 from launchA resolves true -> MUST NOT dispatch launchA and MUST NOT clear launchB's retrying state!
    await act(async () => confirm1.resolve(true));
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // ABA: switch back to launchA under new scope -> confirm2 from launchB resolves -> ignored!
    await act(async () => {
      switchScope("scope-2");
      switchLaunch(launchA);
    });
    await flush();
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    await act(async () => confirm2.resolve(true));
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(putLaunch).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("resets uncontrolled draft fields when switching editable launches and fences getter-only scopeKey mutations without getting stuck busy", async () => {
    let liveScope = "acct-1";
    const editableA = makeLaunch({ id: "ed-a", launcher: "claude-a", text: "draft-a" });
    const editableB = makeLaunch({ id: "ed-b", launcher: "codex-b", text: "draft-b" });
    const putLaunch = vi.fn();
    const deliverLaunch = vi.fn();
    const navigate = vi.fn();

    const baseDeps = (): StartingAgentDeps => ({
      scopeKey: () => liveScope,
      machineName: "Mac",
      providerNames: [],
      putLaunch,
      deliverLaunch,
      navigate,
    });

    await act(async () => root.render(<StartingAgent launch={editableA} deps={baseDeps()} />));
    await setInputValue(host.querySelector("input")!, "edited-launcher");
    await setInputValue(host.querySelector("textarea")!, "edited-prompt");
    expect(host.querySelector("input")!.value).toBe("edited-launcher");
    expect(host.querySelector("textarea")!.value).toBe("edited-prompt");

    // Re-render same launch id/machine (e.g. updated error) -> preserves in-progress edits.
    await act(async () => root.render(<StartingAgent launch={{ ...editableA, error: "still failed" }} deps={baseDeps()} />));
    expect(host.querySelector("input")!.value).toBe("edited-launcher");
    expect(host.querySelector("textarea")!.value).toBe("edited-prompt");

    // Switch to editableB -> resets uncontrolled inputs to editableB's values!
    await act(async () => root.render(<StartingAgent launch={editableB} deps={baseDeps()} />));
    await flush();
    expect(host.querySelector("input")!.value).toBe("codex-b");
    expect(host.querySelector("textarea")!.value).toBe("draft-b");

    // Getter-only scope mutation while async confirm is pending (NO re-render yet):
    // must discard dispatch AND clear retrying so UI does not stick in busy state.
    const pendingConfirm = deferred<boolean>();
    const onRetryingChange = vi.fn();
    const uncertainLaunch = makeLaunch({ id: "u-1", uncertain: true });
    await act(async () => root.render(
      <StartingAgent
        launch={uncertainLaunch}
        deps={{
          ...baseDeps(),
          confirm: () => pendingConfirm.promise,
          onRetryingChange,
        }}
      />,
    ));
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // Mutate getter-only scope WITHOUT re-rendering, then resolve confirm true.
    liveScope = "acct-2";
    await act(async () => pendingConfirm.resolve(true));
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(putLaunch).not.toHaveBeenCalled();
    expect(onRetryingChange).toHaveBeenLastCalledWith(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);

    // Pre-submit live launch check via getLaunch: if launch is no longer failed when confirm approves, refuse dispatch.
    let liveLaunchState: AgentLaunch = makeLaunch({ id: "u-2", uncertain: true, phase: "failed" });
    const confirmLive = deferred<boolean>();
    await act(async () => root.render(
      <StartingAgent
        launch={liveLaunchState}
        deps={{
          ...baseDeps(),
          getLaunch: () => liveLaunchState,
          confirm: () => confirmLive.promise,
        }}
      />,
    ));
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    liveLaunchState = { ...liveLaunchState, phase: "ready" };
    await act(async () => confirmLive.resolve(true));
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    expectNoDefaultSingletons();
  });

  it("fences imperative subscribed ABA scope burst on pending async confirm", async () => {
    let currentScope = "s1";
    const listeners = new Set<() => void>();
    const emit = (next: string) => {
      currentScope = next;
      listeners.forEach((l) => l());
    };
    const pending = deferred<boolean>();
    const deliverLaunch = vi.fn();
    const onRetryingChange = vi.fn();

    await act(async () => root.render(
      <StartingAgent
        launch={makeLaunch({ id: "aba-1", uncertain: true })}
        deps={{
          scopeKey: () => currentScope,
          subscribeScopeKey: (l) => { listeners.add(l); return () => listeners.delete(l); },
          machineName: "Mac",
          providerNames: [],
          confirm: () => pending.promise,
          putLaunch: vi.fn(),
          deliverLaunch,
          navigate: vi.fn(),
          onRetryingChange,
        }}
      />,
    ));

    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // Imperative ABA burst s1 -> s2 -> s1 must reset retrying and discard pending confirm.
    await act(async () => {
      emit("s2");
      emit("s1");
    });
    await flush();
    expect(onRetryingChange).toHaveBeenLastCalledWith(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);

    await act(async () => pending.resolve(true));
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("resets controlled draft and retrying state when switching from direct scopeKey to getter+subscribe (P1-1)", async () => {
    const scopeListeners = new Set<() => void>();
    let liveScope = "acct-b";
    const launchA = makeLaunch({ id: "l-1", launcher: "orig-a", text: "orig-text-a" });
    const launchB = makeLaunch({ id: "l-1", launcher: "orig-b", text: "orig-text-b" });
    let switchAccount!: () => void;
    let stateSnap = { text: "", launcher: "", retrying: false };

    function BridgeSwitchHost() {
      const [subscribedMode, setSubscribedMode] = useState(false);
      const [text, setText] = useState("edited-secret-a");
      const [launcher, setLauncher] = useState("custom-a");
      const [retrying, setRetrying] = useState(true);
      stateSnap = { text, launcher, retrying };
      switchAccount = () => setSubscribedMode(true);
      return (
        <StartingAgent
          launch={subscribedMode ? launchB : launchA}
          deps={{
            scopeKey: subscribedMode ? () => liveScope : "acct-a",
            subscribeScopeKey: subscribedMode ? (l) => { scopeListeners.add(l); return () => scopeListeners.delete(l); } : undefined,
            machineName: "Mac",
            providerNames: [],
            putLaunch: vi.fn(),
            deliverLaunch: vi.fn(),
            navigate: vi.fn(),
            text,
            onTextChange: setText,
            launcher,
            onLauncherChange: setLauncher,
            retrying,
            onRetryingChange: setRetrying,
          }}
        />
      );
    }

    await act(async () => root.render(<BridgeSwitchHost />));
    expect(stateSnap).toEqual({ text: "edited-secret-a", launcher: "custom-a", retrying: true });

    await act(async () => switchAccount());
    await flush();
    expect(stateSnap).toEqual({ text: "orig-text-b", launcher: "orig-b", retrying: false });
    expect(host.querySelector("input")!.value).toBe("orig-b");
    expect(host.querySelector("textarea")!.value).toBe("orig-text-b");
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    expectNoDefaultSingletons();
  });

  it("re-checks scope immediately before deliverLaunch if putLaunch synchronously switches account (P1-3)", async () => {
    let liveScope = "acct-a";
    const scopeListeners = new Set<() => void>();
    const deliverLaunch = vi.fn();
    const onRetryingChange = vi.fn();
    const putLaunch = vi.fn(() => {
      liveScope = "acct-b";
      scopeListeners.forEach((l) => l());
    });

    await act(async () => root.render(
      <StartingAgent
        launch={makeLaunch({ id: "l-sync-switch", launcher: "claude", text: "task-a" })}
        deps={{
          scopeKey: () => liveScope,
          subscribeScopeKey: (l) => { scopeListeners.add(l); return () => scopeListeners.delete(l); },
          machineName: "Mac",
          providerNames: [],
          putLaunch,
          deliverLaunch,
          navigate: vi.fn(),
          onRetryingChange,
        }}
      />,
    ));

    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    await flush();
    expect(putLaunch).toHaveBeenCalledTimes(1);
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(onRetryingChange).toHaveBeenLastCalledWith(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    expectNoDefaultSingletons();
  });

  it("cancels stale async confirm when launch gains a session and clearable draft while awaiting confirmation (P1-4)", async () => {
    let liveLaunch: AgentLaunch = makeLaunch({ id: "l-op", machineId: "mac-1", uncertain: true, session: undefined, clearable: undefined });
    const confirmReq = deferred<boolean>();
    const confirmFn = vi.fn(() => confirmReq.promise);
    const deliverLaunch = vi.fn();
    const onRetryingChange = vi.fn();

    await act(async () => root.render(
      <StartingAgent
        launch={liveLaunch}
        deps={{
          scopeKey: "acct-1",
          machineName: "Mac",
          providerNames: [],
          getLaunch: () => liveLaunch,
          confirm: confirmFn,
          putLaunch: vi.fn(),
          deliverLaunch,
          navigate: vi.fn(),
          onRetryingChange,
        }}
      />,
    ));

    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(confirmFn).toHaveBeenCalledWith(t("startingagent.restart_confirm"));
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(true);

    // While waiting on restart_confirm, the same launch gains session S and clearable draft D.
    liveLaunch = {
      ...liveLaunch,
      session: { machine_id: "mac-1", session_id: "S", title: "Session S", status: "idle", cwd: "/code/app" },
      clearable: "D",
    };
    await act(async () => confirmReq.resolve(true));
    await flush();

    // Approving the old restart_confirm must NOT resend to session S with clear_draft D.
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(onRetryingChange).toHaveBeenLastCalledWith(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    expectNoDefaultSingletons();
  });

  it("caches fresh-array providerNames getter snapshots with subscribeProviderNames without infinite re-renders (P1-5)", async () => {
    let label = "Team Relay v1";
    const listeners = new Set<() => void>();
    const readProviderNames = vi.fn(() => [{ name: "relay-1", display_name: label }]);

    await act(async () => root.render(
      <StartingAgent
        launch={makeLaunch({ launcher: "relay-1" })}
        deps={{
          scopeKey: "s1",
          machineName: "Mac",
          providerNames: readProviderNames,
          subscribeProviderNames: (l) => { listeners.add(l); return () => listeners.delete(l); },
          putLaunch: vi.fn(),
          deliverLaunch: vi.fn(),
          navigate: vi.fn(),
        }}
      />,
    ));
    expect(host.querySelector("header p")?.textContent).toContain("Team Relay v1");

    label = "Team Relay v2";
    await act(async () => { listeners.forEach((l) => l()); });
    expect(host.querySelector("header p")?.textContent).toContain("Team Relay v2");
    expectNoDefaultSingletons();
  });

  it("catches putLaunch throw, releases retrying state, shows recoverable error, and permits retry (P1-6)", async () => {
    const putLaunch = vi.fn()
      .mockImplementationOnce(() => { throw new Error("storage quota exceeded"); })
      .mockImplementationOnce(() => {});
    const deliverLaunch = vi.fn();
    const onRetryingChange = vi.fn();

    await act(async () => root.render(
      <StartingAgent
        launch={makeLaunch({ id: "l-throw", launcher: "claude", text: "prompt" })}
        deps={{
          scopeKey: "acct-1",
          machineName: "Mac",
          providerNames: [],
          putLaunch,
          deliverLaunch,
          navigate: vi.fn(),
          formatError: (e) => `persist:${(e as Error).message}`,
          onRetryingChange,
        }}
      />,
    ));

    // First attempt throws inside putLaunch: busy must clear, deliverLaunch skipped, recoverable error shown.
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    await flush();
    expect(deliverLaunch).not.toHaveBeenCalled();
    expect(onRetryingChange).toHaveBeenLastCalledWith(false);
    expect(buttonByText(t("startingagent.retry"))!.disabled).toBe(false);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("persist:storage quota exceeded");

    // Second attempt succeeds and clears the error.
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    await flush();
    expect(putLaunch).toHaveBeenCalledTimes(2);
    expect(deliverLaunch).toHaveBeenCalledExactlyOnceWith("l-throw", undefined);
    expect(host.querySelector('[role="alert"]')?.textContent).not.toContain("persist:storage quota exceeded");
    expectNoDefaultSingletons();
  });

  it("renders StartingAgentView purely and preserves default fallbacks when deps are omitted", async () => {
    const onRetry = vi.fn();
    const onToggleSidebar = vi.fn();
    await act(async () => root.render(
      <StartingAgentView
        launch={makeLaunch({ error: "Pure error" })}
        subtitleText="Mac · /code/app · claude"
        editable={false}
        text="Initial prompt"
        launcher="relay-1"
        loadingLabel="Starting"
        onToggleSidebar={onToggleSidebar}
        onTextChange={vi.fn()}
        onLauncherChange={vi.fn()}
        onRetry={onRetry}
        onViewSession={vi.fn()}
        onViewList={vi.fn()}
      />,
    ));
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Pure error");
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
    await act(async () => host.querySelector<HTMLButtonElement>('header button')!.click());
    expect(onToggleSidebar).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();

    // Default mode (no deps): touches useStore, useProviderNames, putLaunch, deliverLaunch, navigate.
    mocks.useProviderNames.mockReturnValue([{ name: "relay-1", display_name: "Default Relay" }]);
    const item = makeLaunch({ launcher: "relay-1", text: "Default text" });
    await act(async () => root.render(<StartingAgent launch={item} />));
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useProviderNames).toHaveBeenCalled();
    expect(host.querySelector("header p")?.textContent).toContain("Default Mac");
    expect(host.querySelector("header p")?.textContent).toContain("Default Relay");

    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(mocks.putLaunch).toHaveBeenCalledTimes(1);
    expect(mocks.deliverLaunch).toHaveBeenCalledWith("launch-1", undefined);

    await act(async () => buttonByText(t("startingagent.view_list"))!.click());
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "home" });
  });
});
