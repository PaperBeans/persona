// Compare runs (`/compare/<id>`, #8916, docs/web/web.md "Compare runs"): the runs of one New agent start side by side, each
// in a worktree of its own. A card per run says how far it got (its worktree, its start), then, as a session, the sidebar's
// state mark and title, its branch and what it has changed since its branch left the main one; Open and Review changes
// go to it. Nothing here merges or deletes: keeping one result is the Git and worktree tools' job.
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { errorText } from "../lib/api";
import { awaitsReply, justFinished, type AgentMutes } from "../lib/agent-groups";
import { getGroup, runStatus, subscribeGroups, type CompareGroup, type CompareRun, type RunStatus } from "../lib/compare-runs";
import { retryRun, useRunner } from "../lib/compare-runner";
import { ago, backgroundWaiting, sessionTitle, shortPath, stateOf } from "../lib/format";
import { machineWithHub } from "../lib/mesh";
import type { AgentLaunch } from "../lib/new-agent";
import { openPanel, type Panel } from "../lib/panel";
import { navigate, type Route } from "../lib/router";
import { loadReview, REVIEW_FEATURE, reviewTotals, type GitReview, type ReviewTarget } from "../lib/session-review";
import { sameValue } from "../lib/stable-lines";
import { STATE_LABEL, stateName } from "../lib/state-labels";
import { hubHas, useStore } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import { Button, cx, IconButton, Spinner, useNow } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { StatusMark } from "../ui/StatusMark";
import { Empty } from "./assistant/parts";
import { t } from "../i18n";

export interface CompareRunsStoreState {
  machines: readonly Machine[];
  sessions: Readonly<Record<string, readonly Session[]>>;
  launches?: Readonly<Record<string, AgentLaunch>>;
  mutes?: AgentMutes;
  reviews?: boolean;
  features?: readonly string[];
}

export interface CompareRunnerState {
  active: boolean;
  holds: boolean;
}

export type CompareChangesState =
  | { kind: "loading" }
  | { kind: "ready"; files: number; added: number; deleted: number }
  | { kind: "error"; error: string };

export interface CompareRunsDeps {
  /** Controlled comparison group or reader (bypasses `useCompareGroup` when supplied). */
  group?: CompareGroup | null | ((id: string) => CompareGroup | null | undefined);
  /** Subscribe to external group updates when `group` is a reader. */
  subscribeGroup?: (listener: () => void) => () => void;
  /** Controlled store slice or reader (bypasses `useStore` when supplied). */
  store?: CompareRunsStoreState | (() => CompareRunsStoreState);
  /** Subscribe to external store updates when `store` is a reader. */
  subscribeStore?: (listener: () => void) => () => void;
  /** Controlled runner state or reader (bypasses `useRunner` when supplied). */
  runner?: CompareRunnerState | ((id: string) => CompareRunnerState);
  /** Subscribe to external runner updates when `runner` is a reader. */
  subscribeRunner?: (listener: () => void) => () => void;
  /** Load branch diff summary for a started run (defaults to `session-review.loadReview`). */
  loadReview?: (mid: string, cwd: string, target: ReviewTarget, signal: AbortSignal) => Promise<Pick<GitReview, "files">>;
  /** Retry a failed or stopped run (defaults to `compare-runner.retryRun`). */
  retryRun?: (groupId: string, runNumber: number, signal: AbortSignal) => void | Promise<unknown>;
  /** Navigate to another route (defaults to `router.navigate`). */
  navigate?: (to: Route) => void;
  /** Open a side panel (defaults to `panel.openPanel`). */
  openPanel?: (panel: Panel) => void;
  /** Format thrown errors for display (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Fixed timestamp or clock reader (ms) for relative group age (disables `useNow` interval when supplied). */
  now?: number | (() => number);
  /** Optional scope key that resets refresh, diff, and retry state and aborts in-flight work on change. */
  scopeKey?: string;
}

export interface CompareRunsProps {
  id: string;
  onToggleSidebar: () => void;
  deps?: CompareRunsDeps;
}

export interface CompareRunViewModel {
  run: CompareRun;
  status: RunStatus;
  awaiting?: boolean;
  finished?: boolean;
  changes?: CompareChangesState | null;
  retrying?: boolean;
  retryError?: string;
}

