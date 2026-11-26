// @vitest-environment jsdom
// Controlled ActiveWorktreeActions bypasses default store/catalog/API/mutation singletons, caches fresh-array/object
// getters, resets dialogs and fences stale/ABA callbacks across scope/target switches, and keeps default fallbacks intact.
import { act, startTransition, StrictMode, Suspense, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetManagerChoice, setRosterDefault } from "../lib/manager-choice";
import { NO_PINS, type AgentPins } from "../lib/pins";
import type { Machine, Session } from "../lib/types";
import type { WorkspaceCatalogSnapshot } from "../lib/workspace-catalog";
import type { OwnedWorkspaceTarget, ResolvedWorktreeAction, WorktreeActionsRequest } from "../lib/worktree-actions";
import type { WorktreeMutationState } from "../lib/worktree-actions-state";
import type { WorktreeMutationRegistry } from "./WorktreeActions";
import type { WorktreeActionsAccess, WorktreeActionsMenuDeps, WorktreeActionsMenuViewProps, WorktreeActionsViewMode, WorktreeMutationDeps, WorktreeMutationLike } from "./WorktreeActionsMenu";

const mocks = vi.hoisted(() => ({
  catalog: { owner: "default-owner", manager: "default-manager", workspaces: [] } as WorkspaceCatalogSnapshot,
  storeState: {
    machines: [] as Machine[],
    pins: { spaces: [], sessions: [] } as AgentPins,
    sessions: {} as Record<string, Session[]>,
    sessionSummaries: [] as Session[],
  },
  owner: "default-owner",
  currentIds: new Set<number>(),
  useStore: vi.fn(),
  getState: vi.fn(),
  useWorkspaceCatalog: vi.fn(),
  refreshWorkspaceCatalog: vi.fn(),
  api: vi.fn(),
  credentialOwner: vi.fn(),
  worktreeActionsCurrent: vi.fn(),
  closeWorktreeActions: vi.fn(),
  navigate: vi.fn(),
  openNewAgent: vi.fn(),
  openChildWorktree: vi.fn(),
  copyText: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../lib/store", () => ({
  useStore: (select: (s: typeof mocks.storeState) => unknown) => {
    mocks.useStore(select);
    return select(mocks.storeState);
  },
  getState: () => {
    mocks.getState();
    return mocks.storeState;
  },
}));
vi.mock("../lib/workspace-catalog", async (original) => ({
  ...(await original<typeof import("../lib/workspace-catalog")>()),
  useWorkspaceCatalog: (enabled: boolean) => {
    mocks.useWorkspaceCatalog(enabled);
    return mocks.catalog;
  },
  refreshWorkspaceCatalog: (...args: unknown[]) => mocks.refreshWorkspaceCatalog(...args),
}));
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: (...args: unknown[]) => mocks.api(...args),
  credentialOwner: () => {
    mocks.credentialOwner();
    return mocks.owner;
  },
}));
vi.mock("../lib/worktree-actions", async (original) => ({
  ...(await original<typeof import("../lib/worktree-actions")>()),
  worktreeActionsCurrent: (id: number) => {
    mocks.worktreeActionsCurrent(id);
    return mocks.currentIds.has(id);
  },
  closeWorktreeActions: (id: number) => {
    mocks.closeWorktreeActions(id);
    mocks.currentIds.delete(id);
  },
}));
vi.mock("../lib/router", () => ({
  navigate: (...args: unknown[]) => mocks.navigate(...args),
}));
vi.mock("../lib/new-agent", () => ({
  openNewAgent: (...args: unknown[]) => mocks.openNewAgent(...args),
}));
vi.mock("../lib/worktree-creation", () => ({
  openChildWorktree: (...args: unknown[]) => mocks.openChildWorktree(...args),
}));
vi.mock("../ui/atoms", async (original) => ({
  ...(await original<typeof import("../ui/atoms")>()),
  copyText: (...args: unknown[]) => mocks.copyText(...args),
  toast: (...args: unknown[]) => mocks.toast(...args),
}));

const { ActiveWorktreeActions, WorktreeActionsMenuView } = await import("./WorktreeActionsMenu");
const { initI18n, t } = await import("../i18n");

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

interface FakeMutation extends WorktreeMutationLike {
  deps: WorktreeMutationDeps;
  setState: (patch: Partial<WorktreeMutationState>) => void;
  freshSnapshot: boolean;
  activate: ReturnType<typeof vi.fn<WorktreeMutationLike["activate"]>>;
  cancel: ReturnType<typeof vi.fn<WorktreeMutationLike["cancel"]>>;
  invalidate: ReturnType<typeof vi.fn<WorktreeMutationLike["invalidate"]>>;
  dispose: ReturnType<typeof vi.fn<WorktreeMutationLike["dispose"]>>;
  rename: ReturnType<typeof vi.fn<WorktreeMutationLike["rename"]>>;
  preview: ReturnType<typeof vi.fn<WorktreeMutationLike["preview"]>>;
  remove: ReturnType<typeof vi.fn<WorktreeMutationLike["remove"]>>;
  reconcile: ReturnType<typeof vi.fn<WorktreeMutationLike["reconcile"]>>;
}

