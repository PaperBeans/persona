// @vitest-environment jsdom
// Controlled SpaceMenu bypasses default store/catalog/action singletons, caches fresh-array getters, fences stale/ABA
// callbacks and async continuations, recovers from async failures, and keeps default fallbacks intact.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SpaceLike } from "../lib/agent-filter";
import type { AgentMutes } from "../lib/agent-groups";
import type { NodeActionDeps } from "../lib/node-actions";
import type { AgentPins } from "../lib/pins";
import type { Todo } from "../lib/todos";
import type { Machine, Session } from "../lib/types";
import type { WorkspaceCatalogSnapshot } from "../lib/workspace-catalog";
import type { WorkspaceTarget } from "../lib/workspace-target";
import type { MenuItem } from "../ui/menu-model";
import type { SpaceMenuDeps, SpaceMenuStoreSnapshot, SpaceMenuViewProps } from "./SpaceMenu";

const mocks = vi.hoisted(() => ({
  storeState: {
    pins: { spaces: [], sessions: [] } as AgentPins,
    mutes: { spaces: [], sessions: [], allowed: [] } as AgentMutes,
    todos: [] as Todo[],
    todosSupported: true as boolean | null,
    todosLoaded: true,
    spacesSupported: true as boolean | null,
    machines: [] as Machine[],
  },
  catalog: { owner: "default-owner", manager: "default-manager", workspaces: [] } as WorkspaceCatalogSnapshot,
  useStore: vi.fn(),
  getState: vi.fn(),
  refresh: vi.fn(),
  refreshTodos: vi.fn(),
  setSpacesSupported: vi.fn(),
  useWorkspaceCatalog: vi.fn(),
  refreshWorkspaceCatalog: vi.fn(),
  flipSpacePin: vi.fn(),
  flipSpaceMute: vi.fn(),
  unlinkSpaceFromTodo: vi.fn(),
  navigate: vi.fn(),
  openChildWorktree: vi.fn(),
  toast: vi.fn(),
  nodeMenuRender: vi.fn(),
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
  refresh: (...args: unknown[]) => mocks.refresh(...args),
  refreshTodos: (...args: unknown[]) => mocks.refreshTodos(...args),
  setSpacesSupported: (...args: unknown[]) => mocks.setSpacesSupported(...args),
}));
vi.mock("../lib/workspace-catalog", async (original) => ({
  ...(await original<typeof import("../lib/workspace-catalog")>()),
  useWorkspaceCatalog: (enabled: boolean) => {
    mocks.useWorkspaceCatalog(enabled);
    return mocks.catalog;
  },
  refreshWorkspaceCatalog: (...args: unknown[]) => mocks.refreshWorkspaceCatalog(...args),
}));
vi.mock("../lib/agent-actions", () => ({
  flipSpacePin: (...args: unknown[]) => mocks.flipSpacePin(...args),
  flipSpaceMute: (...args: unknown[]) => mocks.flipSpaceMute(...args),
}));
vi.mock("../lib/todos", async (original) => ({
  ...(await original<typeof import("../lib/todos")>()),
  unlinkSpaceFromTodo: (...args: unknown[]) => mocks.unlinkSpaceFromTodo(...args),
}));
vi.mock("../lib/router", () => ({
  navigate: (...args: unknown[]) => mocks.navigate(...args),
}));
vi.mock("../lib/worktree-creation", () => ({
  openChildWorktree: (...args: unknown[]) => mocks.openChildWorktree(...args),
}));
vi.mock("../ui/atoms", async (original) => ({
  ...(await original<typeof import("../ui/atoms")>()),
  toast: (...args: unknown[]) => mocks.toast(...args),
}));
vi.mock("./NodeMenu", () => ({
  NodeMenu: (props: Record<string, unknown>) => {
    mocks.nodeMenuRender(props);
    return null;
  },
}));

const { SpaceMenu, SpaceMenuView } = await import("./SpaceMenu");
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

const makeSession = (overrides: Partial<Session> = {}): Session => ({
  machine_id: "m1",
  session_id: "s1",
  title: "Agent 1",
  status: "idle",
  state: "unread",
  space_id: "ws-1",
  workspace_id: "ws-1",
  space_kind: "herdr",
  cwd: "/repo/a",
  ...overrides,
});

