// @vitest-environment jsdom
// Controlled WorkSessionForm bypasses store/api/attachment/add-computer singletons, resets controlled and uncontrolled
// state across scope/owner switches, and fences stale/ABA/getter-only async loads and submissions; default fallbacks stay pinned.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LocalAttachment } from "../lib/attachments";
import type { Machine } from "../lib/types";
import type { WorkSessionDirs, WorkSessionFormDeps, WorkSessionStartOptions } from "./WorkSessionForm";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  credentialOwner: vi.fn(() => "default-owner"),
  subscribeCredentialOwner: vi.fn((_listener: () => void) => () => {}),
  errorText: vi.fn((e: unknown) => (e instanceof Error ? e.message : String(e))),
  pickAttachment: vi.fn(),
  releaseAttachments: vi.fn(),
  useScopedMachines: vi.fn(() => [] as Machine[]),
  machineLaunchers: vi.fn(),
  launchAgent: vi.fn(),
  getState: vi.fn(() => ({ machines: [] as Machine[] })),
  toast: vi.fn(),
  useAddComputer: vi.fn(() => ({ open: vi.fn(), dialog: null })),
}));

vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: mocks.api,
  credentialOwner: mocks.credentialOwner,
  subscribeCredentialOwner: mocks.subscribeCredentialOwner,
  errorText: mocks.errorText,
}));
vi.mock("../lib/attachments", () => ({
  pickAttachment: mocks.pickAttachment,
  releaseAttachments: mocks.releaseAttachments,
}));
vi.mock("../lib/current-assistant", () => ({
  useScopedMachines: mocks.useScopedMachines,
}));
vi.mock("../lib/launcher-models", async (original) => ({
  ...(await original<typeof import("../lib/launcher-models")>()),
  machineLaunchers: mocks.machineLaunchers,
}));
vi.mock("../lib/new-agent", () => ({
  launchAgent: mocks.launchAgent,
}));
vi.mock("../lib/store", () => ({
  getState: mocks.getState,
  hubHas: () => false,
}));
vi.mock("../ui/atoms", async (original) => ({
  ...(await original<typeof import("../ui/atoms")>()),
  toast: mocks.toast,
}));
vi.mock("./machines/AddComputerEntry", () => ({
  useAddComputer: mocks.useAddComputer,
}));
vi.mock("react-dom", async (original) => ({
  ...(await original<typeof import("react-dom")>()),
  createPortal: (children: React.ReactNode) => children,
}));

const { WorkSessionForm, WorkSessionFormView } = await import("./WorkSessionForm");
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

const MACHINES: Machine[] = [
  { id: "m1", name: "Alpha", online: true, manager: "mgr-1", engine: "herdr", engine_default: "herdr" },
  { id: "m2", name: "Beta", online: true, manager: "mgr-1", engine: "herdr", engine_default: "herdr" },
];

const DIRS_A: WorkSessionDirs = {
  path: "/work",
  parent: "/",
  entries: [{ name: "app", path: "/work/app", is_git: true }],
};

const START_OPTS: WorkSessionStartOptions = {
  agent_type: "claude",
  model_mode: "selectable",
  models: [{ id: "sonnet", name: "Sonnet" }],
  prompt_required: true,
  inline_prompt: false,
  attachments: true,
};

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const selects = () => [...host.querySelectorAll<HTMLSelectElement>("select")];
const choose = async (index: number, value: string) => {
  await act(async () => {
    selects()[index].value = value;
    selects()[index].dispatchEvent(new Event("change", { bubbles: true }));
  });
  await flush();
};
const fillPrompt = async (value: string) => {
  const ta = host.querySelector<HTMLTextAreaElement>("textarea")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(ta, value);
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  });
};
const buttonByText = (label: string) =>
  [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);