const createFakeMutation = (target: OwnedWorkspaceTarget, initialDeps: WorktreeMutationDeps, freshSnapshot = false): FakeMutation => {
  let state: WorktreeMutationState = { phase: "idle", error: "", dispatched: false };
  const listeners = new Set<() => void>();
  const model: FakeMutation = {
    target,
    deps: initialDeps,
    freshSnapshot,
    activate: vi.fn<WorktreeMutationLike["activate"]>(),
    updateDependencies(next: WorktreeMutationDeps) {
      model.deps = next;
    },
    snapshot: () => (model.freshSnapshot ? { ...state } : state),
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    setState(patch: Partial<WorktreeMutationState>) {
      state = { ...state, ...patch };
      listeners.forEach((l) => l());
    },
    cancel: vi.fn<WorktreeMutationLike["cancel"]>(() => {
      if (["renaming", "previewing", "deleting", "checking"].includes(state.phase)) {
        model.setState({ phase: state.dispatched ? "uncertain" : "idle" });
      }
    }),
    invalidate: vi.fn<WorktreeMutationLike["invalidate"]>(() => {
      model.setState({ phase: "invalid", error: "invalidated" });
    }),
    dispose: vi.fn<WorktreeMutationLike["dispose"]>(),
    rename: vi.fn<WorktreeMutationLike["rename"]>().mockResolvedValue(undefined),
    preview: vi.fn<WorktreeMutationLike["preview"]>().mockResolvedValue(undefined),
    remove: vi.fn<WorktreeMutationLike["remove"]>().mockResolvedValue(undefined),
    reconcile: vi.fn<WorktreeMutationLike["reconcile"]>().mockResolvedValue(undefined),
  };
  return model;
};

const makeTarget = (id = "work-1", overrides: Partial<OwnedWorkspaceTarget> = {}): OwnedWorkspaceTarget => ({
  owner: "acct-1",
  manager: "mgr-1",
  machineId: "m1",
  host: "orca",
  workspaceId: id,
  cwd: `/repo/${id}`,
  label: `Work ${id}`,
  ...overrides,
});

const makeCatalog = (target: OwnedWorkspaceTarget): WorkspaceCatalogSnapshot => ({
  owner: target.owner,
  manager: target.manager,
  workspaces: [
    { id: `row-${target.workspaceId}`, machine_id: target.machineId, source: target.host, source_id: target.workspaceId, title: target.label || target.workspaceId, cwd: target.cwd, parent_source_id: "parent-1", available: true, online: true },
    { id: "row-parent", machine_id: target.machineId, source: target.host, source_id: "parent-1", title: "Parent", cwd: "/repo" },
  ],
});

const makeResolved = (target: OwnedWorkspaceTarget, catalog: WorkspaceCatalogSnapshot, machines: readonly Machine[], writable: boolean, linkedWorktree = false): ResolvedWorktreeAction => {
  const workspace = catalog.workspaces.find((w) => w.source_id === target.workspaceId);
  const machine = machines.find((m) => m.id === target.machineId);
  const parentRow = catalog.workspaces.find((w) => w.source_id === workspace?.parent_source_id);
  const available = Boolean(workspace && machine?.online !== false);
  const canWrite = available && writable;
  return {
    workspace,
    machine,
    parent: parentRow ? { owner: target.owner, manager: target.manager, machineId: target.machineId, host: target.host, workspaceId: parentRow.source_id, cwd: parentRow.cwd, label: parentRow.title } : undefined,
    available,
    writable: canWrite,
    reason: !available ? t("worktree-actions.unavailable") : !writable ? t("worktree-actions.read_only") : "",
    hasChildren: false,
    canRename: canWrite,
    canCreate: canWrite,
    canReconcile: Boolean(machine?.online !== false && writable),
    canClose: canWrite && target.host === "herdr",
    canDelete: canWrite && (target.host === "orca" || (target.host === "herdr" && linkedWorktree)),
  };
};

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.useWorkspaceCatalog).not.toHaveBeenCalled();
  expect(mocks.refreshWorkspaceCatalog).not.toHaveBeenCalled();
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
  expect(mocks.worktreeActionsCurrent).not.toHaveBeenCalled();
  expect(mocks.closeWorktreeActions).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
  expect(mocks.openNewAgent).not.toHaveBeenCalled();
  expect(mocks.openChildWorktree).not.toHaveBeenCalled();
  expect(mocks.copyText).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
};