export interface CompareRunCardViewProps {
  run: CompareRun;
  status: RunStatus;
  reviews: boolean;
  awaiting?: boolean;
  finished?: boolean;
  changes?: CompareChangesState | null;
  changesSlot?: ReactNode;
  retrying?: boolean;
  retryError?: string;
  onOpenSession?: (session: Session) => void;
  onReviewSession?: (session: Session) => void;
  onRetryRun?: (run: CompareRun) => void;
  onOpenLaunch?: (run: CompareRun) => void;
}

export interface CompareRunsViewProps {
  group?: CompareGroup | null;
  computerName?: string;
  now?: number;
  reviews?: boolean;
  runs?: readonly CompareRunViewModel[];
  onToggleSidebar: () => void;
  onRefresh?: () => void;
  onGoAgents: () => void;
  onOpenSession?: (session: Session) => void;
  onReviewSession?: (session: Session) => void;
  onRetryRun?: (run: CompareRun) => void;
  onOpenLaunch?: (run: CompareRun) => void;
  /** Optional custom slot renderer for a started run's diff summary. */
  renderChanges?: (run: CompareRun, session: Session) => ReactNode;
}

interface ResolvedStore {
  machines: Machine[];
  sessions: Record<string, Session[]>;
  launches: Record<string, AgentLaunch>;
  mutes: AgentMutes;
  reviews: boolean;
}

const noopSubscribe = () => () => {};
const EMPTY_LAUNCHES: Record<string, AgentLaunch> = {};
const EMPTY_MUTES: AgentMutes = { spaces: [], sessions: [] };
const DEFAULT_RUNNER: CompareRunnerState = { active: false, holds: false };

function safeFormatError(fmt: ((error: unknown) => string) | undefined, error: unknown): string {
  if (!fmt) return errorText(error);
  try {
    return fmt(error);
  } catch {
    return errorText(error);
  }
}

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return value !== null && (typeof value === "object" || typeof value === "function") && typeof (value as PromiseLike<unknown>).then === "function";
}

function resolveStoreSnapshot(raw: CompareRunsStoreState): ResolvedStore {
  return {
    machines: raw.machines as Machine[],
    sessions: raw.sessions as Record<string, Session[]>,
    launches: (raw.launches ?? EMPTY_LAUNCHES) as Record<string, AgentLaunch>,
    mutes: raw.mutes ?? EMPTY_MUTES,
    reviews: raw.reviews !== undefined ? raw.reviews : Boolean(raw.features && hubHas(raw.features as string[], REVIEW_FEATURE)),
  };
}

/** Pure presentational view for a single run's branch diff summary line. */
export function CompareChangesView({ state }: { state: CompareChangesState }) {
  let text: string;
  if (state.kind === "loading") text = t("compare-runs.changes_reading");
  else if (state.kind === "error") text = t("compare-runs.changes_failed");
  else if (!state.files) text = t("compare-runs.no_changes");
  else text = t("compare-runs.changes", { count: state.files, added: state.added, deleted: state.deleted });
  return (
    <p data-run-changes className="flex min-w-0 items-center gap-1.5 text-label text-ink-2 tabular-nums" title={state.kind === "error" ? state.error : undefined}>
      <Icon name="fileDiff" size={12} stroke={2.2} className="shrink-0 text-ink-3" />
      <span className="truncate">{text}</span>
    </p>
  );
}

