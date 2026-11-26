// @vitest-environment jsdom
// Controlled panels supply worktree creation session, ownership, machines, catalog and action callbacks; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";
import type { HostWorkspace, WorkspaceCatalogSnapshot } from "../lib/workspace-catalog";
import type { CreationDraft, CreationIntent, CreationSnapshot, RepositoryDraft } from "../lib/worktree-creation";
import type { SparsePreset, WorktreeCreated, WorktreePrepare, WorktreeRequest } from "../lib/worktree";
import type { NewWorktreeProps, WorktreeComposerSession, WorktreeSourcesResult } from "./NewWorktree";

const mocks = vi.hoisted(() => ({
  creationSession: vi.fn(),
  prepareWorktree: vi.fn(),
  worktreeSources: vi.fn(),
  saveWorktreePreset: vi.fn(),
  openCreated: vi.fn(),
  worktreeAdmission: vi.fn(),
  credentialOwner: vi.fn(),
  subscribeCredentialOwner: vi.fn(),
  useKnownDefault: vi.fn(),
  defaultManager: vi.fn(),
  useStore: vi.fn(),
  getState: vi.fn(),
  useWorkspaceCatalog: vi.fn(),
  openNewAgent: vi.fn(),
}));

vi.mock("../lib/worktree-creation", async (importOriginal) => ({
  ...await importOriginal<typeof import("../lib/worktree-creation")>(),
  creationSession: mocks.creationSession,
}));
vi.mock("../lib/worktree", async (importOriginal) => ({
  ...await importOriginal<typeof import("../lib/worktree")>(),
  prepareWorktree: mocks.prepareWorktree,
  worktreeSources: mocks.worktreeSources,
  saveWorktreePreset: mocks.saveWorktreePreset,
  openCreated: mocks.openCreated,
  worktreeAdmission: mocks.worktreeAdmission,
}));
vi.mock("../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../lib/api")>(),
  credentialOwner: mocks.credentialOwner,
  subscribeCredentialOwner: mocks.subscribeCredentialOwner,
}));
vi.mock("../lib/manager-choice", async (importOriginal) => ({
  ...await importOriginal<typeof import("../lib/manager-choice")>(),
  useKnownDefault: mocks.useKnownDefault,
  defaultManager: mocks.defaultManager,
}));
vi.mock("../lib/store", () => ({
  useStore: mocks.useStore,
  getState: mocks.getState,
}));
vi.mock("../lib/workspace-catalog", async (importOriginal) => ({
  ...await importOriginal<typeof import("../lib/workspace-catalog")>(),
  useWorkspaceCatalog: mocks.useWorkspaceCatalog,
}));
vi.mock("../lib/new-agent", () => ({
  openNewAgent: mocks.openNewAgent,
}));

const { repositoryDraft, sourceKey } = await import("../lib/worktree-creation");
const { WORKTREE_COPY } = await import("../lib/worktree");
const { NewWorktree } = await import("./NewWorktree");
const { initI18n, t } = await import("../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const OWNER = "controlled-owner";
const MANAGER = "assistant";

const advancedPrepare: WorktreePrepare = {
  root: "/projects/demo",
  repoName: "Demo",
  path: "/worktrees",
  branch: "main",
  composer: {
    project: "Demo",
    base_ref: "origin/main",
    refs: ["origin/main", "feature/existing"],
    presets: [],
    capabilities: ["create", "branch_override", "source.github", "sparse_presets", "folder_parent"],
  },
};

const sampleMachines: Machine[] = [
  { id: "computer", name: "Development computer", online: true, manager: MANAGER } as Machine,
];

const sampleWorkspaces: HostWorkspace[] = [
  { id: "q-a", machine_id: "computer", source: "orca", source_id: "source-a", cwd: "/projects/demo", title: "Main checkout", project_id: "demo", project_name: "Demo", workspace_kind: "git" },
  { id: "q-b", machine_id: "computer", source: "orca", source_id: "source-b", cwd: "/projects/demo-b", title: "Other checkout", project_id: "demo", project_name: "Demo", workspace_kind: "git" },
];

const sampleCatalog: WorkspaceCatalogSnapshot = { owner: OWNER, manager: MANAGER, workspaces: sampleWorkspaces };

const makeRequest = (workspaceId = "source-a", host = "orca"): WorktreeRequest => ({
  target: { mid: "computer", workspaceId, host, cwd: `/projects/${workspaceId}`, label: workspaceId, owner: OWNER, manager: MANAGER },
  changed: vi.fn(),
  onCreated: vi.fn(),
});

const makeDraft = (request: WorktreeRequest): CreationDraft => ({
  source: { ...request.target },
  parent: request.parent,
  name: "",
  branch: "",
  path: "",
  pathEdited: false,
  automaticName: false,
  note: "",
  repositories: {},
});

function makeMemorySession(
  request: WorktreeRequest,
  onSubmit?: (draft: CreationDraft, info?: WorktreePrepare) => Promise<CreationIntent>,
): WorktreeComposerSession & { setSnapshot: (next: CreationSnapshot) => void } {
  let snapshot: CreationSnapshot = { draft: makeDraft(request) };
  const listeners = new Set<() => void>();
  const publish = (next: CreationSnapshot) => {
    snapshot = next;
    listeners.forEach((l) => l());
  };
  return {
    subscribe: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => snapshot,
    setSnapshot: publish,
    update: (patch) => publish({ ...snapshot, draft: { ...snapshot.draft, ...patch } }),
    updateRepository: (patch: Partial<RepositoryDraft>) => {
      const draft = snapshot.draft;
      publish({
        ...snapshot,
        draft: {
          ...draft,
          repositories: { ...draft.repositories, [sourceKey(draft.source)]: { ...repositoryDraft(draft), ...patch } },
        },
      });
    },
    fresh: () => { if (snapshot.intent?.phase === "created") publish({ draft: snapshot.draft }); },
    submit: async (info) => {
      if (!onSubmit) return;
      const intent = await onSubmit(snapshot.draft, info);
      publish({ ...snapshot, intent });
    },
  };
}

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};

