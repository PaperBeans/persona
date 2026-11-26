// @vitest-environment jsdom
import { act, startTransition, Suspense, useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import type { Session } from "../lib/types";
import type { MenuItem, ToastAction } from "../ui/atoms";

const spies = vi.hoisted(() => ({
  useCanDeleteConversation: vi.fn(),
  navigate: vi.fn(),
  defaultMenuCall: vi.fn(),
}));

vi.mock("../lib/conversation-delete", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/conversation-delete")>();
  return {
    ...actual,
    useCanDeleteConversation: (s: Session, main?: boolean, builtIn?: boolean) => {
      spies.useCanDeleteConversation(s, main, builtIn);
      return actual.useCanDeleteConversation(s, main, builtIn);
    },
  };
});

vi.mock("../lib/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/router")>();
  return {
    ...actual,
    navigate: (to: Parameters<typeof actual.navigate>[0]) => {
      spies.navigate(to);
    },
  };
});

vi.mock("./ConversationMenu", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./ConversationMenu")>();
  const wrap = <K extends keyof typeof actual.conversationMenuDeps>(k: K) =>
    ((...args: unknown[]) => {
      spies.defaultMenuCall(k, ...args);
      return (actual.conversationMenuDeps[k] as (...a: unknown[]) => unknown)(...args);
    }) as (typeof actual.conversationMenuDeps)[K];
  return {
    ...actual,
    conversationMenuDeps: {
      rename: wrap("rename"),
      archive: wrap("archive"),
      unarchive: wrap("unarchive"),
      end: wrap("end"),
      refresh: wrap("refresh"),
      lead: wrap("lead"),
      copy: wrap("copy"),
      notify: wrap("notify"),
    },
  };
});

import { CONVERSATION_DELETE_FEATURE } from "../lib/conversation-delete";
import { getState, reset, upsertSession } from "../lib/store";
import {
  ConversationRowMenu,
  ConversationRowMenuView,
  type ConversationRowMenuDeps,
  type ConversationRowMenuViewProps,
} from "./ConversationRowMenu";
import type { ConversationMenuDeps } from "./ConversationMenu";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function makeSession(patch: Partial<Session> = {}): Session {
  return {
    machine_id: "hub",
    session_id: "conv-1",
    manager: "mgr-1",
    status: "idle",
    title: "Trip planning",
    cwd: "",
    assistant: true,
    updated_at: "2026-11-26T00:00:00Z",
    ...patch,
  };
}

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createExternalBox<T>(initial: T) {
  let current = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => current,
    set: (next: T) => {
      current = next;
      listeners.forEach((l) => l());
    },
    setSilent: (next: T) => {
      current = next;
    },
    notify: () => {
      listeners.forEach((l) => l());
    },
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    listenerCount: () => listeners.size,
  };
}

function makeControlledMenuDeps(): {
  deps: ConversationMenuDeps;
  calls: {
    rename: ReturnType<typeof vi.fn>;
    archive: ReturnType<typeof vi.fn>;
    unarchive: ReturnType<typeof vi.fn>;
    end: ReturnType<typeof vi.fn>;
    refresh: ReturnType<typeof vi.fn>;
    lead: ReturnType<typeof vi.fn>;
    copy: ReturnType<typeof vi.fn>;
    notify: ReturnType<typeof vi.fn>;
  };
} {
  const calls = {
    rename: vi.fn(async () => {}),
    archive: vi.fn(async () => {}),
    unarchive: vi.fn(async () => {}),
    end: vi.fn(async () => {}),
    refresh: vi.fn(async () => {}),
    lead: vi.fn(async () => {}),
    copy: vi.fn(),
    notify: vi.fn(),
  };
  return { deps: calls, calls };
}

let host: HTMLDivElement;
let anchor: HTMLButtonElement;
let root: Root;

function expectNoDefaultSingletons() {
  expect(spies.useCanDeleteConversation).not.toHaveBeenCalled();
  expect(spies.navigate).not.toHaveBeenCalled();
  expect(spies.defaultMenuCall).not.toHaveBeenCalled();
}

beforeEach(() => {
  Object.assign(globalThis, {
    matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
  });
  reset();
  spies.useCanDeleteConversation.mockClear();
  spies.navigate.mockClear();
  spies.defaultMenuCall.mockClear();
  host = document.createElement("div");
  anchor = document.createElement("button");
  document.body.append(host, anchor);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  anchor.remove();
  reset();
  vi.restoreAllMocks();
});