/** Pure presentational view for a single compare run card. */
export function CompareRunCardView({
  run,
  status,
  reviews,
  awaiting = false,
  finished = false,
  changes,
  changesSlot,
  retrying = false,
  retryError,
  onOpenSession,
  onReviewSession,
  onRetryRun,
  onOpenLaunch,
}: CompareRunCardViewProps) {
  const session = status.kind === "started" ? status.session : undefined;
  return (
    <section
      data-run={run.n}
      data-run-status={status.kind}
      aria-label={t("compare-runs.run", { n: run.n })}
      className="flex min-w-0 flex-col gap-2.5 rounded-card bg-surface p-4 shadow-card"
    >
      <div className="flex items-center gap-2">
        <span className="shrink-0 text-label font-semibold tracking-wide text-ink-3 uppercase">{t("compare-runs.run", { n: run.n })}</span>
        <span className="min-w-0 flex-1" />
        <StatusLine status={status} awaiting={awaiting} finished={finished} />
      </div>
      <p className="truncate text-card-title font-semibold text-ink" title={session ? sessionTitle(session) : undefined}>
        {session ? sessionTitle(session) : t("compare-runs.not_started_title")}
      </p>
      <p className="flex min-w-0 items-center gap-1.5 text-label text-ink-3">
        <Icon name="branch" size={12} stroke={2.2} className="shrink-0" />
        <span className="truncate font-mono" title={run.branch}>{run.branch}</span>
      </p>
      {session && reviews && (changesSlot ?? (changes ? <CompareChangesView state={changes} /> : null))}
      <Detail status={status} />
      {retryError && (
        <p role="alert" data-run-retry-error className="text-label leading-snug text-red [overflow-wrap:anywhere]">
          {retryError}
        </p>
      )}
      <Actions
        run={run}
        status={status}
        reviews={reviews}
        retrying={retrying}
        onOpenSession={onOpenSession}
        onReviewSession={onReviewSession}
        onRetryRun={onRetryRun}
        onOpenLaunch={onOpenLaunch}
      />
    </section>
  );
}

/** Pure presentational view for the Compare runs page. */
export function CompareRunsView({
  group,
  computerName,
  now,
  reviews = false,
  runs = [],
  onToggleSidebar,
  onRefresh,
  onGoAgents,
  onOpenSession,
  onReviewSession,
  onRetryRun,
  onOpenLaunch,
  renderChanges,
}: CompareRunsViewProps) {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("assistant-history.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <h1 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{t("compare-runs.title")}</h1>
        {group && onRefresh && <IconButton icon="retry" label={t("compare-runs.refresh")} onClick={onRefresh} />}
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[760px] px-4 py-5 sm:px-8 sm:py-6">
          {group ? (
            <>
              <p className="px-1 text-meta text-ink-2">
                <span className="font-medium text-ink">
                  {t("compare-runs.summary", { count: group.runs.length, computer: computerName ?? group.mid })}
                </span>
                {" · "}
                <span className="font-mono [overflow-wrap:anywhere]">{shortPath(group.cwd)}</span>
                {" · "}
                {ago(new Date(group.at).toISOString(), now)}
              </p>
              <p className="mt-1 mb-5 px-1 text-submeta leading-relaxed text-ink-3">{t("compare-runs.description")}</p>
              <div data-compare-runs className="grid gap-3 sm:grid-cols-2">
                {runs.map((item) => {
                  const session = item.status.kind === "started" ? item.status.session : undefined;
                  return (
                    <CompareRunCardView
                      key={item.run.n}
                      run={item.run}
                      status={item.status}
                      reviews={reviews}
                      awaiting={item.awaiting}
                      finished={item.finished}
                      changes={item.changes}
                      changesSlot={session && reviews && renderChanges ? renderChanges(item.run, session) : undefined}
                      retrying={item.retrying}
                      retryError={item.retryError}
                      onOpenSession={onOpenSession}
                      onReviewSession={onReviewSession}
                      onRetryRun={onRetryRun}
                      onOpenLaunch={onOpenLaunch}
                    />
                  );
                })}
              </div>
            </>
          ) : (
            <Empty
              icon="branch"
              title={t("compare-runs.not_found_title")}
              action={<Button tone="secondary" onClick={onGoAgents}>{t("compare-runs.go_agents")}</Button>}
            >
              {t("compare-runs.not_found_body")}
            </Empty>
          )}
        </div>
      </div>
    </div>
  );
}

