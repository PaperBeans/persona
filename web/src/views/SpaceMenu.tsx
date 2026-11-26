// The logical Agent space's menu, loaded when the user opens it.
import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { flipSpaceMute, flipSpacePin } from "../lib/agent-actions";
import { isSpaceMuted, unreadOf, type SpaceLike } from "../lib/agent-filter";
import type { AgentMutes } from "../lib/agent-groups";
import { credentialOwner, errorText } from "../lib/api";
import { shownManager } from "../lib/manager-choice";
import { isSpacePinned, type AgentPins } from "../lib/pins";
import { getState, refresh, refreshTodos, setSpacesSupported, useStore } from "../lib/store";
import { findTodoForSpace, isLegacySpacesRouteError, unlinkSpaceFromTodo, type Todo } from "../lib/todos";
import type { Machine, Session } from "../lib/types";
import { nodeActions, type MenuNode, type NodeActionDeps } from "../lib/node-actions";
import { navigate } from "../lib/router";
import { sameValue } from "../lib/stable-lines";
import { Menu, toast, type MenuItem } from "../ui/atoms";
import type { MenuAt } from "../ui/menu-model";
import { NodeMenu } from "./NodeMenu";
import { t } from "../i18n";
import { markAllSeenLabel } from "../lib/state-labels";
import { refreshWorkspaceCatalog, useWorkspaceCatalog, type WorkspaceCatalogSnapshot } from "../lib/workspace-catalog";
import { renamesRuntimeSpace, spaceWorktreeSources, worktreeActionKey } from "../lib/worktree-actions-context";
import type { WorkspaceTarget } from "../lib/workspace-target";

export type SpaceMenuNoticeTone = "ok" | "error" | "info";

export interface SpaceMenuStoreSnapshot {
  pins: AgentPins;
  mutes: AgentMutes;
  todos: Todo[];
  todosSupported?: boolean | null;
  todosLoaded?: boolean;
  spacesSupported?: boolean | null;
  machines: Machine[];
}

