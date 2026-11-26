// @vitest-environment jsdom
// Controlled-mode, pure-view, bridge-switch, snapshot-caching, async-fencing, error-recovery, and default-fallback tests for CompareRuns.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import { addGroup, resetGroupsCache, type CompareGroup } from "../lib/compare-runs";
import { resetRunner } from "../lib/compare-runner";
import { closePanel, getPanel, type Panel } from "../lib/panel";
import type { Route } from "../lib/router";
import { REVIEW_FEATURE, type GitReview } from "../lib/session-review";
import { getState } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import {
  CompareChangesView,
  CompareRunCardView,
  CompareRuns,
  CompareRunsView,
  type CompareRunnerState,
  type CompareRunsDeps,
  type CompareRunsStoreState,
} from "./CompareRuns";
import { t } from "../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const initialStore = { ...getState() };
const FIXED_NOW = Date.UTC(2026, 10, 26, 12, 0, 0);

let root: Root;
let host: HTMLDivElement;

const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); });
};

const card = (n: number) => host.querySelector<HTMLElement>(`[data-run="${n}"]`)!;
const buttonIn = (el: Element, text: string) =>
  [...el.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === text);

const makeSession = (over: Partial<Session> = {}): Session =>
  ({
    machine_id: "box-a",
    session_id: "s-1",
    title: "Run one session",
    cwd: "/work/g1-run-1",
    status: "idle",
    ...over,
  }) as Session;

const makeMachine = (over: Partial<Machine> = {}): Machine =>
  ({ id: "box-a", name: "Builder Box", online: true, ...over }) as Machine;

const makeGroup = (over: Partial<CompareGroup> = {}): CompareGroup => ({
  id: "g1",
  at: FIXED_NOW - 120_000,
  mid: "box-a",
  cwd: "/work/demo",
  spec: { launcher: "claude" },
  attachments: false,
  runs: [
    { n: 1, branch: "feat/demo-1", phase: "launched", path: "/work/g1-run-1", sid: "s-1" },
    { n: 2, branch: "feat/demo-2", phase: "failed", error: "Worktree collision" },
  ],
  ...over,
});

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
  localStorage.clear();
  resetGroupsCache();
  resetRunner();
  closePanel();
  setToken("synthetic-token");
  history.replaceState({}, "", "/compare/g1");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  Object.assign(getState(), initialStore);
  closePanel();
  resetGroupsCache();
  resetRunner();
  localStorage.clear();
  vi.unstubAllGlobals();
  history.replaceState({}, "", "/");
});

