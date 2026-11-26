// @vitest-environment jsdom
// Controlled UsageUnconnected bypasses default store/session-models/usage-windows hooks, caches fresh-array getters,
// aborts and fences stale/ABA launcher reads across machine/scope switches, recovers from failures, and preserves defaults.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";
import type { UnconnectedProvider, UsageAccount } from "../lib/usage-windows";
import type { UsageProviderCandidate, UsageUnconnectedDeps } from "./UsageUnconnected";

const mocks = vi.hoisted(() => ({
  storeState: {
    assistant: { machine_id: "m-default" } as { machine_id?: string } | undefined,
    machines: [{ id: "m-online", name: "Online", online: true }] as Machine[],
  },
  modelVersion: 0,
  usageReady: true,
  hiddenProviders: [] as readonly string[],
  launchersByMid: {} as Record<string, UsageProviderCandidate[] | undefined>,
  useStore: vi.fn(),
  useModelLists: vi.fn(),
  assistantLaunchers: vi.fn(),
  readAssistantLaunchers: vi.fn(),
  useUsageWindowsReady: vi.fn(),
  useHiddenUsageProviders: vi.fn(),
}));

vi.mock("../lib/store", () => ({
  useStore: (select: (s: typeof mocks.storeState) => unknown) => {
    mocks.useStore(select);
    return select(mocks.storeState);
  },
}));
vi.mock("../lib/session-models", () => ({
  useModelLists: () => {
    mocks.useModelLists();
    return mocks.modelVersion;
  },
  assistantLaunchers: (mid: string) => {
    mocks.assistantLaunchers(mid);
    const list = mocks.launchersByMid[mid];
    return list ? { info: { launchers: list, unavailable: [] } } : {};
  },
  readAssistantLaunchers: (mid: string) => mocks.readAssistantLaunchers(mid),
}));
vi.mock("../lib/usage-windows", async (original) => ({
  ...(await original<typeof import("../lib/usage-windows")>()),
  useUsageWindowsReady: () => {
    mocks.useUsageWindowsReady();
    return mocks.usageReady;
  },
  useHiddenUsageProviders: () => {
    mocks.useHiddenUsageProviders();
    return mocks.hiddenProviders;
  },
}));

const { UsageUnconnected, UsageUnconnectedView } = await import("./UsageUnconnected");

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

const NOW = Date.parse("2026-11-26T00:00:00Z");