export interface SpaceMenuDeps {
  /** Optional scope partition (value or getter); switching it remounts the menu and fences stale actions/callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled store snapshot or reader (bypasses `useStore` and `getState` when supplied). */
  store?: SpaceMenuStoreSnapshot | (() => SpaceMenuStoreSnapshot);
  /** Subscribe to controlled store / slice updates when getters are supplied. */
  subscribeStore?: (listener: () => void) => () => void;
  pins?: AgentPins | (() => AgentPins);
  mutes?: AgentMutes | (() => AgentMutes);
  todos?: Todo[] | (() => Todo[]);
  todosSupported?: boolean | null | (() => boolean | null);
  todosLoaded?: boolean | (() => boolean);
  spacesSupported?: boolean | null | (() => boolean | null);
  machines?: Machine[] | (() => Machine[]);
  /** Controlled workspace catalog snapshot or reader (bypasses `useWorkspaceCatalog` when supplied). */
  catalog?: WorkspaceCatalogSnapshot | (() => WorkspaceCatalogSnapshot);
  /** Subscribe to workspace catalog updates when `catalog` is a getter. */
  subscribeCatalog?: (listener: () => void) => () => void;
  /** Controlled worktree creation sources (bypasses `spaceWorktreeSources` when supplied). */
  sources?: readonly WorkspaceTarget[] | (() => readonly WorkspaceTarget[]);
  /** Subscribe to worktree source updates when `sources` is a getter. */
  subscribeSources?: (listener: () => void) => () => void;
  /** Custom worktree source resolver (defaults to `spaceWorktreeSources`). */
  resolveWorktreeSources?: (sp: SpaceLike, catalog: WorkspaceCatalogSnapshot, machines: readonly Machine[]) => WorkspaceTarget[];
  /** Custom runtime-space rename predicate (defaults to `renamesRuntimeSpace`). */
  canRenameRuntimeSpace?: (session: Session | undefined, catalog: WorkspaceCatalogSnapshot) => boolean;
  /** Navigate to the space page (defaults to `router.navigate`). */
  navigate?: (to: Parameters<typeof navigate>[0]) => void;
  /** Unlink the space from its linked to-do (defaults to `todos.unlinkSpaceFromTodo`). */
  unlinkSpaceFromTodo?: (todoId: string, machineId: string, spaceName: string) => Promise<unknown>;
  /** Refresh to-dos after unlink (defaults to `store.refreshTodos`). */
  refreshTodos?: () => void | Promise<unknown>;
  /** Refresh hub state after unlink or child worktree creation (defaults to `store.refresh`). */
  refresh?: () => void | Promise<unknown>;
  /** Mark spaces API unsupported on legacy 404 (defaults to `store.setSpacesSupported`). */
  setSpacesSupported?: (supported: boolean) => void;
  /** Identify legacy 404 route errors (defaults to `todos.isLegacySpacesRouteError`). */
  isLegacySpacesRouteError?: (error: unknown) => boolean;
  /** Format errors for toast notifications (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Show status/error toast notifications (defaults to `atoms.toast`). */
  notify?: (message: string, tone: SpaceMenuNoticeTone) => void;
  /** Toggle pinned state for the space (defaults to `agent-actions.flipSpacePin`). */
  flipSpacePin?: (machineId: string, spaceName: string) => void | Promise<unknown>;
  /** Toggle muted state for the space (defaults to `agent-actions.flipSpaceMute`). */
  flipSpaceMute?: (machineId: string, spaceName: string) => void | Promise<unknown>;
  /** Open the child worktree composer (defaults to lazy `lib/worktree-creation.openChildWorktree`). */
  openChildWorktree?: (target: WorkspaceTarget, options: { changed?: () => void }) => void | Promise<unknown>;
  /** Refresh workspace catalog after child worktree creation (defaults to `workspace-catalog.refreshWorkspaceCatalog`). */
  refreshWorkspaceCatalog?: () => void | Promise<unknown>;
  /** Controlled node action dependencies (renders `<Menu>` directly without `NodeMenu`'s default singletons). */
  nodeDeps?: NodeActionDeps;
  /** Pre-built node menu items (renders `<Menu>` directly without `NodeMenu`'s default singletons). */
  nodeItems?: readonly MenuItem[];
  /** Custom view renderer for pure/controlled host shells (defaults to `<SpaceMenuView>`). */
  renderMenu?: (props: SpaceMenuViewProps) => ReactNode;
}

export interface SpaceMenuProps {
  sp: SpaceLike;
  anchor: HTMLElement;
  /** Where a right click opened it (the pointer); without it, beside `anchor`. */
  at?: MenuAt;
  /** The space's fold, when it has one to toggle. */
  fold?: MenuNode["fold"];
  onClose: () => void;
  onRename: (s: Session) => void;
  onMarkSeen: (list: Session[], release?: Session[]) => void;
  onLinkTodo?: (sp: SpaceLike) => void;
  /** "Open space page" (/space/…) leads the menu; the space's own page leaves it out. */
  openPage?: boolean;
  deps?: SpaceMenuDeps;
}

export interface SpaceMenuViewProps {
  node: MenuNode;
  at: MenuAt;
  onClose: () => void;
  header: string;
  before: MenuItem[];
  omit?: readonly string[];
  menuKey?: string | number;
  nodeDeps?: NodeActionDeps;
  nodeItems?: readonly MenuItem[];
}

const SPACE_MENU_OMIT = ["node-rename", "node-worktree"] as const;
const EMPTY_CATALOG: WorkspaceCatalogSnapshot = { owner: "", manager: "", workspaces: [] };
const noopSubscribe = () => () => {};

function useTrackedScope(subscribe: (listener: () => void) => () => void, read: () => string) {
  const stateRef = useRef<{ value: string; version: number } | null>(null);
  const sub = useCallback((listener: () => void) => subscribe(() => {
    const next = read();
    if (!stateRef.current || next !== stateRef.current.value) {
      stateRef.current = { value: next, version: (stateRef.current?.version ?? 0) + 1 };
    }
    listener();
  }), [subscribe, read]);
  const getSnapshot = useCallback(() => {
    const next = read();
    if (!stateRef.current) {
      stateRef.current = { value: next, version: 0 };
    } else if (next !== stateRef.current.value) {
      stateRef.current = { value: next, version: stateRef.current.version + 1 };
    }
    return stateRef.current;
  }, [read]);
  const snap = useSyncExternalStore(sub, getSnapshot, getSnapshot);
  const readToken = useCallback(() => {
    const cur = read();
    const s = stateRef.current;
    return s && s.value === cur ? `${s.version}:${cur}` : `drift:${cur}`;
  }, [read]);
  return { value: snap.value, token: `${snap.version}:${snap.value}`, readToken };
}

