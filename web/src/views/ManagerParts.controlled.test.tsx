// @vitest-environment jsdom
// Controlled-mode, pure-view, bridge-switch, snapshot-caching, async-fencing, error-recovery, and default-fallback tests for ManagerParts.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import { chooseManager, resetManagerChoice } from "../lib/manager-choice";
import { resetRoster, setRoster, type Manager, type ManagerList } from "../lib/managers";
import {
  MoveToManager,
  MoveToManagerView,
  OfflineState,
  OfflineStateView,
  useAssistantTitle,
  useManagerOffline,
  useShownOffline,
  type AssistantTitleDeps,
  type ManagerOfflineDeps,
  type MoveToManagerDeps,
  type ShownOffline,
  type ShownOfflineDeps,
} from "./ManagerParts";
import { t } from "../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FIXED_NOW = Date.UTC(2026, 10, 26, 12, 0, 0);
const LAST_SEEN = new Date(FIXED_NOW - 5 * 60_000).toISOString();

const M_ALPHA: Manager = { id: "alpha", name: "Alpha", online: true, default: true, builtin: true };
const M_BETA: Manager = { id: "beta", name: "Beta", online: false, last_seen: LAST_SEEN };
const M_GAMMA: Manager = { id: "gamma", name: "Gamma", online: true };

let root: Root;
let host: HTMLDivElement;

const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); });
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  setToken("synthetic-token");
  resetRoster();
  resetManagerChoice();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  resetRoster();
  resetManagerChoice();
  vi.unstubAllGlobals();
});

describe("ManagerParts pure presentational views", () => {
  it("renders OfflineStateView / OfflineState and MoveToManagerView without touching singletons", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const offline: ShownOffline = {
      manager: M_BETA,
      isDefault: false,
      composer: "Beta · last synced 5m ago",
      body: "Read-only while offline",
      title: "Beta is offline",
      note: "Last synced 5m ago",
    };

    const onToggleMenu = vi.fn();
    const onCloseMenu = vi.fn();
    const onSelect = vi.fn();
    const anchor = document.createElement("button");
    document.body.append(anchor);

    try {
      await act(async () => {
        root.render(
          <div>
            <OfflineStateView offline={offline} />
            <OfflineState offline={offline} />
            <MoveToManagerView
              managers={[M_ALPHA, M_BETA, M_GAMMA]}
              owner="alpha"
              busy={false}
              anchor={anchor}
              onToggleMenu={onToggleMenu}
              onCloseMenu={onCloseMenu}
              onSelect={onSelect}
            />
          </div>,
        );
      });

      expect(host.querySelectorAll('[data-assistant-offline="beta"]')).toHaveLength(2);
      expect(host.textContent).toContain("Beta is offline");
      expect(host.textContent).toContain("Last synced 5m ago");

      const moveBtn = host.querySelector<HTMLButtonElement>("button")!;
      await act(async () => moveBtn.click());
      expect(onToggleMenu).toHaveBeenCalledTimes(1);

      const menuItems = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
      const alphaItem = menuItems.find((b) => b.textContent?.includes("Alpha"))!;
      const betaItem = menuItems.find((b) => b.textContent?.includes("Beta"))!;
      expect(alphaItem.disabled).toBe(true);
      expect(betaItem.disabled).toBe(false);

      await act(async () => betaItem.click());
      expect(onSelect).toHaveBeenCalledWith(M_BETA);
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      anchor.remove();
    }
  });
});