describe("CompareRunsView (pure presentation)", () => {
  it("renders empty and populated states and dispatches callbacks without global singletons", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const onToggleSidebar = vi.fn();
    const onGoAgents = vi.fn();
    const onRefresh = vi.fn();
    const onOpenSession = vi.fn();
    const onReviewSession = vi.fn();
    const onRetryRun = vi.fn();
    const onOpenLaunch = vi.fn();

    await act(async () => {
      root.render(<CompareRunsView group={null} onToggleSidebar={onToggleSidebar} onGoAgents={onGoAgents} />);
    });
    expect(host.textContent).toContain(t("compare-runs.not_found_title"));
    await act(async () => {
      buttonIn(host, t("compare-runs.go_agents"))!.click();
    });
    expect(onGoAgents).toHaveBeenCalledTimes(1);

    const group = makeGroup();
    const session = makeSession();
    const uncertainRun = { n: 3, branch: "feat/demo-3", phase: "launched" as const, launch: "launch-3" };

    await act(async () => {
      root.render(
        <CompareRunsView
          group={group}
          computerName="Isolated Box"
          now={FIXED_NOW}
          reviews
          runs={[
            {
              run: group.runs[0]!,
              status: { kind: "started", session },
              changes: { kind: "ready", files: 2, added: 7, deleted: 3 },
            },
            {
              run: group.runs[1]!,
              status: { kind: "failed", error: "Worktree collision", retry: "run" },
              retrying: false,
              retryError: "Retry still blocked",
            },
            {
              run: uncertainRun,
              status: { kind: "failed", error: "Timeout", retry: null, uncertain: true },
            },
          ]}
          onToggleSidebar={onToggleSidebar}
          onRefresh={onRefresh}
          onGoAgents={onGoAgents}
          onOpenSession={onOpenSession}
          onReviewSession={onReviewSession}
          onRetryRun={onRetryRun}
          onOpenLaunch={onOpenLaunch}
        />,
      );
    });

    expect(host.textContent).toContain("Isolated Box");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(
      t("compare-runs.changes", { count: 2, added: 7, deleted: 3 }),
    );
    expect(card(2).querySelector("[data-run-retry-error]")!.textContent).toBe("Retry still blocked");

    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("assistant-history.sidebar")}"]`)!.click();
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("compare-runs.refresh")}"]`)!.click();
      buttonIn(card(1), t("compare-runs.open"))!.click();
      buttonIn(card(1), t("compare-runs.review"))!.click();
      buttonIn(card(2), t("compare-runs.retry"))!.click();
      buttonIn(card(3), t("compare-runs.open"))!.click();
    });

    expect(onToggleSidebar).toHaveBeenCalledTimes(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(onOpenSession).toHaveBeenCalledWith(session);
    expect(onReviewSession).toHaveBeenCalledWith(session);
    expect(onRetryRun).toHaveBeenCalledWith(group.runs[1]);
    expect(onOpenLaunch).toHaveBeenCalledWith(uncertainRun);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("renders CompareChangesView loading, error, and no-changes variants and CompareRunCardView", async () => {
    const run = makeGroup().runs[0]!;
    const session = makeSession();
    await act(async () => {
      root.render(
        <div>
          <CompareChangesView state={{ kind: "loading" }} />
          <CompareChangesView state={{ kind: "error", error: "Disk busy" }} />
          <CompareRunCardView
            run={run}
            status={{ kind: "started", session }}
            reviews
            changes={{ kind: "ready", files: 0, added: 0, deleted: 0 }}
          />
        </div>,
      );
    });
    const lines = [...host.querySelectorAll<HTMLElement>("[data-run-changes]")];
    expect(lines[0]!.textContent).toBe(t("compare-runs.changes_reading"));
    expect(lines[1]!.textContent).toBe(t("compare-runs.changes_failed"));
    expect(lines[1]!.title).toBe("Disk busy");
    expect(lines[2]!.textContent).toBe(t("compare-runs.no_changes"));
  });
});

