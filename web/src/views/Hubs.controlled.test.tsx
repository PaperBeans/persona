// @vitest-environment jsdom
// Controlled Hubs (useHubs, goToHub, hubMenuItems, HubList, OtherHubs) bypass default localStorage/location/toast
// singletons, cache fresh-array getters, preserve render purity across abandoned concurrent renders, reset drafts/dialogs/
// errors across scope & hub switches, recover via try/finally on throw/reject, and preserve default behavior.
import { act, startTransition, StrictMode, Suspense, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import type { SavedHub } from "../lib/hubs";
import type { HubListDeps, OtherHubsDeps, UseHubsDeps } from "./Hubs";

const mocks = vi.hoisted(() => ({
  hubs: [] as SavedHub[],
  currentUrl: "https://hub-a.example.com",
  hubListeners: new Set<() => void>(),
  here: vi.fn(),
  loadHubs: vi.fn(),
  onHubsChange: vi.fn(),
  addHub: vi.fn(),
  renameHub: vi.fn(),
  removeHub: vi.fn(),
  switchURL: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../lib/hubs", async (original) => {
  const real = await original<typeof import("../lib/hubs")>();
  return {
    ...real,
    here: () => {
      mocks.here();
      return mocks.currentUrl;
    },
    loadHubs: () => {
      mocks.loadHubs();
      return mocks.hubs.map((h) => ({ ...h }));
    },
    onHubsChange: (fn: () => void) => {
      mocks.onHubsChange(fn);
      mocks.hubListeners.add(fn);
      return () => { mocks.hubListeners.delete(fn); };
    },
    addHub: (url: string, label?: string) => mocks.addHub(url, label),
    renameHub: (url: string, label: string) => mocks.renameHub(url, label),
    removeHub: (url: string) => mocks.removeHub(url),
    switchURL: (target: string, list: SavedHub[]) => mocks.switchURL(target, list),
  };
});

vi.mock("../ui/atoms", async (original) => ({
  ...(await original<typeof import("../ui/atoms")>()),
  toast: (message: string, tone?: string) => mocks.toast(message, tone),
}));

const {
  AddHubDialogView,
  goToHub,
  HubList,
  HubListView,
  hubMenuItems,
  OtherHubs,
  OtherHubsView,
  RenameHubDialogView,
  useHubs,
} = await import("./Hubs");

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

const HUB_A: SavedHub = { url: "https://hub-a.example.com", name: "Home Hub" };
const HUB_B: SavedHub = { url: "https://hub-b.example.com", name: "Work Hub", label: "Work" };
const HUB_C: SavedHub = { url: "https://hub-c.example.com", name: "Lab Hub" };

const expectNoDefaultSingletons = () => {
  expect(mocks.here).not.toHaveBeenCalled();
  expect(mocks.loadHubs).not.toHaveBeenCalled();
  expect(mocks.onHubsChange).not.toHaveBeenCalled();
  expect(mocks.addHub).not.toHaveBeenCalled();
  expect(mocks.renameHub).not.toHaveBeenCalled();
  expect(mocks.removeHub).not.toHaveBeenCalled();
  expect(mocks.switchURL).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
};

const setInput = async (input: HTMLInputElement, value: string) => {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    setter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
};

const findButton = (rootEl: ParentNode, text: string) =>
  [...rootEl.querySelectorAll("button")].find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

const activeDialog = () => document.querySelector<HTMLElement>('[role="dialog"]');

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  Object.values(mocks).forEach((v) => {
    if (typeof v === "function") v.mockReset();
  });
  mocks.hubListeners.clear();
  mocks.hubs = [HUB_A, HUB_B];
  mocks.currentUrl = HUB_A.url;
  mocks.addHub.mockImplementation((url: string, label?: string) => {
    const added: SavedHub = { url: url.startsWith("http") ? url : `https://${url}`, ...(label ? { label } : {}) };
    mocks.hubs = [...mocks.hubs, added];
    mocks.hubListeners.forEach((l) => l());
    return { list: mocks.hubs };
  });
  mocks.renameHub.mockImplementation((url: string, label: string) => {
    mocks.hubs = mocks.hubs.map((h) => (h.url === url ? { ...h, label: label || undefined } : h));
    mocks.hubListeners.forEach((l) => l());
    return mocks.hubs;
  });
  mocks.removeHub.mockImplementation((url: string) => {
    mocks.hubs = mocks.hubs.filter((h) => h.url !== url);
    mocks.hubListeners.forEach((l) => l());
    return mocks.hubs;
  });
  mocks.switchURL.mockImplementation((target: string) => `${target}/#hubs=encoded`);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("Pure presentational Hub views", () => {
  it("renders HubListView, AddHubDialogView, RenameHubDialogView, and OtherHubsView without touching singletons", async () => {
    const onOpenAdd = vi.fn();
    const onOpenRename = vi.fn();
    const onRemove = vi.fn();
    const onSwitch = vi.fn();

    await act(async () => root.render(
      <HubListView
        rows={[HUB_A, HUB_B]}
        current={HUB_A.url}
        actionError="Switch failed"
        switchingUrl={HUB_B.url}
        removingUrls={[]}
        addOpen={false}
        addUrl=""
        addLabel=""
        renameHub={null}
        renameLabel=""
        onOpenAdd={onOpenAdd}
        onCloseAdd={() => {}}
        onAddUrlChange={() => {}}
        onAddLabelChange={() => {}}
        onSaveAdd={() => {}}
        onOpenRename={onOpenRename}
        onCloseRename={() => {}}
        onRenameLabelChange={() => {}}
        onSaveRename={() => {}}
        onRemove={onRemove}
        onSwitch={onSwitch}
      />,
    ));
    expect(host.textContent).toContain("Home Hub");
    expect(host.textContent).toContain("Work");
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Switch failed");
    expect(findButton(host, t("hubs.switch"))?.getAttribute("aria-busy")).toBe("true");
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    expect(onOpenAdd).toHaveBeenCalledTimes(1);

    // Pure AddHubDialogView & RenameHubDialogView render error/busy states.
    await act(async () => root.render(
      <AddHubDialogView
        open
        url="https://hub-c.example.com"
        label="Lab"
        error="Duplicate hub"
        busy
        onUrlChange={() => {}}
        onLabelChange={() => {}}
        onSave={() => {}}
        onClose={() => {}}
      />,
    ));
    expect(activeDialog()?.querySelector('[role="alert"]')?.textContent).toBe("Duplicate hub");
    expect(findButton(activeDialog()!, t("hubs.add"))?.getAttribute("aria-busy")).toBe("true");

    await act(async () => root.render(
      <RenameHubDialogView
        hub={HUB_B}
        label="Renamed"
        error="Rename rejected"
        busy
        onLabelChange={() => {}}
        onSave={() => {}}
        onClose={() => {}}
      />,
    ));
    expect(activeDialog()?.querySelector('[role="alert"]')?.textContent).toBe("Rename rejected");
    expect(findButton(activeDialog()!, t("common.save"))?.getAttribute("aria-busy")).toBe("true");

    // Pure OtherHubsView: empty renders null, populated renders rows + inline error.
    await act(async () => root.render(<OtherHubsView hubs={[]} onSwitch={onSwitch} />));
    expect(host.innerHTML).toBe("");

    await act(async () => root.render(<OtherHubsView hubs={[HUB_B]} error="Cannot open hub" onSwitch={onSwitch} />));
    expect(host.textContent).toContain("Work");
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Cannot open hub");
    await act(async () => { host.querySelector("button")!.click(); });
    expect(onSwitch).toHaveBeenCalledWith(HUB_B);
    expectNoDefaultSingletons();
  });
});

describe("Controlled Hubs helpers, fresh-array snapshot caching, and bridge identity stability", () => {
  it("supports controlled goToHub, hubMenuItems, and fresh-array useHubs without infinite re-renders", async () => {
    const navigated: Array<{ url: string; hub: SavedHub }> = [];
    goToHub(HUB_B, {
      hubs: () => [HUB_A, HUB_B].map((h) => ({ ...h })),
      buildSwitchURL: (target, list) => `${target}?n=${list.length}`,
      navigateTo: (url, hub) => { navigated.push({ url, hub }); },
    });
    expect(navigated).toEqual([{ url: "https://hub-b.example.com?n=2", hub: HUB_B }]);

    const switched: SavedHub[] = [];
    const items = hubMenuItems([HUB_A, HUB_B], { current: HUB_A.url, onSwitch: (h) => { switched.push(h); } });
    expect(items.map((i) => i.key)).toEqual(["hub:https://hub-b.example.com"]);
    items[0].onSelect();
    expect(switched).toEqual([HUB_B]);

    // useHubs with fresh-array getter caches snapshot by value equality.
    const listeners = new Set<() => void>();
    let currentList = [HUB_A, HUB_B];
    let reads = 0;
    const seenRefs: SavedHub[][] = [];
    function Probe({ deps }: { deps: UseHubsDeps }) {
      const list = useHubs(deps);
      seenRefs.push(list);
      return <div data-hub-count={list.length} />;
    }
    const deps: UseHubsDeps = {
      hubs: () => {
        reads += 1;
        return currentList.map((h) => ({ ...h }));
      },
      subscribeHubs: (l) => { listeners.add(l); return () => { listeners.delete(l); }; },
    };
    await act(async () => root.render(<StrictMode><Probe deps={deps} /></StrictMode>));
    expect(host.querySelector("[data-hub-count]")?.getAttribute("data-hub-count")).toBe("2");
    expect(reads).toBeLessThan(20);
    const stableRef = seenRefs.at(-1)!;

    // Equal re-emission keeps the exact cached array reference.
    await act(async () => { listeners.forEach((l) => l()); });
    expect(seenRefs.at(-1)).toBe(stableRef);

    // Mutating list updates cleanly.
    currentList = [HUB_A, HUB_B, HUB_C];
    await act(async () => { listeners.forEach((l) => l()); });
    expect(host.querySelector("[data-hub-count]")?.getAttribute("data-hub-count")).toBe("3");
    expectNoDefaultSingletons();
  });

  it("preserves open dialog and typed draft when switching HubList from direct props to fresh-array getter + subscribe on the same scope", async () => {
    const hubListeners = new Set<() => void>();
    const scopeListeners = new Set<() => void>();
    const curListeners = new Set<() => void>();

    await act(async () => root.render(
      <StrictMode>
        <HubList
          deps={{
            scopeKey: "s1",
            current: HUB_A.url,
            hubs: [HUB_A, HUB_B],
            notify: vi.fn(),
          }}
        />
      </StrictMode>,
    ));

    // Open Add dialog and type a draft.
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    const inputs = activeDialog()!.querySelectorAll("input");
    await setInput(inputs[0], "https://draft.example.com");
    await setInput(inputs[1], "Draft Name");
    expect(inputs[0].value).toBe("https://draft.example.com");

    // Switch bridge in-place (direct -> fresh-array getter + subscribe) on same scopeKey & current hub.
    const subscribeScopeKey = vi.fn((l: () => void) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; });
    const subscribeCurrent = vi.fn((l: () => void) => { curListeners.add(l); return () => { curListeners.delete(l); }; });
    const subscribeHubs = vi.fn((l: () => void) => { hubListeners.add(l); return () => { hubListeners.delete(l); }; });
    const addHubSpy = vi.fn();
    const renderSub = () => root.render(
      <StrictMode>
        <HubList
          deps={{
            scopeKey: () => "s1",
            subscribeScopeKey,
            current: () => HUB_A.url,
            subscribeCurrent,
            hubs: () => [HUB_A, HUB_B].map((h) => ({ ...h })),
            subscribeHubs,
            addHub: addHubSpy,
            notify: vi.fn(),
          }}
        />
      </StrictMode>,
    );
    await act(async () => renderSub());

    // Dialog stays open with the typed draft intact.
    const inputsAfter = activeDialog()!.querySelectorAll("input");
    expect(inputsAfter[0].value).toBe("https://draft.example.com");
    expect(inputsAfter[1].value).toBe("Draft Name");

    // Re-rendering with fresh inline arrow getters must not churn subscriptions.
    const scopeSubCount = subscribeScopeKey.mock.calls.length;
    const curSubCount = subscribeCurrent.mock.calls.length;
    const hubSubCount = subscribeHubs.mock.calls.length;
    await act(async () => renderSub());
    await act(async () => renderSub());
    expect(subscribeScopeKey).toHaveBeenCalledTimes(scopeSubCount);
    expect(subscribeCurrent).toHaveBeenCalledTimes(curSubCount);
    expect(subscribeHubs).toHaveBeenCalledTimes(hubSubCount);

    // Submitting form with an invalid URL surfaces validation error without invoking controlled addHub.
    await setInput(activeDialog()!.querySelectorAll("input")[0], "not a url");
    await act(async () => {
      activeDialog()!.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(addHubSpy).not.toHaveBeenCalled();
    expect(activeDialog()!.querySelector('[role="alert"]')?.textContent).toBeTruthy();
    expectNoDefaultSingletons();
  });
});

describe("Cross-scope, cross-hub, ABA, hub-removal, render-purity, and try/finally recovery", () => {
  it("resets drafts/dialogs/errors and aborts stale add/rename/remove/switch callbacks across scope switches, hub switches, ABA bursts, hub removal, and unmount", async () => {
    const scopeListeners = new Set<() => void>();
    const hubListeners = new Set<() => void>();
    let currentScope = "s1";
    let currentHub = HUB_A.url;
    let hubsList = [HUB_A, HUB_B];
    const notify = vi.fn();
    const onError = vi.fn();

    const addCalls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<{ list: SavedHub[] }>> }> = [];
    const renameCalls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const removeCalls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const switchCalls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];

    const makeDeps = (): HubListDeps => ({
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      current: currentHub,
      hubs: () => hubsList.map((h) => ({ ...h })),
      subscribeHubs: (l) => { hubListeners.add(l); return () => { hubListeners.delete(l); }; },
      addHub: (_u, _l, signal) => {
        const gate = deferred<{ list: SavedHub[] }>();
        addCalls.push({ signal, gate });
        return gate.promise;
      },
      renameHub: (_u, _l, signal) => {
        const gate = deferred<void>();
        renameCalls.push({ signal, gate });
        return gate.promise;
      },
      removeHub: (_u, signal) => {
        const gate = deferred<void>();
        removeCalls.push({ signal, gate });
        return gate.promise;
      },
      switchHub: (_h, signal) => {
        const gate = deferred<void>();
        switchCalls.push({ signal, gate });
        return gate.promise;
      },
      notify,
      onError,
    });

    await act(async () => root.render(<HubList deps={makeDeps()} />));

    // 1) Open Add dialog, type draft, start async addHub, then switch scope s1 -> s2.
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    await setInput(activeDialog()!.querySelectorAll("input")[0], "https://hub-c.example.com");
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(addCalls).toHaveLength(1);
    expect(findButton(activeDialog()!, t("hubs.add"))?.getAttribute("aria-busy")).toBe("true");

    currentScope = "s2";
    await act(async () => { scopeListeners.forEach((l) => l()); });
    expect(activeDialog()).toBeNull();
    expect(addCalls[0].signal.aborted).toBe(true);
    await act(async () => {
      addCalls[0].gate.resolve({ list: [...hubsList, HUB_C] });
      await Promise.resolve();
    });
    expect(notify).not.toHaveBeenCalled();

    // 2) Start async remove + switch + open Rename on HUB_B, then fire synchronous ABA scope burst (s2 -> s3 -> s2).
    const renameBtn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.rename_label", { name: "Work" })}"]`)!;
    await act(async () => renameBtn.click());
    await setInput(activeDialog()!.querySelector("input")!, "Work Edited");
    await act(async () => { findButton(activeDialog()!, t("common.save"))!.click(); });
    await act(async () => { findButton(host, t("hubs.switch"))!.click(); });
    expect(renameCalls).toHaveLength(1);
    expect(switchCalls).toHaveLength(1);

    await act(async () => {
      currentScope = "s3";
      scopeListeners.forEach((l) => l());
      currentScope = "s2";
      scopeListeners.forEach((l) => l());
    });
    expect(activeDialog()).toBeNull();
    expect(renameCalls[0].signal.aborted).toBe(true);
    expect(switchCalls[0].signal.aborted).toBe(true);
    await act(async () => {
      renameCalls[0].gate.reject(new Error("stale rename"));
      switchCalls[0].gate.reject(new Error("stale switch"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expect(host.querySelector('[role="alert"]')).toBeNull();

    // 3) Removing the hub currently open in Rename closes the dialog and re-adding it (ABA) does not reopen it.
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.rename_label", { name: "Work" })}"]`)!.click());
    expect(activeDialog()).not.toBeNull();
    await act(async () => { findButton(activeDialog()!, t("common.save"))!.click(); });
    expect(renameCalls).toHaveLength(2);
    hubsList = [HUB_A];
    await act(async () => { hubListeners.forEach((l) => l()); });
    expect(activeDialog()).toBeNull();
    expect(renameCalls[1].signal.aborted).toBe(true);
    hubsList = [HUB_A, HUB_B];
    await act(async () => { hubListeners.forEach((l) => l()); });
    expect(activeDialog()).toBeNull();

    // 4) Switching current hub resets in-flight remove and clears errors.
    const removeBtn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.remove_label", { name: "Work" })}"]`)!;
    await act(async () => removeBtn.click());
    expect(removeCalls).toHaveLength(1);
    currentHub = HUB_B.url;
    await act(async () => root.render(<HubList deps={makeDeps()} />));
    expect(removeCalls[0].signal.aborted).toBe(true);
    await act(async () => {
      removeCalls[0].gate.reject(new Error("stale remove"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();

    // 5) Unmount aborts active switch on OtherHubs and discards late rejection.
    const otherCalls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const otherDeps: OtherHubsDeps = {
      scopeKey: "s1",
      current: HUB_A.url,
      hubs: [HUB_A, HUB_B],
      switchHub: (_h, signal) => {
        const gate = deferred<void>();
        otherCalls.push({ signal, gate });
        return gate.promise;
      },
      onError,
    };
    await act(async () => root.render(<OtherHubs deps={otherDeps} />));
    await act(async () => { host.querySelector("button")!.click(); });
    expect(otherCalls).toHaveLength(1);
    await act(async () => root.render(null));
    expect(otherCalls[0].signal.aborted).toBe(true);
    await act(async () => {
      otherCalls[0].gate.reject(new Error("unmounted switch"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("keeps committed add dialog, draft, and in-flight async addHub intact when a concurrent transition render is abandoned", async () => {
    const suspendGate = deferred<void>();
    const addGate = deferred<{ list: SavedHub[] }>();
    let capturedSignal: AbortSignal | null = null;
    const notify = vi.fn();
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
          <HubList
            deps={{
              scopeKey: state.scope,
              current: HUB_A.url,
              hubs: [HUB_A],
              addHub: (_u, _l, signal) => {
                capturedSignal = signal;
                return addGate.promise;
              },
              notify,
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    await setInput(activeDialog()!.querySelectorAll("input")[0], "https://hub-c.example.com");
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(capturedSignal!.aborted).toBe(false);
    expect(findButton(activeDialog()!, t("hubs.add"))?.getAttribute("aria-busy")).toBe("true");

    // Suspended concurrent transition to s2 must NOT abort s1's addHub or corrupt committed scope.
    await act(async () => {
      startTransition(() => setHarness({ scope: "s2", block: true }));
    });
    expect(renderedScopes).toContain("s2");
    expect(capturedSignal!.aborted).toBe(false);
    expect(activeDialog()).not.toBeNull();

    // Abandon transition back to s1; resolving addGate completes cleanly with toast and closes dialog.
    await act(async () => setHarness({ scope: "s1", block: false }));
    expect(capturedSignal!.aborted).toBe(false);
    await act(async () => {
      addGate.resolve({ list: [HUB_A, HUB_C] });
      await Promise.resolve();
    });
    expect(notify).toHaveBeenCalledWith(t("hubs.added", { name: "Lab Hub" }), undefined);
    expect(activeDialog()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("releases busy via try/finally on sync throws and async rejections for add, rename, remove, and switch while keeping recoverable errors", async () => {
    const notify = vi.fn(() => { throw new Error("toast crashed"); });
    const onError = vi.fn();
    let addFail: "sync" | "async" | "ok" = "sync";
    let renameFail: "async" | "ok" = "async";
    let removeFail: "async" | "ok" = "async";
    let switchFail: "async" | "ok" = "async";

    await act(async () => root.render(
      <HubList
        deps={{
          scopeKey: "s1",
          current: HUB_A.url,
          hubs: [HUB_A, HUB_B],
          addHub: (url, label) => {
            if (addFail === "sync") throw new Error("add sync fail");
            if (addFail === "async") return Promise.reject(new Error("add async fail"));
            return Promise.resolve({ added: { url, label } });
          },
          renameHub: () => (renameFail === "async" ? Promise.reject(new Error("rename fail")) : Promise.resolve()),
          removeHub: () => (removeFail === "async" ? Promise.reject(new Error("remove fail")) : Promise.resolve()),
          switchHub: () => (switchFail === "async" ? Promise.reject(new Error("switch fail")) : Promise.resolve()),
          notify,
          formatError: (e) => `err:${e instanceof Error ? e.message : String(e)}`,
          onError,
        }}
      />,
    ));

    // Add: sync throw -> error preserved + draft retained -> async reject -> busy released -> retry succeeds.
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    await setInput(activeDialog()!.querySelectorAll("input")[0], "https://hub-c.example.com");
    await setInput(activeDialog()!.querySelectorAll("input")[1], "Lab");
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(activeDialog()!.querySelector('[role="alert"]')?.textContent).toBe("err:add sync fail");
    expect(activeDialog()!.querySelectorAll("input")[0].value).toBe("https://hub-c.example.com");

    addFail = "async";
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(activeDialog()!.querySelector('[role="alert"]')?.textContent).toBe("err:add async fail");
    expect(findButton(activeDialog()!, t("hubs.add"))?.getAttribute("aria-busy")).toBeNull();

    addFail = "ok";
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(activeDialog()).toBeNull();

    // Rename: async reject releases busy and retains draft + alert -> retry succeeds.
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.rename_label", { name: "Work" })}"]`)!.click());
    await setInput(activeDialog()!.querySelector("input")!, "Work New");
    await act(async () => { findButton(activeDialog()!, t("common.save"))!.click(); });
    expect(activeDialog()!.querySelector('[role="alert"]')?.textContent).toBe("err:rename fail");
    expect(activeDialog()!.querySelector("input")!.value).toBe("Work New");
    expect(findButton(activeDialog()!, t("common.save"))?.getAttribute("aria-busy")).toBeNull();

    renameFail = "ok";
    await act(async () => { findButton(activeDialog()!, t("common.save"))!.click(); });
    expect(activeDialog()).toBeNull();

    // Remove: async reject releases disabled state, surfaces inline alert, retry clears alert.
    const removeBtn = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.remove_label", { name: "Work" })}"]`)!;
    await act(async () => removeBtn().click());
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("err:remove fail");
    expect(removeBtn().disabled).toBe(false);

    removeFail = "ok";
    await act(async () => removeBtn().click());
    expect(host.querySelector('[role="alert"]')).toBeNull();

    // Switch: async reject releases busy state, surfaces inline alert, retry clears alert.
    await act(async () => { findButton(host, t("hubs.switch"))!.click(); });
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("err:switch fail");
    expect(findButton(host, t("hubs.switch"))?.getAttribute("aria-busy")).toBeNull();

    switchFail = "ok";
    await act(async () => { findButton(host, t("hubs.switch"))!.click(); });
    expect(host.querySelector('[role="alert"]')).toBeNull();

    // OtherHubs switch error recovery.
    let otherFail = true;
    await act(async () => root.render(
      <OtherHubs
        deps={{
          scopeKey: "s1",
          current: HUB_A.url,
          hubs: [HUB_A, HUB_B],
          switchHub: () => (otherFail ? Promise.reject(new Error("other fail")) : Promise.resolve()),
          formatError: (e) => `other:${e instanceof Error ? e.message : String(e)}`,
        }}
      />,
    ));
    await act(async () => { host.querySelector("button")!.click(); });
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("other:other fail");
    expect(host.querySelector("button")!.disabled).toBe(false);

    otherFail = false;
    await act(async () => { host.querySelector("button")!.click(); });
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expectNoDefaultSingletons();
  });

  it("preserves 100% backward-compatible default storage, toast, and OtherHubs behavior when deps is omitted", async () => {
    await act(async () => root.render(<HubList />));
    expect(mocks.here).toHaveBeenCalled();
    expect(mocks.loadHubs).toHaveBeenCalled();
    expect(mocks.onHubsChange).toHaveBeenCalled();
    expect(host.textContent).toContain("Home Hub");
    expect(host.textContent).toContain("Work");

    // Default add flow calls addHub + toast.
    await act(async () => { findButton(host, t("hubs.add_hub"))!.click(); });
    await setInput(activeDialog()!.querySelectorAll("input")[0], "https://hub-c.example.com");
    await setInput(activeDialog()!.querySelectorAll("input")[1], "Lab");
    await act(async () => { findButton(activeDialog()!, t("hubs.add"))!.click(); });
    expect(mocks.addHub).toHaveBeenCalledWith("https://hub-c.example.com", "Lab");
    expect(mocks.toast).toHaveBeenCalledWith(t("hubs.added", { name: "Lab" }), undefined);

    // Default rename flow calls renameHub.
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.rename_label", { name: "Work" })}"]`)!.click());
    await setInput(activeDialog()!.querySelector("input")!, "Office");
    await act(async () => { findButton(activeDialog()!, t("common.save"))!.click(); });
    expect(mocks.renameHub).toHaveBeenCalledWith(HUB_B.url, "Office");

    // Default remove flow calls removeHub + toast.
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("hubs.remove_label", { name: "Office" })}"]`)!.click());
    expect(mocks.removeHub).toHaveBeenCalledWith(HUB_B.url);
    expect(mocks.toast).toHaveBeenCalledWith(t("hubs.removed", { name: "Office" }), undefined);

    // Default OtherHubs filters out current hub.
    await act(async () => root.render(<OtherHubs />));
    expect(host.textContent).toContain("Lab");
    expect(host.textContent).not.toContain("Home Hub");
  });
});