const makeSpace = (overrides: Partial<SpaceLike> = {}): SpaceLike => ({
  key: "m1/SpaceA",
  machineId: "m1",
  name: "SpaceA",
  sessions: [makeSession()],
  ...overrides,
});

const makeSource = (id = "ws-1", overrides: Partial<WorkspaceTarget> = {}): WorkspaceTarget => ({
  machineId: "m1",
  host: "herdr",
  workspaceId: id,
  cwd: "/repo/a",
  label: "SpaceA",
  owner: "acct-1",
  manager: "mgr-1",
  ...overrides,
});

const makeStore = (overrides: Partial<SpaceMenuStoreSnapshot> = {}): SpaceMenuStoreSnapshot => ({
  pins: { spaces: [], sessions: [] },
  mutes: { spaces: [], sessions: [], allowed: [] },
  todos: [{
    id: "todo-1",
    title: "Ship",
    spaces: [{ machine_id: "m1", space: "SpaceA" }],
  } as Todo],
  todosSupported: true,
  todosLoaded: true,
  spacesSupported: true,
  machines: [{ id: "m1", name: "Computer", online: true, engine_hosts: ["herdr", "orca"] }],
  ...overrides,
});

const makeCatalog = (overrides: Partial<WorkspaceCatalogSnapshot> = {}): WorkspaceCatalogSnapshot => ({
  owner: "acct-1",
  manager: "mgr-1",
  workspaces: [{ id: "row-1", machine_id: "m1", source: "herdr", source_id: "ws-1", title: "SpaceA", cwd: "/repo/a" }],
  ...overrides,
});

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.refresh).not.toHaveBeenCalled();
  expect(mocks.refreshTodos).not.toHaveBeenCalled();
  expect(mocks.setSpacesSupported).not.toHaveBeenCalled();
  expect(mocks.useWorkspaceCatalog).not.toHaveBeenCalled();
  expect(mocks.refreshWorkspaceCatalog).not.toHaveBeenCalled();
  expect(mocks.flipSpacePin).not.toHaveBeenCalled();
  expect(mocks.flipSpaceMute).not.toHaveBeenCalled();
  expect(mocks.unlinkSpaceFromTodo).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
  expect(mocks.openChildWorktree).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
  expect(mocks.nodeMenuRender).not.toHaveBeenCalled();
};

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  Object.values(mocks).forEach((v) => {
    if (typeof v === "function") v.mockReset();
  });
  mocks.storeState = {
    pins: { spaces: [], sessions: [] },
    mutes: { spaces: [], sessions: [], allowed: [] },
    todos: [{ id: "todo-1", title: "Ship", spaces: [{ machine_id: "m1", space: "SpaceA" }] } as Todo],
    todosSupported: true,
    todosLoaded: true,
    spacesSupported: true,
    machines: [{ id: "m1", name: "Computer", online: true, engine_hosts: ["herdr"] }],
  };
  mocks.catalog = makeCatalog({ owner: "default-owner", manager: "default-manager" });
  mocks.refresh.mockResolvedValue(undefined);
  mocks.refreshTodos.mockResolvedValue(undefined);
  mocks.refreshWorkspaceCatalog.mockResolvedValue(undefined);
  mocks.unlinkSpaceFromTodo.mockResolvedValue(undefined);
  mocks.flipSpacePin.mockResolvedValue(undefined);
  mocks.flipSpaceMute.mockResolvedValue(undefined);
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} }));
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

