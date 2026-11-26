// @vitest-environment jsdom
import { act, startTransition, Suspense, useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import type { Panel } from "../lib/panel";

const spies = vi.hoisted(() => ({
  usePanel: vi.fn(),
  usePanelCanGoBack: vi.fn(),
  closePanel: vi.fn(),
  popPanel: vi.fn(),
  panelOpener: vi.fn(),
  useLayout: vi.fn(),
  useMedia: vi.fn(),
  mediaMatches: vi.fn(),
  activeComposer: vi.fn(),
  openOverlay: vi.fn(),
}));

vi.mock("../lib/panel", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/panel")>();
  return {
    ...actual,
    usePanel: () => {
      spies.usePanel();
      return actual.usePanel();
    },
    usePanelCanGoBack: () => {
      spies.usePanelCanGoBack();
      return actual.usePanelCanGoBack();
    },
    closePanel: () => {
      spies.closePanel();
      return actual.closePanel();
    },
    popPanel: () => {
      spies.popPanel();
      return actual.popPanel();
    },
    panelOpener: () => {
      spies.panelOpener();
      return actual.panelOpener();
    },
  };
});

vi.mock("../lib/layout", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/layout")>();
  return {
    ...actual,
    useLayout: () => {
      spies.useLayout();
      return actual.useLayout();
    },
  };
});

vi.mock("../lib/viewport", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/viewport")>();
  return {
    ...actual,
    useMedia: (query: string) => {
      spies.useMedia(query);
      return actual.useMedia(query);
    },
    mediaMatches: (query: string) => {
      spies.mediaMatches(query);
      return actual.mediaMatches(query);
    },
  };
});

vi.mock("../lib/links", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/links")>();
  return {
    ...actual,
    activeComposer: () => {
      spies.activeComposer();
      return actual.activeComposer();
    },
  };
});

vi.mock("../lib/back-close", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/back-close")>();
  return {
    ...actual,
    useCloseOnBack: (open: boolean, onClose: () => void, enabled = true, step = 0) => {
      if (open && enabled) spies.openOverlay(step);
      return actual.useCloseOnBack(open, onClose, enabled, step);
    },
  };
});

vi.mock("./session/FilesPanel", () => ({
  FilesPanel: ({ cwd, onClose }: { cwd: string; onClose: () => void }) => (
    <div data-testid="default-files">
      <span>{cwd}</span>
      <button type="button" data-testid="default-files-close" onClick={onClose}>Close</button>
    </div>
  ),
}));

import { closePanel as rawClosePanel, openPanel, useSidePanelBack } from "../lib/panel";
import { SidePanel, SidePanelView, type SidePanelRenderActions, type SidePanelViewProps } from "./SidePanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

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
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    listenerCount: () => listeners.size,
  };
}

let host: HTMLDivElement;
let root: Root;

const until = async (ok: () => boolean) => {
  for (let i = 0; i < 200 && !ok(); i++) {
    await act(async () => { await new Promise((r) => setTimeout(r, 10)); });
  }
};

function clearSpies() {
  Object.values(spies).forEach((s) => s.mockClear());
}

function expectNoDefaultSingletons() {
  expect(spies.usePanel).not.toHaveBeenCalled();
  expect(spies.usePanelCanGoBack).not.toHaveBeenCalled();
  expect(spies.closePanel).not.toHaveBeenCalled();
  expect(spies.popPanel).not.toHaveBeenCalled();
  expect(spies.panelOpener).not.toHaveBeenCalled();
  expect(spies.useLayout).not.toHaveBeenCalled();
  expect(spies.useMedia).not.toHaveBeenCalled();
  expect(spies.mediaMatches).not.toHaveBeenCalled();
  expect(spies.activeComposer).not.toHaveBeenCalled();
  expect(spies.openOverlay).not.toHaveBeenCalled();
}

