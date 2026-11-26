// @vitest-environment jsdom
// Controlled NodeMenu bypasses defaultNodeDeps and store.refresh, remounts Menu across node/scope switches,
// and fences stale/ABA/getter-only async actions and callbacks; default fallbacks stay pinned.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MenuNode, NodeActionDeps } from "../lib/node-actions";
import type { NodeMenuViewProps } from "./NodeMenu";

const mocks = vi.hoisted(() => ({
  defaultNodeDeps: vi.fn(),
  refresh: vi.fn(() => Promise.resolve()),
}));

vi.mock("../lib/node-actions", async (original) => ({
  ...(await original<typeof import("../lib/node-actions")>()),
  defaultNodeDeps: mocks.defaultNodeDeps,
}));
vi.mock("../lib/store", () => ({
  refresh: mocks.refresh,
}));
vi.mock("react-dom", async (original) => ({
  ...(await original<typeof import("react-dom")>()),
  createPortal: (children: React.ReactNode) => children,
}));

const { NodeMenu, NodeMenuView } = await import("./NodeMenu");
const { initI18n, t } = await import("../i18n");

function deferred<T = unknown>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const paneNode = (patch: Partial<MenuNode> = {}): MenuNode => ({
  kind: "pane",
  mid: "mac-a",
  online: true,
  host: "herdr",
  workspaceId: "ws-a",
  tabId: "tab-a",
  paneId: "pane-a",
  agent: "",
  cwd: "/code/a",
  ...patch,
});