export function CompareRuns({ id, onToggleSidebar, deps }: CompareRunsProps) {
  const depsRef = useRef(deps);
  useLayoutEffect(() => {
    depsRef.current = deps;
  });

  const controlledGroup = deps?.group !== undefined || deps?.subscribeGroup !== undefined;
  const subGroup = controlledGroup ? (deps?.subscribeGroup ?? noopSubscribe) : subscribeGroups;
  const groupDep = deps?.group;
  const groupCache = useRef<{ id: string; set: boolean; value: CompareGroup | undefined }>({ id, set: false, value: undefined });
  const getGroupSnapshot = useCallback(() => {
    const raw = groupDep !== undefined
      ? (typeof groupDep === "function" ? groupDep(id) : groupDep)
      : getGroup(id);
    const next = raw ?? undefined;
    if (groupCache.current.set && groupCache.current.id === id && sameValue(groupCache.current.value, next)) {
      return groupCache.current.value;
    }
    groupCache.current = { id, set: true, value: next };
    return next;
  }, [groupDep, id]);
  const getServerGroupSnapshot = useCallback(
    () => (groupDep === undefined ? undefined : getGroupSnapshot()),
    [groupDep, getGroupSnapshot],
  );
  const group = useSyncExternalStore(subGroup, getGroupSnapshot, getServerGroupSnapshot);

  // Scope tracking lives in the identity-stable outer component so bridge switches never drop resets.
  const pageScopeKey = JSON.stringify([deps?.scopeKey ?? null, id, group?.id ?? null]);
  // the header's refresh reads every run's changes again
  const [readingState, setReadingState] = useState<{ scope: string; count: number }>(() => ({ scope: pageScopeKey, count: 0 }));
  if (readingState.scope !== pageScopeKey) {
    setReadingState({ scope: pageScopeKey, count: 0 });
  }
  const reading = readingState.scope === pageScopeKey ? readingState.count : 0;

  const fixedNow = deps?.now;
  const tick = useNow(60_000, fixedNow === undefined);
  const now = fixedNow === undefined ? tick : typeof fixedNow === "function" ? fixedNow() : fixedNow;

  const onRefresh = () => setReadingState((cur) => ({ scope: pageScopeKey, count: (cur.scope === pageScopeKey ? cur.count : 0) + 1 }));
  const onGoAgents = () => (depsRef.current?.navigate ?? navigate)({ name: "agent-list" });

  if (!group) {
    return <CompareRunsView group={null} onToggleSidebar={onToggleSidebar} onGoAgents={onGoAgents} />;
  }

  const controlledStore = deps?.store !== undefined || deps?.subscribeStore !== undefined;
  const commonProps = {
    group,
    reading,
    now,
    onToggleSidebar,
    onRefresh,
    onGoAgents,
    deps,
    depsRef,
  };
  return controlledStore
    ? <ControlledStoreRunsBridge {...commonProps} deps={deps!} />
    : <DefaultStoreRunsBridge {...commonProps} />;
}

interface RunsBridgeBaseProps {
  group: CompareGroup;
  reading: number;
  now: number;
  onToggleSidebar: () => void;
  onRefresh: () => void;
  onGoAgents: () => void;
  deps?: CompareRunsDeps;
  depsRef: { current?: CompareRunsDeps };
}

function DefaultStoreRunsBridge(props: RunsBridgeBaseProps) {
  const machines = useStore((s) => s.machines);
  const sessions = useStore((s) => s.sessions);
  const launches = useStore((s) => s.launches);
  const mutes = useStore((s) => s.mutes);
  const reviews = useStore((s) => hubHas(s.features, REVIEW_FEATURE));
  const store: ResolvedStore = { machines, sessions, launches, mutes, reviews };
  return <RunnerRunsBridge {...props} store={store} />;
}

function ControlledStoreRunsBridge({ deps, ...props }: RunsBridgeBaseProps & { deps: CompareRunsDeps }) {
  const storeDep = deps.store;
  const subscribe = deps.subscribeStore ?? noopSubscribe;
  const cache = useRef<ResolvedStore | null>(null);
  const getSnapshot = useCallback((): ResolvedStore => {
    const raw: CompareRunsStoreState = storeDep !== undefined
      ? (typeof storeDep === "function" ? storeDep() : storeDep)
      : { machines: [], sessions: {} };
    const next = resolveStoreSnapshot(raw);
    if (cache.current && sameValue(cache.current, next)) return cache.current;
    cache.current = next;
    return next;
  }, [storeDep]);
  const store = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <RunnerRunsBridge {...props} deps={deps} store={store} />;
}