beforeEach(() => {
  localStorage.removeItem("persona.panelWidth");
  clearSpies();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => rawClosePanel());
  act(() => root.unmount());
  host.remove();
  localStorage.removeItem("persona.panelWidth");
  vi.restoreAllMocks();
});

describe("SidePanelView (pure presentational)", () => {
  it("renders null when closed, exposes SidePanelBackContext when canGoBack is true, and closes on Escape", () => {
    const onClose = vi.fn();
    const onBack = vi.fn();
    act(() => root.render(
      <SidePanelView
        panel={null}
        width={600}
        maxWidth={900}
        onClose={onClose}
        onBack={onBack}
        onResize={vi.fn()}
        onCommitWidth={vi.fn()}
        onResetWidth={vi.fn()}
      />,
    ));
    expect(host.querySelector("aside")).toBeNull();

    function BackReader() {
      const ctxBack = useSidePanelBack();
      return ctxBack ? <button type="button" data-testid="ctx-back" onClick={ctxBack}>Back</button> : <span data-testid="no-back" />;
    }

    const panel: Panel = { kind: "files", mid: "m1", sid: "s1", cwd: "/work/demo" };
    act(() => root.render(
      <SidePanelView
        panel={panel}
        canGoBack
        width={540}
        maxWidth={900}
        onClose={onClose}
        onBack={onBack}
        onResize={vi.fn()}
        onCommitWidth={vi.fn()}
        onResetWidth={vi.fn()}
        renderPanel={() => <BackReader />}
      />,
    ));

    const aside = host.querySelector<HTMLElement>("aside")!;
    expect(aside.getAttribute("aria-label")).toBe(t("sidepanel.files"));
    expect(aside.style.getPropertyValue("--panel-w")).toBe("540px");
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="ctx-back"]')!.click());
    expect(onBack).toHaveBeenCalledTimes(1);

    const sep = host.querySelector<HTMLElement>('aside [role="separator"]')!;
    sep.focus();
    const esc = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    act(() => { sep.dispatchEvent(esc); });
    expect(esc.defaultPrevented).toBe(true);
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });
});