const makeNodeDeps = (patch: Partial<NodeActionDeps> = {}): NodeActionDeps => ({
  post: vi.fn(() => Promise.resolve({})),
  renameWorkspace: vi.fn(() => Promise.resolve({})),
  renameTab: vi.fn(() => Promise.resolve({})),
  launchers: vi.fn(() => Promise.resolve([])),
  newAgent: vi.fn(),
  openSession: vi.fn(),
  copy: vi.fn(),
  notify: vi.fn(),
  changed: vi.fn(),
  ...patch,
});

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const menuButton = (label: string) =>
  [...host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) =>
    [...b.querySelectorAll("span")].some((s) => s.textContent?.trim() === label),
  );

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  await initI18n("en");
  mocks.defaultNodeDeps.mockReset();
  mocks.refresh.mockReset().mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("NodeMenu controlled deps, view separation, and scope fencing", () => {
  it("runs fully controlled via deps.nodeDeps without calling defaultNodeDeps or store.refresh", async () => {
    const nodeDeps = makeNodeDeps();
    const onClose = vi.fn();
    const beforeSelect = vi.fn();

    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 10, y: 10 } }}
        header="Pane A"
        before={[{ key: "custom-pin", label: "Pin", onSelect: beforeSelect }]}
        onClose={onClose}
        deps={{ scopeKey: "scope-1", nodeDeps }}
      />,
    ));

    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(menuButton("Pin")).not.toBeUndefined();
    expect(menuButton(t("lib-node-actions.copy_path"))).not.toBeUndefined();

    await act(async () => menuButton(t("lib-node-actions.copy_path"))!.click());
    expect(nodeDeps.copy).toHaveBeenCalledWith("/code/a");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
    expect(mocks.refresh).not.toHaveBeenCalled();
  });

  it("remounts Menu and fences stale/ABA async actions across node and scope switches", async () => {
    const postA = deferred();
    const nodeDeps = makeNodeDeps({
      post: vi.fn(() => postA.promise),
    });
    const onClose = vi.fn();
    const nodeA = paneNode({ mid: "mac-a", paneId: "pane-a", cwd: "/code/a" });
    const nodeB = paneNode({ mid: "mac-b", paneId: "pane-b", cwd: "/code/b" });

    await act(async () => root.render(
      <NodeMenu node={nodeA} at={{ point: { x: 10, y: 10 } }} header="Pane A" onClose={onClose} deps={{ scopeKey: "s1", nodeDeps }} />,
    ));

    // Confirm and trigger inline Close pane on nodeA -> leaves postA in flight.
    await act(async () => menuButton(t("lib-node-actions.close_pane"))!.click());
    await act(async () => menuButton(t("lib-node-actions.close_confirm"))!.click());
    expect(nodeDeps.post).toHaveBeenCalledWith("mac-a", "panes/close", { pane: "pane-a" });

    // Switch to nodeB while postA is still pending -> Menu remounts clean (not confirming, not busy).
    await act(async () => root.render(
      <NodeMenu node={nodeB} at={{ point: { x: 10, y: 10 } }} header="Pane B" onClose={onClose} deps={{ scopeKey: "s1", nodeDeps }} />,
    ));
    await flush();
    expect(menuButton(t("lib-node-actions.close_pane"))).not.toBeUndefined();
    expect(menuButton(t("lib-node-actions.close_confirm"))).toBeUndefined();

    // ABA: switch back to nodeA, then resolve stale postA from epoch 1.
    await act(async () => root.render(
      <NodeMenu node={nodeA} at={{ point: { x: 10, y: 10 } }} header="Pane A" onClose={onClose} deps={{ scopeKey: "s1", nodeDeps }} />,
    ));
    await flush();
    await act(async () => postA.resolve({}));
    await flush();

    // Stale completion from epoch 1 must not notify, refresh, or close the epoch-3 menu.
    expect(nodeDeps.notify).not.toHaveBeenCalled();
    expect(nodeDeps.changed).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
  });

  it("blocks cross-scope mutations and clears stale callbacks when getter-only scopeKey mutates without re-render", async () => {
    let liveScope = "acct-1";
    const postReq = deferred();
    const nodeDeps = makeNodeDeps({
      post: vi.fn(() => postReq.promise),
    });
    const onRefresh = vi.fn();
    const onClose = vi.fn();
    const node = paneNode({ kind: "machine", mid: "mac-a", onRefresh });

    // First verify pre-action gate when getter-only scope mutates before click.
    await act(async () => root.render(
      <NodeMenu node={node} at={{ point: { x: 10, y: 10 } }} onClose={onClose} deps={{ scopeKey: () => liveScope, nodeDeps }} />,
    ));
    liveScope = "acct-2";
    await act(async () => menuButton(t("lib-node-actions.refresh"))!.click());
    expect(onRefresh).not.toHaveBeenCalled();

    // Re-render in acct-2 on a pane, start inline close, mutate getter-only scope to acct-3 before post resolves.
    await act(async () => root.render(
      <NodeMenu node={paneNode()} at={{ point: { x: 10, y: 10 } }} onClose={onClose} deps={{ scopeKey: () => liveScope, nodeDeps }} />,
    ));
    await act(async () => menuButton(t("lib-node-actions.close_pane"))!.click());
    await act(async () => menuButton(t("lib-node-actions.close_confirm"))!.click());
    expect(nodeDeps.post).toHaveBeenCalledTimes(1);

    liveScope = "acct-3";
    await act(async () => postReq.resolve({}));
    await flush();
    expect(nodeDeps.notify).not.toHaveBeenCalled();
    expect(nodeDeps.changed).not.toHaveBeenCalled();
  });

  it("fences pre-built actions, before items, submenu children, and rename commits after getter-only scopeKey mutation (P1-2)", async () => {
    let liveScope = "acct-a";
    const beforeSelect = vi.fn();
    const closeAction = vi.fn(() => Promise.resolve());
    const childAction = vi.fn();
    const renameCommit = vi.fn(() => Promise.resolve());
    const onClose = vi.fn();

    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 10, y: 10 } }}
        onClose={onClose}
        before={[{ key: "before-pin", label: "Pin", onSelect: beforeSelect }]}
        deps={{
          scopeKey: () => liveScope,
          actions: [
            { key: "pre-close", label: "Close pane", confirm: "Confirm close", inline: true, onSelect: closeAction },
            { key: "pre-sub", label: "More", onSelect: () => {}, children: [{ key: "sub-item", label: "Sub action", onSelect: childAction }] },
            { key: "pre-rename", label: "Rename", onSelect: () => {}, rename: { value: "old", commit: renameCommit } },
          ],
        }}
      />,
    ));

    // Step 1: click Close pane once to enter confirming state while still in acct-a.
    await act(async () => menuButton("Close pane")!.click());
    expect(menuButton("Confirm close")).not.toBeUndefined();

    // Mutate getter-only scope WITHOUT re-rendering, then confirm Close pane -> raw closeAction MUST NOT fire.
    liveScope = "acct-b";
    await act(async () => menuButton("Confirm close")!.click());
    await flush();
    expect(closeAction).not.toHaveBeenCalled();

    // Re-open in acct-b, mutate to acct-c without re-render, and verify before item, rename commit, and submenu child are all fenced.
    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 10, y: 10 } }}
        onClose={onClose}
        before={[{ key: "before-pin", label: "Pin", onSelect: beforeSelect }]}
        deps={{
          scopeKey: () => liveScope,
          actions: [
            { key: "pre-sub", label: "More", onSelect: () => {}, children: [{ key: "sub-item", label: "Sub action", onSelect: childAction }] },
            { key: "pre-rename", label: "Rename", onSelect: () => {}, rename: { value: "old", commit: renameCommit } },
          ],
        }}
      />,
    ));

    // Open submenu and rename input while in acct-b, then mutate scope to acct-c before activating them.
    await act(async () => menuButton("More")!.click());
    expect(menuButton("Sub action")).not.toBeUndefined();
    liveScope = "acct-c";
    await act(async () => menuButton("Sub action")!.click());
    await act(async () => menuButton("Pin")!.click());
    expect(childAction).not.toHaveBeenCalled();
    expect(beforeSelect).not.toHaveBeenCalled();

    // Also verify rename commit is fenced when scope mutates while rename input is open.
    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 10, y: 10 } }}
        onClose={onClose}
        deps={{
          scopeKey: () => liveScope,
          actions: [{ key: "pre-rename", label: "Rename", onSelect: () => {}, rename: { value: "old", commit: renameCommit } }],
        }}
      />,
    ));
    await act(async () => menuButton("Rename")!.click());
    const input = host.querySelector<HTMLInputElement>("input")!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "new-name");
    await act(async () => input.dispatchEvent(new Event("input", { bubbles: true })));
    liveScope = "acct-d";
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    await flush();
    expect(renameCommit).not.toHaveBeenCalled();
    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
  });

  it("supports deps.actions, deps.renderMenu, and pure NodeMenuView without default singletons", async () => {
    let captured: NodeMenuViewProps | undefined;
    const onAction = vi.fn();
    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 20, y: 30 } }}
        header="Custom"
        before={[{ key: "b1", label: "First", onSelect: vi.fn() }]}
        onClose={vi.fn()}
        deps={{
          actions: [{ key: "a1", label: "Custom Action", onSelect: onAction }],
          renderMenu: (props) => {
            captured = props;
            return <div data-testid="custom-menu">{props.items.map((i) => i.label).join(",")}</div>;
          },
        }}
      />,
    ));

    expect(host.querySelector('[data-testid="custom-menu"]')?.textContent).toBe("First,Custom Action");
    expect(captured?.items[1].divider).toBe(true);
    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
    expect(mocks.refresh).not.toHaveBeenCalled();

    // Pure NodeMenuView renders directly.
    const onCloseView = vi.fn();
    await act(async () => root.render(
      <NodeMenuView
        at={{ point: { x: 10, y: 10 } }}
        header="Pure View"
        items={[{ key: "v1", label: "View Item", onSelect: onAction }]}
        onClose={onCloseView}
      />,
    ));
    await act(async () => menuButton("View Item")!.click());
    expect(onAction).toHaveBeenCalledTimes(1);
    expect(onCloseView).toHaveBeenCalledTimes(1);
  });

  it("fences imperative subscribed ABA scope burst on pending async actions", async () => {
    let currentScope = "s1";
    const listeners = new Set<() => void>();
    const emit = (next: string) => {
      currentScope = next;
      listeners.forEach((l) => l());
    };
    const postReq = deferred();
    const nodeDeps = makeNodeDeps({ post: vi.fn(() => postReq.promise) });
    const onClose = vi.fn();

    await act(async () => root.render(
      <NodeMenu
        node={paneNode()}
        at={{ point: { x: 10, y: 10 } }}
        onClose={onClose}
        deps={{
          scopeKey: () => currentScope,
          subscribeScopeKey: (l) => { listeners.add(l); return () => listeners.delete(l); },
          nodeDeps,
        }}
      />,
    ));

    await act(async () => menuButton(t("lib-node-actions.close_pane"))!.click());
    await act(async () => menuButton(t("lib-node-actions.close_confirm"))!.click());
    expect(nodeDeps.post).toHaveBeenCalledTimes(1);

    await act(async () => {
      emit("s2");
      emit("s1");
    });
    await flush();
    await act(async () => postReq.resolve({}));
    await flush();
    expect(nodeDeps.notify).not.toHaveBeenCalled();
    expect(nodeDeps.changed).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.defaultNodeDeps).not.toHaveBeenCalled();
  });

  it("preserves defaultNodeDeps and store.refresh fallback when deps are omitted, and honors deps.refresh override", async () => {
    let capturedChanged: (() => void) | undefined;
    mocks.defaultNodeDeps.mockImplementation((changed: () => void) => {
      capturedChanged = changed;
      return makeNodeDeps({ changed });
    });

    await act(async () => root.render(
      <NodeMenu node={paneNode()} at={{ point: { x: 5, y: 5 } }} onClose={vi.fn()} />,
    ));

    expect(mocks.defaultNodeDeps).toHaveBeenCalledTimes(1);
    capturedChanged?.();
    expect(mocks.refresh).toHaveBeenCalledTimes(1);

    // Supplying deps.refresh replaces store.refresh while keeping defaultNodeDeps.
    const customRefresh = vi.fn();
    await act(async () => root.render(
      <NodeMenu node={paneNode()} at={{ point: { x: 5, y: 5 } }} onClose={vi.fn()} deps={{ refresh: customRefresh }} />,
    ));
    capturedChanged?.();
    expect(customRefresh).toHaveBeenCalledTimes(1);
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
  });
});