let host: HTMLDivElement;
let root: Root;
let onClose: ReturnType<typeof vi.fn<() => void>>;
let fetchSpy: ReturnType<typeof vi.fn>;
let getItemSpy: ReturnType<typeof vi.spyOn>;
let setItemSpy: ReturnType<typeof vi.spyOn>;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

const button = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);

const field = (label: string) => {
  const el = [...document.querySelectorAll("label")].find((l) => l.textContent === label)!;
  return document.getElementById(el.htmlFor) as HTMLInputElement;
};

async function setField(label: string, value: string) {
  await act(async () => {
    const el = field(label);
    const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : el.tagName === "SELECT" ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event(el.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
  });
}

const expectNoDefaultSingletons = () => {
  expect(mocks.creationSession).not.toHaveBeenCalled();
  expect(mocks.prepareWorktree).not.toHaveBeenCalled();
  expect(mocks.worktreeSources).not.toHaveBeenCalled();
  expect(mocks.saveWorktreePreset).not.toHaveBeenCalled();
  expect(mocks.openCreated).not.toHaveBeenCalled();
  expect(mocks.worktreeAdmission).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
  expect(mocks.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(mocks.useKnownDefault).not.toHaveBeenCalled();
  expect(mocks.defaultManager).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.useWorkspaceCatalog).not.toHaveBeenCalled();
  expect(mocks.openNewAgent).not.toHaveBeenCalled();
  expect(fetchSpy).not.toHaveBeenCalled();
  expect(getItemSpy).not.toHaveBeenCalled();
  expect(setItemSpy).not.toHaveBeenCalled();
};

beforeEach(async () => {
  await initI18n("en");
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }));
  Element.prototype.scrollIntoView = vi.fn();
  fetchSpy = vi.fn();
  vi.stubGlobal("fetch", fetchSpy);
  getItemSpy = vi.spyOn(Storage.prototype, "getItem");
  setItemSpy = vi.spyOn(Storage.prototype, "setItem");

  mocks.credentialOwner.mockReturnValue(OWNER);
  mocks.subscribeCredentialOwner.mockReturnValue(() => {});
  mocks.useKnownDefault.mockReturnValue(MANAGER);
  mocks.defaultManager.mockReturnValue(MANAGER);
  mocks.worktreeAdmission.mockReturnValue(true);
  mocks.useStore.mockImplementation((selector: (st: { machines: Machine[] }) => unknown) => selector({ machines: sampleMachines }));
  mocks.getState.mockReturnValue({ machines: sampleMachines });
  mocks.useWorkspaceCatalog.mockReturnValue(sampleCatalog);
  mocks.prepareWorktree.mockResolvedValue(advancedPrepare);
  mocks.worktreeSources.mockResolvedValue({ items: [] });
  mocks.saveWorktreePreset.mockResolvedValue({ preset: { id: "preset-default", name: "Default", directories: ["web"] } });

  onClose = vi.fn();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("NewWorktree controlled mode", () => {
  it("runs prepare, provider search, preset save, create, openCreated, startAgent and create-another with zero default singletons", async () => {
    const request = makeRequest();
    const created: WorktreeCreated = { workspace_id: "new-wt", path: "/worktrees/new-wt", branch: "feat/new", terminal: "pane:p1" };
    const session = makeMemorySession(request, async (draft) => ({
      id: "op-1",
      body: { workspace_id: draft.source.workspaceId, kind: draft.source.host, composer: true },
      target: draft.source,
      phase: "created",
      created,
    }));
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const onSearchSources = vi.fn().mockResolvedValue({
      items: [{ type: "issue", number: 7, title: "Controlled issue", url: "https://github.com/example/demo/issues/7" }],
    } satisfies WorktreeSourcesResult);
    const onSavePreset = vi.fn().mockResolvedValue({
      preset: { id: "preset-ui", name: "UI", directories: ["web"] } satisfies SparsePreset,
    });
    const onOpenCreated = vi.fn();
    const onStartAgent = vi.fn();

    await act(async () => root.render(
      <NewWorktree
        request={request}
        onClose={onClose}
        session={session}
        owner={OWNER}
        machines={sampleMachines}
        catalog={sampleCatalog}
        isAdmitted={() => true}
        onPrepare={onPrepare}
        onSearchSources={onSearchSources}
        onSavePreset={onSavePreset}
        onOpenCreated={onOpenCreated}
        onStartAgent={onStartAgent}
      />,
    ));
    await flush();

    expect(onPrepare).toHaveBeenCalledWith(request.target, expect.objectContaining({ composer: true }));
    await setField(t("worktree-composer.name"), "Feature shell");
    await setField(t("worktree-composer.search_source"), "shell");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    await flush();
    expect(onSearchSources).toHaveBeenCalledWith(request.target, "shell", expect.any(AbortSignal));
    await act(async () => { button("#7 · Controlled issue")!.click(); });

    await setField(t("worktree-composer.preset_name"), "UI");
    await setField(t("worktree-composer.directories"), "web");
    await act(async () => { button(t("worktree-composer.save_preset"))!.click(); });
    await flush();
    expect(onSavePreset).toHaveBeenCalledWith(request.target, "UI", "web");

    await act(async () => { button(t("worktree-composer.create"))!.click(); });
    await flush();
    expect(request.changed).toHaveBeenCalledOnce();
    expect(request.onCreated).toHaveBeenCalledWith(created, expect.objectContaining({ workspaceId: "new-wt", cwd: "/worktrees/new-wt" }));

    await act(async () => { button(t("worktree-composer.open"))!.click(); });
    expect(onOpenCreated).toHaveBeenCalledWith("computer", created);
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => { button(t("worktree-composer.start_agent"))!.click(); });
    expect(onStartAgent).toHaveBeenCalledWith(expect.objectContaining({ workspaceId: "new-wt", cwd: "/worktrees/new-wt" }));
    expect(onClose).toHaveBeenCalledTimes(2);

    // "Create another" resets launched state so a subsequent worktree can start an agent again.
    await act(async () => { button(t("worktree-composer.create_another"))!.click(); });
    await flush();
    expect(button(t("worktree-composer.create"))).toBeTruthy();
    expectNoDefaultSingletons();
  });

  it("handles controlled loadSession with ABA race and error isolation", async () => {
    const reqA = makeRequest("source-a");
    const reqB = makeRequest("source-b");
    const staleA = makeMemorySession(reqA);
    const freshA = makeMemorySession(reqA);
    const pA1 = deferred<WorktreeComposerSession>();
    const pB = deferred<WorktreeComposerSession>();
    const pA2 = deferred<WorktreeComposerSession>();
    const loadSession = vi.fn()
      .mockReturnValueOnce(pA1.promise)
      .mockReturnValueOnce(pB.promise)
      .mockReturnValueOnce(pA2.promise);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);

    const mountWith = async (request: WorktreeRequest) => {
      await act(async () => root.render(
        <NewWorktree
          request={request}
          onClose={onClose}
          loadSession={loadSession}
          owner={OWNER}
          machines={sampleMachines}
          catalog={sampleCatalog}
          defaultManager={MANAGER}
          onPrepare={onPrepare}
        />,
      ));
      await flush();
    };

    await mountWith(reqA);
    expect(document.body.textContent).toContain(WORKTREE_COPY.reading);

    // Switch to reqB and reject it -> error renders for reqB.
    await mountWith(reqB);
    await act(async () => { pB.reject(new Error("failed B")); });
    await flush();
    expect(document.querySelector("[role=alert]")?.textContent).toBe("failed B");

    // Switch back to reqA (ABA): error from reqB clears immediately to loading status.
    await mountWith(reqA);
    expect(document.querySelector("[role=alert]")).toBeNull();
    expect(document.body.textContent).toContain(WORKTREE_COPY.reading);

    // Late resolution of stale pA1 must be ignored.
    await act(async () => { pA1.resolve(staleA); });
    await flush();
    expect(onPrepare).not.toHaveBeenCalled();

    // Resolving active pA2 mounts Composer cleanly.
    await act(async () => { pA2.resolve(freshA); });
    await flush();
    expect(onPrepare).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("isolates draft inputs, launched state and in-flight ABA async operations when switching worktrees or sources", async () => {
    const reqA = makeRequest("source-a");
    const reqB = makeRequest("source-b");
    const createdA: WorktreeCreated = { workspace_id: "wt-a", path: "/worktrees/a", branch: "feat/a" };
    const createdB: WorktreeCreated = { workspace_id: "wt-b", path: "/worktrees/b", branch: "feat/b" };
    const sessionA = makeMemorySession(reqA);
    const sessionB = makeMemorySession(reqB);
    sessionB.setSnapshot({
      draft: makeDraft(reqB),
      intent: { id: "op-b", body: { workspace_id: "source-b" }, target: reqB.target, phase: "created", created: createdB },
    });

    const pSearchA1 = deferred<WorktreeSourcesResult>();
    const pPresetA1 = deferred<{ preset: SparsePreset }>();
    const pPrepareA2 = deferred<WorktreePrepare>();
    let prepareCalls = 0;
    const onPrepare = vi.fn((source: WorktreeRequest["target"]) => {
      prepareCalls++;
      if (source.workspaceId === "source-a" && prepareCalls === 1) return Promise.resolve(advancedPrepare);
      if (source.workspaceId === "source-a") return pPrepareA2.promise;
      return Promise.resolve({ ...advancedPrepare, repoName: "Demo B" });
    });
    const onSearchSources = vi.fn().mockReturnValueOnce(pSearchA1.promise);
    const onSavePreset = vi.fn().mockReturnValueOnce(pPresetA1.promise);
    const onStartAgent = vi.fn();

    const mountSession = async (request: WorktreeRequest, session: WorktreeComposerSession, extra: Partial<NewWorktreeProps> = {}) => {
      await act(async () => root.render(
        <NewWorktree
          request={request}
          onClose={onClose}
          session={session}
          owner={OWNER}
          machines={sampleMachines}
          catalog={sampleCatalog}
          isAdmitted={() => true}
          onPrepare={onPrepare}
          onSearchSources={onSearchSources}
          onSavePreset={onSavePreset}
          onStartAgent={onStartAgent}
          {...extra}
        />,
      ));
      await flush();
    };

    // Start in-flight search & preset save on session A, plus type preset drafts.
    await mountSession(reqA, sessionA);
    await setField(t("worktree-composer.search_source"), "stale-query");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    await setField(t("worktree-composer.preset_name"), "Stale preset");
    await setField(t("worktree-composer.directories"), "packages/a");
    await act(async () => { button(t("worktree-composer.save_preset"))!.click(); });

    // Switch to session B without unmounting: created stage is clean and startAgent works.
    await mountSession(reqB, sessionB);
    await act(async () => { button(t("worktree-composer.start_agent"))!.click(); });
    expect(onStartAgent).toHaveBeenCalledWith(expect.objectContaining({ workspaceId: "wt-b" }));

    // Switch back to session A (new instance or same session after remount): preset inputs and launched flag are clean.
    const sessionA2 = makeMemorySession(reqA);
    sessionA2.setSnapshot({
      draft: makeDraft(reqA),
      intent: { id: "op-a", body: { workspace_id: "source-a" }, target: reqA.target, phase: "created", created: createdA },
    });
    await mountSession(reqA, sessionA2);
    expect(button(t("worktree-composer.start_agent"))!.disabled).toBe(false);

    // Late settlement of session A1's search and preset save must not throw or leak state.
    await act(async () => {
      pSearchA1.resolve({ items: [{ type: "issue", number: 99, title: "Stale item", url: "https://github.com/example/demo/issues/99" }] });
      pPresetA1.reject(new Error("stale preset failure"));
      pPrepareA2.resolve(advancedPrepare);
    });
    await flush();
    expect(document.body.textContent).not.toContain("Stale item");
    expect(document.body.textContent).not.toContain("stale preset failure");
    expectNoDefaultSingletons();
  });

  it("blocks submit and ignores in-flight prepare when controlled admission is revoked, and supports Paseo agent-only open", async () => {
    const req = makeRequest("paseo-1", "paseo");
    const created: WorktreeCreated = { workspace_id: "wks-2", path: "/paseo/wt", branch: "feat/paseo" };
    const session = makeMemorySession(req);
    const pendingPrepare = deferred<WorktreePrepare>();
    const onPrepare = vi.fn().mockReturnValue(pendingPrepare.promise);
    const onStartAgent = vi.fn();
    let admitted = true;

    const mountWith = async (owner = OWNER) => {
      await act(async () => root.render(
        <NewWorktree
          request={req}
          onClose={onClose}
          session={session}
          owner={owner}
          machines={sampleMachines}
          catalog={{ owner, manager: MANAGER, workspaces: [] }}
          isAdmitted={() => admitted}
          onPrepare={onPrepare}
          onStartAgent={onStartAgent}
        />,
      ));
      await flush();
    };

    await mountWith();
    admitted = false;
    await mountWith("other-owner");
    expect(document.body.textContent).toContain(t("worktree.owner_changed"));
    await act(async () => { pendingPrepare.resolve({ root: "/p", repoName: "Demo", branch: "main", path: "/paseo" }); });
    await flush();
    expect(document.body.textContent).toContain(t("worktree.owner_changed"));

    // Restore admission on created Paseo workspace: shows primary Start agent, no Open button.
    admitted = true;
    await act(async () => {
      session.setSnapshot({
        draft: makeDraft(req),
        intent: { id: "op-paseo", body: { workspace_id: "paseo-1", kind: "paseo" }, target: req.target, phase: "created", created },
      });
    });
    await mountWith(OWNER);
    expect(button(t("worktree-composer.open"))).toBeUndefined();
    await act(async () => { button(t("worktree-composer.start_agent"))!.click(); });
    expect(onStartAgent).toHaveBeenCalledWith(expect.objectContaining({ workspaceId: "wks-2", host: "paseo" }));
    expectNoDefaultSingletons();
  });

  it("blocks synchronous create when controlled admission is revoked before re-render", async () => {
    const req = makeRequest();
    const onSubmit = vi.fn();
    const session = makeMemorySession(req, onSubmit);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    let admitted = true;

    await act(async () => root.render(
      <NewWorktree
        request={req}
        onClose={onClose}
        session={session}
        owner={OWNER}
        machines={sampleMachines}
        catalog={sampleCatalog}
        isAdmitted={() => admitted}
        onPrepare={onPrepare}
      />,
    ));
    await flush();

    // Revoke admission synchronously without re-rendering NewWorktree.
    admitted = false;
    await act(async () => {
      button(t("worktree-composer.create"))!.click();
      document.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    await flush();

    expect(onSubmit).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("blocks synchronous preset save when controlled admission is revoked before re-render", async () => {
    const req = makeRequest();
    const session = makeMemorySession(req);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const onSavePreset = vi.fn().mockResolvedValue({
      preset: { id: "preset-blocked", name: "Blocked", directories: ["web"] } satisfies SparsePreset,
    });
    let admitted = true;

    await act(async () => root.render(
      <NewWorktree
        request={req}
        onClose={onClose}
        session={session}
        owner={OWNER}
        machines={sampleMachines}
        catalog={sampleCatalog}
        isAdmitted={() => admitted}
        onPrepare={onPrepare}
        onSavePreset={onSavePreset}
      />,
    ));
    await flush();

    await setField(t("worktree-composer.preset_name"), "Blocked");
    await setField(t("worktree-composer.directories"), "web");

    // Revoke admission synchronously without re-rendering NewWorktree.
    admitted = false;
    await act(async () => {
      button(t("worktree-composer.save_preset"))!.click();
    });
    await flush();

    expect(onSavePreset).not.toHaveBeenCalled();
    expect(repositoryDraft(session.getSnapshot().draft).preset).toBe("");
    expectNoDefaultSingletons();
  });

  it("blocks synchronous provider search when controlled admission is revoked before re-render", async () => {
    const req = makeRequest();
    const session = makeMemorySession(req);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const onSearchSources = vi.fn().mockResolvedValue({
      items: [{ type: "issue", number: 12, title: "Should not run", url: "https://github.com/example/demo/issues/12" }],
    } satisfies WorktreeSourcesResult);
    let admitted = true;

    await act(async () => root.render(
      <NewWorktree
        request={req}
        onClose={onClose}
        session={session}
        owner={OWNER}
        machines={sampleMachines}
        catalog={sampleCatalog}
        isAdmitted={() => admitted}
        onPrepare={onPrepare}
        onSearchSources={onSearchSources}
      />,
    ));
    await flush();

    await setField(t("worktree-composer.search_source"), "auth");

    // Revoke admission synchronously without re-rendering NewWorktree.
    admitted = false;
    await act(async () => {
      button(t("worktree-composer.search"))!.click();
      field(t("worktree-composer.search_source")).dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    await flush();

    expect(onSearchSources).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain("Should not run");
    expectNoDefaultSingletons();
  });

  it("prevents wrong-owner POSTs across create, preset save, search and created actions on owner A to B switch", async () => {
    const OWNER_A = "owner-a";
    const OWNER_B = "owner-b";
    let liveAuth = { owner: OWNER_A, token: "token-a" };
    const dispatched: { op: string; workspaceId: string; owner?: string; token: string }[] = [];

    const reqA: WorktreeRequest = {
      target: { mid: "computer", workspaceId: "source-a", host: "orca", cwd: "/projects/source-a", label: "source-a", owner: OWNER_A, manager: MANAGER },
      changed: vi.fn(),
      onCreated: vi.fn(),
    };
    const createdA: WorktreeCreated = { workspace_id: "wt-a", path: "/worktrees/wt-a", branch: "feat/a", terminal: "pane:a1" };
    const sessionA = makeMemorySession(reqA, async (draft) => {
      dispatched.push({ op: "create", workspaceId: draft.source.workspaceId, owner: draft.source.owner, token: liveAuth.token });
      return {
        id: "op-a",
        body: { workspace_id: draft.source.workspaceId, kind: draft.source.host, composer: true },
        target: draft.source,
        phase: "created",
        created: createdA,
      };
    });
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const onSearchSources = vi.fn(async (source) => {
      dispatched.push({ op: "search", workspaceId: source.workspaceId, owner: source.owner, token: liveAuth.token });
      return { items: [{ type: "issue", number: 31, title: "Wrong owner issue", url: "https://github.com/example/demo/issues/31" }] } satisfies WorktreeSourcesResult;
    });
    const onSavePreset = vi.fn(async (source) => {
      dispatched.push({ op: "preset", workspaceId: source.workspaceId, owner: source.owner, token: liveAuth.token });
      return { preset: { id: "preset-wrong-owner", name: "Wrong", directories: ["web"] } satisfies SparsePreset };
    });
    const onOpenCreated = vi.fn();
    const onStartAgent = vi.fn();

    await act(async () => root.render(
      <NewWorktree
        request={reqA}
        onClose={onClose}
        session={sessionA}
        owner={OWNER_A}
        machines={sampleMachines}
        catalog={{ owner: OWNER_A, manager: MANAGER, workspaces: sampleWorkspaces }}
        isAdmitted={(target) => target.owner === liveAuth.owner}
        onPrepare={onPrepare}
        onSearchSources={onSearchSources}
        onSavePreset={onSavePreset}
        onOpenCreated={onOpenCreated}
        onStartAgent={onStartAgent}
      />,
    ));
    await flush();

    await setField(t("worktree-composer.search_source"), "leak");
    await setField(t("worktree-composer.preset_name"), "Leak");
    await setField(t("worktree-composer.directories"), "web");

    // Switch live auth owner A -> B before React re-renders NewWorktree with owner B.
    liveAuth = { owner: OWNER_B, token: "token-b" };
    await act(async () => {
      button(t("worktree-composer.create"))!.click();
      document.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      button(t("worktree-composer.save_preset"))!.click();
      button(t("worktree-composer.search"))!.click();
    });
    await flush();

    expect(dispatched).toEqual([]);
    expect(onSavePreset).not.toHaveBeenCalled();
    expect(onSearchSources).not.toHaveBeenCalled();

    // Preserve Start/Open live-admission boundary on created stage too.
    liveAuth = { owner: OWNER_A, token: "token-a" };
    await act(async () => {
      sessionA.setSnapshot({
        draft: makeDraft(reqA),
        intent: { id: "op-a-created", body: { workspace_id: "source-a", kind: "orca", composer: true }, target: reqA.target, phase: "created", created: createdA },
      });
    });
    await flush();

    liveAuth = { owner: OWNER_B, token: "token-b" };
    await act(async () => {
      button(t("worktree-composer.open"))!.click();
      button(t("worktree-composer.start_agent"))!.click();
    });
    expect(onOpenCreated).not.toHaveBeenCalled();
    expect(onStartAgent).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("rejects stale in-flight provider results and preset completions after admission revoke/restore ABA", async () => {
    const req = makeRequest();
    const session = makeMemorySession(req);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const pSearch1 = deferred<WorktreeSourcesResult>();
    const pSearch2 = deferred<WorktreeSourcesResult>();
    const pSearchFresh = deferred<WorktreeSourcesResult>();
    const pPreset1 = deferred<{ preset: SparsePreset }>();
    const signals: Array<AbortSignal | undefined> = [];
    const onSearchSources = vi.fn((_source, _query, signal?: AbortSignal) => {
      signals.push(signal);
      if (signals.length === 1) return pSearch1.promise;
      if (signals.length === 2) return pSearch2.promise;
      return pSearchFresh.promise;
    });
    const onSavePreset = vi.fn().mockReturnValueOnce(pPreset1.promise);
    let admitted = true;

    const mountWith = async () => {
      await act(async () => root.render(
        <NewWorktree
          request={req}
          onClose={onClose}
          session={session}
          owner={OWNER}
          machines={sampleMachines}
          catalog={sampleCatalog}
          isAdmitted={() => admitted}
          onPrepare={onPrepare}
          onSearchSources={onSearchSources}
          onSavePreset={onSavePreset}
        />,
      ));
      await flush();
    };

    await mountWith();
    await setField(t("worktree-composer.search_source"), "before-revoke");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    await setField(t("worktree-composer.preset_name"), "Before revoke");
    await setField(t("worktree-composer.directories"), "web");
    await act(async () => { button(t("worktree-composer.save_preset"))!.click(); });
    expect(signals[0]?.aborted).toBe(false);

    // Revoke admission and re-render.
    admitted = false;
    await mountWith();
    expect(document.body.textContent).toContain(t("worktree.owner_changed"));
    expect(signals[0]?.aborted).toBe(true);

    // Restore admission (ABA) and re-render.
    admitted = true;
    await mountWith();
    expect(document.body.textContent).not.toContain(t("worktree.owner_changed"));

    // Late resolution of pre-revoke search & preset must not populate the restored scope.
    await act(async () => {
      pSearch1.resolve({
        items: [{ type: "issue", number: 88, title: "Stale revoked issue", url: "https://github.com/example/demo/issues/88" }],
      });
      pPreset1.resolve({
        preset: { id: "stale-preset", name: "Stale preset", directories: ["web"] },
      });
    });
    await flush();
    expect(document.body.textContent).not.toContain("Stale revoked issue");
    expect(repositoryDraft(session.getSnapshot().draft).preset).toBe("");

    // Late error from a revoked search must also be ignored after revoke/restore ABA.
    await setField(t("worktree-composer.search_source"), "second-before-revoke");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    admitted = false;
    await mountWith();
    admitted = true;
    await mountWith();
    await act(async () => { pSearch2.reject(new Error("stale search error")); });
    await flush();
    expect(document.body.textContent).not.toContain("stale search error");

    // A fresh search in the restored scope still works normally.
    await setField(t("worktree-composer.search_source"), "fresh-after-restore");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    await act(async () => {
      pSearchFresh.resolve({
        items: [{ type: "issue", number: 101, title: "Fresh restored issue", url: "https://github.com/example/demo/issues/101" }],
      });
    });
    await flush();
    expect(document.body.textContent).toContain("Fresh restored issue");
    expectNoDefaultSingletons();
  });

  it("blocks stale source A actions when the live session snapshot switches source before re-render", async () => {
    const reqA = makeRequest("source-a");
    const reqB = makeRequest("source-b");
    const onSubmit = vi.fn();
    const session = makeMemorySession(reqA, onSubmit);
    const onPrepare = vi.fn().mockResolvedValue(advancedPrepare);
    const onSearchSources = vi.fn().mockResolvedValue({ items: [] } satisfies WorktreeSourcesResult);
    const onSavePreset = vi.fn().mockResolvedValue({ preset: { id: "p-b", name: "P", directories: ["web"] } satisfies SparsePreset });

    await act(async () => root.render(
      <NewWorktree
        request={reqA}
        onClose={onClose}
        session={session}
        owner={OWNER}
        machines={sampleMachines}
        catalog={sampleCatalog}
        isAdmitted={() => true}
        onPrepare={onPrepare}
        onSearchSources={onSearchSources}
        onSavePreset={onSavePreset}
      />,
    ));
    await flush();

    await setField(t("worktree-composer.search_source"), "query-a");
    await setField(t("worktree-composer.preset_name"), "Preset A");
    await setField(t("worktree-composer.directories"), "web");

    // Mutate session snapshot to source-b synchronously right before dispatching clicks on the source-a DOM.
    await act(async () => {
      session.setSnapshot({ draft: makeDraft(reqB) });
      button(t("worktree-composer.create"))!.click();
      button(t("worktree-composer.save_preset"))!.click();
      button(t("worktree-composer.search"))!.click();
    });
    await flush();

    expect(onSubmit).not.toHaveBeenCalled();
    expect(onSavePreset).not.toHaveBeenCalled();
    expect(onSearchSources).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });
});

describe("NewWorktree default compatibility", () => {
  it("falls back to default creationSession, credentialOwner, store, catalog, prepare, search, preset, openCreated and openNewAgent", async () => {
    const request = makeRequest();
    const created: WorktreeCreated = { workspace_id: "default-wt", path: "/worktrees/default-wt", branch: "feat/default", terminal: "pane:d1" };
    const defaultSession = makeMemorySession(request, async (draft) => ({
      id: "op-default",
      body: { workspace_id: draft.source.workspaceId, kind: draft.source.host, composer: true },
      target: draft.source,
      phase: "created",
      created,
    }));
    mocks.creationSession.mockResolvedValueOnce(defaultSession);

    await act(async () => root.render(<NewWorktree request={request} onClose={onClose} />));
    await flush();

    expect(mocks.creationSession).toHaveBeenCalledWith(request);
    expect(mocks.subscribeCredentialOwner).toHaveBeenCalled();
    expect(mocks.useKnownDefault).toHaveBeenCalled();
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useWorkspaceCatalog).toHaveBeenCalledWith(true);
    expect(mocks.worktreeAdmission).toHaveBeenCalled();
    expect(mocks.prepareWorktree).toHaveBeenCalledWith(request.target, expect.objectContaining({ composer: true }));

    await setField(t("worktree-composer.search_source"), "abc");
    await act(async () => { button(t("worktree-composer.search"))!.click(); });
    await flush();
    expect(mocks.worktreeSources).toHaveBeenCalledWith(request.target, "abc", expect.any(AbortSignal));

    await setField(t("worktree-composer.preset_name"), "Core");
    await setField(t("worktree-composer.directories"), "web");
    await act(async () => { button(t("worktree-composer.save_preset"))!.click(); });
    await flush();
    expect(mocks.saveWorktreePreset).toHaveBeenCalledWith(request.target, "Core", "web");

    await act(async () => { button(t("worktree-composer.create"))!.click(); });
    await flush();

    await act(async () => { button(t("worktree-composer.open"))!.click(); });
    expect(mocks.openCreated).toHaveBeenCalledWith("computer", created);

    await act(async () => { button(t("worktree-composer.start_agent"))!.click(); });
    expect(mocks.openNewAgent).toHaveBeenCalledWith({ workspace: expect.objectContaining({ workspaceId: "default-wt" }) });
  });

  it("revalidates live default credentialOwner and worktreeAdmission before synchronous create, preset save and search", async () => {
    const request = makeRequest();
    const onSubmit = vi.fn();
    const defaultSession = makeMemorySession(request, onSubmit);
    mocks.creationSession.mockResolvedValueOnce(defaultSession);

    await act(async () => root.render(<NewWorktree request={request} onClose={onClose} />));
    await flush();

    await setField(t("worktree-composer.search_source"), "default-q");
    await setField(t("worktree-composer.preset_name"), "Default P");
    await setField(t("worktree-composer.directories"), "web");

    // Simulate synchronous credential owner rotation before React re-renders.
    mocks.credentialOwner.mockReturnValue("other-owner");
    mocks.worktreeAdmission.mockReturnValue(false);
    await act(async () => {
      button(t("worktree-composer.create"))!.click();
      button(t("worktree-composer.save_preset"))!.click();
      button(t("worktree-composer.search"))!.click();
    });
    await flush();

    expect(onSubmit).not.toHaveBeenCalled();
    expect(mocks.saveWorktreePreset).not.toHaveBeenCalled();
    expect(mocks.worktreeSources).not.toHaveBeenCalled();
  });
});