function RunnerRunsBridge(props: RunsBridgeBaseProps & { store: ResolvedStore }) {
  const controlledRunner = props.deps?.runner !== undefined || props.deps?.subscribeRunner !== undefined;
  return controlledRunner
    ? <ControlledRunnerRunsBridge {...props} deps={props.deps!} />
    : <DefaultRunnerRunsBridge {...props} />;
}

function DefaultRunnerRunsBridge(props: RunsBridgeBaseProps & { store: ResolvedStore }) {
  const runner = useRunner(props.group.id);
  return <RunsBody {...props} runner={runner} />;
}

function ControlledRunnerRunsBridge({ deps, ...props }: RunsBridgeBaseProps & { store: ResolvedStore; deps: CompareRunsDeps }) {
  const runnerDep = deps.runner;
  const subscribe = deps.subscribeRunner ?? noopSubscribe;
  const groupId = props.group.id;
  const cache = useRef<{ id: string; value: CompareRunnerState } | null>(null);
  const getSnapshot = useCallback((): CompareRunnerState => {
    const next = runnerDep !== undefined
      ? (typeof runnerDep === "function" ? runnerDep(groupId) : runnerDep)
      : DEFAULT_RUNNER;
    if (cache.current && cache.current.id === groupId && sameValue(cache.current.value, next)) {
      return cache.current.value;
    }
    cache.current = { id: groupId, value: next };
    return next;
  }, [runnerDep, groupId]);
  const runner = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <RunsBody {...props} deps={deps} runner={runner} />;
}