describe("ManagerParts controlled hooks", () => {
  it("supports controlled useAssistantTitle with direct values, fresh-object getters, and bridge switches without fetching", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    function Harness({ deps }: { deps?: AssistantTitleDeps }) {
      return <h1>{useAssistantTitle(deps)}</h1>;
    }

    await act(async () => {
      root.render(<Harness deps={{ identity: { name: "Controlled Assistant" }, personaName: "Fallback Look" }} />);
    });
    expect(host.querySelector("h1")!.textContent).toBe("Controlled Assistant");

    // Null identity falls back to controlled personaName.
    await act(async () => {
      root.render(<Harness deps={{ identity: null, personaName: "Fallback Look" }} />);
    });
    expect(host.querySelector("h1")!.textContent).toBe("Fallback Look");

    // Switch bridge mode (direct -> fresh-object getter + subscribe) without hook errors or render loops.
    let currentName: string | null = "Live Assistant v1";
    let currentPersona = "Live Persona";
    const idListeners = new Set<() => void>();
    const personaListeners = new Set<() => void>();
    let reads = 0;

    await act(async () => {
      root.render(
        <Harness
          deps={{
            identity: () => {
              reads += 1;
              return currentName ? { name: currentName } : null;
            },
            subscribeIdentity: (l) => {
              idListeners.add(l);
              return () => idListeners.delete(l);
            },
            personaName: () => currentPersona,
            subscribePersona: (l) => {
              personaListeners.add(l);
              return () => personaListeners.delete(l);
            },
          }}
        />,
      );
    });
    expect(host.querySelector("h1")!.textContent).toBe("Live Assistant v1");
    expect(reads).toBeLessThan(20);

    currentName = null;
    currentPersona = "Live Persona v2";
    await act(async () => {
      idListeners.forEach((l) => l());
      personaListeners.forEach((l) => l());
    });
    expect(host.querySelector("h1")!.textContent).toBe("Live Persona v2");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("supports controlled useManagerOffline and useShownOffline with fresh-array getters, cross-manager switches, and zero singleton calls", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    // Pollute global roster with a conflicting default to prove controlled hooks never read global currentRoster().
    setRoster({ default: "alpha", managers: [M_ALPHA, M_GAMMA] });

    let roster: ManagerList = {
      default: "beta",
      managers: [
        { ...M_ALPHA, default: false },
        { ...M_BETA, default: true },
        M_GAMMA,
      ],
    };
    let shown = "beta";
    const rosterListeners = new Set<() => void>();
    const shownListeners = new Set<() => void>();
    let rosterReads = 0;

    function Harness({
      managerId,
      offlineDeps,
      shownDeps,
    }: {
      managerId: string | undefined;
      offlineDeps: ManagerOfflineDeps;
      shownDeps: ShownOfflineDeps;
    }) {
      const composer = useManagerOffline(managerId, offlineDeps);
      const shownOff = useShownOffline(shownDeps);
      return (
        <div>
          <span data-composer>{composer ?? ""}</span>
          <span data-shown-title>{shownOff?.title ?? ""}</span>
          <span data-shown-default>{shownOff ? String(shownOff.isDefault) : "none"}</span>
        </div>
      );
    }

    const subscribedOfflineDeps: ManagerOfflineDeps = {
      roster: () => {
        rosterReads += 1;
        return { default: roster.default, managers: roster.managers.map((m) => ({ ...m })) };
      },
      subscribeRoster: (l) => {
        rosterListeners.add(l);
        return () => rosterListeners.delete(l);
      },
      now: FIXED_NOW,
    };

    const subscribedShownDeps: ShownOfflineDeps = {
      ...subscribedOfflineDeps,
      shownManager: () => shown,
      subscribeShownManager: (l) => {
        shownListeners.add(l);
        return () => shownListeners.delete(l);
      },
    };

    // Start with direct values, then switch to subscribed fresh-array getters.
    await act(async () => {
      root.render(
        <Harness
          managerId="beta"
          offlineDeps={{ roster, now: FIXED_NOW }}
          shownDeps={{ roster, shownManager: "beta", now: FIXED_NOW }}
        />,
      );
    });
    expect(host.querySelector("[data-composer]")!.textContent).toContain("Beta");
    expect(host.querySelector("[data-shown-title]")!.textContent).toContain("Beta");
    // Controlled roster.default is "beta", NOT global "alpha".
    expect(host.querySelector("[data-shown-default]")!.textContent).toBe("true");

    await act(async () => {
      root.render(<Harness managerId="beta" offlineDeps={subscribedOfflineDeps} shownDeps={subscribedShownDeps} />);
    });
    expect(host.querySelector("[data-composer]")!.textContent).toContain("Beta");
    expect(host.querySelector("[data-shown-default]")!.textContent).toBe("true");
    expect(rosterReads).toBeLessThan(30);

    // Switch manager id & shownManager to online manager "alpha" -> offline notices clear.
    shown = "alpha";
    await act(async () => {
      shownListeners.forEach((l) => l());
      root.render(<Harness managerId="alpha" offlineDeps={subscribedOfflineDeps} shownDeps={subscribedShownDeps} />);
    });
    expect(host.querySelector("[data-composer]")!.textContent).toBe("");
    expect(host.querySelector("[data-shown-title]")!.textContent).toBe("");
    expect(host.querySelector("[data-shown-default]")!.textContent).toBe("none");

    // Single-manager roster hides offline restrictions even for an offline manager.
    roster = { default: "beta", managers: [{ ...M_BETA, default: true }] };
    shown = "beta";
    await act(async () => {
      rosterListeners.forEach((l) => l());
      shownListeners.forEach((l) => l());
      root.render(<Harness managerId="beta" offlineDeps={subscribedOfflineDeps} shownDeps={subscribedShownDeps} />);
    });
    expect(host.querySelector("[data-composer]")!.textContent).toBe("");
    expect(host.querySelector("[data-shown-title]")!.textContent).toBe("");
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});

describe("MoveToManager controlled and default modes", () => {
  it("handles fresh-array roster getters, bridge switches, cross-machine/ABA fencing, and unmount abort", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    const roster: ManagerList = { default: "alpha", managers: [M_ALPHA, M_BETA, M_GAMMA] };
    const rosterListeners = new Set<() => void>();
    const calls: Array<{ machineId: string; to: Manager; signal: AbortSignal; d: ReturnType<typeof deferred<void>> }> = [];
    const notifications: Array<{ message: string; tone: "ok" | "error" }> = [];
    let rosterReads = 0;

    const moveMachine = vi.fn((machineId: string, to: Manager, signal: AbortSignal) => {
      const d = deferred<void>();
      calls.push({ machineId, to, signal, d });
      return d.promise;
    });
    const notify = (message: string, tone: "ok" | "error") => notifications.push({ message, tone });

    // Start in direct roster mode.
    await act(async () => {
      root.render(
        <MoveToManager
          machineId="machine-1"
          manager="alpha"
          deps={{ roster, moveMachine, notify }}
        />,
      );
    });

    const moveBtn = () => host.querySelector<HTMLButtonElement>("button")!;
    await act(async () => moveBtn().click());
    const gammaItem = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("Gamma"))!;
    await act(async () => gammaItem.click());

    expect(calls).toHaveLength(1);
    expect(calls[0]!.machineId).toBe("machine-1");
    expect(calls[0]!.to.id).toBe("gamma");
    expect(moveBtn().disabled).toBe(true);

    // Switch BOTH bridge mode (direct -> fresh-array getter+subscribe) AND machineId (machine-1 -> machine-2).
    const subscribedDeps: MoveToManagerDeps = {
      roster: () => {
        rosterReads += 1;
        return { default: roster.default, managers: roster.managers.map((m) => ({ ...m })) };
      },
      subscribeRoster: (l) => {
        rosterListeners.add(l);
        return () => rosterListeners.delete(l);
      },
      moveMachine,
      notify,
    };

    await act(async () => {
      root.render(<MoveToManager machineId="machine-2" manager="alpha" deps={subscribedDeps} />);
    });
    expect(calls[0]!.signal.aborted).toBe(true);
    expect(moveBtn().disabled).toBe(false);
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(rosterReads).toBeLessThan(20);

    // Switch BACK to machine-1 (ABA!), start a fresh move, and settle the stale first move — stale callback is discarded.
    await act(async () => {
      root.render(<MoveToManager machineId="machine-1" manager="alpha" deps={subscribedDeps} />);
    });
    await act(async () => moveBtn().click());
    const betaItem = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("Beta"))!;
    await act(async () => betaItem.click());
    expect(calls).toHaveLength(2);
    expect(moveBtn().disabled).toBe(true);

    calls[0]!.d.resolve(undefined);
    await flush();
    expect(notifications).toEqual([]);
    expect(moveBtn().disabled).toBe(true);

    // Resolve the current move -> notifies ok and releases busy.
    calls[1]!.d.resolve(undefined);
    await flush();
    expect(notifications).toEqual([{ message: t("settings-managers.moved", { name: "Beta" }), tone: "ok" }]);
    expect(moveBtn().disabled).toBe(false);

    // Becoming unmovable while a move is in flight aborts and discards late settlement.
    await act(async () => moveBtn().click());
    const gammaAgain = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("Gamma"))!;
    await act(async () => gammaAgain.click());
    expect(calls).toHaveLength(3);

    await act(async () => {
      root.render(<MoveToManager machineId="machine-1" manager="alpha" movable={false} deps={subscribedDeps} />);
    });
    expect(host.querySelector("button")).toBeNull();
    expect(calls[2]!.signal.aborted).toBe(true);
    calls[2]!.d.reject(new Error("late unmovable error"));
    await flush();
    expect(notifications).toHaveLength(1);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("releases busy on async rejection, sync throw, and notify throw so user can immediately retry", async () => {
    const roster: ManagerList = { default: "alpha", managers: [M_ALPHA, M_GAMMA] };
    const notifications: Array<{ message: string; tone: "ok" | "error" }> = [];
    let mode: "reject" | "syncThrow" | "ok" = "reject";

    const moveMachine = vi.fn((_machineId: string, _to: Manager): Promise<void> => {
      if (mode === "syncThrow") throw new Error("Sync move boom");
      if (mode === "reject") return Promise.reject(new Error("Move rejected"));
      return Promise.resolve();
    });

    let notifyThrows = false;
    await act(async () => {
      root.render(
        <MoveToManager
          machineId="machine-1"
          manager="alpha"
          deps={{
            roster,
            moveMachine,
            notify: (message, tone) => {
              notifications.push({ message, tone });
              if (notifyThrows) throw new Error("Toast container unavailable");
            },
          }}
        />,
      );
    });

    const clickGamma = async () => {
      await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
      const gamma = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("Gamma"))!;
      await act(async () => gamma.click());
      await flush();
    };

    await clickGamma();
    expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(false);
    expect(notifications.at(-1)).toEqual({
      message: t("settings-managers.move_failed", { error: "Move rejected" }),
      tone: "error",
    });

    mode = "syncThrow";
    await clickGamma();
    expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(false);
    expect(notifications.at(-1)).toEqual({
      message: t("settings-managers.move_failed", { error: "Sync move boom" }),
      tone: "error",
    });

    // Even if notify itself throws, try/finally must still release busy and allow immediate retry.
    mode = "ok";
    notifyThrows = true;
    await clickGamma();
    expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(false);

    notifyThrows = false;
    await clickGamma();
    expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(false);
    expect(notifications.at(-1)).toEqual({
      message: t("settings-managers.moved", { name: "Gamma" }),
      tone: "ok",
    });
  });

  it("keeps 100% backward-compatible default behavior when deps is omitted", async () => {
    const requests: Array<{ url: string; method?: string; body?: string }> = [];
    const roster: ManagerList = { default: "alpha", managers: [M_ALPHA, M_BETA, M_GAMMA] };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        requests.push({ url, method: init?.method, body: init?.body ? String(init.body) : undefined });
        if (url.endsWith("/managers")) {
          return new Response(JSON.stringify(roster), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
      }),
    );

    setRoster(roster);
    chooseManager("beta");

    function DefaultHarness() {
      const offline = useManagerOffline("beta");
      const shown = useShownOffline();
      return (
        <div>
          <span data-default-offline>{offline ?? ""}</span>
          <span data-default-shown>{shown?.title ?? ""}</span>
          <MoveToManager machineId="machine-default" manager="alpha" />
        </div>
      );
    }

    await act(async () => {
      root.render(<DefaultHarness />);
    });
    await flush();

    expect(host.querySelector("[data-default-offline]")!.textContent).toContain("Beta");
    expect(host.querySelector("[data-default-shown]")!.textContent).toContain("Beta");

    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    const gamma = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("Gamma"))!;
    await act(async () => gamma.click());
    await flush();

    const moveReq = requests.find((r) => r.url.includes("/managers/gamma/machines"));
    expect(moveReq).toBeDefined();
    expect(moveReq!.method).toBe("POST");
    expect(JSON.parse(moveReq!.body!)).toEqual({ machine_id: "machine-default" });
  });
});