let host: HTMLDivElement;
let anchor: HTMLButtonElement;
let root: Root;

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  Object.values(mocks).forEach((v) => {
    if (typeof v === "function") v.mockReset();
  });
  mocks.owner = "default-owner";
  mocks.currentIds = new Set([1]);
  mocks.catalog = makeCatalog(makeTarget("work-1", { owner: "default-owner", manager: "default-manager" }));
  mocks.storeState = {
    machines: [{ id: "m1", manager: "default-manager", name: "Computer", online: true, engine_hosts: ["orca", "herdr"] }],
    pins: NO_PINS,
    sessions: {},
    sessionSummaries: [],
  };
  mocks.refreshWorkspaceCatalog.mockResolvedValue(undefined);
  mocks.copyText.mockResolvedValue(undefined);
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} }));
  host = document.createElement("div");
  anchor = document.createElement("button");
  anchor.textContent = "Anchor";
  document.body.append(anchor, host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  anchor.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ActiveWorktreeActions controlled deps, view separation, snapshot caching, fencing, and error recovery", () => {
  it("runs fully controlled without touching default singletons and renders pure WorktreeActionsMenuView", async () => {
    const target = makeTarget("work-1");
    const catalog = makeCatalog(target);
    const machines: Machine[] = [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["orca"] }];
    const registry: WorktreeMutationRegistry = new Map();
    const request: WorktreeActionsRequest = { id: 10, target, anchor };
    let captured: WorktreeActionsMenuViewProps | null = null;
    let fakeModel!: FakeMutation;
    const nav = vi.fn();
    const openAgent = vi.fn();
    const openChild = vi.fn();
    const copy = vi.fn().mockResolvedValue(undefined);
    const refreshCat = vi.fn().mockResolvedValue(undefined);
    const notify = vi.fn();
    const closeRequest = vi.fn();
    const onOpenWorkspace = vi.fn();
    const directAgent: Session = { machine_id: "m1", session_id: "sess-1", title: "Direct", status: "idle", cwd: target.cwd, host: "orca", workspace_id: "work-1" };

    const deps: WorktreeActionsMenuDeps = {
      scopeKey: "acct-1",
      credentialOwner: "acct-1",
      isRequestCurrent: (id) => id === 10,
      closeRequest,
      catalog,
      machines,
      pins: NO_PINS,
      storeSessions: [{}, []],
      resolveAction: (tg, ctx) => makeResolved(tg, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
      firstDirectAgent: () => directAgent,
      checkAccess: vi.fn().mockResolvedValue("full"),
      createMutation: (tg, d) => {
        fakeModel = createFakeMutation(tg, d);
        return fakeModel;
      },
      navigate: nav,
      openNewAgent: openAgent,
      openChildWorktree: openChild,
      copyText: copy,
      refreshWorkspaceCatalog: refreshCat,
      notify,
      renderView: (props) => {
        captured = props;
        return <div data-controlled-wt>{props.view}:{props.header}</div>;
      },
    };

    await act(async () => root.render(
      <StrictMode>
        <ActiveWorktreeActions request={request} registry={registry} onOpenWorkspace={onOpenWorkspace} sessions={[directAgent]} deps={deps} />
      </StrictMode>,
    ));

    expect(host.querySelector("[data-controlled-wt]")?.textContent).toBe("menu:Work work-1");
    const byKey = (k: string) => captured!.items.find((i) => i.key === k)!;

    byKey("open").onSelect();
    expect(nav).toHaveBeenCalledExactlyOnceWith({ name: "session", mid: "m1", sid: "sess-1" });
    expect(closeRequest).toHaveBeenCalledWith(10);

    byKey("new-agent").onSelect();
    expect(openAgent).toHaveBeenCalledExactlyOnceWith({ workspace: target });

    await act(async () => { await byKey("child").onSelect(); });
    expect(openChild).toHaveBeenCalledExactlyOnceWith(target, { changed: expect.any(Function) });
    openChild.mock.calls[0][1].changed();
    expect(refreshCat).toHaveBeenCalledTimes(1);

    await act(async () => { await byKey("copy-name").onSelect(); });
    await act(async () => { await byKey("copy-path").onSelect(); });
    expect(copy).toHaveBeenNthCalledWith(1, "Work work-1");
    expect(copy).toHaveBeenNthCalledWith(2, "/repo/work-1");

    byKey("parent").onSelect();
    expect(onOpenWorkspace).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ workspaceId: "parent-1" }));

    await act(async () => { byKey("rename").onSelect(); });
    expect(captured!.view).toBe("rename");

    // Acknowledged mutation notifies, refreshes catalog, and closes.
    await act(async () => { fakeModel.deps.acknowledged("rename"); });
    expect(notify).toHaveBeenCalledWith(t("worktree-actions.renamed"), "ok");
    expect(refreshCat).toHaveBeenCalledTimes(2);

    // Pure WorktreeActionsMenuView renders custom renderers without touching default singletons.
    await act(async () => root.render(
      <WorktreeActionsMenuView
        {...captured!}
        view="menu"
        renderMenu={({ header }) => <div data-pure-menu>{header}</div>}
      />,
    ));
    expect(host.querySelector("[data-pure-menu]")?.textContent).toBe("Work work-1");
    expectNoDefaultSingletons();
  });

  it("caches fresh-array/object getters and model snapshots while preserving bridge-switch stability and controlled view reset", async () => {
    const target = makeTarget("work-1");
    const catalogListeners = new Set<() => void>();
    const storeListeners = new Set<() => void>();
    const scopeListeners = new Set<() => void>();
    const sub = (set: Set<() => void>) => (l: () => void) => {
      set.add(l);
      return () => { set.delete(l); };
    };
    let currentScope = "acct-1";
    let title = "Work work-1";
    let controlledView: WorktreeActionsViewMode = "menu";
    const onViewChange = vi.fn((next: WorktreeActionsViewMode) => { controlledView = next; });
    const registry: WorktreeMutationRegistry = new Map();
    let captured: WorktreeActionsMenuViewProps | null = null;

    const baseDeps = (asGetter: boolean): WorktreeActionsMenuDeps => ({
      scopeKey: asGetter ? () => currentScope : currentScope,
      subscribeScopeKey: asGetter ? sub(scopeListeners) : undefined,
      credentialOwner: "acct-1",
      isRequestCurrent: () => true,
      closeRequest: vi.fn(),
      catalog: asGetter ? () => ({
        ...makeCatalog(target),
        workspaces: makeCatalog(target).workspaces.map((w) => (w.source_id === "work-1" ? { ...w, title } : { ...w })),
      }) : makeCatalog(target),
      subscribeCatalog: asGetter ? sub(catalogListeners) : undefined,
      machines: asGetter ? () => [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["orca"] }] : [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["orca"] }],
      pins: asGetter ? () => ({ spaces: [], sessions: [] }) : NO_PINS,
      storeSessions: asGetter ? () => [{}, []] : [{}, []],
      subscribeStore: asGetter ? sub(storeListeners) : undefined,
      resolveAction: (tg, ctx) => makeResolved(tg, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
      firstDirectAgent: () => undefined,
      access: "full",
      createMutation: (tg, d) => createFakeMutation(tg, d, true), // fresh snapshot object every read
      view: controlledView,
      onViewChange,
      renderView: (props) => {
        captured = props;
        return null;
      },
    });

    const request: WorktreeActionsRequest = { id: 1, target, anchor };
    await act(async () => root.render(
      <StrictMode><ActiveWorktreeActions request={request} registry={registry} deps={baseDeps(false)} /></StrictMode>,
    ));
    const initialKey = captured!.viewKey;

    // Open delete dialog, then switch from direct values to getter+subscribe on the SAME scope: dialog stays open (no spurious remount).
    await act(async () => { captured!.items.find((i) => i.key === "delete")!.onSelect(); });
    expect(controlledView).toBe("delete");
    await act(async () => root.render(
      <StrictMode><ActiveWorktreeActions request={request} registry={registry} deps={baseDeps(true)} /></StrictMode>,
    ));
    expect(captured!.view).toBe("delete");
    expect(captured!.viewKey).toBe(initialKey);

    // Emit unchanged fresh-object/array snapshots: no infinite loop, dialog stays open.
    await act(async () => {
      catalogListeners.forEach((l) => l());
      storeListeners.forEach((l) => l());
    });
    expect(captured!.view).toBe("delete");

    // Emit real title update through fresh getter.
    title = "Renamed Live";
    await act(async () => { catalogListeners.forEach((l) => l()); });
    expect(captured!.header).toBe("Renamed Live");

    // Switch scopeKey while on getter+subscribe: outer component resets controlled view to "menu" via onViewChange!
    onViewChange.mockClear();
    currentScope = "acct-2";
    await act(async () => { scopeListeners.forEach((l) => l()); });
    expect(onViewChange).toHaveBeenCalledWith("menu");
    expect(captured!.viewKey).not.toBe(initialKey);
    expectNoDefaultSingletons();
  });

  it("resets dialogs/probes, cancels non-dispatched models, and fences stale/ABA callbacks across target and owner switches", async () => {
    const scopeListeners = new Set<() => void>();
    const ownerListeners = new Set<() => void>();
    let currentScope = "acct-1";
    let currentOwner = "acct-1";
    const targetA = makeTarget("work-a", { host: "herdr" });
    const targetB = makeTarget("work-b", { host: "herdr" });
    const accessGates = [deferred<WorktreeActionsAccess>(), deferred<WorktreeActionsAccess>(), deferred<WorktreeActionsAccess>()];
    const statusGates = [deferred<boolean>(), deferred<boolean>()];
    const accessSignals: AbortSignal[] = [];
    const statusSignals: AbortSignal[] = [];
    let accessCall = 0;
    let statusCall = 0;
    const models: FakeMutation[] = [];
    const nav = vi.fn();
    const openAgent = vi.fn();
    const openChild = vi.fn();
    const copy = vi.fn().mockResolvedValue(undefined);
    const refreshCat = vi.fn().mockResolvedValue(undefined);
    const notify = vi.fn();
    const closeRequest = vi.fn();
    const onOpenWorkspace = vi.fn();
    const registry: WorktreeMutationRegistry = new Map();
    let captured: WorktreeActionsMenuViewProps | null = null;

    const makeDeps = (tg: OwnedWorkspaceTarget): WorktreeActionsMenuDeps => ({
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      credentialOwner: () => currentOwner,
      subscribeCredentialOwner: (l) => { ownerListeners.add(l); return () => { ownerListeners.delete(l); }; },
      isRequestCurrent: () => true,
      closeRequest,
      catalog: makeCatalog(tg),
      machines: [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["herdr"] }],
      pins: NO_PINS,
      storeSessions: [{}, []],
      resolveAction: (tTarget, ctx) => makeResolved(tTarget, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
      firstDirectAgent: () => undefined,
      checkAccess: (_t, signal) => {
        accessSignals.push(signal);
        return accessGates[accessCall++]?.promise ?? Promise.resolve("full");
      },
      checkWorktreeStatus: (_t, signal) => {
        statusSignals.push(signal);
        return statusGates[statusCall++]?.promise ?? Promise.resolve(true);
      },
      createMutation: (tTarget, d) => {
        const m = createFakeMutation(tTarget, d);
        models.push(m);
        return m;
      },
      navigate: nav,
      openNewAgent: openAgent,
      openChildWorktree: openChild,
      copyText: copy,
      refreshWorkspaceCatalog: refreshCat,
      notify,
      renderView: (props) => {
        captured = props;
        return null;
      },
    });

    await act(async () => root.render(
      <ActiveWorktreeActions request={{ id: 1, target: targetA, anchor }} registry={registry} onOpenWorkspace={onOpenWorkspace} deps={makeDeps(targetA)} />,
    ));
    await act(async () => { accessGates[0].resolve("full"); await Promise.resolve(); });
    await act(async () => { statusGates[0].resolve(true); await Promise.resolve(); });

    const staleViewA = captured!;
    const staleModelA = models[0];
    // Open delete dialog and mark modelA as previewing (non-dispatched) + capture child changed callback.
    await act(async () => { staleViewA.items.find((i) => i.key === "delete")!.onSelect(); });
    expect(captured!.view).toBe("delete");
    await act(async () => { staleModelA.setState({ phase: "previewing" }); });
    await act(async () => { await staleViewA.items.find((i) => i.key === "child")!.onSelect(); });
    const staleChildChanged = openChild.mock.calls[0][1].changed as () => void;
    openChild.mockClear();
    closeRequest.mockClear();

    // Switch in-place (same React component instance!) to targetB: dialog resets to "menu", old model cancels & disposes.
    await act(async () => root.render(
      <ActiveWorktreeActions request={{ id: 2, target: targetB, anchor }} registry={registry} onOpenWorkspace={onOpenWorkspace} deps={makeDeps(targetB)} />,
    ));
    expect(captured!.view).toBe("menu");
    expect(staleModelA.cancel).toHaveBeenCalled();
    expect(staleModelA.dispose).toHaveBeenCalled();

    // Switch back via ABA on scopeKey (acct-1 -> acct-2 -> acct-1) while targetB's access probe is in flight.
    await act(async () => {
      currentScope = "acct-2";
      scopeListeners.forEach((l) => l());
      currentScope = "acct-1";
      scopeListeners.forEach((l) => l());
    });
    expect(accessSignals[1].aborted).toBe(true);
    // Late resolution of aborted access probe for targetB epoch 2 must not overwrite epoch 3.
    await act(async () => { accessGates[1].resolve("readOnly"); await Promise.resolve(); });
    await act(async () => { accessGates[2].resolve("full"); await Promise.resolve(); });
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(false);

    // Every stale callback captured from targetA epoch 1 must be discarded!
    staleViewA.items.find((i) => i.key === "open")!.onSelect();
    staleViewA.items.find((i) => i.key === "new-agent")!.onSelect();
    await staleViewA.items.find((i) => i.key === "child")!.onSelect();
    staleViewA.items.find((i) => i.key === "rename")!.onSelect();
    await staleViewA.items.find((i) => i.key === "copy-name")!.onSelect();
    await staleViewA.items.find((i) => i.key === "copy-path")!.onSelect();
    staleViewA.items.find((i) => i.key === "parent")!.onSelect();
    staleViewA.items.find((i) => i.key === "close")!.onSelect();
    staleViewA.items.find((i) => i.key === "delete")!.onSelect();
    staleViewA.onClose();
    staleChildChanged();
    staleModelA.deps.acknowledged("delete");

    // Also test owner change fencing live.
    currentOwner = "acct-other";
    await act(async () => { ownerListeners.forEach((l) => l()); });
    expect(captured!.items.find((i) => i.key === "new-agent")).toBeTruthy();
    captured!.items.find((i) => i.key === "new-agent")!.onSelect();

    expect(nav).not.toHaveBeenCalled();
    expect(openAgent).not.toHaveBeenCalled();
    expect(openChild).not.toHaveBeenCalled();
    expect(copy).not.toHaveBeenCalled();
    expect(onOpenWorkspace).not.toHaveBeenCalled();
    expect(closeRequest).not.toHaveBeenCalled();
    expect(refreshCat).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();
    expect(captured!.view).toBe("menu");
    expectNoDefaultSingletons();
  });

  it("recovers cleanly via try/finally when probes, copyText, openChildWorktree, or catalog refresh throw/reject", async () => {
    const target = makeTarget("work-1", { host: "herdr" });
    const registry: WorktreeMutationRegistry = new Map();
    let captured: WorktreeActionsMenuViewProps | null = null;
    let fakeModel!: FakeMutation;

    // 1) Synchronous throw from checkAccess fails closed to readOnly (never stuck on loading).
    const failAccessDeps: WorktreeActionsMenuDeps = {
      scopeKey: "s1",
      credentialOwner: "acct-1",
      isRequestCurrent: () => true,
      closeRequest: vi.fn(),
      catalog: makeCatalog(target),
      machines: [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["herdr"] }],
      pins: NO_PINS,
      storeSessions: [{}, []],
      resolveAction: (tg, ctx) => makeResolved(tg, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
      firstDirectAgent: () => undefined,
      checkAccess: () => { throw new Error("auth boom"); },
      createMutation: (tg, d) => createFakeMutation(tg, d),
      renderView: (props) => {
        captured = props;
        return null;
      },
    };
    await act(async () => root.render(
      <ActiveWorktreeActions request={{ id: 1, target, anchor }} registry={registry} deps={failAccessDeps} />,
    ));
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.read_only"));

    // 2) Rejected checkWorktreeStatus shows git-unavailable note; failing copyText/openChild release locks for retry; failing refresh still toasts & closes.
    const copy = vi.fn().mockRejectedValueOnce(new Error("clipboard denied")).mockResolvedValueOnce(undefined);
    const openChild = vi.fn().mockRejectedValueOnce(new Error("composer unavailable")).mockResolvedValueOnce(undefined);
    const refreshCat = vi.fn().mockRejectedValue(new Error("catalog timeout"));
    const notify = vi.fn();
    const closeRequest = vi.fn();

    const recoverDeps: WorktreeActionsMenuDeps = {
      ...failAccessDeps,
      scopeKey: "s2",
      checkAccess: vi.fn().mockResolvedValue("full"),
      checkWorktreeStatus: vi.fn().mockRejectedValue(new Error("git status failed")),
      createMutation: (tg, d) => {
        fakeModel = createFakeMutation(tg, d);
        return fakeModel;
      },
      copyText: copy,
      openChildWorktree: openChild,
      refreshWorkspaceCatalog: refreshCat,
      notify,
      closeRequest,
    };
    await act(async () => root.render(
      <ActiveWorktreeActions request={{ id: 2, target, anchor }} registry={registry} deps={recoverDeps} />,
    ));
    expect(captured!.items.some((i) => i.key === "git-unavailable")).toBe(true);

    const copyItem = captured!.items.find((i) => i.key === "copy-path")!;
    await expect(copyItem.onSelect()).rejects.toThrow("clipboard denied");
    await expect(copyItem.onSelect()).resolves.toBeUndefined();
    expect(copy).toHaveBeenCalledTimes(2);

    const childItem = captured!.items.find((i) => i.key === "child")!;
    await expect(childItem.onSelect()).rejects.toThrow("composer unavailable");
    await expect(childItem.onSelect()).resolves.toBeUndefined();
    expect(openChild).toHaveBeenCalledTimes(2);

    await act(async () => { fakeModel.deps.acknowledged("close"); });
    expect(notify).toHaveBeenCalledWith(t("worktree-actions.closed"), "ok");
    expect(closeRequest).toHaveBeenCalledWith(2);
    expectNoDefaultSingletons();
  });

  it("preserves default store, workspace catalog, /auth/tokens, and worktree-actions fallbacks when deps are omitted", async () => {
    resetManagerChoice();
    setRosterDefault("default-manager");
    const target = makeTarget("work-1", { owner: "default-owner", manager: "default-manager" });
    mocks.api.mockResolvedValue({ current: "tok-1", tokens: [{ id: "tok-1", role: "full" }] });
    const registry: WorktreeMutationRegistry = new Map();

    await act(async () => {
      root.render(<ActiveWorktreeActions request={{ id: 1, target, anchor }} registry={registry} />);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(mocks.useWorkspaceCatalog).toHaveBeenCalledWith(true);
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.api).toHaveBeenCalledWith("/auth/tokens", expect.objectContaining({ headers: expect.any(Object) }));
    const copyPath = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find((el) => el.textContent?.includes("Copy folder path"))!;
    await act(async () => {
      copyPath.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(mocks.copyText).toHaveBeenCalledWith("/repo/work-1");
    resetManagerChoice();
  });

  it("invalidates cached probe authorization after explicit readOnly downgrade and stays fail-closed while replacement probe is pending", async () => {
    const target = makeTarget("work-1");
    const catalog = makeCatalog(target);
    const machines: Machine[] = [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["orca"] }];
    const registry: WorktreeMutationRegistry = new Map();
    const request: WorktreeActionsRequest = { id: 1, target, anchor };
    const openAgent = vi.fn();
    const closeRequest = vi.fn();
    const stage1Probe = deferred<WorktreeActionsAccess>();
    const stage3Probe = deferred<WorktreeActionsAccess>();
    let stage: 1 | 3 = 1;
    const checkAccess = vi.fn(() => (stage === 1 ? stage1Probe.promise : stage3Probe.promise));
    let captured: WorktreeActionsMenuViewProps | null = null;

    const baseDeps: WorktreeActionsMenuDeps = {
      scopeKey: "acct-1",
      credentialOwner: "acct-1",
      isRequestCurrent: () => true,
      closeRequest,
      catalog,
      machines,
      pins: NO_PINS,
      storeSessions: [{}, []],
      resolveAction: (tg, ctx) => makeResolved(tg, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
      firstDirectAgent: () => undefined,
      createMutation: (tg, d) => createFakeMutation(tg, d),
      openNewAgent: openAgent,
      checkAccess,
      renderView: (props) => {
        captured = props;
        return null;
      },
    };

    // Stage 1: initial probe grants full write access on epoch 1.
    await act(async () => root.render(
      <StrictMode>
        <ActiveWorktreeActions request={request} registry={registry} deps={baseDeps} />
      </StrictMode>,
    ));
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(true);
    await act(async () => {
      stage1Probe.resolve("full");
      await Promise.resolve();
    });
    const frame1NewAgent = captured!.items.find((i) => i.key === "new-agent")!;
    expect(frame1NewAgent.disabled).toBe(false);

    // Stage 2: explicit readOnly downgrade on the exact same owner/target/scope/request epoch.
    await act(async () => root.render(
      <StrictMode>
        <ActiveWorktreeActions request={request} registry={registry} deps={{ ...baseDeps, access: "readOnly" }} />
      </StrictMode>,
    ));
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(true);
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.read_only"));

    // Stage 3: remove direct access override and switch back to the same probe function while its new probe is pending.
    stage = 3;
    const callsBeforeReturn = checkAccess.mock.calls.length;
    await act(async () => root.render(
      <StrictMode>
        <ActiveWorktreeActions request={request} registry={registry} deps={baseDeps} />
      </StrictMode>,
    ));
    expect(checkAccess.mock.calls.length).toBeGreaterThan(callsBeforeReturn);
    const pendingNewAgent = captured!.items.find((i) => i.key === "new-agent")!;
    expect(pendingNewAgent.disabled).toBe(true);
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.checking_access"));

    // Neither the current nor the earlier captured write callback may fire while the replacement probe is pending.
    pendingNewAgent.onSelect();
    frame1NewAgent.onSelect();
    expect(openAgent).not.toHaveBeenCalled();
    expect(closeRequest).not.toHaveBeenCalled();

    // Settling the denying probe keeps write callbacks blocked.
    await act(async () => {
      stage3Probe.resolve("readOnly");
      await Promise.resolve();
    });
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(true);
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.read_only"));
    captured!.items.find((i) => i.key === "new-agent")!.onSelect();
    frame1NewAgent.onSelect();
    expect(openAgent).not.toHaveBeenCalled();
    expect(closeRequest).not.toHaveBeenCalled();

    // Also verify generation fencing: a superseded probe resolving "full" late cannot overwrite a newer probe generation.
    const staleProbe = deferred<WorktreeActionsAccess>();
    const activeProbe = deferred<WorktreeActionsAccess>();
    const staleFn = vi.fn(() => staleProbe.promise);
    const activeFn = vi.fn(() => activeProbe.promise);
    await act(async () => root.render(
      <ActiveWorktreeActions request={request} registry={registry} deps={{ ...baseDeps, checkAccess: staleFn }} />,
    ));
    await act(async () => root.render(
      <ActiveWorktreeActions request={request} registry={registry} deps={{ ...baseDeps, checkAccess: activeFn }} />,
    ));
    await act(async () => {
      staleProbe.resolve("full");
      await Promise.resolve();
    });
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(true);
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.checking_access"));
    captured!.items.find((i) => i.key === "new-agent")!.onSelect();
    expect(openAgent).not.toHaveBeenCalled();
    await act(async () => {
      activeProbe.resolve("readOnly");
      await Promise.resolve();
    });
    expect(captured!.items.find((i) => i.key === "new-agent")?.disabled).toBe(true);
    expect(captured!.items.find((i) => i.key === "availability")?.label).toBe(t("worktree-actions.read_only"));
    expectNoDefaultSingletons();
  });

  it("keeps the committed model, active preview, and shared registry intact when a concurrent transition render is abandoned", async () => {
    const target = makeTarget("work-1");
    const catalog = makeCatalog(target);
    const machines: Machine[] = [{ id: "m1", manager: "mgr-1", name: "Studio", online: true, engine_hosts: ["orca"] }];
    const registry: WorktreeMutationRegistry = new Map();
    const regKey = `acct-1\0${JSON.stringify([target.owner, target.manager, target.machineId, target.host, target.workspaceId, target.cwd])}`;
    const models: FakeMutation[] = [];
    const createMutation = vi.fn((tg: OwnedWorkspaceTarget, d: WorktreeMutationDeps) => {
      const m = createFakeMutation(tg, d);
      models.push(m);
      return m;
    });
    let captured: WorktreeActionsMenuViewProps | null = null;
    const suspendGate = deferred<void>();
    const renderedIds: number[] = [];

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarnessState!: (next: { id: number; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ id: 1, block: false });
      setHarnessState = setState;
      renderedIds.push(state.id);
      const req: WorktreeActionsRequest = { id: state.id, target, anchor };
      return (
        <Suspense fallback={<div data-suspense-fallback />}>
          <ActiveWorktreeActions
            request={req}
            registry={registry}
            deps={{
              scopeKey: "acct-1",
              credentialOwner: "acct-1",
              isRequestCurrent: () => true,
              closeRequest: vi.fn(),
              catalog,
              machines,
              pins: NO_PINS,
              storeSessions: [{}, []],
              resolveAction: (tg, ctx) => makeResolved(tg, ctx.catalog, ctx.machines, ctx.writable, ctx.linkedWorktree),
              firstDirectAgent: () => undefined,
              access: "full",
              createMutation,
              renderView: (props) => {
                captured = props;
                return <div data-active-view={`${props.view}:${props.model.snapshot().phase}`} />;
              },
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    expect(createMutation).toHaveBeenCalledTimes(1);
    const model1 = models[0];
    expect(registry.get(regKey)).toBe(model1);

    // Open delete dialog and put model1 into an active non-dispatched preview.
    await act(async () => { captured!.items.find((i) => i.key === "delete")!.onSelect(); });
    await act(async () => { model1.setState({ phase: "previewing" }); });
    expect(captured!.view).toBe("delete");
    expect(captured!.model).toBe(model1);
    expect(host.querySelector("[data-active-view]")?.getAttribute("data-active-view")).toBe("delete:previewing");
    model1.cancel.mockClear();
    model1.dispose.mockClear();

    // Start a concurrent transition to request id 2 (same target/registryKey) that suspends before commit.
    await act(async () => {
      startTransition(() => {
        setHarnessState({ id: 2, block: true });
      });
    });
    expect(renderedIds).toContain(2);
    // While suspended (uncommitted), the committed model, its active preview, and shared registry must be completely untouched.
    expect(model1.cancel).not.toHaveBeenCalled();
    expect(model1.dispose).not.toHaveBeenCalled();
    expect(createMutation).toHaveBeenCalledTimes(1);
    expect(registry.get(regKey)).toBe(model1);
    expect(model1.snapshot().phase).toBe("previewing");
    expect(model1.deps.current()).toBe(true);
    expect(host.querySelector("[data-active-view]")?.getAttribute("data-active-view")).toBe("delete:previewing");

    // Abandon/revert the transition via an urgent update back to id 1 (block: false).
    await act(async () => {
      setHarnessState({ id: 1, block: false });
    });
    expect(model1.cancel).not.toHaveBeenCalled();
    expect(model1.dispose).not.toHaveBeenCalled();
    expect(createMutation).toHaveBeenCalledTimes(1);
    expect(registry.get(regKey)).toBe(model1);
    expect(captured!.view).toBe("delete");
    expect(captured!.model).toBe(model1);
    expect(model1.snapshot().phase).toBe("previewing");
    expect(model1.deps.current()).toBe(true);

    // Once a transition to id 2 actually commits, model1 is cleanly cancelled/disposed and replaced in commit effects.
    await act(async () => {
      setHarnessState({ id: 2, block: false });
    });
    expect(model1.cancel).toHaveBeenCalled();
    expect(model1.dispose).toHaveBeenCalled();
    expect(createMutation).toHaveBeenCalledTimes(2);
    expect(registry.get(regKey)).toBe(models[1]);
    expect(captured!.view).toBe("menu");
    expect(captured!.model).toBe(models[1]);
    expectNoDefaultSingletons();
  });
});