function RunsBody({
  group,
  reading,
  now,
  onToggleSidebar,
  onRefresh,
  onGoAgents,
  deps,
  depsRef,
  store,
  runner,
}: RunsBridgeBaseProps & { store: ResolvedStore; runner: CompareRunnerState }) {
  const runsScopeKey = JSON.stringify([deps?.scopeKey ?? null, group.id]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: runsScopeKey, epoch: 1 }));
  const [retryingRuns, setRetryingRuns] = useState<Record<number, { seq: number; identity: string }>>({});
  const [retryErrors, setRetryErrors] = useState<Record<number, { identity: string; message: string }>>({});
  if (scope.key !== runsScopeKey) {
    setScope({ key: runsScopeKey, epoch: scope.epoch + 1 });
    setRetryingRuns({});
    setRetryErrors({});
  }

  const activeEpochRef = useRef(0);
  const retrySeqRef = useRef(0);
  const pendingRetriesRef = useRef<Map<number, { epoch: number; seq: number; identity: string; controller: AbortController }>>(new Map());

  const ctx = { sessions: store.sessions, launches: store.launches, active: runner.active, holds: runner.holds };
  const evaluatedRuns = group.runs.map((run) => {
    const status = runStatus(run, group, ctx);
    const retryable = (status.kind === "failed" && Boolean(status.retry)) || (status.kind === "stopped" && status.retry);
    // Keep operation identity (run slot + branch within scope.epoch) separate from mutable progress phase/eligibility.
    const identity = run.branch;
    return { run, status, retryable, identity };
  });
  const runLifecycleKey = JSON.stringify(evaluatedRuns.map((entry) => [entry.run.n, entry.identity, entry.status.kind]));

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    return () => {
      activeEpochRef.current = 0;
      for (const entry of pendingRetriesRef.current.values()) entry.controller.abort();
      pendingRetriesRef.current.clear();
      retrySeqRef.current += 1;
    };
  }, [scope.epoch]);

  useLayoutEffect(() => {
    const activeRuns = new Map(evaluatedRuns.map((entry) => [entry.run.n, entry]));
    let prunedPending = false;
    for (const [n, pending] of pendingRetriesRef.current) {
      const current = activeRuns.get(n);
      // Only cancel an in-flight retry if the run was removed or replaced; normal lifecycle progress stays alive.
      if (!current || current.identity !== pending.identity) {
        pending.controller.abort();
        pendingRetriesRef.current.delete(n);
        prunedPending = true;
      }
    }
    if (prunedPending) {
      setRetryingRuns((prev) => {
        let changed = false;
        const next: Record<number, { seq: number; identity: string }> = {};
        for (const [k, entry] of Object.entries(prev)) {
          const n = Number(k);
          const pending = pendingRetriesRef.current.get(n);
          const current = activeRuns.get(n);
          if (pending && pending.seq === entry.seq && current?.identity === entry.identity) next[n] = entry;
          else changed = true;
        }
        return changed ? next : prev;
      });
    }
    setRetryErrors((prev) => {
      let changed = false;
      const next: Record<number, { identity: string; message: string }> = {};
      for (const [k, err] of Object.entries(prev)) {
        const n = Number(k);
        const current = activeRuns.get(n);
        const inFlight = pendingRetriesRef.current.has(n);
        const movedPastFailure = current && (
          current.status.kind === "started"
          || (!inFlight && (current.status.kind === "waiting" || current.status.kind === "worktree" || current.status.kind === "starting"))
        );
        if (current && current.identity === err.identity && !movedPastFailure) next[n] = err;
        else changed = true;
      }
      return changed ? next : prev;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope.epoch, runLifecycleKey]);

  const runModels: CompareRunViewModel[] = evaluatedRuns.map(({ run, status, retryable, identity }) => {
    const session = status.kind === "started" ? status.session : undefined;
    const err = status.kind !== "started" && retryErrors[run.n]?.identity === identity ? retryErrors[run.n].message : undefined;
    return {
      run,
      status,
      awaiting: session ? awaitsReply(session, store.mutes) : false,
      finished: session ? justFinished(session, store.mutes) : false,
      retrying: Boolean(retryable && retryingRuns[run.n]?.identity === identity),
      retryError: err,
    };
  });

  const handleOpenSession = (session: Session) => {
    (depsRef.current?.navigate ?? navigate)({ name: "session", mid: session.machine_id, sid: session.session_id });
  };
  const handleReviewSession = (session: Session) => {
    // the panel is the session's: it stays as the page arrives there (lib/panel-memory.ts)
    (depsRef.current?.openPanel ?? openPanel)({ kind: "review", mid: session.machine_id, sid: session.session_id, cwd: session.cwd, scope: "branch" });
    handleOpenSession(session);
  };
  const handleOpenLaunch = (run: CompareRun) => {
    if (!run.launch) return;
    (depsRef.current?.navigate ?? navigate)({ name: "session", mid: group.mid, sid: run.launch });
  };
  const handleRetryRun = async (run: CompareRun) => {
    const runEpoch = scope.epoch;
    const entry = evaluatedRuns.find((item) => item.run.n === run.n);
    if (!entry || !entry.retryable || activeEpochRef.current !== runEpoch || pendingRetriesRef.current.has(run.n)) return;
    const identity = entry.identity;
    const controller = new AbortController();
    const runSeq = ++retrySeqRef.current;
    pendingRetriesRef.current.set(run.n, { epoch: runEpoch, seq: runSeq, identity, controller });
    const isCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === runEpoch
      && pendingRetriesRef.current.get(run.n)?.seq === runSeq;

    setRetryErrors((prev) => {
      if (!(run.n in prev)) return prev;
      const next = { ...prev };
      delete next[run.n];
      return next;
    });

    let asyncStarted = false;
    try {
      const fn = depsRef.current?.retryRun;
      const result = fn ? fn(group.id, run.n, controller.signal) : retryRun(group.id, run.n);
      if (!isThenable(result)) return;
      if (!isCurrent()) {
        controller.abort();
        return;
      }
      asyncStarted = true;
      setRetryingRuns((prev) => ({ ...prev, [run.n]: { seq: runSeq, identity } }));
      await result;
      if (!isCurrent()) return;
    } catch (e) {
      if (!isCurrent()) return;
      setRetryErrors((prev) => ({ ...prev, [run.n]: { identity, message: safeFormatError(depsRef.current?.formatError, e) } }));
    } finally {
      if (pendingRetriesRef.current.get(run.n)?.seq === runSeq) pendingRetriesRef.current.delete(run.n);
      if (asyncStarted && activeEpochRef.current === runEpoch) {
        setRetryingRuns((prev) => {
          if (prev[run.n]?.seq !== runSeq) return prev;
          const next = { ...prev };
          delete next[run.n];
          return next;
        });
      }
    }
  };

  return (
    <CompareRunsView
      group={group}
      computerName={machineWithHub(group.mid, store.machines)}
      now={now}
      reviews={store.reviews}
      runs={runModels}
      onToggleSidebar={onToggleSidebar}
      onRefresh={onRefresh}
      onGoAgents={onGoAgents}
      onOpenSession={handleOpenSession}
      onReviewSession={handleReviewSession}
      onRetryRun={(run) => void handleRetryRun(run)}
      onOpenLaunch={handleOpenLaunch}
      renderChanges={(run, session) => (
        <Changes
          scopeKey={deps?.scopeKey}
          groupId={group.id}
          runNumber={run.n}
          sessionId={session.session_id}
          mid={group.mid}
          cwd={session.cwd}
          state={stateOf(session)}
          reading={reading}
          depsRef={depsRef}
        />
      )}
    />
  );
}

/** The run's state in the sidebar's mark and words once it is a session; before that, how far its start got. */
function StatusLine({ status, awaiting, finished }: { status: RunStatus; awaiting: boolean; finished: boolean }) {
  if (status.kind === "started") {
    const s = status.session;
    const state = stateOf(s);
    const done = !awaiting && finished;
    const bg = backgroundWaiting(s);
    return (
      <span className="flex shrink-0 items-center gap-1.5 text-label text-ink-2">
        <StatusMark state={state} awaiting={awaiting} finished={done} waiting={bg} label={false} />
        {awaiting ? stateName("awaits_reply") : done ? stateName("just_finished") : bg ? stateName("bg_waiting") : STATE_LABEL[state]}
      </span>
    );
  }
  const busy = status.kind === "worktree" || status.kind === "starting";
  return (
    <span className={cx("flex shrink-0 items-center gap-1.5 text-label", status.kind === "failed" ? "text-ink" : "text-ink-2")}>
      {busy && <Spinner size={11} />}
      {status.kind === "failed" && <Icon name="alertCircle" size={13} className="text-red" />}
      {phaseWords(status)}
    </span>
  );
}

function phaseWords(status: Exclude<RunStatus, { kind: "started" }>): string {
  switch (status.kind) {
    case "waiting":
      return t("compare-runs.waiting");
    case "worktree":
      return t("compare-runs.making_worktree");
    case "starting":
      return t("compare-runs.starting");
    case "elsewhere":
      return t("compare-runs.elsewhere");
    case "stopped":
      return t("compare-runs.stopped");
    case "failed":
      return stateName("failed");
  }
}

/** Why a run did not start, or what a stopped one needs. */
function Detail({ status }: { status: RunStatus }) {
  let text = "";
  if (status.kind === "failed") text = status.uncertain ? t("compare-runs.uncertain") : status.error;
  else if (status.kind === "stopped") text = status.retry ? t("compare-runs.stopped_retry") : t("compare-runs.stopped_gone");
  else if (status.kind === "elsewhere") text = t("compare-runs.elsewhere_hint");
  if (!text) return null;
  return <p role={status.kind === "failed" ? "alert" : undefined} className="text-label leading-snug text-ink-2 [overflow-wrap:anywhere]">{text}</p>;
}

function Actions({
  run,
  status,
  reviews,
  retrying,
  onOpenSession,
  onReviewSession,
  onRetryRun,
  onOpenLaunch,
}: {
  run: CompareRun;
  status: RunStatus;
  reviews: boolean;
  retrying: boolean;
  onOpenSession?: (session: Session) => void;
  onReviewSession?: (session: Session) => void;
  onRetryRun?: (run: CompareRun) => void;
  onOpenLaunch?: (run: CompareRun) => void;
}) {
  if (status.kind === "started") {
    const s = status.session;
    return (
      <div className="mt-auto flex flex-wrap gap-1.5 pt-1">
        <Button tone="secondary" icon="chat" onClick={() => onOpenSession?.(s)}>{t("compare-runs.open")}</Button>
        {reviews && (
          <Button tone="quiet" icon="fileDiff" onClick={() => onReviewSession?.(s)}>
            {t("compare-runs.review")}
          </Button>
        )}
      </div>
    );
  }
  const retry = (status.kind === "failed" && status.retry) || (status.kind === "stopped" && status.retry);
  // a start whose outcome is unknown is decided on its own page, where New agent's start shows it
  const launchPage = status.kind === "failed" && status.uncertain && run.launch;
  if (!retry && !launchPage) return null;
  return (
    <div className="mt-auto flex flex-wrap gap-1.5 pt-1">
      {retry && <Button tone="secondary" icon="retry" busy={retrying} onClick={() => onRetryRun?.(run)}>{t("compare-runs.retry")}</Button>}
      {launchPage && <Button tone="secondary" icon="chat" onClick={() => onOpenLaunch?.(run)}>{t("compare-runs.open")}</Button>}
    </div>
  );
}

/** What the run changed since its branch left the main one: its commits and what is not committed yet (the review's
 *  Branch scope). Read when the card shows, when the session's state moves (a turn ended) and on the header's refresh. */
function Changes({
  scopeKey,
  groupId,
  runNumber,
  sessionId,
  mid,
  cwd,
  state,
  reading,
  depsRef,
}: {
  scopeKey?: string;
  groupId: string;
  runNumber: number;
  sessionId: string;
  mid: string;
  cwd: string;
  state: string;
  reading: number;
  depsRef: { current?: CompareRunsDeps };
}) {
  const targetKey = JSON.stringify([scopeKey ?? null, groupId, runNumber, sessionId, mid, cwd]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: targetKey, epoch: 1 }));
  const [readState, setReadState] = useState<{ epoch: number; value: CompareChangesState }>(() => ({
    epoch: 1,
    value: { kind: "loading" },
  }));
  if (scope.key !== targetKey) {
    const nextEpoch = scope.epoch + 1;
    setScope({ key: targetKey, epoch: nextEpoch });
    setReadState({ epoch: nextEpoch, value: { kind: "loading" } });
  }

  const activeEpochRef = useRef(0);
  const reqSeqRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);
  const lastReadingRef = useRef(reading);

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    lastReadingRef.current = reading;
    return () => {
      activeEpochRef.current = 0;
      controllerRef.current?.abort();
      controllerRef.current = null;
      reqSeqRef.current += 1;
    };
  }, [scope.epoch]);

  useEffect(() => {
    const runEpoch = scope.epoch;
    const runSeq = ++reqSeqRef.current;
    const controller = new AbortController();
    controllerRef.current?.abort();
    controllerRef.current = controller;
    const isCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === runEpoch
      && reqSeqRef.current === runSeq;

    const refreshed = reading !== lastReadingRef.current;
    lastReadingRef.current = reading;
    if (refreshed) {
      setReadState({ epoch: runEpoch, value: { kind: "loading" } });
    } else {
      setReadState((cur) => (cur.epoch !== runEpoch || cur.value.kind === "error" ? { epoch: runEpoch, value: { kind: "loading" } } : cur));
    }

    let settled = false;
    const load = async () => {
      try {
        const fn = depsRef.current?.loadReview;
        const r = fn
          ? await fn(mid, cwd, { scope: "branch" }, controller.signal)
          : await loadReview(mid, cwd, { scope: "branch" }, controller.signal);
        if (!isCurrent()) return;
        settled = true;
        setReadState({ epoch: runEpoch, value: { kind: "ready", files: r.files.length, ...reviewTotals(r.files) } });
      } catch (e) {
        if (!isCurrent()) return;
        settled = true;
        setReadState({ epoch: runEpoch, value: { kind: "error", error: safeFormatError(depsRef.current?.formatError, e) } });
      } finally {
        if (controllerRef.current === controller) controllerRef.current = null;
        if (!settled && isCurrent()) {
          setReadState({ epoch: runEpoch, value: { kind: "error", error: t("compare-runs.changes_failed") } });
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (controllerRef.current === controller) controllerRef.current = null;
      if (reqSeqRef.current === runSeq) reqSeqRef.current += 1;
    };
  }, [scope.epoch, mid, cwd, state, reading, depsRef]);

  const shown: CompareChangesState = readState.epoch === scope.epoch ? readState.value : { kind: "loading" };
  return <CompareChangesView state={shown} />;
}