function useCachedExternalValue<T>(
  source: T | (() => T) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
): T | undefined {
  const cache = useRef<{ has: boolean; value: T | undefined }>({ has: false, value: undefined });
  const getSnapshot = useCallback((): T | undefined => {
    const next = typeof source === "function" ? (source as () => T)() : source;
    if (cache.current.has && sameValue(cache.current.value, next)) {
      return cache.current.value;
    }
    cache.current = { has: true, value: next };
    return next;
  }, [source]);
  const sub = subscribe && typeof source === "function" ? subscribe : noopSubscribe;
  return useSyncExternalStore(sub, getSnapshot, getSnapshot);
}

function fenceAsync<T>(isCurrent: () => boolean, run: () => Promise<T>, fallback: T): Promise<T> {
  if (!isCurrent()) return Promise.resolve(fallback);
  let pending: Promise<T>;
  try {
    pending = Promise.resolve(run());
  } catch (error) {
    return isCurrent() ? Promise.reject(error) : Promise.resolve(fallback);
  }
  return pending.then(
    (value) => (isCurrent() ? value : fallback),
    (error) => (isCurrent() ? Promise.reject(error) : fallback),
  );
}

function fenceNodeDeps(raw: NodeActionDeps, isCurrent: () => boolean): NodeActionDeps {
  return {
    post: (mid, path, body) => fenceAsync(isCurrent, () => raw.post(mid, path, body), undefined),
    renameWorkspace: (n, name) => fenceAsync(isCurrent, () => raw.renameWorkspace(n, name), undefined),
    renameTab: (n, name) => fenceAsync(isCurrent, () => raw.renameTab(n, name), undefined),
    launchers: (mid) => fenceAsync(isCurrent, () => raw.launchers(mid), []),
    newAgent: (mid, cwd) => { if (isCurrent()) raw.newAgent(mid, cwd); },
    ...(raw.newWorktree ? { newWorktree: (n: MenuNode) => { if (isCurrent()) raw.newWorktree!(n); } } : {}),
    openSession: (mid, sid) => { if (isCurrent()) raw.openSession(mid, sid); },
    copy: (text) => { if (isCurrent()) raw.copy(text); },
    notify: (text, tone) => { if (isCurrent()) raw.notify(text, tone); },
    changed: () => { if (isCurrent()) raw.changed(); },
    ...(raw.closeAgent ? {
      closeAgent: (mid: string, sid: string) => {
        const action = raw.closeAgent!(mid, sid);
        return action ? { ...action, onSelect: () => (isCurrent() ? action.onSelect() : undefined) } : undefined;
      },
    } : {}),
  };
}

function fenceMenuItem(item: MenuItem, isCurrent: () => boolean): MenuItem {
  const onSelect = () => {
    if (!isCurrent()) return;
    if (!item.inline) return item.onSelect();
    return fenceAsync(isCurrent, () => Promise.resolve(item.onSelect()), undefined);
  };
  const children = !item.children
    ? undefined
    : typeof item.children === "function"
      ? () => fenceAsync(isCurrent, () => (item.children as () => Promise<MenuItem[]>)(), []).then((sub) => (isCurrent() ? fenceMenuItems(sub, isCurrent) : []))
      : fenceMenuItems(item.children, isCurrent);
  const rename = item.rename
    ? {
        ...item.rename,
        commit: (name: string) => fenceAsync(isCurrent, () => Promise.resolve(item.rename!.commit(name)), undefined),
      }
    : undefined;
  return {
    ...item,
    onSelect,
    ...(children !== undefined ? { children } : {}),
    ...(rename !== undefined ? { rename } : {}),
  };
}