const expectNoDefaultSingletons = () => {
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
  expect(mocks.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(mocks.pickAttachment).not.toHaveBeenCalled();
  expect(mocks.releaseAttachments).not.toHaveBeenCalled();
  expect(mocks.useScopedMachines).not.toHaveBeenCalled();
  expect(mocks.machineLaunchers).not.toHaveBeenCalled();
  expect(mocks.launchAgent).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
  expect(mocks.useAddComputer).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  await initI18n("en");
  Object.values(mocks).forEach((fn) => fn.mockReset());
  mocks.credentialOwner.mockReturnValue("default-owner");
  mocks.subscribeCredentialOwner.mockReturnValue(() => {});
  mocks.errorText.mockImplementation((e: unknown) => (e instanceof Error ? e.message : String(e)));
  mocks.useScopedMachines.mockReturnValue([]);
  mocks.getState.mockReturnValue({ machines: [] });
  mocks.useAddComputer.mockReturnValue({ open: vi.fn(), dialog: null });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("WorkSessionForm controlled deps, view separation, and race guards", () => {
  it("runs fully controlled via deps without touching default singletons", async () => {
    const onClose = vi.fn();
    const launchFn = vi.fn();
    const releaseFn = vi.fn();
    const notifyFn = vi.fn();
    const file = new File(["hello"], "notes.txt", { type: "text/plain" });
    const picked: LocalAttachment = { id: "att-1", name: "notes.txt", mime: "text/plain", file };
    const pickFn = vi.fn((f: File) => {
      if (f.name === "bad.bin") throw new Error("too big");
      return picked;
    });
    const loadDirs = vi.fn(async (_mid: string, path: string) => ({
      ...DIRS_A,
      path: path || "/work",
    }));
    const loadLaunchers = vi.fn(async () => ({
      launchers: [{ name: "claude-main", agent_type: "claude" }],
    }));
    const loadStartOptions = vi.fn(async () => START_OPTS);
    const deps: WorkSessionFormDeps = {
      scopeKey: "scope-1",
      machines: MACHINES,
      credentialOwner: "acct-1",
      addComputer: { open: vi.fn(), dialog: null },
      loadDirs,
      loadLaunchers,
      loadStartOptions,
      launchAgent: launchFn,
      pickAttachment: pickFn,
      releaseAttachments: releaseFn,
      notify: notifyFn,
      formatError: (e) => `fmt:${(e as Error).message}`,
    };

    await act(async () => root.render(<WorkSessionForm open onClose={onClose} deps={deps} />));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await choose(4, "sonnet");
    await fillPrompt("Ship feature");

    // Pick one valid and one rejected file through the hidden file input.
    const bad = new File(["x"], "bad.bin", { type: "application/octet-stream" });
    const fileInput = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(fileInput, "files", { configurable: true, value: [file, bad] });
    await act(async () => fileInput.dispatchEvent(new Event("change", { bubbles: true })));
    expect(notifyFn).toHaveBeenCalledWith("fmt:too big", "error");

    await act(async () => buttonByText(t("newagent.start"))!.click());
    expect(launchFn).toHaveBeenCalledExactlyOnceWith({
      machineId: "m1",
      cwd: "/work/app",
      launcher: "claude-main",
      agentType: "claude",
      workSession: true,
      model: "sonnet",
      inlinePrompt: false,
      text: "Ship feature",
      attachments: [picked],
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(releaseFn).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("resets both controlled and uncontrolled state via unified setters on scopeKey switch and fences stale/ABA loads", async () => {
    const req1 = deferred<WorkSessionStartOptions>();
    const req2 = deferred<WorkSessionStartOptions>();
    const loadDirs = vi.fn(async (_mid: string, path: string) => ({ ...DIRS_A, path: path || "/work" }));
    const loadLaunchers = vi.fn(async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }));
    const loadStartOptions = vi.fn()
      .mockImplementationOnce(() => req1.promise)
      .mockImplementationOnce(() => req2.promise);
    const launchFn = vi.fn();

    const resets: Record<string, string[]> = { mid: [], cwd: [], prompt: [] };
    let setExternalScope!: (s: string) => void;

    function ControlledHost() {
      const [scopeKey, setScopeKey] = useState("scope-a");
      const [mid, setMid] = useState("");
      const [cwd, setCwd] = useState("");
      const [harness, setHarness] = useState("");
      const [launcher, setLauncher] = useState("");
      const [model, setModel] = useState("");
      const [prompt, setPrompt] = useState("");
      setExternalScope = setScopeKey;
      return (
        <WorkSessionForm
          open
          onClose={vi.fn()}
          deps={{
            scopeKey,
            machines: MACHINES,
            credentialOwner: "acct-1",
            addComputer: vi.fn<() => void>(),
            loadDirs,
            loadLaunchers,
            loadStartOptions,
            launchAgent: launchFn,
            mid,
            onMidChange: (v) => { resets.mid.push(v); setMid(v); },
            cwd,
            onCwdChange: (v) => { resets.cwd.push(v); setCwd(v); },
            harness,
            onHarnessChange: setHarness,
            launcher,
            onLauncherChange: setLauncher,
            model,
            onModelChange: setModel,
            prompt,
            onPromptChange: (v) => { resets.prompt.push(v); setPrompt(v); },
          }}
        />
      );
    }

    await act(async () => root.render(<ControlledHost />));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await fillPrompt("secret scope-a draft");
    expect(loadStartOptions).toHaveBeenCalledTimes(1);
    const dirsCallsBeforeSwitch = loadDirs.mock.calls.length;

    // Switch scope-a -> scope-b: controlled fields must reset through unified setters with zero spurious loads for scope-a's mid/cwd.
    await act(async () => setExternalScope("scope-b"));
    await flush();
    expect(selects()[0].value).toBe("");
    expect(selects()[1].value).toBe("");
    expect(host.querySelector("textarea")!.value).toBe("");
    expect(resets.mid.at(-1)).toBe("");
    expect(resets.cwd.at(-1)).toBe("");
    expect(resets.prompt.at(-1)).toBe("");
    expect(loadDirs).toHaveBeenCalledTimes(dirsCallsBeforeSwitch);

    // ABA: switch back to scope-a, pick m1 again, and resolve stale req1 from epoch 1 before req2.
    await act(async () => setExternalScope("scope-a"));
    await flush();
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    expect(loadStartOptions).toHaveBeenCalledTimes(2);

    await act(async () => req1.resolve({ ...START_OPTS, models: [{ id: "stale-model", name: "Stale" }] }));
    await flush();
    expect(selects()[4].textContent).not.toContain("Stale");
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(true);

    await act(async () => req2.resolve({ ...START_OPTS, models: [{ id: "fresh-model", name: "Fresh" }] }));
    await flush();
    expect(selects()[4].textContent).toContain("Fresh");
    expectNoDefaultSingletons();
  });

  it("blocks stale async results and pre-submit actions when getter-only credentialOwner or live machine changes", async () => {
    let liveOwner = "owner-1";
    let liveMachines: Machine[] = [{ ...MACHINES[0] }];
    const pendingOpts = deferred<WorkSessionStartOptions>();
    const launchFn = vi.fn();
    const releaseFn = vi.fn();
    const picked: LocalAttachment = { id: "att-local", name: "a.txt", mime: "text/plain", file: new File(["a"], "a.txt") };

    const makeDeps = (): WorkSessionFormDeps => ({
      machines: () => liveMachines,
      credentialOwner: () => liveOwner,
      addComputer: vi.fn<() => void>(),
      loadDirs: async (_mid, path) => ({ ...DIRS_A, path: path || "/work" }),
      loadLaunchers: async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }),
      loadStartOptions: () => pendingOpts.promise,
      launchAgent: launchFn,
      pickAttachment: () => picked,
      releaseAttachments: releaseFn,
    });

    await act(async () => root.render(<WorkSessionForm open onClose={vi.fn()} deps={makeDeps()} />));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await fillPrompt("draft");

    // Mutate getter-only owner WITHOUT re-rendering while start-options is in flight.
    liveOwner = "owner-2";
    await act(async () => pendingOpts.resolve(START_OPTS));
    await flush();
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(true);
    expect(selects()[4].textContent).not.toContain("Sonnet");

    // Re-render picks up resolved owner-2 and resets uncontrolled selections cleanly.
    await act(async () => root.render(<WorkSessionForm open onClose={vi.fn()} deps={makeDeps()} />));
    await flush();
    expect(selects()[0].value).toBe("");
    expect(host.querySelector("textarea")!.value).toBe("");

    // Now test live admission check right before Start when getter-only machine goes offline without re-render.
    const readyOpts = deferred<WorkSessionStartOptions>();
    const readyDeps: WorkSessionFormDeps = {
      ...makeDeps(),
      loadStartOptions: () => readyOpts.promise,
    };
    await act(async () => root.render(<WorkSessionForm open onClose={vi.fn()} deps={readyDeps} />));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await act(async () => readyOpts.resolve(START_OPTS));
    await flush();
    await fillPrompt("ready to start");
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(false);

    // Machine goes offline in getter before click without re-render -> start refuses submission.
    liveMachines = [{ ...MACHINES[0], online: false }];
    await act(async () => buttonByText(t("newagent.start"))!.click());
    expect(launchFn).not.toHaveBeenCalled();

    // Restore online, mutate getter-only owner -> still refuses submission.
    liveMachines = [{ ...MACHINES[0], online: true }];
    liveOwner = "owner-3";
    await act(async () => buttonByText(t("newagent.start"))!.click());
    expect(launchFn).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("handles imperative subscribed ABA scope burst and resets state cleanly", async () => {
    let currentScope = "s1";
    const listeners = new Set<() => void>();
    const emit = (next: string) => {
      currentScope = next;
      listeners.forEach((l) => l());
    };
    const pendingOpts = deferred<WorkSessionStartOptions>();
    const loadStartOptions = vi.fn(() => pendingOpts.promise);

    await act(async () => root.render(
      <WorkSessionForm
        open
        onClose={vi.fn()}
        deps={{
          scopeKey: () => currentScope,
          subscribeScopeKey: (l) => { listeners.add(l); return () => listeners.delete(l); },
          machines: MACHINES,
          credentialOwner: "acct-1",
          addComputer: vi.fn<() => void>(),
          loadDirs: async (_mid, path) => ({ ...DIRS_A, path: path || "/work" }),
          loadLaunchers: async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }),
          loadStartOptions,
          launchAgent: vi.fn(),
        }}
      />,
    ));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await fillPrompt("aba draft");
    expect(loadStartOptions).toHaveBeenCalledTimes(1);

    // Imperative ABA burst s1 -> s2 -> s1 inside a single act before pendingOpts resolves.
    await act(async () => {
      emit("s2");
      emit("s1");
    });
    await flush();
    expect(selects()[0].value).toBe("");
    expect(host.querySelector("textarea")!.value).toBe("");

    await act(async () => pendingOpts.resolve(START_OPTS));
    await flush();
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(true);
    expectNoDefaultSingletons();
  });

  it("releases local attachments on scope switch and respects IME composition guard on Ctrl+Enter", async () => {
    const picked: LocalAttachment = { id: "att-2", name: "a.txt", mime: "text/plain", file: new File(["a"], "a.txt") };
    const releaseFn = vi.fn();
    const launchFn = vi.fn();
    const makeDeps = (scopeKey: string): WorkSessionFormDeps => ({
      scopeKey,
      machines: MACHINES,
      credentialOwner: "acct-1",
      addComputer: vi.fn<() => void>(),
      loadDirs: async (_mid, path) => ({ ...DIRS_A, path: path || "/work" }),
      loadLaunchers: async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }),
      loadStartOptions: async () => START_OPTS,
      launchAgent: launchFn,
      pickAttachment: () => picked,
      releaseAttachments: releaseFn,
    });

    await act(async () => root.render(<WorkSessionForm open onClose={vi.fn()} deps={makeDeps("s1")} />));
    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await fillPrompt("hello");

    // IME keyCode 229 on Ctrl+Enter must not submit.
    const ta = host.querySelector<HTMLTextAreaElement>("textarea")!;
    await act(async () => {
      const ev = new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true });
      Object.defineProperty(ev, "keyCode", { value: 229 });
      ta.dispatchEvent(ev);
    });
    expect(launchFn).not.toHaveBeenCalled();

    // Attach a file, then switch scope -> local attachment is released and cleared.
    const fileInput = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(fileInput, "files", { configurable: true, value: [picked.file] });
    await act(async () => fileInput.dispatchEvent(new Event("change", { bubbles: true })));
    await act(async () => root.render(<WorkSessionForm open onClose={vi.fn()} deps={makeDeps("s2")} />));
    await flush();
    expect(releaseFn).toHaveBeenCalledWith([picked]);
    expectNoDefaultSingletons();
  });

  it("resets controlled state via unified setters when switching from direct scope/owner to getter+subscribe bridges (P1-1)", async () => {
    const launchFn = vi.fn();
    const scopeListeners = new Set<() => void>();
    const ownerListeners = new Set<() => void>();
    let liveScope = "acct-b";
    let liveOwner = "owner-b";
    const attA: LocalAttachment = { id: "att-a", name: "secret.txt", mime: "text/plain", file: new File(["secret"], "secret.txt") };

    let switchToSubscribedAccountB!: () => void;
    let latestState = { mid: "", cwd: "", prompt: "", attachments: 0 };

    function BridgeSwitchHost() {
      const [subscribedMode, setSubscribedMode] = useState(false);
      const [mid, setMid] = useState("m1");
      const [cwd, setCwd] = useState("/work/app");
      const [harness, setHarness] = useState("claude");
      const [launcher, setLauncher] = useState("claude-main");
      const [model, setModel] = useState("sonnet");
      const [prompt, setPrompt] = useState("account-a secret draft");
      const [attachments, setAttachments] = useState<LocalAttachment[]>([attA]);
      latestState = { mid, cwd, prompt, attachments: attachments.length };
      switchToSubscribedAccountB = () => setSubscribedMode(true);
      return (
        <WorkSessionForm
          open
          onClose={vi.fn()}
          deps={{
            scopeKey: subscribedMode ? () => liveScope : "acct-a",
            subscribeScopeKey: subscribedMode ? (l) => { scopeListeners.add(l); return () => scopeListeners.delete(l); } : undefined,
            credentialOwner: subscribedMode ? () => liveOwner : "owner-a",
            subscribeCredentialOwner: subscribedMode ? (l) => { ownerListeners.add(l); return () => ownerListeners.delete(l); } : undefined,
            machines: MACHINES,
            addComputer: vi.fn<() => void>(),
            loadDirs: async (_mid, path) => ({ ...DIRS_A, path: path || "/work" }),
            loadLaunchers: async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }),
            loadStartOptions: async () => START_OPTS,
            launchAgent: launchFn,
            mid,
            onMidChange: setMid,
            cwd,
            onCwdChange: setCwd,
            harness,
            onHarnessChange: setHarness,
            launcher,
            onLauncherChange: setLauncher,
            model,
            onModelChange: setModel,
            prompt,
            onPromptChange: setPrompt,
            attachments,
            onAttachmentsChange: setAttachments,
          }}
        />
      );
    }

    await act(async () => root.render(<BridgeSwitchHost />));
    await flush();
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(false);

    // Switching account AND switching scope/owner from direct string -> getter+subscribe must still invoke unified resets.
    await act(async () => switchToSubscribedAccountB());
    await flush();
    expect(latestState).toEqual({ mid: "", cwd: "", prompt: "", attachments: 0 });
    expect(selects()[0].value).toBe("");
    expect(host.querySelector("textarea")!.value).toBe("");
    expect(buttonByText(t("newagent.start"))!.disabled).toBe(true);
    await act(async () => buttonByText(t("newagent.start"))!.click());
    expect(launchFn).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("caches fresh-array machines getter snapshots with subscribeMachines without infinite re-renders (P1-5)", async () => {
    let online = true;
    const listeners = new Set<() => void>();
    const readMachines = vi.fn(() => [{ ...MACHINES[0], online, engine_hosts: ["herdr"] }]);

    await act(async () => root.render(
      <WorkSessionForm
        open
        onClose={vi.fn()}
        deps={{
          scopeKey: "s1",
          credentialOwner: "acct-1",
          machines: readMachines,
          subscribeMachines: (l) => { listeners.add(l); return () => listeners.delete(l); },
          addComputer: vi.fn<() => void>(),
          loadDirs: async (_mid, path) => ({ ...DIRS_A, path: path || "/work" }),
          loadLaunchers: async () => ({ launchers: [{ name: "claude-main", agent_type: "claude" }] }),
          loadStartOptions: async () => START_OPTS,
          launchAgent: vi.fn(),
        }}
      />,
    ));
    await flush();
    expect(selects()[0].textContent).toContain("Alpha");
    expect(selects()[0].options[1].disabled).toBe(false);

    // Mutate content and notify subscriber -> updates cleanly once.
    online = false;
    await act(async () => { listeners.forEach((l) => l()); });
    await flush();
    expect(selects()[0].options[1].disabled).toBe(true);
    expectNoDefaultSingletons();
  });

  it("renders WorkSessionFormView purely without touching singletons", async () => {
    const onAddComputer = vi.fn();
    const onClose = vi.fn();
    const onRetry = vi.fn();
    await act(async () => root.render(
      <WorkSessionFormView
        open
        machines={[]}
        mid=""
        offline={false}
        hasTarget={false}
        cwd=""
        browse=""
        dirsLoading={false}
        projectReady={false}
        launchersLoaded={false}
        groups={new Map()}
        harness=""
        launcher=""
        matching={[]}
        validLauncher={false}
        optionsLoading={false}
        models={[]}
        model=""
        compatible={false}
        staleModel={false}
        canStart={false}
        error="View error"
        prompt=""
        attachments={[]}
        onClose={onClose}
        onStart={vi.fn()}
        onChooseDevice={vi.fn()}
        onAddComputer={onAddComputer}
        onCwdChange={vi.fn()}
        onBrowseChange={vi.fn()}
        onChooseHarness={vi.fn()}
        onChooseLauncher={vi.fn()}
        onModelChange={vi.fn()}
        onRetry={onRetry}
        onPromptChange={vi.fn()}
        onAddFiles={vi.fn()}
        onRemoveAttachment={vi.fn()}
      />,
    ));

    expect(host.querySelector('[role="alert"]')?.textContent).toContain("View error");
    await act(async () => buttonByText(t("startingagent.retry"))!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
    await act(async () => buttonByText(t("machines.add"))!.click());
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onAddComputer).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("preserves default singleton fallbacks when deps are omitted", async () => {
    mocks.useScopedMachines.mockReturnValue(MACHINES);
    mocks.getState.mockReturnValue({ machines: MACHINES });
    mocks.api.mockImplementation(async (path: string) => {
      if (path.includes("/dirs")) return DIRS_A;
      return START_OPTS;
    });
    mocks.machineLaunchers.mockResolvedValue({ launchers: [{ name: "claude-main", agent_type: "claude" }] });
    const onClose = vi.fn();

    await act(async () => root.render(<WorkSessionForm open onClose={onClose} />));
    expect(mocks.useScopedMachines).toHaveBeenCalled();
    expect(mocks.useAddComputer).toHaveBeenCalled();
    expect(mocks.subscribeCredentialOwner).toHaveBeenCalled();

    await choose(0, "m1");
    await choose(1, "/work/app");
    await choose(2, "claude");
    await fillPrompt("default launch");
    await act(async () => buttonByText(t("newagent.start"))!.click());
    expect(mocks.getState).toHaveBeenCalled();
    expect(mocks.launchAgent).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      machineId: "m1",
      cwd: "/work",
      launcher: "claude-main",
      text: "default launch",
    }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