describe("SidePanel (controlled & default)", () => {
  it("bypasses default singletons, caches fresh-object panel getter, and preserves body identity across bridge switches", () => {
    const panelBox = createExternalBox<Panel | null>({ kind: "background", mid: "m1", sid: "s1", run: "w1" });
    const canGoBackBox = createExternalBox(false);
    const maximizedBox = createExternalBox(false);
    const narrowBox = createExternalBox(false);
    const onClose = vi.fn();
    const onPop = vi.fn(() => false);
    const restoreFocus = vi.fn();
    let contentMounts = 0;

    function PanelProbe({ p }: { p: Panel }) {
      useEffect(() => { contentMounts += 1; }, []);
      return <div data-testid="panel-probe">{p.kind === "background" ? p.run : p.kind}</div>;
    }

    // Start in getter+subscribe mode with a fresh-object panel getter.
    act(() => root.render(
      <SidePanel
        deps={{
          panel: () => (panelBox.get() ? { ...panelBox.get()! } : null),
          subscribePanel: panelBox.subscribe,
          canGoBack: canGoBackBox.get,
          subscribeCanGoBack: canGoBackBox.subscribe,
          maximized: maximizedBox.get,
          subscribeMaximized: maximizedBox.subscribe,
          narrow: narrowBox.get,
          subscribeNarrow: narrowBox.subscribe,
          isPhone: false,
          getOpener: () => null,
          focusFallback: null,
          restoreFocus,
          closePanel: onClose,
          popPanel: onPop,
          closeOnBack: false,
          room: 1400,
          readStoredWidth: () => 560,
          writeStoredWidth: vi.fn(),
          clearStoredWidth: vi.fn(),
          renderPanel: (p) => <PanelProbe p={p} />,
        }}
      />,
    ));
    expect(contentMounts).toBe(1);
    expect(host.querySelector('[data-testid="panel-probe"]')?.textContent).toBe("w1");
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("560");
    expectNoDefaultSingletons();

    // Mutable progress update (run w1 -> w2, canGoBack -> true) keeps body mounted without remount.
    act(() => {
      panelBox.set({ kind: "background", mid: "m1", sid: "s1", run: "w2" });
      canGoBackBox.set(true);
    });
    expect(contentMounts).toBe(1);
    expect(host.querySelector('[data-testid="panel-probe"]')?.textContent).toBe("w2");

    // Switch all 4 bridges from getter+subscribe to direct values: SidePanelBody and content must NOT remount.
    act(() => root.render(
      <SidePanel
        deps={{
          panel: { kind: "background", mid: "m1", sid: "s1", run: "w2" },
          canGoBack: true,
          maximized: false,
          narrow: false,
          isPhone: false,
          getOpener: () => null,
          focusFallback: null,
          restoreFocus,
          closePanel: onClose,
          popPanel: onPop,
          closeOnBack: false,
          room: 1400,
          readStoredWidth: () => 560,
          writeStoredWidth: vi.fn(),
          clearStoredWidth: vi.fn(),
          renderPanel: (p) => <PanelProbe p={p} />,
        }}
      />,
    ));
    expect(contentMounts).toBe(1);
    expect(panelBox.listenerCount()).toBe(0);
    expect(canGoBackBox.listenerCount()).toBe(0);
    expect(maximizedBox.listenerCount()).toBe(0);
    expect(narrowBox.listenerCount()).toBe(0);
    expectNoDefaultSingletons();
  });

  it("handles width/room source switches fail-closed and recovers via try/finally when storage callbacks throw", () => {
    const onWidthChange = vi.fn();
    const onWidthCommit = vi.fn();
    const onWidthReset = vi.fn();
    const writeStoredWidth = vi.fn(() => { throw new Error("quota exceeded"); });
    const clearStoredWidth = vi.fn(() => { throw new Error("clear failed"); });
    const panel: Panel = { kind: "files", mid: "m1", sid: "s1", cwd: "/work" };

    // Start with controlled width 500 and controlled room 1200.
    act(() => root.render(
      <SidePanel
        deps={{
          panel,
          canGoBack: false,
          maximized: false,
          narrow: false,
          closeOnBack: false,
          width: 500,
          room: 1200,
          readStoredWidth: () => 480,
          writeStoredWidth,
          clearStoredWidth,
          onWidthChange,
          onWidthCommit,
          onWidthReset,
          renderPanel: () => <div data-testid="p" />,
        }}
      />,
    ));
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("500");

    // Switching width from controlled (500) -> uncontrolled (undefined) immediately re-reads readStoredWidth (480).
    act(() => root.render(
      <SidePanel
        deps={{
          panel,
          canGoBack: false,
          maximized: false,
          narrow: false,
          closeOnBack: false,
          room: 1200,
          readStoredWidth: () => 480,
          writeStoredWidth,
          clearStoredWidth,
          onWidthChange,
          onWidthCommit,
          onWidthReset,
          renderPanel: () => <div data-testid="p" />,
        }}
      />,
    ));
    const sep = () => host.querySelector<HTMLElement>('aside [role="separator"]')!;
    expect(sep().getAttribute("aria-valuenow")).toBe("480");

    // Keyboard ArrowLeft widens to 496; writeStoredWidth throws, yet local width and onWidthCommit still update in finally.
    act(() => { sep().dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true, cancelable: true })); });
    expect(writeStoredWidth).toHaveBeenCalledWith(496);
    expect(sep().getAttribute("aria-valuenow")).toBe("496");
    expect(onWidthCommit).toHaveBeenCalledWith(496);
    expect(onWidthChange).toHaveBeenCalledWith(496);
    expect(localStorage.getItem("persona.panelWidth")).toBeNull();

    // Enter resets width to 600; clearStoredWidth throws, yet local width and onWidthReset still run in finally.
    act(() => { sep().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
    expect(clearStoredWidth).toHaveBeenCalledTimes(1);
    expect(sep().getAttribute("aria-valuenow")).toBe("600");
    expect(onWidthReset).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("resets on scopeKey ABA transitions, fences stale callbacks/back overlays, and skips stale focus restoration across scope switches", () => {
    const scopeBox = createExternalBox("acct-1");
    const savedWidths: Record<string, number> = { "acct-1": 520, "acct-2": 640 };
    const closeSpy = vi.fn();
    const popSpy = vi.fn(() => true);
    const restoreFocus = vi.fn();
    let backHandlers: Array<() => void> = [];
    let backCleanups = 0;
    const registerBackOverlay = vi.fn((cb: () => void) => {
      backHandlers.push(cb);
      return () => { backCleanups += 1; };
    });
    let contentMounts = 0;
    let capturedActions: SidePanelRenderActions | null = null;

    function ScopedContent({ actions }: { actions: SidePanelRenderActions }) {
      capturedActions = actions;
      useEffect(() => { contentMounts += 1; }, []);
      return <div data-testid="scoped-content" />;
    }

    const panel: Panel = { kind: "files", mid: "m1", sid: "s1", cwd: "/work" };
    act(() => root.render(
      <SidePanel
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          panel,
          canGoBack: true,
          maximized: false,
          narrow: true,
          isPhone: false,
          getOpener: () => null,
          focusFallback: null,
          restoreFocus,
          closePanel: closeSpy,
          popPanel: popSpy,
          registerBackOverlay,
          room: 1400,
          readStoredWidth: () => savedWidths[scopeBox.get()] ?? null,
          writeStoredWidth: (w) => { savedWidths[scopeBox.get()] = w; },
          clearStoredWidth: vi.fn(),
          renderPanel: (_p, actions) => <ScopedContent actions={actions} />,
        }}
      />,
    ));

    expect(contentMounts).toBe(1);
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("520");
    expect(backHandlers).toHaveLength(1);
    const staleActionsFromA1 = capturedActions!;
    const staleBackFromA1 = backHandlers[0];

    // Switch scope acct-1 -> acct-2: remounts panel content, loads acct-2 width, tears down old back overlay, no focus steal.
    act(() => scopeBox.set("acct-2"));
    expect(contentMounts).toBe(2);
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("640");
    expect(backCleanups).toBe(1);
    expect(restoreFocus).not.toHaveBeenCalled();

    // ABA return acct-2 -> acct-1: remounts again and fences callbacks from original acct-1 epoch.
    act(() => scopeBox.set("acct-1"));
    expect(contentMounts).toBe(3);
    act(() => {
      staleActionsFromA1.onClose();
      staleActionsFromA1.onBack?.();
      staleBackFromA1();
    });
    expect(closeSpy).not.toHaveBeenCalled();
    expect(popSpy).not.toHaveBeenCalled();

    // Active back overlay fires popPanel and re-arms Back registration.
    const countBefore = registerBackOverlay.mock.calls.length;
    act(() => backHandlers[backHandlers.length - 1]());
    expect(popSpy).toHaveBeenCalledTimes(1);
    expect(registerBackOverlay.mock.calls.length).toBe(countBefore + 1);

    // Closing within the same scope DOES restore focus.
    act(() => root.render(
      <SidePanel
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          panel: null,
          canGoBack: false,
          maximized: false,
          narrow: true,
          isPhone: false,
          getOpener: () => null,
          focusFallback: null,
          restoreFocus,
          closePanel: closeSpy,
          popPanel: popSpy,
          registerBackOverlay,
          room: 1400,
          readStoredWidth: () => savedWidths[scopeBox.get()] ?? null,
          writeStoredWidth: vi.fn(),
          clearStoredWidth: vi.fn(),
          renderPanel: (_p, actions) => <ScopedContent actions={actions} />,
        }}
      />,
    ));
    expect(restoreFocus).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("keeps committed back overlay, width, and callbacks intact when a concurrent transition render is abandoned", async () => {
    const closeSpy = vi.fn();
    const popSpy = vi.fn(() => false);
    const suspendGate = deferred<void>();
    const renderedScopes: string[] = [];
    let backActive = 0;
    let latestBack: (() => void) | null = null;
    let capturedView: SidePanelViewProps | null = null;
    const registerBackOverlay = vi.fn((cb: () => void) => {
      backActive += 1;
      latestBack = cb;
      return () => { backActive -= 1; };
    });

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarness!: (next: { scope: string; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ scope: "acct-1", block: false });
      setHarness = setState;
      renderedScopes.push(state.scope);
      return (
        <Suspense fallback={<div data-fallback />}>
          <SidePanel
            deps={{
              scopeKey: state.scope,
              panel: { kind: "files", mid: "m1", sid: "s1", cwd: "/work" },
              canGoBack: true,
              maximized: false,
              narrow: true,
              isPhone: false,
              getOpener: () => null,
              focusFallback: null,
              restoreFocus: vi.fn(),
              closePanel: closeSpy,
              popPanel: popSpy,
              registerBackOverlay,
              room: 1400,
              readStoredWidth: () => (state.scope === "acct-1" ? 500 : 660),
              writeStoredWidth: vi.fn(),
              clearStoredWidth: vi.fn(),
              renderView: (props) => {
                capturedView = props;
                return <SidePanelView {...props} />;
              },
              renderPanel: () => <div data-testid="concurrent-panel" />,
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    expect(backActive).toBe(1);
    expect(capturedView!.width).toBe(500);
    const registerCallsBefore = registerBackOverlay.mock.calls.length;

    // Start a concurrent transition to acct-2 that suspends before commit.
    await act(async () => {
      startTransition(() => {
        setHarness({ scope: "acct-2", block: true });
      });
    });
    expect(renderedScopes).toContain("acct-2");
    // Committed back overlay, width, and close callback must remain untouched.
    expect(backActive).toBe(1);
    expect(registerBackOverlay.mock.calls.length).toBe(registerCallsBefore);
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("500");

    // Abandon the transition back to acct-1; active back handler and close handler still work.
    await act(async () => setHarness({ scope: "acct-1", block: false }));
    expect(backActive).toBe(1);
    await act(async () => latestBack!());
    expect(popSpy).toHaveBeenCalledTimes(1);
    await act(async () => capturedView!.onClose());
    expect(closeSpy).toHaveBeenCalledTimes(1);

    // Committing acct-2 switches width and re-registers back overlay cleanly.
    await act(async () => setHarness({ scope: "acct-2", block: false }));
    expect(host.querySelector<HTMLElement>('aside [role="separator"]')?.getAttribute("aria-valuenow")).toBe("660");
    expect(registerBackOverlay.mock.calls.length).toBeGreaterThan(registerCallsBefore);
    expectNoDefaultSingletons();
  });

  it("preserves default fallback behavior when deps are omitted", async () => {
    act(() => openPanel({ kind: "files", mid: "m1", sid: "s1", cwd: "/work/default" }));
    await act(async () => { root.render(<SidePanel />); });
    await until(() => Boolean(host.querySelector('[data-testid="default-files"]')));
    expect(spies.usePanel).toHaveBeenCalled();
    expect(spies.useLayout).toHaveBeenCalled();
    expect(spies.useMedia).toHaveBeenCalled();
    expect(host.querySelector('[data-testid="default-files"]')?.textContent).toContain("/work/default");

    act(() => host.querySelector<HTMLButtonElement>('[data-testid="default-files-close"]')!.click());
    expect(spies.closePanel).toHaveBeenCalled();
    expect(host.querySelector("aside")).toBeNull();
  });
});