function fenceMenuItems(items: readonly MenuItem[], isCurrent: () => boolean): MenuItem[] {
  return items.map((item) => fenceMenuItem(item, isCurrent));
}

/** Pure presentational view for a space context menu. */
export function SpaceMenuView({
  node,
  at,
  onClose,
  header,
  before,
  omit = SPACE_MENU_OMIT,
  menuKey,
  nodeDeps,
  nodeItems,
}: SpaceMenuViewProps) {
  if (nodeItems !== undefined || nodeDeps !== undefined) {
    const own = nodeItems !== undefined ? [...nodeItems] : nodeActions(node, nodeDeps!, { omit });
    const items = before.length && own.length ? [...before, { ...own[0], divider: true }, ...own.slice(1)] : [...before, ...own];
    return <Menu key={menuKey} anchor={at.anchor ?? null} point={at.point} onClose={onClose} align="right" width={224} header={header} headerAsWritten items={items} />;
  }
  return <NodeMenu key={menuKey} node={node} at={at} onClose={onClose} header={header} before={before} omit={omit} />;
}

interface ResolvedSpaceMenuStore {
  pins: AgentPins;
  mutes: AgentMutes;
  todos: Todo[];
  todosSupported: boolean | null;
  todosLoaded: boolean;
  spacesSupported: boolean | null;
  machines: Machine[];
}

function DefaultSpaceMenuStore({
  partial,
  render,
}: {
  partial: Partial<ResolvedSpaceMenuStore>;
  render: (resolved: ResolvedSpaceMenuStore) => ReactNode;
}) {
  const pins = useStore((st) => st.pins);
  const mutes = useStore((st) => st.mutes);
  const todos = useStore((st) => st.todos);
  const todosSupported = useStore((st) => st.todosSupported);
  const todosLoaded = useStore((st) => st.todosLoaded);
  const spacesSupported = useStore((st) => st.spacesSupported);
  const machines = useStore((st) => st.machines);
  return <>{render({
    pins: partial.pins ?? pins,
    mutes: partial.mutes ?? mutes,
    todos: partial.todos ?? todos,
    todosSupported: partial.todosSupported !== undefined ? partial.todosSupported : todosSupported,
    todosLoaded: partial.todosLoaded !== undefined ? partial.todosLoaded : todosLoaded,
    spacesSupported: partial.spacesSupported !== undefined ? partial.spacesSupported : spacesSupported,
    machines: partial.machines ?? machines,
  })}</>;
}

function DefaultSpaceMenuCatalog({
  render,
}: {
  render: (catalog: WorkspaceCatalogSnapshot) => ReactNode;
}) {
  const catalog = useWorkspaceCatalog(true);
  return <>{render(catalog)}</>;
}