describe("SpaceMenu controlled deps, view separation, snapshot caching, fencing, and error recovery", () => {
  it("runs fully controlled without touching default singletons and separates pure SpaceMenuView", async () => {
    let captured: SpaceMenuViewProps | null = null;
    const onClose = vi.fn();
    const onRename = vi.fn();
    const onMarkSeen = vi.fn();
    const onLinkTodo = vi.fn();
    const foldToggle = vi.fn();
    const nav = vi.fn();
    const unlink = vi.fn().mockResolvedValue(undefined);
    const refTodos = vi.fn().mockResolvedValue(undefined);
    const refStore = vi.fn().mockResolvedValue(undefined);
    const refCat = vi.fn().mockResolvedValue(undefined);
    const notify = vi.fn();
    const pin = vi.fn().mockResolvedValue(undefined);
    const mute = vi.fn().mockResolvedValue(undefined);
    const openChild = vi.fn();
    const source = makeSource();
    const sp = makeSpace();

    const deps: SpaceMenuDeps = {
      scopeKey: "acct-1",
      store: makeStore(),
      catalog: makeCatalog(),
      sources: [source],
      navigate: nav,
      unlinkSpaceFromTodo: unlink,
      refreshTodos: refTodos,
      refresh: refStore,
      refreshWorkspaceCatalog: refCat,
      notify,
      flipSpacePin: pin,
      flipSpaceMute: mute,
      openChildWorktree: openChild,
      renderMenu: (props) => {
        captured = props;
        return <div data-controlled-space-menu>{props.header}</div>;
      },
    };

    await act(async () => root.render(
      <StrictMode>
        <SpaceMenu sp={sp} anchor={host} fold={{ folded: false, toggle: foldToggle }} onClose={onClose} onRename={onRename} onMarkSeen={onMarkSeen} onLinkTodo={onLinkTodo} deps={deps} />
      </StrictMode>,
    ));

    expect(host.querySelector("[data-controlled-space-menu]")?.textContent).toBe(t("agentmenus.space_header", { name: "SpaceA" }));
    const byKey = (k: string) => captured!.before.find((i) => i.key === k)!;

    byKey("open").onSelect();
    expect(nav).toHaveBeenCalledExactlyOnceWith({ name: "space", mid: "m1", space: "SpaceA" });

    byKey("seen").onSelect();
    expect(onMarkSeen).toHaveBeenCalledExactlyOnceWith(sp.sessions, sp.sessions);

    byKey("rename").onSelect();
    expect(onRename).toHaveBeenCalledExactlyOnceWith(sp.sessions[0]);

    byKey("relink-todo").onSelect();
    expect(onLinkTodo).toHaveBeenCalledExactlyOnceWith(sp);

    await act(async () => { await byKey("unlink-todo").onSelect(); });
    expect(unlink).toHaveBeenCalledExactlyOnceWith("todo-1", "m1", "SpaceA");
    expect(refTodos).toHaveBeenCalledTimes(1);
    expect(refStore).toHaveBeenCalledTimes(1);
    expect(notify).toHaveBeenCalledWith(t("agentmenus.removed_from_todo"), "ok");

    await act(async () => { byKey("pin").onSelect(); });
    expect(pin).toHaveBeenCalledExactlyOnceWith("m1", "SpaceA");

    await act(async () => { byKey("mute").onSelect(); });
    expect(mute).toHaveBeenCalledExactlyOnceWith("m1", "SpaceA");

    await act(async () => { await byKey("new-worktree").onSelect(); });
    expect(openChild).toHaveBeenCalledExactlyOnceWith(source, { changed: expect.any(Function) });
    openChild.mock.calls[0][1].changed();
    expect(refStore).toHaveBeenCalledTimes(2);
    expect(refCat).toHaveBeenCalledTimes(1);

    captured!.node.fold!.toggle();
    expect(foldToggle).toHaveBeenCalledTimes(1);

    // Pure SpaceMenuView with explicit nodeItems renders real Menu without touching NodeMenu or default singletons.
    const nodeItemClick = vi.fn();
    await act(async () => root.render(
      <SpaceMenuView
        node={captured!.node}
        at={{ anchor: host }}
        onClose={onClose}
        header="Pure Space"
        before={[{ key: "custom-before", label: "Before item", onSelect: vi.fn() }]}
        nodeItems={[{ key: "custom-node", label: "Node item", onSelect: nodeItemClick }]}
      />,
    ));
    const items = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")];
    expect(items.map((el) => el.textContent)).toEqual(expect.arrayContaining(["Before item", "Node item"]));
    await act(async () => items.find((el) => el.textContent === "Node item")!.click());
    expect(nodeItemClick).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("caches fresh-array/object getters for useSyncExternalStore and keeps bridge switching identity-stable", async () => {
    const storeListeners = new Set<() => void>();
    const catalogListeners = new Set<() => void>();
    const sourceListeners = new Set<() => void>();
    const scopeListeners = new Set<() => void>();
    let currentScope = "acct-1";
    let pinned = false;
    let currentSources: WorkspaceTarget[] = [makeSource("ws-1")];
    const seenKeys: Array<string | number | undefined> = [];
    let latestProps: SpaceMenuViewProps | null = null;

    const subscribe = (set: Set<() => void>) => (l: () => void) => {
      set.add(l);
      return () => { set.delete(l); };
    };

    const sp = makeSpace();
    const directDeps: SpaceMenuDeps = {
      scopeKey: "acct-1",
      store: makeStore(),
      catalog: makeCatalog(),
      sources: [makeSource("ws-1")],
      renderMenu: (props) => {
        latestProps = props;
        seenKeys.push(props.menuKey);
        return null;
      },
    };

    await act(async () => root.render(
      <StrictMode>
        <SpaceMenu sp={sp} anchor={host} onClose={() => {}} onRename={() => {}} onMarkSeen={() => {}} deps={directDeps} />
      </StrictMode>,
    ));
    const initialKey = latestProps!.menuKey;
    expect(latestProps!.before.find((i) => i.key === "pin")?.label).toBe(t("agentmenus.pin_space"));

    // Switch from direct values to fresh-array/object getters + subscribe: same scope keeps menuKey (no spurious reset).
    const getterDeps: SpaceMenuDeps = {
      scopeKey: () => currentScope,
      subscribeScopeKey: subscribe(scopeListeners),
      store: () => ({
        ...makeStore(),
        pins: { spaces: pinned ? ["m1/SpaceA"] : [], sessions: [] },
        machines: [{ id: "m1", name: "Computer", online: true, engine_hosts: ["herdr"] }],
      }),
      subscribeStore: subscribe(storeListeners),
      catalog: () => ({
        owner: "acct-1",
        manager: "mgr-1",
        workspaces: [{ id: "row-1", machine_id: "m1", source: "herdr", source_id: "ws-1", title: "SpaceA", cwd: "/repo/a" }],
      }),
      subscribeCatalog: subscribe(catalogListeners),
      sources: () => currentSources.map((s) => ({ ...s })),
      subscribeSources: subscribe(sourceListeners),
      renderMenu: (props) => {
        latestProps = props;
        seenKeys.push(props.menuKey);
        return null;
      },
    };

    await act(async () => root.render(
      <StrictMode>
        <SpaceMenu sp={sp} anchor={host} onClose={() => {}} onRename={() => {}} onMarkSeen={() => {}} deps={getterDeps} />
      </StrictMode>,
    ));
    expect(latestProps!.menuKey).toBe(initialKey);

    // Emitting unchanged values from fresh-array/object getters stays stable without infinite re-renders.
    await act(async () => {
      storeListeners.forEach((l) => l());
      catalogListeners.forEach((l) => l());
      sourceListeners.forEach((l) => l());
    });
    expect(latestProps!.menuKey).toBe(initialKey);

    // Emitting real data updates propagates cleanly.
    pinned = true;
    currentSources = [makeSource("ws-1"), makeSource("ws-2", { host: "orca", cwd: "/repo/b", label: "WorkB" })];
    await act(async () => {
      storeListeners.forEach((l) => l());
      sourceListeners.forEach((l) => l());
    });
    expect(latestProps!.before.find((i) => i.key === "pin")?.label).toBe(t("agentmenus.unpin_space"));
    expect((latestProps!.before.find((i) => i.key === "new-worktree")?.children as MenuItem[])).toHaveLength(2);

    // Switching scopeKey increments menuKey to remount/reset any open confirmation or rename state.
    currentScope = "acct-2";
    await act(async () => { scopeListeners.forEach((l) => l()); });
    expect(latestProps!.menuKey).not.toBe(initialKey);
    expectNoDefaultSingletons();
  });

  it("fences pre-constructed menu/node callbacks and in-flight async ops across space switches and ABA scope bursts", async () => {
    const scopeListeners = new Set<() => void>();
    let currentScope = "acct-1";
    let captured: SpaceMenuViewProps | null = null;
    const unlinkGate = deferred<void>();
    const onClose = vi.fn();
    const onRename = vi.fn();
    const onMarkSeen = vi.fn();
    const onLinkTodo = vi.fn();
    const foldToggle = vi.fn();
    const nav = vi.fn();
    const unlink = vi.fn(() => unlinkGate.promise);
    const refTodos = vi.fn().mockResolvedValue(undefined);
    const refStore = vi.fn().mockResolvedValue(undefined);
    const refCat = vi.fn().mockResolvedValue(undefined);
    const notify = vi.fn();
    const pin = vi.fn().mockResolvedValue(undefined);
    const mute = vi.fn().mockResolvedValue(undefined);
    const openChild = vi.fn();
    const nodeRenameCommit = vi.fn().mockResolvedValue(undefined);
    const nodeActionSelect = vi.fn();
    const nodeSubLoad = vi.fn().mockResolvedValue([{ key: "sub-1", label: "Sub", onSelect: nodeActionSelect }]);
    const nodeDeps: NodeActionDeps = {
      post: vi.fn().mockResolvedValue(undefined),
      renameWorkspace: vi.fn().mockResolvedValue(undefined),
      renameTab: vi.fn().mockResolvedValue(undefined),
      launchers: vi.fn().mockResolvedValue([]),
      newAgent: vi.fn(),
      openSession: vi.fn(),
      copy: vi.fn(),
      notify: vi.fn(),
      changed: vi.fn(),
    };

    const deps: SpaceMenuDeps = {
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      store: makeStore(),
      catalog: makeCatalog(),
      sources: [makeSource("ws-1"), makeSource("ws-2", { cwd: "/repo/b" })],
      navigate: nav,
      unlinkSpaceFromTodo: unlink,
      refreshTodos: refTodos,
      refresh: refStore,
      refreshWorkspaceCatalog: refCat,
      notify,
      flipSpacePin: pin,
      flipSpaceMute: mute,
      openChildWorktree: openChild,
      nodeDeps,
      nodeItems: [
        { key: "n-confirm", label: "Dangerous", confirm: "Confirm", onSelect: nodeActionSelect },
        { key: "n-rename", label: "Rename", onSelect: () => {}, rename: { value: "old", commit: nodeRenameCommit } },
        { key: "n-sub", label: "More", onSelect: () => {}, children: nodeSubLoad },
      ],
      renderMenu: (props) => {
        captured = props;
        return null;
      },
    };

    const spA = makeSpace({ key: "m1/SpaceA", name: "SpaceA" });
    await act(async () => root.render(
      <SpaceMenu sp={spA} anchor={host} fold={{ folded: false, toggle: foldToggle }} onClose={onClose} onRename={onRename} onMarkSeen={onMarkSeen} onLinkTodo={onLinkTodo} deps={deps} />,
    ));

    const staleView = captured!;
    const staleByKey = (k: string) => staleView.before.find((i) => i.key === k)!;
    // Start in-flight unlink and capture openChildWorktree's `changed` callback before switching scope.
    const pendingUnlink = staleByKey("unlink-todo").onSelect();
    await act(async () => {
      await (staleByKey("new-worktree").children as MenuItem[])[0].onSelect();
    });
    const staleChanged = openChild.mock.calls[0][1].changed as () => void;
    openChild.mockClear();

    // Perform an ABA scope burst (acct-1 -> acct-2 -> acct-1) synchronously.
    await act(async () => {
      currentScope = "acct-2";
      scopeListeners.forEach((l) => l());
      currentScope = "acct-1";
      scopeListeners.forEach((l) => l());
    });
    expect(captured!.menuKey).not.toBe(staleView.menuKey);

    // Every stale captured callback from epoch 1 must be discarded even though currentScope returned to "acct-1".
    staleByKey("open").onSelect();
    staleByKey("seen").onSelect();
    staleByKey("rename").onSelect();
    staleByKey("relink-todo").onSelect();
    staleByKey("pin").onSelect();
    staleByKey("mute").onSelect();
    await (staleByKey("new-worktree").children as MenuItem[])[1].onSelect();
    staleView.node.fold!.toggle();
    staleView.onClose();
    staleView.nodeItems![0].onSelect();
    await staleView.nodeItems![1].rename!.commit("stale-name");
    expect(await (staleView.nodeItems![2].children as () => Promise<MenuItem[]>)()).toEqual([]);
    staleView.nodeDeps!.copy("stale");
    staleView.nodeDeps!.changed();
    staleChanged();

    // Resolve the stale in-flight unlink from epoch 1: must NOT refresh or toast.
    await act(async () => {
      unlinkGate.resolve();
      await pendingUnlink;
    });

    expect(nav).not.toHaveBeenCalled();
    expect(onMarkSeen).not.toHaveBeenCalled();
    expect(onRename).not.toHaveBeenCalled();
    expect(onLinkTodo).not.toHaveBeenCalled();
    expect(pin).not.toHaveBeenCalled();
    expect(mute).not.toHaveBeenCalled();
    expect(openChild).not.toHaveBeenCalled();
    expect(foldToggle).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(nodeActionSelect).not.toHaveBeenCalled();
    expect(nodeRenameCommit).not.toHaveBeenCalled();
    expect(nodeSubLoad).not.toHaveBeenCalled();
    expect(nodeDeps.copy).not.toHaveBeenCalled();
    expect(nodeDeps.changed).not.toHaveBeenCalled();
    expect(refTodos).not.toHaveBeenCalled();
    expect(refStore).not.toHaveBeenCalled();
    expect(refCat).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();

    // Switching space prop (SpaceA -> SpaceB) also increments menuKey and cancels live Menu confirm state.
    const spB = makeSpace({ key: "m1/SpaceB", name: "SpaceB" });
    const confirmSelect = vi.fn();
    await act(async () => root.render(
      <SpaceMenu
        sp={spA}
        anchor={host}
        onClose={onClose}
        onRename={onRename}
        onMarkSeen={onMarkSeen}
        deps={{ ...deps, renderMenu: undefined, nodeDeps: undefined, nodeItems: [{ key: "danger", label: "Close", confirm: "Really close?", onSelect: confirmSelect }] }}
      />,
    ));
    const closeRow = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find((el) => el.textContent?.includes("Close"))!;
    await act(async () => closeRow.click());
    expect(document.body.textContent).toContain("Really close?");

    await act(async () => root.render(
      <SpaceMenu
        sp={spB}
        anchor={host}
        onClose={onClose}
        onRename={onRename}
        onMarkSeen={onMarkSeen}
        deps={{ ...deps, renderMenu: undefined, nodeDeps: undefined, nodeItems: [{ key: "danger", label: "Close", confirm: "Really close?", onSelect: confirmSelect }] }}
      />,
    ));
    expect(document.body.textContent).not.toContain("Really close?");
    expect(confirmSelect).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("releases busy/pending locks in try/finally on thrown/rejected persistence calls and preserves error feedback", async () => {
    let captured: SpaceMenuViewProps | null = null;
    const setSpacesSup = vi.fn();
    const notify = vi.fn();
    const refTodos = vi.fn().mockRejectedValueOnce(new Error("refresh failed")).mockResolvedValue(undefined);
    const refStore = vi.fn().mockResolvedValue(undefined);
    const unlink = vi.fn()
      .mockRejectedValueOnce(new Error("legacy-404"))
      .mockResolvedValueOnce(undefined);
    const pin = vi.fn()
      .mockRejectedValueOnce(new Error("pin boom"))
      .mockResolvedValueOnce(undefined);
    const mute = vi.fn()
      .mockImplementationOnce(() => { throw new Error("mute sync throw"); })
      .mockResolvedValueOnce(undefined);
    const openChild = vi.fn()
      .mockRejectedValueOnce(new Error("open failed"))
      .mockResolvedValueOnce(undefined);

    const deps: SpaceMenuDeps = {
      scopeKey: "acct-1",
      store: makeStore(),
      catalog: makeCatalog(),
      sources: [makeSource()],
      unlinkSpaceFromTodo: unlink,
      refreshTodos: refTodos,
      refresh: refStore,
      setSpacesSupported: setSpacesSup,
      isLegacySpacesRouteError: (e) => e instanceof Error && e.message === "legacy-404",
      formatError: (e) => (e instanceof Error ? e.message : String(e)),
      notify,
      flipSpacePin: pin,
      flipSpaceMute: mute,
      openChildWorktree: openChild,
      renderMenu: (props) => {
        captured = props;
        return null;
      },
    };

    await act(async () => root.render(
      <SpaceMenu sp={makeSpace()} anchor={host} onClose={() => {}} onRename={() => {}} onMarkSeen={() => {}} deps={deps} />,
    ));
    const byKey = (k: string) => captured!.before.find((i) => i.key === k)!;

    // 1) Failed unlink marks legacy spaces unsupported, toasts error, survives failing refreshTodos, releases lock for retry.
    await act(async () => { await byKey("unlink-todo").onSelect(); });
    expect(setSpacesSup).toHaveBeenCalledExactlyOnceWith(false);
    expect(notify).toHaveBeenCalledWith(t("agentmenus.remove_failed", { error: "legacy-404" }), "error");
    await act(async () => { await byKey("unlink-todo").onSelect(); });
    expect(unlink).toHaveBeenCalledTimes(2);
    expect(notify).toHaveBeenLastCalledWith(t("agentmenus.removed_from_todo"), "ok");

    // 2) Failed pin releases lock and preserves error toast; retry succeeds.
    await act(async () => { byKey("pin").onSelect(); await Promise.resolve(); });
    expect(notify).toHaveBeenCalledWith(t("lib-agent-actions.save_failed", { error: "pin boom" }), "error");
    await act(async () => { byKey("pin").onSelect(); await Promise.resolve(); });
    expect(pin).toHaveBeenCalledTimes(2);

    // 3) Synchronously throwing mute releases lock and preserves error toast; retry succeeds.
    await act(async () => { byKey("mute").onSelect(); await Promise.resolve(); });
    expect(notify).toHaveBeenCalledWith(t("lib-agent-actions.save_failed", { error: "mute sync throw" }), "error");
    await act(async () => { byKey("mute").onSelect(); await Promise.resolve(); });
    expect(mute).toHaveBeenCalledTimes(2);

    // 4) Failed new-worktree releases lock in finally so retry succeeds.
    await expect(byKey("new-worktree").onSelect()).rejects.toThrow("open failed");
    await expect(byKey("new-worktree").onSelect()).resolves.toBeUndefined();
    expect(openChild).toHaveBeenCalledTimes(2);
    expectNoDefaultSingletons();
  });

  it("preserves default store, workspace catalog, NodeMenu, and action fallbacks when deps are omitted", async () => {
    const sp = makeSpace();
    await act(async () => root.render(
      <SpaceMenu sp={sp} anchor={host} onClose={() => {}} onRename={() => {}} onMarkSeen={() => {}} />,
    ));
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useWorkspaceCatalog).toHaveBeenCalledWith(true);
    expect(mocks.nodeMenuRender).toHaveBeenCalledTimes(1);

    const before = mocks.nodeMenuRender.mock.calls[0][0].before as MenuItem[];
    before.find((i) => i.key === "open")!.onSelect();
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "space", mid: "m1", space: "SpaceA" });

    await act(async () => { await before.find((i) => i.key === "unlink-todo")!.onSelect(); });
    expect(mocks.unlinkSpaceFromTodo).toHaveBeenCalledExactlyOnceWith("todo-1", "m1", "SpaceA");
    expect(mocks.refreshTodos).toHaveBeenCalledTimes(1);
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
    expect(mocks.toast).toHaveBeenCalledWith(t("agentmenus.removed_from_todo"), "ok");

    await act(async () => { before.find((i) => i.key === "pin")!.onSelect(); });
    expect(mocks.flipSpacePin).toHaveBeenCalledExactlyOnceWith("m1", "SpaceA");

    await act(async () => { before.find((i) => i.key === "mute")!.onSelect(); });
    expect(mocks.flipSpaceMute).toHaveBeenCalledExactlyOnceWith("m1", "SpaceA");
  });
});