describe("CompareRuns controlled mode", () => {
  it("runs fully controlled without touching global store, localStorage, fetch, router, or panel", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    // Pollute global store to prove controlled deps bypass it completely.
    Object.assign(getState(), {
      features: [],
      machines: [makeMachine({ id: "box-a", name: "Global Polluted Name" })],
      sessions: { "box-a": [] },
      launches: {},
    });

    const group = makeGroup();
    const session = makeSession();
    const navCalls: Route[] = [];
    const panelCalls: Panel[] = [];
    const retryCalls: Array<{ groupId: string; runNumber: number }> = [];
    const loadReview = vi.fn(async (): Promise<Pick<GitReview, "files">> => ({
      files: [{ path: "src/app.ts", status: "modified", added: 3, deleted: 1 }],
    }));

    const deps: CompareRunsDeps = {
      group,
      store: {
        machines: [makeMachine({ id: "box-a", name: "Controlled Box" })],
        sessions: { "box-a": [session] },
        features: [REVIEW_FEATURE],
      },
      runner: { active: false, holds: true },
      loadReview,
      retryRun: (groupId, runNumber) => {
        retryCalls.push({ groupId, runNumber });
      },
      navigate: (to) => navCalls.push(to),
      openPanel: (panel) => panelCalls.push(panel),
      now: FIXED_NOW,
    };

    await act(async () => {
      root.render(<CompareRuns id="g1" onToggleSidebar={() => {}} deps={deps} />);
    });
    await flush();

    expect(host.textContent).toContain("Controlled Box");
    expect(host.textContent).not.toContain("Global Polluted Name");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(
      t("compare-runs.changes", { count: 1, added: 3, deleted: 1 }),
    );
    expect(loadReview).toHaveBeenCalledTimes(1);

    await act(async () => {
      buttonIn(card(1), t("compare-runs.review"))!.click();
      buttonIn(card(2), t("compare-runs.retry"))!.click();
    });

    expect(panelCalls).toEqual([{ kind: "review", mid: "box-a", sid: "s-1", cwd: "/work/g1-run-1", scope: "branch" }]);
    expect(navCalls).toEqual([{ name: "session", mid: "box-a", sid: "s-1" }]);
    expect(retryCalls).toEqual([{ groupId: "g1", runNumber: 2 }]);
    expect(getPanel()).toBeNull();
    expect(location.pathname).toBe("/compare/g1");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("caches fresh-array and fresh-object getters under useSyncExternalStore without infinite re-renders", async () => {
    let currentGroup = makeGroup();
    let currentStore: CompareRunsStoreState = {
      machines: [makeMachine({ name: "Fresh Box v1" })],
      sessions: { "box-a": [makeSession({ title: "Fresh Session v1" })] },
      features: [REVIEW_FEATURE],
    };
    let currentRunner: CompareRunnerState = { active: false, holds: false };

    const groupListeners = new Set<() => void>();
    const storeListeners = new Set<() => void>();
    const runnerListeners = new Set<() => void>();
    const loadReview = vi.fn(async () => ({ files: [] }));

    let groupReads = 0;
    let storeReads = 0;
    let runnerReads = 0;

    const deps: CompareRunsDeps = {
      group: () => {
        groupReads += 1;
        return { ...currentGroup, runs: currentGroup.runs.map((r) => ({ ...r })) };
      },
      subscribeGroup: (l) => {
        groupListeners.add(l);
        return () => groupListeners.delete(l);
      },
      store: () => {
        storeReads += 1;
        return {
          machines: currentStore.machines.map((m) => ({ ...m })),
          sessions: { "box-a": (currentStore.sessions["box-a"] ?? []).map((s) => ({ ...s })) },
          features: [...(currentStore.features ?? [])],
        };
      },
      subscribeStore: (l) => {
        storeListeners.add(l);
        return () => storeListeners.delete(l);
      },
      runner: () => {
        runnerReads += 1;
        return { ...currentRunner };
      },
      subscribeRunner: (l) => {
        runnerListeners.add(l);
        return () => runnerListeners.delete(l);
      },
      loadReview,
      now: FIXED_NOW,
    };

    await act(async () => {
      root.render(<CompareRuns id="g1" onToggleSidebar={() => {}} deps={deps} />);
    });
    await flush();

    expect(host.textContent).toContain("Fresh Box v1");
    expect(card(1).textContent).toContain("Fresh Session v1");
    expect(buttonIn(card(2), t("compare-runs.retry"))).toBeUndefined();
    expect(groupReads).toBeLessThan(20);
    expect(storeReads).toBeLessThan(20);
    expect(runnerReads).toBeLessThan(20);

    // Emit updates through each external subscriber.
    currentStore = {
      ...currentStore,
      machines: [makeMachine({ name: "Fresh Box v2" })],
      sessions: { "box-a": [makeSession({ title: "Fresh Session v2" })] },
    };
    currentRunner = { active: false, holds: true };
    currentGroup = makeGroup({
      runs: [
        { n: 1, branch: "feat/demo-1", phase: "launched", path: "/work/g1-run-1", sid: "s-1" },
        { n: 2, branch: "feat/demo-2-renamed", phase: "failed", error: "Worktree collision" },
      ],
    });
    await act(async () => {
      groupListeners.forEach((l) => l());
      storeListeners.forEach((l) => l());
      runnerListeners.forEach((l) => l());
    });
    await flush();

    expect(host.textContent).toContain("Fresh Box v2");
    expect(card(1).textContent).toContain("Fresh Session v2");
    expect(card(2).textContent).toContain("feat/demo-2-renamed");
    expect(buttonIn(card(2), t("compare-runs.retry"))).toBeDefined();
  });

  it("aborts and fences stale loadReview responses across group switches, bridge switches, ABA, and unmount", async () => {
    const g1 = makeGroup({
      id: "g1",
      runs: [{ n: 1, branch: "b/g1-1", phase: "launched", path: "/work/g1-1", sid: "s-g1" }],
    });
    const g2 = makeGroup({
      id: "g2",
      runs: [{ n: 1, branch: "b/g2-1", phase: "launched", path: "/work/g2-1", sid: "s-g2" }],
    });
    const groupsById: Record<string, CompareGroup> = { g1, g2 };

    const store1: CompareRunsStoreState = {
      machines: [makeMachine()],
      sessions: { "box-a": [makeSession({ session_id: "s-g1", title: "G1 Session", cwd: "/work/g1-1" })] },
      reviews: true,
    };
    const store2: CompareRunsStoreState = {
      machines: [makeMachine()],
      sessions: { "box-a": [makeSession({ session_id: "s-g2", title: "G2 Session", cwd: "/work/g2-1" })] },
      reviews: true,
    };
    const storeBoth: CompareRunsStoreState = {
      machines: [makeMachine()],
      sessions: {
        "box-a": [
          makeSession({ session_id: "s-g1", title: "G1 Session", cwd: "/work/g1-1" }),
          makeSession({ session_id: "s-g2", title: "G2 Session", cwd: "/work/g2-1" }),
        ],
      },
      reviews: true,
    };

    const requests: Array<{ cwd: string; signal: AbortSignal; d: ReturnType<typeof deferred<Pick<GitReview, "files">>> }> = [];
    const loadReview = vi.fn((_mid: string, cwd: string, _target: unknown, signal: AbortSignal) => {
      const d = deferred<Pick<GitReview, "files">>();
      requests.push({ cwd, signal, d });
      return d.promise;
    });

    // Render g1 via direct values.
    await act(async () => {
      root.render(
        <CompareRuns
          id="g1"
          onToggleSidebar={() => {}}
          deps={{ group: g1, store: store1, runner: { active: false, holds: false }, loadReview, now: FIXED_NOW }}
        />,
      );
    });
    await flush();

    expect(requests).toHaveLength(1);
    expect(requests[0]!.cwd).toBe("/work/g1-1");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_reading"));

    // Switch BOTH bridge mode (direct -> getter+subscribe) AND group (g1 -> g2) simultaneously.
    const subscribeGroup = () => () => {};
    const subscribeStore = () => () => {};
    const subscribeRunner = () => () => {};
    await act(async () => {
      root.render(
        <CompareRuns
          id="g2"
          onToggleSidebar={() => {}}
          deps={{
            group: (id) => ({ ...groupsById[id]!, runs: groupsById[id]!.runs.map((r) => ({ ...r })) }),
            subscribeGroup,
            store: () => ({ ...store2, machines: [...store2.machines] }),
            subscribeStore,
            runner: () => ({ active: false, holds: false }),
            subscribeRunner,
            loadReview,
            now: FIXED_NOW,
          }}
        />,
      );
    });
    await flush();

    // g1 request was aborted on switch; g2 request is now active.
    expect(requests[0]!.signal.aborted).toBe(true);
    expect(requests).toHaveLength(2);
    expect(requests[1]!.cwd).toBe("/work/g2-1");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_reading"));

    // Resolve g2 successfully.
    requests[1]!.d.resolve({ files: [{ path: "g2.ts", status: "added", added: 9, deleted: 0 }] });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(
      t("compare-runs.changes", { count: 1, added: 9, deleted: 0 }),
    );

    // Switch back to g1 (ABA!) — old g2 counts must reset immediately to loading, and a new g1 request starts.
    await act(async () => {
      root.render(
        <CompareRuns
          id="g1"
          onToggleSidebar={() => {}}
          deps={{ group: g1, store: storeBoth, runner: { active: false, holds: false }, loadReview, now: FIXED_NOW }}
        />,
      );
    });
    await flush();

    expect(requests).toHaveLength(3);
    expect(requests[2]!.cwd).toBe("/work/g1-1");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_reading"));

    // Late resolution of original g1 request #0 (even ignoring abort!) must be discarded on ABA epoch.
    requests[0]!.d.resolve({ files: [{ path: "stale.ts", status: "modified", added: 99, deleted: 88 }] });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_reading"));

    // Current g1 request #2 resolves cleanly.
    requests[2]!.d.resolve({ files: [{ path: "fresh.ts", status: "modified", added: 2, deleted: 1 }] });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(
      t("compare-runs.changes", { count: 1, added: 2, deleted: 1 }),
    );

    // Trigger refresh and unmount mid-flight; signal aborts and late rejection is discarded safely.
    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("compare-runs.refresh")}"]`)!.click();
    });
    await flush();
    expect(requests).toHaveLength(4);
    await act(async () => root.unmount());
    expect(requests[3]!.signal.aborted).toBe(true);
    requests[3]!.d.reject(new Error("late unmount error"));
    await flush();
    // Re-create root for afterEach cleanup.
    root = createRoot(host);
  });

  it("recovers from loadReview failures (async rejection and sync throw) on refresh and clears error on switch", async () => {
    const g1 = makeGroup({
      id: "g1",
      runs: [{ n: 1, branch: "b/1", phase: "launched", path: "/work/g1-1", sid: "s-1" }],
    });
    const g2 = makeGroup({
      id: "g2",
      runs: [{ n: 1, branch: "b/2", phase: "launched", path: "/work/g2-1", sid: "s-2" }],
    });
    const store: CompareRunsStoreState = {
      machines: [makeMachine()],
      sessions: {
        "box-a": [
          makeSession({ session_id: "s-1", cwd: "/work/g1-1" }),
          makeSession({ session_id: "s-2", cwd: "/work/g2-1" }),
        ],
      },
      reviews: true,
    };

    let attempt = 0;
    const loadReview = vi.fn((_mid: string, _cwd: string): Promise<Pick<GitReview, "files">> => {
      attempt += 1;
      if (attempt === 1) throw new Error("Sync load failure");
      if (attempt === 2) return Promise.reject(new Error("Async load failure"));
      return Promise.resolve({ files: [{ path: "ok.ts", status: "modified", added: 4, deleted: 2 }] });
    });

    const renderGroup = (id: string, group: CompareGroup) =>
      act(async () => {
        root.render(
          <CompareRuns
            id={id}
            onToggleSidebar={() => {}}
            deps={{ group, store, runner: { active: false, holds: false }, loadReview, now: FIXED_NOW }}
          />,
        );
      });

    await renderGroup("g1", g1);
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_failed"));
    expect(card(1).querySelector<HTMLElement>("[data-run-changes]")!.title).toBe("Sync load failure");

    // Refresh clears sync error, hits async rejection.
    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("compare-runs.refresh")}"]`)!.click();
    });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_failed"));
    expect(card(1).querySelector<HTMLElement>("[data-run-changes]")!.title).toBe("Async load failure");

    // Refresh again recovers to ready counts.
    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("compare-runs.refresh")}"]`)!.click();
    });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(
      t("compare-runs.changes", { count: 1, added: 4, deleted: 2 }),
    );

    // Switch to g2 when loadReview rejects, then switch back to g1 — error never bleeds across groups.
    const g2Fail = vi.fn(async () => { throw new Error("G2 broken"); });
    await act(async () => {
      root.render(
        <CompareRuns
          id="g2"
          onToggleSidebar={() => {}}
          deps={{ group: g2, store, runner: { active: false, holds: false }, loadReview: g2Fail, now: FIXED_NOW }}
        />,
      );
    });
    await flush();
    expect(card(1).querySelector<HTMLElement>("[data-run-changes]")!.title).toBe("G2 broken");

    const g1Pending = deferred<Pick<GitReview, "files">>();
    await act(async () => {
      root.render(
        <CompareRuns
          id="g1"
          onToggleSidebar={() => {}}
          deps={{ group: g1, store, runner: { active: false, holds: false }, loadReview: () => g1Pending.promise, now: FIXED_NOW }}
        />,
      );
    });
    await flush();
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.changes_reading"));
    expect(card(1).querySelector<HTMLElement>("[data-run-changes]")!.title).toBe("");
  });

  it("fences async retryRun across group ABA switches and releases busy with recoverable error on throw", async () => {
    const g1 = makeGroup({
      id: "g1",
      runs: [
        { n: 1, branch: "b/g1-1", phase: "failed", error: "Initial failure" },
        { n: 2, branch: "b/g1-2", phase: "waiting" },
      ],
    });
    const g2 = makeGroup({
      id: "g2",
      runs: [
        { n: 1, branch: "b/g2-1", phase: "failed", error: "Other failure" },
        { n: 2, branch: "b/g2-2", phase: "waiting" },
      ],
    });
    const store: CompareRunsStoreState = { machines: [makeMachine()], sessions: {}, reviews: false };

    const calls: Array<{ groupId: string; runNumber: number; signal: AbortSignal; d: ReturnType<typeof deferred<void>> }> = [];
    const retryRun = vi.fn((groupId: string, runNumber: number, signal: AbortSignal) => {
      const d = deferred<void>();
      calls.push({ groupId, runNumber, signal, d });
      return d.promise;
    });

    const renderWith = (id: string, group: CompareGroup, fn: CompareRunsDeps["retryRun"] = retryRun) =>
      act(async () => {
        root.render(
          <CompareRuns
            id={id}
            onToggleSidebar={() => {}}
            deps={{ group, store, runner: { active: false, holds: true }, retryRun: fn, now: FIXED_NOW }}
          />,
        );
      });

    await renderWith("g1", g1);
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    expect(calls).toHaveLength(1);
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(true);

    // Duplicate click while in flight is ignored synchronously.
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    expect(calls).toHaveLength(1);

    // Switch g1 -> g2 -> g1 (ABA): first retry is aborted and busy resets immediately.
    await renderWith("g2", g2);
    expect(calls[0]!.signal.aborted).toBe(true);
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);

    await renderWith("g1", g1);
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);

    // Start a fresh retry on g1, then reject the stale first retry — must NOT clear busy or set error!
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    expect(calls).toHaveLength(2);
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(true);

    calls[0]!.d.reject(new Error("Stale ABA retry error"));
    await flush();
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(true);
    expect(card(1).querySelector("[data-run-retry-error]")).toBeNull();

    // Reject current retry #2 — busy releases and recoverable error appears.
    calls[1]!.d.reject(new Error("Hub refused retry"));
    await flush();
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);
    expect(card(1).querySelector("[data-run-retry-error]")!.textContent).toBe("Hub refused retry");

    // Clicking Retry again clears error and succeeds cleanly.
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    expect(calls).toHaveLength(3);
    expect(card(1).querySelector("[data-run-retry-error]")).toBeNull();
    calls[2]!.d.resolve(undefined);
    await flush();
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);
    expect(card(1).querySelector("[data-run-retry-error]")).toBeNull();

    // Synchronous throw in retryRun also shows recoverable error without locking busy.
    await renderWith("g1", g1, () => {
      throw new Error("Sync retry boom");
    });
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    await flush();
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);
    expect(card(1).querySelector("[data-run-retry-error]")!.textContent).toBe("Sync retry boom");

    // Transitioning run 1 failed -> waiting -> failed prunes the prior retry error cleanly.
    const g1Waiting = makeGroup({
      id: "g1",
      runs: [
        { n: 1, branch: "b/g1-1", phase: "waiting" },
        { n: 2, branch: "b/g1-2", phase: "waiting" },
      ],
    });
    await act(async () => {
      root.render(
        <CompareRuns
          id="g1"
          onToggleSidebar={() => {}}
          deps={{ group: g1Waiting, store, runner: { active: true, holds: true }, retryRun, now: FIXED_NOW }}
        />,
      );
    });
    await renderWith("g1", g1, retryRun);
    expect(card(1).querySelector("[data-run-retry-error]")).toBeNull();
  });

  it("keeps an in-flight controlled retry alive across normal failed -> waiting progress updates and only aborts on run removal/replacement", async () => {
    let currentGroup = makeGroup({
      id: "g1",
      runs: [
        { n: 1, branch: "b/g1-1", phase: "failed", error: "Worktree busy" },
        { n: 2, branch: "b/g1-2", phase: "waiting" },
      ],
    });
    let currentRunner: CompareRunnerState = { active: false, holds: true };
    const groupListeners = new Set<() => void>();
    const runnerListeners = new Set<() => void>();
    const emitGroup = () => groupListeners.forEach((l) => l());
    const emitRunner = () => runnerListeners.forEach((l) => l());
    const store: CompareRunsStoreState = { machines: [makeMachine()], sessions: {}, reviews: false };

    let attempt = 0;
    const signals: AbortSignal[] = [];
    const deferreds: Array<ReturnType<typeof deferred<void>>> = [];
    const completions: Array<{ attempt: number; aborted: boolean }> = [];

    const retryRun = vi.fn(async (_groupId: string, _runNumber: number, signal: AbortSignal) => {
      const idx = ++attempt;
      signals.push(signal);
      const d = deferred<void>();
      deferreds.push(d);
      // Simulate the controlled runner publishing normal progress (failed -> waiting) while still awaiting the network call.
      currentGroup = {
        ...currentGroup,
        runs: currentGroup.runs.map((r) => (r.n === 1 ? { ...r, phase: "waiting", error: undefined } : r)),
      };
      currentRunner = { active: true, holds: true };
      emitGroup();
      emitRunner();
      await d.promise;
      completions.push({ attempt: idx, aborted: signal.aborted });
    });

    const deps: CompareRunsDeps = {
      group: () => ({ ...currentGroup, runs: currentGroup.runs.map((r) => ({ ...r })) }),
      subscribeGroup: (l) => {
        groupListeners.add(l);
        return () => groupListeners.delete(l);
      },
      store,
      runner: () => ({ ...currentRunner }),
      subscribeRunner: (l) => {
        runnerListeners.add(l);
        return () => runnerListeners.delete(l);
      },
      retryRun,
      now: FIXED_NOW,
    };

    await act(async () => {
      root.render(<CompareRuns id="g1" onToggleSidebar={() => {}} deps={deps} />);
    });

    // Attempt 1: Retry emits failed -> waiting, signal must remain un-aborted and complete normally.
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    await flush();

    expect(card(1).dataset.runStatus).toBe("waiting");
    expect(signals).toHaveLength(1);
    expect(signals[0]!.aborted).toBe(false);

    // Advance further to worktree while the same retry promise is still pending.
    currentGroup = {
      ...currentGroup,
      runs: currentGroup.runs.map((r) => (r.n === 1 ? { ...r, phase: "worktree" } : r)),
    };
    await act(async () => emitGroup());
    expect(card(1).dataset.runStatus).toBe("worktree");
    expect(signals[0]!.aborted).toBe(false);

    deferreds[0]!.resolve(undefined);
    await flush();
    expect(completions).toEqual([{ attempt: 1, aborted: false }]);

    // Attempt 2: Retry emits failed -> waiting and then rejects; error must be preserved (not dropped by stale abort).
    currentGroup = {
      ...currentGroup,
      runs: currentGroup.runs.map((r) => (r.n === 1 ? { ...r, phase: "failed", error: "Worktree busy" } : r)),
    };
    currentRunner = { active: false, holds: true };
    await act(async () => {
      emitGroup();
      emitRunner();
    });

    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    await flush();
    expect(card(1).dataset.runStatus).toBe("waiting");
    expect(signals[1]!.aborted).toBe(false);

    currentGroup = {
      ...currentGroup,
      runs: currentGroup.runs.map((r) => (r.n === 1 ? { ...r, phase: "failed", error: "Worktree busy" } : r)),
    };
    currentRunner = { active: false, holds: true };
    deferreds[1]!.reject(new Error("Upstream network timeout"));
    await act(async () => {
      emitGroup();
      emitRunner();
    });
    await flush();
    expect(signals[1]!.aborted).toBe(false);
    expect(card(1).querySelector("[data-run-retry-error]")!.textContent).toBe("Upstream network timeout");
    expect(buttonIn(card(1), t("compare-runs.retry"))!.disabled).toBe(false);

    // Attempt 3: Replacing/removing run 1 while retry is pending DOES abort its AbortSignal and discards late rejection.
    await act(async () => {
      buttonIn(card(1), t("compare-runs.retry"))!.click();
    });
    await flush();
    expect(signals[2]!.aborted).toBe(false);

    currentGroup = {
      ...currentGroup,
      runs: currentGroup.runs.map((r) => (r.n === 1 ? { n: 1, branch: "b/g1-1-replaced", phase: "failed", error: "Replaced" } : r)),
    };
    currentRunner = { active: false, holds: true };
    await act(async () => {
      emitGroup();
      emitRunner();
    });
    expect(signals[2]!.aborted).toBe(true);
    deferreds[2]!.reject(new Error("Stale replaced run error"));
    await flush();
    expect(card(1).querySelector("[data-run-retry-error]")).toBeNull();
  });

  it("keeps 100% backward-compatible default behavior when deps is omitted", async () => {
    const group = makeGroup();
    addGroup(group);
    Object.assign(getState(), {
      loaded: true,
      features: [REVIEW_FEATURE],
      launches: {},
      machines: [makeMachine({ name: "Default Store Box" })],
      sessions: { "box-a": [makeSession({ title: "Default Store Session" })] },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify({ root: "/work/g1-run-1", scope: "branch", base: {}, files: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    await act(async () => {
      root.render(<CompareRuns id="g1" onToggleSidebar={() => {}} />);
    });
    await flush();

    expect(host.textContent).toContain("Default Store Box");
    expect(card(1).textContent).toContain("Default Store Session");
    expect(card(1).querySelector("[data-run-changes]")!.textContent).toBe(t("compare-runs.no_changes"));
  });
});