export function SpaceMenu({
  sp,
  anchor,
  at,
  fold,
  onClose,
  onRename,
  onMarkSeen,
  onLinkTodo,
  openPage = true,
  deps,
}: SpaceMenuProps) {
  const scopeDep = deps?.scopeKey;
  const readScope = useCallback(() => (typeof scopeDep === "function" ? scopeDep() : (scopeDep ?? "")), [scopeDep]);
  const scopeSubscribe = deps?.subscribeScopeKey && typeof scopeDep === "function" ? deps.subscribeScopeKey : noopSubscribe;
  const trackedScope = useTrackedScope(scopeSubscribe, readScope);

  // Keep identity and epoch tracking in the stable outer component so direct <-> getter+subscribe switches never skip resets.
  const spaceScope = JSON.stringify([trackedScope.token, sp.machineId, sp.name, sp.key]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: spaceScope, epoch: 1 }));
  const effectiveEpoch = scope.key === spaceScope ? scope.epoch : scope.epoch + 1;
  if (scope.key !== spaceScope) {
    setScope({ key: spaceScope, epoch: effectiveEpoch });
  }

  const activeEpochRef = useRef(effectiveEpoch);
  const minValidEpochRef = useRef(effectiveEpoch);
  const depsRef = useRef(deps);
  const onCloseRef = useRef(onClose);
  const onRenameRef = useRef(onRename);
  const onMarkSeenRef = useRef(onMarkSeen);
  const onLinkTodoRef = useRef(onLinkTodo);
  const readScopeTokenRef = useRef(trackedScope.readToken);
  const liveCatalogRef = useRef<WorkspaceCatalogSnapshot>(EMPTY_CATALOG);
  const pendingUnlinkRef = useRef(0);
  const pendingPinRef = useRef(0);
  const pendingMuteRef = useRef(0);
  const pendingWorktreeRef = useRef(0);

  useLayoutEffect(() => {
    depsRef.current = deps;
    onCloseRef.current = onClose;
    onRenameRef.current = onRename;
    onMarkSeenRef.current = onMarkSeen;
    onLinkTodoRef.current = onLinkTodo;
    readScopeTokenRef.current = trackedScope.readToken;
  });

  useLayoutEffect(() => {
    activeEpochRef.current = effectiveEpoch;
    if (effectiveEpoch > minValidEpochRef.current) {
      minValidEpochRef.current = effectiveEpoch;
    }
    return () => {
      if (activeEpochRef.current === effectiveEpoch) activeEpochRef.current = 0;
      if (pendingUnlinkRef.current === effectiveEpoch) pendingUnlinkRef.current = 0;
      if (pendingPinRef.current === effectiveEpoch) pendingPinRef.current = 0;
      if (pendingMuteRef.current === effectiveEpoch) pendingMuteRef.current = 0;
      if (pendingWorktreeRef.current === effectiveEpoch) pendingWorktreeRef.current = 0;
    };
  }, [effectiveEpoch]);

  const storeSnap = useCachedExternalValue(deps?.store, deps?.subscribeStore);
  const controlledPins = useCachedExternalValue(deps?.pins, deps?.subscribeStore) ?? storeSnap?.pins;
  const controlledMutes = useCachedExternalValue(deps?.mutes, deps?.subscribeStore) ?? storeSnap?.mutes;
  const controlledTodos = useCachedExternalValue(deps?.todos, deps?.subscribeStore) ?? storeSnap?.todos;
  const controlledTodosSupported = useCachedExternalValue(deps?.todosSupported, deps?.subscribeStore) ?? (storeSnap ? (storeSnap.todosSupported ?? true) : undefined);
  const controlledTodosLoaded = useCachedExternalValue(deps?.todosLoaded, deps?.subscribeStore) ?? (storeSnap ? (storeSnap.todosLoaded ?? true) : undefined);
  const controlledSpacesSupported = useCachedExternalValue(deps?.spacesSupported, deps?.subscribeStore) ?? (storeSnap ? (storeSnap.spacesSupported ?? true) : undefined);
  const controlledMachines = useCachedExternalValue(deps?.machines, deps?.subscribeStore) ?? storeSnap?.machines;
  const controlledCatalog = useCachedExternalValue(deps?.catalog, deps?.subscribeCatalog);
  const controlledSources = useCachedExternalValue(deps?.sources, deps?.subscribeSources);

  const needsDefaultStore = controlledPins === undefined
    || controlledMutes === undefined
    || controlledTodos === undefined
    || controlledTodosSupported === undefined
    || controlledTodosLoaded === undefined
    || controlledSpacesSupported === undefined
    || controlledMachines === undefined;
  const needsDefaultCatalog = controlledCatalog === undefined
    && (controlledSources === undefined || deps?.canRenameRuntimeSpace === undefined);

  const renderResolved = (resolvedStore: ResolvedSpaceMenuStore, catalog: WorkspaceCatalogSnapshot): ReactNode => {
    liveCatalogRef.current = catalog;
    const { pins, mutes, todos, todosSupported, todosLoaded, spacesSupported, machines } = resolvedStore;
    const capturedEpoch = effectiveEpoch;
    const capturedScopeToken = trackedScope.token;
    const hasCustomScope = deps?.scopeKey !== undefined;
    const isMenuCurrent = () =>
      activeEpochRef.current === capturedEpoch && readScopeTokenRef.current() === capturedScopeToken;
    const isOpCurrent = () =>
      capturedEpoch >= minValidEpochRef.current
      && (activeEpochRef.current === 0 || activeEpochRef.current === capturedEpoch)
      && readScopeTokenRef.current() === capturedScopeToken;

    const pinned = isSpacePinned(sp.machineId, sp.name, pins);
    const muted = isSpaceMuted(sp, mutes);
    const unread = unreadOf(sp.sessions);
    const first = sp.sessions[0];
    const online = machines.find((m) => m.id === sp.machineId)?.online !== false;
    const sources: readonly WorkspaceTarget[] = controlledSources !== undefined
      ? controlledSources
      : (deps?.resolveWorktreeSources ?? spaceWorktreeSources)(sp, catalog, machines);

    const readLiveMachines = (): readonly Machine[] => {
      const cur = depsRef.current;
      if (cur?.machines !== undefined) return typeof cur.machines === "function" ? cur.machines() : cur.machines;
      if (cur?.store !== undefined) return (typeof cur.store === "function" ? cur.store() : cur.store).machines;
      return getState().machines;
    };
    const readLiveCatalog = (): WorkspaceCatalogSnapshot => {
      const cur = depsRef.current;
      if (cur?.catalog !== undefined) return typeof cur.catalog === "function" ? cur.catalog() : cur.catalog;
      return liveCatalogRef.current;
    };

    const newWorktree = async (target: WorkspaceTarget) => {
      if (!isMenuCurrent() || pendingWorktreeRef.current === capturedEpoch) return;
      pendingWorktreeRef.current = capturedEpoch;
      const useDefaultAuthScope = !hasCustomScope && depsRef.current?.openChildWorktree === undefined;
      const capturedOwner = useDefaultAuthScope ? credentialOwner() : "";
      const capturedManager = useDefaultAuthScope ? shownManager() : "";
      try {
        const curDeps = depsRef.current;
        const openChild = curDeps?.openChildWorktree
          ?? (await import("../lib/worktree-creation")).openChildWorktree;
        if (!isOpCurrent()) return;
        const latest = depsRef.current;
        const currentSources: readonly WorkspaceTarget[] = latest?.sources !== undefined
          ? (typeof latest.sources === "function" ? latest.sources() : latest.sources)
          : (latest?.resolveWorktreeSources ?? spaceWorktreeSources)(sp, readLiveCatalog(), readLiveMachines());
        if (!currentSources.some((source) => worktreeActionKey(source) === worktreeActionKey(target))) {
          throw new Error(t("worktree.owner_changed"));
        }
        await openChild(target, {
          changed: () => {
            if (!isOpCurrent()) return;
            if (useDefaultAuthScope && (credentialOwner() !== capturedOwner || shownManager() !== capturedManager)) return;
            try {
              void Promise.resolve((depsRef.current?.refresh ?? refresh)()).catch(() => {});
            } catch { /* non-blocking refresh */ }
            try {
              void Promise.resolve((depsRef.current?.refreshWorkspaceCatalog ?? refreshWorkspaceCatalog)()).catch(() => {});
            } catch { /* non-blocking refresh */ }
          },
        });
      } finally {
        if (pendingWorktreeRef.current === capturedEpoch) pendingWorktreeRef.current = 0;
      }
    };

    const supportSpaces = todosLoaded && todosSupported !== false && spacesSupported !== false;
    const linkedTodo = supportSpaces ? findTodoForSpace(todos, sp.machineId, sp.name) : undefined;
    const canRenameSpace = Boolean(
      first?.space_id && (!first.source_kind || (deps?.canRenameRuntimeSpace ?? renamesRuntimeSpace)(first, catalog)),
    );

    const notify = (message: string, tone: SpaceMenuNoticeTone) => {
      if (depsRef.current?.notify) depsRef.current.notify(message, tone);
      else toast(message, tone);
    };
    const formatErr = (error: unknown) => (depsRef.current?.formatError ?? errorText)(error);

    const page = { name: "space" as const, mid: sp.machineId, space: sp.name };
    const rawItems: MenuItem[] = [
      ...(openPage && first?.space_id ? [{
        key: "open",
        label: t("agentmenus.open_space"),
        icon: "layers" as const,
        onSelect: () => {
          if (!isMenuCurrent()) return;
          (depsRef.current?.navigate ?? navigate)(page);
        },
      }] : []),
      ...(unread.length ? [{
        key: "seen",
        label: markAllSeenLabel(unread.length),
        icon: "check" as const,
        onSelect: () => {
          if (!isMenuCurrent()) return;
          onMarkSeenRef.current(unread, sp.sessions);
        },
      }] : []),
      ...(canRenameSpace && first ? [{
        key: "rename",
        label: t("agentmenus.rename_space"),
        icon: "pencil" as const,
        onSelect: () => {
          if (!isMenuCurrent()) return;
          onRenameRef.current(first);
        },
      }] : []),
      ...(supportSpaces
        ? linkedTodo
          ? [
              {
                key: "relink-todo",
                label: t("agentmenus.move_to_other_todo"),
                icon: "todo" as const,
                onSelect: () => {
                  if (!isMenuCurrent()) return;
                  onLinkTodoRef.current?.(sp);
                },
              },
              {
                key: "unlink-todo",
                label: t("agentmenus.remove_from_todo"),
                icon: "x" as const,
                onSelect: async () => {
                  if (!isMenuCurrent() || pendingUnlinkRef.current === capturedEpoch) return;
                  pendingUnlinkRef.current = capturedEpoch;
                  const useDefaultAuthScope = !hasCustomScope && depsRef.current?.unlinkSpaceFromTodo === undefined;
                  const capturedOwner = useDefaultAuthScope ? credentialOwner() : "";
                  const capturedManager = useDefaultAuthScope ? shownManager() : "";
                  const isUnlinkCurrent = () =>
                    isOpCurrent() && (!useDefaultAuthScope || (credentialOwner() === capturedOwner && shownManager() === capturedManager));
                  try {
                    await (depsRef.current?.unlinkSpaceFromTodo ?? unlinkSpaceFromTodo)(linkedTodo.id, sp.machineId, sp.name);
                    if (!isUnlinkCurrent()) return;
                    await (depsRef.current?.refreshTodos ?? refreshTodos)();
                    if (!isUnlinkCurrent()) return;
                    await (depsRef.current?.refresh ?? refresh)();
                    if (!isUnlinkCurrent()) return;
                    notify(t("agentmenus.removed_from_todo"), "ok");
                  } catch (e) {
                    if (!isUnlinkCurrent()) return;
                    if ((depsRef.current?.isLegacySpacesRouteError ?? isLegacySpacesRouteError)(e)) {
                      (depsRef.current?.setSpacesSupported ?? setSpacesSupported)(false);
                    }
                    notify(t("agentmenus.remove_failed", { error: formatErr(e) }), "error");
                    try {
                      void Promise.resolve((depsRef.current?.refreshTodos ?? refreshTodos)()).catch(() => {});
                    } catch { /* non-blocking refresh */ }
                  } finally {
                    if (pendingUnlinkRef.current === capturedEpoch) pendingUnlinkRef.current = 0;
                  }
                },
              },
            ]
          : [
              {
                key: "link-todo",
                label: t("agentmenus.attach_to_todo"),
                icon: "todo" as const,
                onSelect: () => {
                  if (!isMenuCurrent()) return;
                  onLinkTodoRef.current?.(sp);
                },
              },
            ]
        : []),
      {
        key: "pin",
        label: pinned ? t("agentmenus.unpin_space") : t("agentmenus.pin_space"),
        icon: "pin",
        onSelect: () => {
          if (!isMenuCurrent() || pendingPinRef.current === capturedEpoch) return;
          pendingPinRef.current = capturedEpoch;
          const isCustomPin = Boolean(depsRef.current?.flipSpacePin);
          const runPin = depsRef.current?.flipSpacePin ?? flipSpacePin;
          void (async () => {
            try {
              await runPin(sp.machineId, sp.name);
            } catch (e) {
              if (isCustomPin && isOpCurrent()) {
                notify(t("lib-agent-actions.save_failed", { error: formatErr(e) }), "error");
              }
            } finally {
              if (pendingPinRef.current === capturedEpoch) pendingPinRef.current = 0;
            }
          })();
        },
      },
      {
        key: "mute",
        label: muted ? t("agentmenus.unmute_space") : t("agentmenus.mute_space"),
        icon: muted ? "bell" : "bellOff",
        onSelect: () => {
          if (!isMenuCurrent() || pendingMuteRef.current === capturedEpoch) return;
          pendingMuteRef.current = capturedEpoch;
          const isCustomMute = Boolean(depsRef.current?.flipSpaceMute);
          const runMute = depsRef.current?.flipSpaceMute ?? flipSpaceMute;
          void (async () => {
            try {
              await runMute(sp.machineId, sp.name);
            } catch (e) {
              if (isCustomMute && isOpCurrent()) {
                notify(t("lib-agent-actions.save_failed", { error: formatErr(e) }), "error");
              }
            } finally {
              if (pendingMuteRef.current === capturedEpoch) pendingMuteRef.current = 0;
            }
          })();
        },
      },
      ...(first?.cwd ? [{
        key: "new-worktree",
        label: t("worktree-actions.new_child"),
        icon: "git" as const,
        divider: true,
        disabled: !sources.length,
        detail: sources.length ? undefined : t("agentmenus.worktree_source_unavailable"),
        onSelect: () => (sources.length === 1 ? newWorktree(sources[0]) : undefined),
        ...(sources.length > 1 ? {
          children: sources.map((source) => ({
            key: worktreeActionKey(source),
            label: source.label || source.cwd,
            detail: `${source.host} · ${source.cwd}`,
            onSelect: () => newWorktree(source),
          })),
        } : {}),
      }] : []),
    ];

    const items = fenceMenuItems(rawItems, isMenuCurrent);
    const fencedFold = fold ? { ...fold, toggle: () => { if (isMenuCurrent()) fold.toggle(); } } : undefined;
    // A runtime's space (Paseo's project, a runtime's folder) is no terminal host's workspace: no herdr ID to copy.
    const node: MenuNode = {
      kind: "workspace",
      mid: sp.machineId,
      online,
      host: first?.space_kind,
      label: sp.name,
      workspaceId: first?.source_kind ? undefined : first?.workspace_id ?? first?.space_id,
      paneId: first?.pane_id,
      cwd: first?.cwd,
      fold: fencedFold,
    };
    const fencedNodeDeps = deps?.nodeDeps ? fenceNodeDeps(deps.nodeDeps, isMenuCurrent) : undefined;
    const fencedNodeItems = deps?.nodeItems !== undefined ? fenceMenuItems(deps.nodeItems, isMenuCurrent) : undefined;
    const viewProps: SpaceMenuViewProps = {
      node,
      at: at ?? { anchor },
      onClose: () => { if (isMenuCurrent()) onCloseRef.current(); },
      header: t("agentmenus.space_header", { name: sp.name }),
      before: items,
      omit: SPACE_MENU_OMIT,
      menuKey: capturedEpoch,
      nodeDeps: fencedNodeDeps,
      nodeItems: fencedNodeItems,
    };
    if (deps?.renderMenu) return <>{deps.renderMenu(viewProps)}</>;
    return <SpaceMenuView {...viewProps} />;
  };

  const renderWithCatalog = (resolvedStore: ResolvedSpaceMenuStore): ReactNode => {
    if (needsDefaultCatalog) {
      return <DefaultSpaceMenuCatalog render={(catalog) => renderResolved(resolvedStore, catalog)} />;
    }
    return renderResolved(resolvedStore, controlledCatalog ?? EMPTY_CATALOG);
  };

  if (needsDefaultStore) {
    return (
      <DefaultSpaceMenuStore
        partial={{
          pins: controlledPins,
          mutes: controlledMutes,
          todos: controlledTodos,
          todosSupported: controlledTodosSupported,
          todosLoaded: controlledTodosLoaded,
          spacesSupported: controlledSpacesSupported,
          machines: controlledMachines,
        }}
        render={renderWithCatalog}
      />
    );
  }

  return renderWithCatalog({
    pins: controlledPins!,
    mutes: controlledMutes!,
    todos: controlledTodos!,
    todosSupported: controlledTodosSupported!,
    todosLoaded: controlledTodosLoaded!,
    spacesSupported: controlledSpacesSupported!,
    machines: controlledMachines!,
  });
}