describe("ConversationRowMenuView (pure presentational)", () => {
  it("renders menu or delete confirmation exclusively through props without default singletons", () => {
    const s = makeSession();
    const onClose = vi.fn();
    const items: MenuItem[] = [{ key: "copy", label: "Copy ID", onSelect: vi.fn() }];
    act(() => root.render(
      <ConversationRowMenuView
        s={s}
        at={{ anchor }}
        items={items}
        deleting={false}
        onClose={onClose}
        renderMenu={(p) => <div data-testid="pure-menu">{p.items.map((i) => i.key).join(",")}</div>}
        renderDeleteConfirmation={() => <div data-testid="pure-del" />}
      />,
    ));
    expect(host.querySelector('[data-testid="pure-menu"]')?.textContent).toBe("copy");
    expect(host.querySelector('[data-testid="pure-del"]')).toBeNull();

    act(() => root.render(
      <ConversationRowMenuView
        s={s}
        at={{ anchor }}
        items={items}
        deleting
        onClose={onClose}
        renderMenu={() => <div data-testid="pure-menu" />}
        renderDeleteConfirmation={(p) => (
          <button type="button" data-testid="pure-del" onClick={p.onClose}>{p.s.session_id}</button>
        )}
      />,
    ));
    expect(host.querySelector('[data-testid="pure-menu"]')).toBeNull();
    expect(host.querySelector('[data-testid="pure-del"]')?.textContent).toBe("conv-1");
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="pure-del"]')!.click());
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });
});

