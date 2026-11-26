// @vitest-environment jsdom
// Controlled ShortcutHelp bypasses default shortcut-help/shortcuts/router singletons, caches fresh-object/array getters,
// preserves render purity across abandoned concurrent renders, fences cross-scope/open-close/ABA actions, recovers via
// try/finally, and preserves default & lazyDialog prop-passthrough behavior.
import { act, startTransition, StrictMode, Suspense, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import type { Bindings, LocalShortcut, Platform, ShortcutDef, ShortcutPrefs } from "../lib/shortcuts";
import type { ShortcutHelpDeps } from "./ShortcutHelp";

const mocks = vi.hoisted(() => ({
  helpOpen: false,
  bindings: { jump: "Mod+K", help: "Mod+/" } as Bindings,
  prefs: { overrides: {}, disabled: [], off: false } as ShortcutPrefs,
  platform: "other" as Platform,
  useHelpOpen: vi.fn(),
  closeHelp: vi.fn(),
  openShortcutSettings: vi.fn(),
  useBindings: vi.fn(),
  useShortcutPrefs: vi.fn(),
  currentPlatform: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock("../lib/shortcut-help", () => ({
  useHelpOpen: () => {
    mocks.useHelpOpen();
    return mocks.helpOpen;
  },
  closeHelp: () => mocks.closeHelp(),
  openShortcutSettings: (go: (to: string) => void) => mocks.openShortcutSettings(go),
}));

vi.mock("../lib/shortcuts", async (original) => ({
  ...(await original<typeof import("../lib/shortcuts")>()),
  useBindings: () => {
    mocks.useBindings();
    return mocks.bindings;
  },
  useShortcutPrefs: () => {
    mocks.useShortcutPrefs();
    return mocks.prefs;
  },
  currentPlatform: () => {
    mocks.currentPlatform();
    return mocks.platform;
  },
}));

vi.mock("../lib/router", async (original) => ({
  ...(await original<typeof import("../lib/router")>()),
  navigate: (to: unknown) => mocks.navigate(to),
}));

const { ShortcutHelp, ShortcutHelpView } = await import("./ShortcutHelp");

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

const SAMPLE_SHORTCUTS: readonly ShortcutDef[] = [
  { id: "jump", label: "Jump", chord: "Mod+K", scope: "global" },
  { id: "help", label: "Help", chord: "Mod+/", scope: "global" },
  { id: "term", label: "Toggle terminal", chord: "Mod+J", scope: "session" },
  { id: "group", label: "Group sidebar", chord: "Mod+E", scope: "sidebar" },
];

const SAMPLE_LOCAL: readonly LocalShortcut[] = [
  { id: "terminal-find", label: "Find in terminal", chord: { mac: "Mod+F", other: "Mod+Shift+F" }, note: "Terminal note on non-Mac" },
];

const expectNoDefaultSingletons = () => {
  expect(mocks.useHelpOpen).not.toHaveBeenCalled();
  expect(mocks.closeHelp).not.toHaveBeenCalled();
  expect(mocks.openShortcutSettings).not.toHaveBeenCalled();
  expect(mocks.useBindings).not.toHaveBeenCalled();
  expect(mocks.useShortcutPrefs).not.toHaveBeenCalled();
  expect(mocks.currentPlatform).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
};

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]');
const editButton = () => [...(dialog()?.querySelectorAll("button") ?? [])].find((b) => b.textContent?.includes(t("shortcuthelp.edit_in_settings"))) as HTMLButtonElement | undefined;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  Object.values(mocks).forEach((v) => {
    if (typeof v === "function") v.mockReset();
  });
  mocks.helpOpen = false;
  mocks.bindings = { jump: "Mod+K", help: "Mod+/" };
  mocks.prefs = { overrides: {}, disabled: [], off: false };
  mocks.platform = "other";
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("ShortcutHelpView (pure presentation)", () => {
  it("renders bound, disabled, unset, Mac vs other platform chords, busy state, and error alert without touching singletons", async () => {
    const onClose = vi.fn();
    const onEdit = vi.fn();

    await act(async () => root.render(
      <ShortcutHelpView
        open
        onClose={onClose}
        onEditInSettings={onEdit}
        bindings={{ jump: "Mod+K", help: "", term: "", group: "Mod+E" }}
        prefs={{ off: false, disabled: ["help"] }}
        platform="mac"
        shortcuts={SAMPLE_SHORTCUTS}
        localShortcuts={SAMPLE_LOCAL}
        busy
        error="Settings route unavailable"
      />,
    ));

    const el = dialog()!;
    expect(el).not.toBeNull();
    expect(el.textContent).toContain("⌘");
    expect(el.textContent).toContain(t("shortcuthelp.off"));
    expect(el.textContent).toContain(t("shortcuthelp.unset"));
    expect(el.textContent).not.toContain("Terminal note on non-Mac");
    expect(el.querySelector('[role="alert"]')?.textContent).toBe("Settings route unavailable");
    expect(editButton()?.getAttribute("aria-busy")).toBe("true");

    // Non-Mac renders Ctrl keycaps and terminal note.
    await act(async () => root.render(
      <ShortcutHelpView
        open
        onClose={onClose}
        onEditInSettings={onEdit}
        bindings={{ jump: "Mod+K", help: "Mod+/", term: "Mod+J", group: "Mod+E" }}
        prefs={{ off: false, disabled: [] }}
        platform="other"
        shortcuts={SAMPLE_SHORTCUTS}
        localShortcuts={SAMPLE_LOCAL}
      />,
    ));
    expect(dialog()!.textContent).toContain("Ctrl");
    expect(dialog()!.textContent).toContain("Terminal note on non-Mac");
    await act(async () => { editButton()!.click(); });
    expect(onEdit).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });
});

describe("ShortcutHelp controlled mode, snapshot caching, render purity, fencing, and error recovery", () => {
  it("runs fully controlled with fresh-object/array getters and preserves dialog identity across direct-to-subscribed bridge switch", async () => {
    const listeners = new Set<() => void>();
    const subscribe = (l: () => void) => {
      listeners.add(l);
      return () => { listeners.delete(l); };
    };
    let open = true;
    let platform: Platform = "mac";
    let bindings: Bindings = { jump: "Mod+K", help: "Mod+/", term: "Mod+J", group: "Mod+E" };
    let disabled: string[] = [];
    let reads = 0;
    const onClose = vi.fn();
    const openSettings = vi.fn();

    // Direct mode first.
    await act(async () => root.render(
      <StrictMode>
        <ShortcutHelp
          deps={{
            scopeKey: "s1",
            open: true,
            bindings,
            prefs: { off: false, disabled: [] },
            platform: "mac",
            shortcuts: SAMPLE_SHORTCUTS,
            localShortcuts: SAMPLE_LOCAL,
            onClose,
            openSettings,
          }}
        />
      </StrictMode>,
    ));
    const initialDialog = dialog()!;
    expect(initialDialog.textContent).toContain("⌘");
    expectNoDefaultSingletons();

    // Switch in-place to fresh-object / fresh-array getters + shared subscribe on the same scope.
    const getterDeps: ShortcutHelpDeps = {
      scopeKey: () => "s1",
      open: () => open,
      bindings: () => {
        reads += 1;
        return { ...bindings };
      },
      prefs: () => ({ off: false, disabled: [...disabled] }),
      platform: () => platform,
      shortcuts: () => SAMPLE_SHORTCUTS.map((s) => ({ ...s })),
      localShortcuts: () => SAMPLE_LOCAL.map((s) => ({ ...s, chord: { ...s.chord } })),
      subscribe,
      onClose,
      openSettings,
    };
    await act(async () => root.render(
      <StrictMode>
        <ShortcutHelp deps={getterDeps} />
      </StrictMode>,
    ));
    expect(dialog()).toBe(initialDialog);
    expect(reads).toBeLessThan(20);

    // Identical re-emission does not loop; changing bindings & platform updates cleanly.
    await act(async () => { listeners.forEach((l) => l()); });
    expect(reads).toBeLessThan(30);

    platform = "other";
    bindings = { ...bindings, jump: "" };
    disabled = ["jump"];
    await act(async () => { listeners.forEach((l) => l()); });
    expect(dialog()!.textContent).toContain("Ctrl");
    expect(dialog()!.textContent).toContain(t("shortcuthelp.off"));
    expect(dialog()!.textContent).toContain("Terminal note on non-Mac");

    // Clicking "Change in Settings" runs controlled openSettings + onClose without global navigate/openShortcutSettings.
    await act(async () => { editButton()!.click(); });
    expect(openSettings).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);

    open = false;
    await act(async () => { listeners.forEach((l) => l()); });
    expect(dialog()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("fences in-flight async openSettings across scope switches, ABA bursts, open->close transitions, and unmount", async () => {
    const scopeListeners = new Set<() => void>();
    let currentScope = "s1";
    const calls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const onClose = vi.fn();
    const onError = vi.fn();

    const makeDeps = (open = true): ShortcutHelpDeps => ({
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      open,
      bindings: { jump: "Mod+K" },
      prefs: { off: false, disabled: [] },
      platform: "mac",
      shortcuts: SAMPLE_SHORTCUTS,
      localShortcuts: SAMPLE_LOCAL,
      onClose,
      openSettings: (signal) => {
        const gate = deferred<void>();
        calls.push({ signal, gate });
        return gate.promise;
      },
      onError,
    });

    await act(async () => root.render(<ShortcutHelp deps={makeDeps(true)} />));
    await act(async () => { editButton()!.click(); });
    await act(async () => { editButton()!.click(); }); // single-flight guard
    expect(calls).toHaveLength(1);
    expect(editButton()?.getAttribute("aria-busy")).toBe("true");

    // ABA scope burst aborts in-flight openSettings, releases busy, and discards late resolve/reject.
    await act(async () => {
      currentScope = "s2";
      scopeListeners.forEach((l) => l());
      currentScope = "s1";
      scopeListeners.forEach((l) => l());
    });
    expect(calls[0].signal.aborted).toBe(true);
    expect(editButton()?.getAttribute("aria-busy")).toBeNull();
    await act(async () => {
      calls[0].gate.resolve();
      await Promise.resolve();
    });
    expect(onClose).not.toHaveBeenCalled();

    // Closing dialog externally while async openSettings is in flight aborts it and discards late rejection.
    await act(async () => { editButton()!.click(); });
    expect(calls).toHaveLength(2);
    await act(async () => root.render(<ShortcutHelp deps={makeDeps(false)} />));
    expect(calls[1].signal.aborted).toBe(true);
    await act(async () => {
      calls[1].gate.reject(new Error("late after close"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();

    // Unmount aborts active action and discards late rejection.
    await act(async () => root.render(<ShortcutHelp deps={makeDeps(true)} />));
    await act(async () => { editButton()!.click(); });
    const last = calls.at(-1)!;
    await act(async () => root.render(null));
    expect(last.signal.aborted).toBe(true);
    await act(async () => {
      last.gate.reject(new Error("late after unmount"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("syncs getter scopeKey via shared deps.subscribe so Escape, close button, and openSettings remain responsive after scope switches", async () => {
    const sharedListeners = new Set<() => void>();
    const subscribe = (listener: () => void) => {
      sharedListeners.add(listener);
      return () => { sharedListeners.delete(listener); };
    };
    let currentScope = "s1";
    const calls: Array<{ scope: string; signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const onClose = vi.fn();
    const openSettings = vi.fn((signal: AbortSignal) => {
      const gate = deferred<void>();
      calls.push({ scope: currentScope, signal, gate });
      return gate.promise;
    });

    await act(async () => root.render(
      <StrictMode>
        <ShortcutHelp
          deps={{
            scopeKey: () => currentScope,
            subscribe,
            open: true,
            bindings: { jump: "Mod+K" },
            prefs: { off: false, disabled: [] },
            platform: "mac",
            shortcuts: SAMPLE_SHORTCUTS,
            localShortcuts: SAMPLE_LOCAL,
            onClose,
            openSettings,
          }}
        />
      </StrictMode>,
    ));
    expect(sharedListeners.size).toBeGreaterThan(0);

    // Start an in-flight openSettings on s1, then switch scope s1 -> s2 through the shared subscriber only.
    await act(async () => { editButton()!.click(); });
    expect(calls).toHaveLength(1);
    expect(editButton()?.getAttribute("aria-busy")).toBe("true");

    currentScope = "s2";
    await act(async () => { sharedListeners.forEach((l) => l()); });

    // Shared subscribe must re-render and commit s2, aborting s1's in-flight work and clearing busy.
    expect(calls[0].signal.aborted).toBe(true);
    expect(editButton()?.getAttribute("aria-busy")).toBeNull();
    await act(async () => {
      calls[0].gate.resolve();
      await Promise.resolve();
    });
    expect(onClose).not.toHaveBeenCalled();

    // After s1 -> s2 via shared subscribe, Escape, close button, and "Change in Settings" all respond normally.
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);

    const closeBtn = dialog()!.querySelector<HTMLButtonElement>(`button[aria-label="${t("ui-atoms.close")}"]`)!;
    await act(async () => { closeBtn.click(); });
    expect(onClose).toHaveBeenCalledTimes(2);

    await act(async () => { editButton()!.click(); });
    expect(calls).toHaveLength(2);
    expect(calls[1].scope).toBe("s2");
    expect(calls[1].signal.aborted).toBe(false);
    await act(async () => {
      calls[1].gate.resolve();
      await Promise.resolve();
    });
    expect(onClose).toHaveBeenCalledTimes(3);
    expectNoDefaultSingletons();
  });

  it("keeps committed scope and in-flight openSettings intact across an abandoned concurrent transition render", async () => {
    const suspendGate = deferred<void>();
    const settingsGate = deferred<void>();
    let capturedSignal: AbortSignal | null = null;
    const onClose = vi.fn();
    const renderedScopes: string[] = [];

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarness!: (next: { scope: string; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ scope: "s1", block: false });
      setHarness = setState;
      renderedScopes.push(state.scope);
      return (
        <Suspense fallback={<div data-fallback />}>
          <ShortcutHelp
            deps={{
              scopeKey: state.scope,
              open: true,
              bindings: { jump: "Mod+K" },
              prefs: { off: false, disabled: [] },
              platform: "mac",
              shortcuts: SAMPLE_SHORTCUTS,
              localShortcuts: SAMPLE_LOCAL,
              onClose,
              openSettings: (signal) => {
                capturedSignal = signal;
                return settingsGate.promise;
              },
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    await act(async () => { editButton()!.click(); });
    expect(capturedSignal!.aborted).toBe(false);
    expect(editButton()?.getAttribute("aria-busy")).toBe("true");

    // Suspended transition to s2 must NOT abort s1's in-flight action or corrupt committed scope.
    await act(async () => {
      startTransition(() => setHarness({ scope: "s2", block: true }));
    });
    expect(renderedScopes).toContain("s2");
    expect(capturedSignal!.aborted).toBe(false);

    // Revert transition back to s1 and resolve settingsGate -> closes cleanly.
    await act(async () => setHarness({ scope: "s1", block: false }));
    expect(capturedSignal!.aborted).toBe(false);
    await act(async () => {
      settingsGate.resolve();
      await Promise.resolve();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(editButton()?.getAttribute("aria-busy")).toBeNull();
    expectNoDefaultSingletons();
  });

  it("releases busy via try/finally on sync throws and async rejections while retaining recoverable errors", async () => {
    const onClose = vi.fn();
    const onError = vi.fn();
    let mode: "sync-throw" | "async-reject" | "ok" = "sync-throw";

    await act(async () => root.render(
      <ShortcutHelp
        deps={{
          scopeKey: "s1",
          open: true,
          bindings: { jump: "Mod+K" },
          prefs: { off: false, disabled: [] },
          platform: "mac",
          shortcuts: SAMPLE_SHORTCUTS,
          localShortcuts: SAMPLE_LOCAL,
          onClose,
          openSettings: () => {
            if (mode === "sync-throw") throw new Error("sync open failed");
            if (mode === "async-reject") return Promise.reject(new Error("async open failed"));
            return Promise.resolve();
          },
          formatError: (e) => {
            if (mode === "async-reject") throw new Error("formatter crashed");
            return `fmt:${e instanceof Error ? e.message : String(e)}`;
          },
          onError,
        }}
      />,
    ));

    // Sync throw surfaces formatted error, keeps dialog open, does not call onClose.
    await act(async () => { editButton()!.click(); });
    expect(dialog()!.querySelector('[role="alert"]')?.textContent).toBe("fmt:sync open failed");
    expect(onError).toHaveBeenLastCalledWith("fmt:sync open failed");
    expect(onClose).not.toHaveBeenCalled();
    expect(editButton()?.getAttribute("aria-busy")).toBeNull();

    // Async rejection with throwing formatError falls back to errorText, releases busy, and preserves alert.
    mode = "async-reject";
    await act(async () => { editButton()!.click(); });
    expect(dialog()!.querySelector('[role="alert"]')?.textContent).toContain("async open failed");
    expect(editButton()?.getAttribute("aria-busy")).toBeNull();
    expect(onClose).not.toHaveBeenCalled();

    // Retry succeeds: clears error and invokes onClose.
    mode = "ok";
    await act(async () => { editButton()!.click(); });
    expect(dialog()!.querySelector('[role="alert"]')).toBeNull();
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("preserves default singletons when deps is omitted and supports App.tsx lazyDialog open/onClose passthrough", async () => {
    mocks.helpOpen = true;
    await act(async () => root.render(<ShortcutHelp />));
    expect(mocks.useHelpOpen).toHaveBeenCalled();
    expect(mocks.useBindings).toHaveBeenCalled();
    expect(mocks.useShortcutPrefs).toHaveBeenCalled();
    expect(mocks.currentPlatform).toHaveBeenCalled();
    expect(dialog()).not.toBeNull();

    await act(async () => { editButton()!.click(); });
    expect(mocks.closeHelp).toHaveBeenCalledTimes(1);
    expect(mocks.openShortcutSettings).toHaveBeenCalledTimes(1);

    // lazyDialog prop passthrough (`<ShortcutHelp open={true} onClose={fn} />`).
    mocks.useHelpOpen.mockClear();
    const passthroughClose = vi.fn();
    await act(async () => root.render(<ShortcutHelp open onClose={passthroughClose} />));
    expect(mocks.useHelpOpen).not.toHaveBeenCalled();
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    expect(passthroughClose).toHaveBeenCalledTimes(1);
  });
});