const makeAccount = (provider: string, name: string): UsageAccount => ({
  id: `${provider}-1`,
  provider,
  name,
  sampled_at: new Date(NOW - 60_000).toISOString(),
  windows: [{ label: "5h", window_minutes: 300, used_percent: 25, resets_at: new Date(NOW + 3600_000).toISOString() }],
});

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.useModelLists).not.toHaveBeenCalled();
  expect(mocks.assistantLaunchers).not.toHaveBeenCalled();
  expect(mocks.readAssistantLaunchers).not.toHaveBeenCalled();
  expect(mocks.useUsageWindowsReady).not.toHaveBeenCalled();
  expect(mocks.useHiddenUsageProviders).not.toHaveBeenCalled();
};

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  Object.values(mocks).forEach((v) => {
    if (typeof v === "function") v.mockReset();
  });
  mocks.storeState = {
    assistant: { machine_id: "m-default" },
    machines: [{ id: "m-online", name: "Online", online: true }],
  };
  mocks.modelVersion = 1;
  mocks.usageReady = true;
  mocks.hiddenProviders = [];
  mocks.launchersByMid = { "m-default": [{ name: "claude" }, { name: "codex", agent: "codex" }] };
  mocks.readAssistantLaunchers.mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("UsageUnconnected controlled deps, pure view separation, snapshot caching, fencing, and error recovery", () => {
  it("runs fully controlled without touching default singletons and separates pure UsageUnconnectedView", async () => {
    const emissions: Array<readonly UnconnectedProvider[]> = [];
    const onChange = vi.fn((next: readonly UnconnectedProvider[]) => { emissions.push(next); });
    const deps: UsageUnconnectedDeps = {
      scopeKey: "acct-1",
      machineId: "m1",
      usageReady: true,
      hiddenProviders: ["codex"],
      launchers: [
        { name: "claude" },
        { name: "codex", agent: "codex" },
        { name: "agy", agent: "agy" },
      ],
      now: NOW,
    };

    await act(async () => root.render(
      <StrictMode>
        <UsageUnconnected accounts={[makeAccount("agy", "Antigravity")]} onChange={onChange} deps={deps} />
      </StrictMode>,
    ));

    expect(host.innerHTML).toBe("");
    expect(emissions.at(-1)).toEqual([{ id: "claude", title: "Claude Code", agent: "claude" }]);

    // Pure UsageUnconnectedView returns null by default and renders custom markup when `render` is provided.
    await act(async () => root.render(<UsageUnconnectedView providers={emissions.at(-1)!} />));
    expect(host.innerHTML).toBe("");

    await act(async () => root.render(
      <UsageUnconnectedView
        providers={emissions.at(-1)!}
        loading
        error="offline"
        render={({ providers, loading, error }) => (
          <div data-pure-usage>{`${providers.map((p) => p.id).join(",")}|${loading}|${error}`}</div>
        )}
      />,
    ));
    expect(host.querySelector("[data-pure-usage]")?.textContent).toBe("claude|true|offline");
    expectNoDefaultSingletons();
  });

  it("caches fresh-array getters for useSyncExternalStore and switches cleanly between direct values and getters", async () => {
    const midListeners = new Set<() => void>();
    const readyListeners = new Set<() => void>();
    const hiddenListeners = new Set<() => void>();
    const launcherListeners = new Set<() => void>();
    const sub = (set: Set<() => void>) => (l: () => void) => {
      set.add(l);
      return () => { set.delete(l); };
    };
    let currentMid = "m1";
    let currentReady = true;
    let currentHidden = ["codex"];
    let currentLaunchers: Record<string, UsageProviderCandidate[]> = {
      m1: [{ name: "claude" }, { name: "codex", agent: "codex" }],
      m2: [{ name: "agy", agent: "agy" }],
    };
    const onChange = vi.fn();

    // Start with direct values.
    await act(async () => root.render(
      <StrictMode>
        <UsageUnconnected
          accounts={[]}
          onChange={onChange}
          deps={{
            scopeKey: "s1",
            machineId: "m1",
            usageReady: true,
            hiddenProviders: ["codex"],
            launchers: [{ name: "claude" }, { name: "codex", agent: "codex" }],
            now: NOW,
          }}
        />
      </StrictMode>,
    ));
    expect(onChange).toHaveBeenLastCalledWith([{ id: "claude", title: "Claude Code", agent: "claude" }]);

    // Switch in-place to fresh-array getters + subscribe without triggering Maximum update depth exceeded.
    const getterDeps: UsageUnconnectedDeps = {
      scopeKey: "s1",
      machineId: () => currentMid,
      subscribeMachineId: sub(midListeners),
      usageReady: () => currentReady,
      subscribeUsageReady: sub(readyListeners),
      hiddenProviders: () => [...currentHidden],
      subscribeHiddenProviders: sub(hiddenListeners),
      launchers: (mid) => (currentLaunchers[mid] ?? []).map((item) => ({ ...item })),
      subscribeLaunchers: sub(launcherListeners),
      now: NOW,
    };
    await act(async () => root.render(
      <StrictMode>
        <UsageUnconnected accounts={[]} onChange={onChange} deps={getterDeps} />
      </StrictMode>,
    ));

    // Re-emitting identical data from fresh-array getters stays stable.
    await act(async () => {
      hiddenListeners.forEach((l) => l());
      launcherListeners.forEach((l) => l());
    });
    expect(onChange).toHaveBeenLastCalledWith([{ id: "claude", title: "Claude Code", agent: "claude" }]);

    // Updating machineId and hiddenProviders through subscriptions recomputes cleanly.
    currentMid = "m2";
    await act(async () => { midListeners.forEach((l) => l()); });
    expect(onChange).toHaveBeenLastCalledWith([{ id: "agy", title: "Antigravity", agent: "agy" }]);

    currentHidden = ["agy"];
    await act(async () => { hiddenListeners.forEach((l) => l()); });
    expect(onChange).toHaveBeenLastCalledWith([]);

    currentReady = false;
    await act(async () => { readyListeners.forEach((l) => l()); });
    expect(onChange).toHaveBeenLastCalledWith([]);
    expectNoDefaultSingletons();
  });

  it("aborts and fences stale async reads across machine switches, ABA scope bursts, and unmount", async () => {
    const scopeListeners = new Set<() => void>();
    let currentScope = "acct-1";
    const reads: Array<{ mid: string; signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const onError = vi.fn();
    const onChange = vi.fn();

    const makeDeps = (mid: string): UsageUnconnectedDeps => ({
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      machineId: mid,
      usageReady: true,
      hiddenProviders: [],
      launchers: (m) => (m === "m1" ? [{ name: "claude" }] : [{ name: "codex", agent: "codex" }]),
      readLaunchers: (m, signal) => {
        const gate = deferred<void>();
        reads.push({ mid: m, signal, gate });
        return gate.promise;
      },
      onError,
      now: NOW,
      render: ({ loading, error, providers }) => (
        <div data-state={`${loading}|${error ?? ""}|${providers.map((p) => p.id).join(",")}`} />
      ),
    });

    await act(async () => root.render(
      <UsageUnconnected accounts={[]} onChange={onChange} deps={makeDeps("m1")} />,
    ));
    expect(reads).toHaveLength(1);
    expect(reads[0].mid).toBe("m1");
    expect(host.querySelector("[data-state]")?.getAttribute("data-state")).toBe("true||claude");

    // Switch machine m1 -> m2 while m1's read is still in flight: m1 aborts and m2 starts loading.
    await act(async () => root.render(
      <UsageUnconnected accounts={[]} onChange={onChange} deps={makeDeps("m2")} />,
    ));
    expect(reads[0].signal.aborted).toBe(true);
    expect(reads).toHaveLength(2);
    expect(reads[1].mid).toBe("m2");
    expect(host.querySelector("[data-state]")?.getAttribute("data-state")).toBe("true||codex");

    // Stale m1 failure must NOT clear m2's loading state or report an error.
    await act(async () => {
      reads[0].gate.reject(new Error("stale m1 error"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expect(host.querySelector("[data-state]")?.getAttribute("data-state")).toBe("true||codex");

    // Perform an ABA scope burst (acct-1 -> acct-2 -> acct-1) while m2's read is in flight.
    await act(async () => {
      currentScope = "acct-2";
      scopeListeners.forEach((l) => l());
      currentScope = "acct-1";
      scopeListeners.forEach((l) => l());
    });
    expect(reads[1].signal.aborted).toBe(true);
    expect(reads).toHaveLength(3);

    // Late rejection from epoch 2 (m2 before ABA) must be discarded; resolving epoch 3 clears loading cleanly.
    await act(async () => {
      reads[1].gate.reject(new Error("stale ABA error"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expect(host.querySelector("[data-state]")?.getAttribute("data-state")).toBe("true||codex");

    await act(async () => {
      reads[2].gate.resolve();
      await Promise.resolve();
    });
    expect(host.querySelector("[data-state]")?.getAttribute("data-state")).toBe("false||codex");

    // Unmount aborts an active read and discards its late rejection.
    await act(async () => root.render(
      <UsageUnconnected accounts={[]} onChange={onChange} deps={makeDeps("m3")} />,
    ));
    const lastRead = reads.at(-1)!;
    await act(async () => root.render(null));
    expect(lastRead.signal.aborted).toBe(true);
    await act(async () => {
      lastRead.gate.reject(new Error("after unmount"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("releases loading via try/finally on synchronous throws and rejected reads while preserving errors and recovering", async () => {
    const onError = vi.fn();
    const onChange = vi.fn();

    // 1) Synchronous throw from readLaunchers releases loading and surfaces formatted error.
    await act(async () => root.render(
      <UsageUnconnected
        accounts={[]}
        onChange={onChange}
        deps={{
          scopeKey: "s1",
          machineId: "m1",
          usageReady: true,
          hiddenProviders: [],
          launchers: [{ name: "claude" }],
          readLaunchers: () => { throw new Error("sync boom"); },
          formatError: (e) => `fmt:${e instanceof Error ? e.message : String(e)}`,
          onError,
          now: NOW,
          render: ({ loading, error }) => <div data-err={`${loading}|${error ?? ""}`} />,
        }}
      />,
    ));
    expect(onError).toHaveBeenCalledExactlyOnceWith("fmt:sync boom");
    expect(host.querySelector("[data-err]")?.getAttribute("data-err")).toBe("false|fmt:sync boom");

    // 2) Async rejection releases loading and preserves error; switching to a succeeding scope clears error and recovers.
    await act(async () => root.render(
      <UsageUnconnected
        accounts={[]}
        onChange={onChange}
        deps={{
          scopeKey: "s2",
          machineId: "m1",
          usageReady: true,
          hiddenProviders: [],
          launchers: [{ name: "claude" }],
          readLaunchers: () => Promise.reject(new Error("async boom")),
          formatError: (e) => `fmt:${e instanceof Error ? e.message : String(e)}`,
          onError,
          now: NOW,
          render: ({ loading, error }) => <div data-err={`${loading}|${error ?? ""}`} />,
        }}
      />,
    ));
    expect(onError).toHaveBeenLastCalledWith("fmt:async boom");
    expect(host.querySelector("[data-err]")?.getAttribute("data-err")).toBe("false|fmt:async boom");

    await act(async () => root.render(
      <UsageUnconnected
        accounts={[]}
        onChange={onChange}
        deps={{
          scopeKey: "s3",
          machineId: "m1",
          usageReady: true,
          hiddenProviders: [],
          launchers: [{ name: "claude" }],
          readLaunchers: () => Promise.resolve(),
          onError,
          now: NOW,
          render: ({ loading, error }) => <div data-err={`${loading}|${error ?? ""}`} />,
        }}
      />,
    ));
    expect(host.querySelector("[data-err]")?.getAttribute("data-err")).toBe("false|");
    expectNoDefaultSingletons();
  });

  it("preserves default store, session-models, and usage-windows hook fallbacks when deps are omitted", async () => {
    const onChange = vi.fn();
    await act(async () => root.render(<UsageUnconnected accounts={[]} onChange={onChange} />));

    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useModelLists).toHaveBeenCalled();
    expect(mocks.useUsageWindowsReady).toHaveBeenCalled();
    expect(mocks.useHiddenUsageProviders).toHaveBeenCalled();
    expect(mocks.readAssistantLaunchers).toHaveBeenCalledWith("m-default");
    expect(mocks.assistantLaunchers).toHaveBeenCalledWith("m-default");
    expect(onChange).toHaveBeenLastCalledWith([
      { id: "claude", title: "Claude Code", agent: "claude" },
      { id: "codex", title: "Codex", agent: "codex" },
    ]);

    // When usage windows are not ready yet, emits [].
    mocks.usageReady = false;
    await act(async () => root.render(<UsageUnconnected accounts={[]} onChange={onChange} />));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });
});