describe("ConversationRowMenu (controlled & default)", () => {
  it("bypasses default singletons, caches fresh-array items getter, and preserves body identity across bridge switches", () => {
    const s = makeSession();
    const canDeleteBox = createExternalBox(true);
    const customSelect = vi.fn();
    const itemsBox = createExternalBox<MenuItem[]>([{ key: "custom-a", label: "Custom A", onSelect: customSelect }]);
    const customNavigate = vi.fn();
    const onArrange = vi.fn();
    const onClose = vi.fn();
    let menuMounts = 0;
    let capturedView: ConversationRowMenuViewProps | null = null;

    function TrackedMenu({ items }: { items: MenuItem[] }) {
      useEffect(() => { menuMounts += 1; }, []);
      return <div data-testid="tracked-menu">{items.map((i) => i.key).join(",")}</div>;
    }

    const renderView = (props: ConversationRowMenuViewProps) => {
      capturedView = props;
      return <TrackedMenu key={props.menuKey} items={props.items} />;
    };

    // Subscribed fresh-array items getter + subscribed canDelete getter.
    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        leader
        anchor={anchor}
        onClose={onClose}
        onArrange={onArrange}
        deps={{
          canDelete: () => canDeleteBox.get(),
          subscribeCanDelete: canDeleteBox.subscribe,
          items: () => itemsBox.get().map((i) => ({ ...i })),
          subscribeItems: itemsBox.subscribe,
          navigate: customNavigate,
          renderView,
        }}
      />,
    ));
    expect(menuMounts).toBe(1);
    expect(capturedView!.items.map((i) => i.key)).toEqual(["arrange", "direction-detail", "custom-a"]);
    act(() => capturedView!.items.find((i) => i.key === "direction-detail")!.onSelect());
    expect(customNavigate).toHaveBeenCalledWith({ name: "todos", direction: "conv-1", manager: "mgr-1" });
    act(() => capturedView!.items.find((i) => i.key === "arrange")!.onSelect());
    expect(onArrange).toHaveBeenCalledTimes(1);
    act(() => capturedView!.items.find((i) => i.key === "custom-a")!.onSelect());
    expect(customSelect).toHaveBeenCalledTimes(1);

    // Switch canDelete and items from getter+subscribe to direct values: menu must NOT remount.
    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        leader
        anchor={anchor}
        onClose={onClose}
        onArrange={onArrange}
        deps={{
          canDelete: true,
          items: [{ key: "custom-a", label: "Custom A", onSelect: customSelect }],
          navigate: customNavigate,
          renderView,
        }}
      />,
    ));
    expect(menuMounts).toBe(1);
    expect(canDeleteBox.listenerCount()).toBe(0);
    // Passing pre-built items alone (without canDelete) also skips useCanDeleteConversation.
    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        anchor={anchor}
        onClose={onClose}
        deps={{
          items: [{ key: "custom-only", label: "Custom Only", onSelect: customSelect }],
          renderView,
        }}
      />,
    ));
    expect(capturedView!.items.map((i) => i.key)).toEqual(["custom-only"]);
    expectNoDefaultSingletons();
  });

  it("enforces fail-closed canDelete downgrades and deleting source-switch invalidation", () => {
    const s = makeSession();
    const canDeleteBox = createExternalBox(true);
    const { deps: menuDeps } = makeControlledMenuDeps();
    const onDeletingChange = vi.fn();
    let captured: ConversationRowMenuViewProps | null = null;
    const renderView = (props: ConversationRowMenuViewProps) => {
      captured = props;
      return <div data-deleting={String(props.deleting)} />;
    };

    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        anchor={anchor}
        onClose={vi.fn()}
        deps={{
          canDelete: () => canDeleteBox.get(),
          subscribeCanDelete: canDeleteBox.subscribe,
          menuDeps,
          onDeletingChange,
          renderView,
        }}
      />,
    ));
    const deleteItemBefore = captured!.items.find((i) => i.key === "delete");
    expect(deleteItemBefore).toBeDefined();

    // Flip external canDelete getter to false before commit: stale onSelect is blocked fail-closed.
    canDeleteBox.setSilent(false);
    act(() => { deleteItemBefore!.onSelect(); });
    expect(captured!.deleting).toBe(false);
    expect(onDeletingChange).not.toHaveBeenCalled();

    // After commit, delete item is absent.
    act(() => { canDeleteBox.notify(); });
    expect(captured!.items.find((i) => i.key === "delete")).toBeUndefined();

    // Switching controlled deleting=true -> uncontrolled deleting=undefined resets fail-closed to false.
    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        anchor={anchor}
        onClose={vi.fn()}
        deps={{ canDelete: true, deleting: true, menuDeps, renderView }}
      />,
    ));
    expect(captured!.deleting).toBe(true);

    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        anchor={anchor}
        onClose={vi.fn()}
        deps={{ canDelete: true, menuDeps, renderView }}
      />,
    ));
    expect(captured!.deleting).toBe(false);
    expectNoDefaultSingletons();
  });

  it("resets on scopeKey ABA and session switches, fences stale callbacks and Undo, and keeps mutable progress intact", async () => {
    const scopeBox = createExternalBox("acct-1");
    const { deps: menuDeps, calls } = makeControlledMenuDeps();
    const onClose = vi.fn();
    const onArrange = vi.fn();
    const onDeletingChange = vi.fn();
    const customNav = vi.fn();
    let captured: ConversationRowMenuViewProps | null = null;

    const renderView = (props: ConversationRowMenuViewProps) => {
      captured = props;
      return <div data-deleting={String(props.deleting)} data-menu-key={String(props.menuKey)} />;
    };

    const s1 = makeSession({ session_id: "conv-1", status: "idle", title: "Original" });
    act(() => root.render(
      <ConversationRowMenu
        s={s1}
        main={false}
        leader={false}
        anchor={anchor}
        onClose={onClose}
        onArrange={onArrange}
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          canDelete: true,
          menuDeps,
          navigate: customNav,
          onDeletingChange,
          renderView,
        }}
      />,
    ));
    const initialMenuKey = captured!.menuKey;
    const staleClose = captured!.onClose;
    const staleArrange = captured!.items.find((i) => i.key === "arrange")!;

    // Start an in-flight lead operation; updating mutable progress (status/title/leader) must NOT cancel it.
    const leadGate = deferred<void>();
    calls.lead.mockReturnValueOnce(leadGate.promise);
    const leadItem = captured!.items.find((i) => i.key === "leader")!;
    const leadPromise = leadItem.onSelect() as Promise<void>;
    expect(calls.lead).toHaveBeenCalledTimes(1);

    // Duplicate click while in flight is blocked (single-flight, no extra call or toast).
    leadItem.onSelect();
    expect(calls.lead).toHaveBeenCalledTimes(1);

    // Mutable progress update on the same session preserves epoch & menuKey.
    act(() => root.render(
      <ConversationRowMenu
        s={makeSession({ session_id: "conv-1", status: "working", title: "Renamed live" })}
        main={false}
        leader
        anchor={anchor}
        onClose={onClose}
        onArrange={onArrange}
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          canDelete: true,
          menuDeps,
          navigate: customNav,
          onDeletingChange,
          renderView,
        }}
      />,
    ));
    expect(captured!.menuKey).toBe(initialMenuKey);

    // Resolving lead after mutable progress still notifies and refreshes.
    await act(async () => {
      leadGate.resolve();
      await leadPromise;
    });
    expect(calls.notify).toHaveBeenCalledTimes(1);
    expect(calls.refresh).toHaveBeenCalledTimes(1);

    // Enter delete confirmation; mutable progress update keeps delete confirmation open.
    act(() => { captured!.items.find((i) => i.key === "delete")!.onSelect(); });
    expect(captured!.deleting).toBe(true);
    expect(onDeletingChange).toHaveBeenLastCalledWith(true);

    act(() => root.render(
      <ConversationRowMenu
        s={makeSession({ session_id: "conv-1", status: "working", title: "Still deleting" })}
        main={false}
        leader
        anchor={anchor}
        onClose={onClose}
        onArrange={onArrange}
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          canDelete: true,
          menuDeps,
          navigate: customNav,
          onDeletingChange,
          renderView,
        }}
      />,
    ));
    expect(captured!.deleting).toBe(true);

    // ABA scope burst acct-1 -> acct-2 -> acct-1 resets deleting, bumps menuKey, and fences old callbacks.
    act(() => {
      scopeBox.set("acct-2");
      scopeBox.set("acct-1");
    });
    expect(captured!.deleting).toBe(false);
    expect(captured!.menuKey).not.toBe(initialMenuKey);
    expect(onDeletingChange).toHaveBeenLastCalledWith(false);
    act(() => {
      staleClose();
      staleArrange.onSelect();
    });
    expect(onClose).not.toHaveBeenCalled();
    expect(onArrange).not.toHaveBeenCalled();

    // Archive completes after menu unmounts (same scope), delivers Undo with single-flight, and fences Undo after scope drift.
    calls.notify.mockClear();
    calls.refresh.mockClear();
    const archiveGate = deferred<void>();
    calls.archive.mockReturnValueOnce(archiveGate.promise);
    const archivePromise = captured!.items.find((i) => i.key === "archive")!.onSelect() as Promise<void>;
    act(() => root.unmount());
    await act(async () => {
      archiveGate.resolve();
      await archivePromise;
    });
    expect(calls.notify).toHaveBeenCalledTimes(1);
    const undoAction = calls.notify.mock.calls[0][2] as ToastAction | undefined;
    expect(undoAction?.label).toBe(t("conversation-menu.undo"));

    // Undo single-flight: double click sends only one unarchive call.
    const unarchiveGate = deferred<void>();
    calls.unarchive.mockReturnValueOnce(unarchiveGate.promise);
    act(() => {
      undoAction!.run();
      undoAction!.run();
    });
    expect(calls.unarchive).toHaveBeenCalledTimes(1);
    await act(async () => {
      unarchiveGate.resolve();
      await Promise.resolve();
    });
    expect(calls.unarchive).toHaveBeenCalledTimes(1);

    // Scope drift after unmount blocks subsequent Undo clicks.
    scopeBox.set("acct-3");
    act(() => { undoAction!.run(); });
    expect(calls.unarchive).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("releases single-flight busy locks in try/finally on throw or rejection so retry succeeds", async () => {
    const s = makeSession();
    const { deps: menuDeps, calls } = makeControlledMenuDeps();
    let captured: ConversationRowMenuViewProps | null = null;

    act(() => root.render(
      <ConversationRowMenu
        s={s}
        main={false}
        anchor={anchor}
        onClose={vi.fn()}
        deps={{
          canDelete: true,
          menuDeps,
          renderView: (props) => {
            captured = props;
            return <div />;
          },
        }}
      />,
    ));

    // Rename throws synchronously on first attempt, then succeeds on retry.
    const renameItem = captured!.items.find((i) => i.key === "rename")!;
    calls.rename.mockImplementationOnce(() => { throw new Error("sync boom"); });
    await expect(renameItem.rename!.commit("New title")).rejects.toThrow("sync boom");
    calls.rename.mockResolvedValueOnce(undefined);
    await expect(renameItem.rename!.commit("New title")).resolves.toBeUndefined();
    expect(calls.rename).toHaveBeenCalledTimes(2);

    // End rejects on first attempt, then succeeds on retry.
    const endItem = captured!.items.find((i) => i.key === "end")!;
    calls.end.mockRejectedValueOnce(new Error("close denied"));
    await expect(endItem.onSelect()).rejects.toThrow("close denied");
    calls.end.mockResolvedValueOnce(undefined);
    await expect(endItem.onSelect()).resolves.toBeUndefined();
    expect(calls.end).toHaveBeenCalledTimes(2);
    expectNoDefaultSingletons();
  });

  it("keeps committed delete confirmation and in-flight operation intact when a concurrent transition render is abandoned", async () => {
    const s = makeSession();
    const { deps: menuDeps, calls } = makeControlledMenuDeps();
    const onDeletingChange = vi.fn();
    const suspendGate = deferred<void>();
    const renderedScopes: string[] = [];
    let captured: ConversationRowMenuViewProps | null = null;

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarness!: (next: { scope: string; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ scope: "acct-1", block: false });
      setHarness = setState;
      renderedScopes.push(state.scope);
      const rowDeps: ConversationRowMenuDeps = {
        scopeKey: state.scope,
        canDelete: true,
        menuDeps,
        onDeletingChange,
        renderView: (props) => {
          captured = props;
          return <div data-testid="row-state" data-deleting={String(props.deleting)} />;
        },
      };
      return (
        <Suspense fallback={<div data-fallback />}>
          <ConversationRowMenu s={s} main={false} anchor={anchor} onClose={vi.fn()} deps={rowDeps} />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    const archiveGate = deferred<void>();
    calls.archive.mockReturnValueOnce(archiveGate.promise);
    const archivePromise = captured!.items.find((i) => i.key === "archive")!.onSelect() as Promise<void>;
    await act(async () => { captured!.items.find((i) => i.key === "delete")!.onSelect(); });
    expect(host.querySelector('[data-testid="row-state"]')?.getAttribute("data-deleting")).toBe("true");
    onDeletingChange.mockClear();

    // Start concurrent transition to acct-2 that suspends before commit.
    await act(async () => {
      startTransition(() => {
        setHarness({ scope: "acct-2", block: true });
      });
    });
    expect(renderedScopes).toContain("acct-2");
    expect(onDeletingChange).not.toHaveBeenCalled();
    expect(host.querySelector('[data-testid="row-state"]')?.getAttribute("data-deleting")).toBe("true");

    // Abandon transition back to acct-1: archive resolves normally and delete confirmation remains open.
    await act(async () => setHarness({ scope: "acct-1", block: false }));
    await act(async () => {
      archiveGate.resolve();
      await archivePromise;
    });
    expect(calls.notify).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[data-testid="row-state"]')?.getAttribute("data-deleting")).toBe("true");

    // Committing acct-2 resets delete confirmation and invokes onDeletingChange(false).
    await act(async () => setHarness({ scope: "acct-2", block: false }));
    expect(host.querySelector('[data-testid="row-state"]')?.getAttribute("data-deleting")).toBe("false");
    expect(onDeletingChange).toHaveBeenCalledWith(false);
    expectNoDefaultSingletons();
  });

  it("preserves default fallback behavior when deps are omitted", () => {
    const s = makeSession({ session_id: "trip" });
    getState().features = [CONVERSATION_DELETE_FEATURE];
    getState().assistant = { machine_id: "hub", main: "main", conversations: [s] } as NonNullable<ReturnType<typeof getState>["assistant"]>;
    upsertSession(s);

    act(() => root.render(<ConversationRowMenu s={s} main={false} leader anchor={anchor} onClose={vi.fn()} />));
    expect(spies.useCanDeleteConversation).toHaveBeenCalled();
    const items = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].map((el) => el.textContent?.trim());
    expect(items).toContain(t("external-sources.direction_details"));
    expect(items).toContain(t("conversation-menu.delete"));

    const dirBtn = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(
      (el) => el.textContent?.trim() === t("external-sources.direction_details"),
    )!;
    act(() => dirBtn.click());
    expect(spies.navigate).toHaveBeenCalledWith({ name: "todos", direction: "trip", manager: "mgr-1" });
  });
});
