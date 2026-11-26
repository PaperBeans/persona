import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { errorText } from "../lib/api";
import { shortPath } from "../lib/format";
import { deliverLaunch, type AgentLaunch } from "../lib/new-agent";
import { namedLauncher, providerIdNote, providerLabel, providerTitle, useProviderNames, type Named } from "../lib/provider-names";
import { navigate, type Route } from "../lib/router";
import { useStore, putLaunch } from "../lib/store";
import { releaseAttachments, type LocalAttachment } from "../lib/attachments";
import { AttachmentTray } from "../ui/AttachmentTray";
import { Button, Loading } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { t } from "../i18n";

// the question the start's terminal tab stopped on, answered from here (#10365): only for such a failure
const ShellQuestion = lazy(() => import("./ShellQuestionCard"));

export interface StartingAgentDeps {
  /** Optional scope partition (value or getter); switching it resets controlled/uncontrolled edit and retry state and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled machine display name or reader (bypasses `useStore` when supplied). */
  machineName?: string | ((machineId: string) => string | undefined);
  /** Subscribe to machine name changes when `machineName` is a getter. */
  subscribeMachine?: (listener: () => void) => () => void;
  /** Controlled provider names list or getter (bypasses `useProviderNames` and `/model-config`). */
  providerNames?: readonly Named[] | (() => readonly Named[]);
  /** Subscribe to provider names updates when `providerNames` is a getter. */
  subscribeProviderNames?: (listener: () => void) => () => void;
  /** Read the latest launch snapshot right before retry or attachment edits. */
  getLaunch?: (id: string) => AgentLaunch | undefined;
  /** Persist an updated launch before retrying or removing an attachment (defaults to `store.putLaunch`). */
  putLaunch?: (launch: AgentLaunch) => void;
  /** Deliver or retry a launch (defaults to `new-agent.deliverLaunch`). */
  deliverLaunch?: (id: string, clearDraft?: string) => void | Promise<unknown>;
  /** Release preview URLs for removed attachments (defaults to `attachments.releaseAttachments`). */
  releaseAttachments?: (attachments: LocalAttachment[]) => void;
  /** Navigate to the started session or list (defaults to `router.navigate`). */
  navigate?: (route: Route, replace?: boolean) => void;
  /** Confirm retrying an uncertain launch (defaults to `window.confirm`). */
  confirm?: (message: string) => boolean | Promise<boolean>;
  /** Format persistence or retry errors for display (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Custom shell-question slot when `launch.shellWaiting` is set (bypasses lazy `ShellQuestionCard`). */
  renderShellQuestion?: (launch: AgentLaunch) => ReactNode;
  /** Optional controlled retry draft fields and busy state. */
  text?: string;
  onTextChange?: (text: string) => void;
  launcher?: string;
  onLauncherChange?: (launcher: string) => void;
  retrying?: boolean;
  onRetryingChange?: (retrying: boolean) => void;
}

export interface StartingAgentProps {
  launch: AgentLaunch;
  onToggleSidebar?: () => void;
  deps?: StartingAgentDeps;
}

export interface StartingAgentViewProps {
  launch: AgentLaunch;
  subtitleText: string;
  subtitleTooltip?: string;
  editable: boolean;
  text: string;
  launcher: string;
  loadingLabel: string;
  retrying?: boolean;
  retryError?: string;
  shellQuestion?: ReactNode;
  onToggleSidebar?: () => void;
  onTextChange: (text: string) => void;
  onLauncherChange: (launcher: string) => void;
  onRemoveAttachment?: (id: string) => void;
  onRetry: () => void;
  onViewSession: () => void;
  onViewList: () => void;
}

const noopSubscribe = () => () => {};

function sameNamedList(a: readonly Named[], b: readonly Named[]): boolean {
  if (Object.is(a, b)) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].name !== b[i].name || a[i].display_name !== b[i].display_name) return false;
  }
  return true;
}

function useCachedProviderNames(
  subscribe: (listener: () => void) => () => void,
  read: () => readonly Named[],
): readonly Named[] {
  const cacheRef = useRef<readonly Named[] | null>(null);
  const getSnapshot = useCallback(() => {
    const next = read();
    if (cacheRef.current !== null && sameNamedList(cacheRef.current, next)) return cacheRef.current;
    cacheRef.current = next;
    return next;
  }, [read]);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

function useTrackedString(subscribe: (listener: () => void) => () => void, read: () => string) {
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
  return { token: `${snap.version}:${snap.value}`, readToken };
}

/** Operation identity bound by an uncertain retry confirmation (restart vs resend, exact target session, clearable draft, lock flags). */
function retryOperationIdentity(l: AgentLaunch): string {
  return JSON.stringify([
    l.id,
    l.machineId,
    l.session?.session_id ?? "",
    l.clearable ?? null,
    Boolean(l.uncertain),
    Boolean(l.repeatOnly),
  ]);
}

/** Pure presentational view for the agent startup / failed-launch recovery screen. */
export function StartingAgentView({
  launch,
  subtitleText,
  subtitleTooltip,
  editable,
  text,
  launcher,
  loadingLabel,
  retrying = false,
  retryError,
  shellQuestion,
  onToggleSidebar,
  onTextChange,
  onLauncherChange,
  onRemoveAttachment,
  onRetry,
  onViewSession,
  onViewList,
}: StartingAgentViewProps) {
  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4">
        {onToggleSidebar && <button type="button" aria-label={t("startingagent.sidebar")} onClick={onToggleSidebar} className="flex size-7 items-center justify-center rounded-control text-ink-3 hover:bg-hover lg:hidden"><Icon name="sidebar" /></button>}
        <div className="min-w-0 flex-1"><h1 className="truncate text-[14px] font-semibold text-ink">{t("startingagent.title")}</h1><p className="truncate text-[11.5px] text-ink-3" title={subtitleTooltip}>{subtitleText}</p></div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[760px] flex-col gap-7 px-5 pt-6 pb-10 sm:px-8">
          {(launch.text || launch.attachments.length > 0) && <div className="flex flex-col items-end gap-1.5 pl-[12%]">
            <AttachmentTray attachments={launch.attachments} onRemove={editable ? onRemoveAttachment : undefined} />
            {launch.text && <div className="max-w-full rounded-[14px] bg-user-bubble px-3.5 py-2 text-[14px] leading-[1.55] whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{launch.text}</div>}
          </div>}
          {launch.phase === "failed" ? <div role="alert" className="flex flex-col items-start gap-3">
            <p className="text-[13px] text-red">{launch.error}</p>
            {retryError && <p role="status" className="text-meta text-red">{retryError}</p>}
            {shellQuestion}
            <p className="text-[12px] text-ink-3">{launch.uncertain ? t("startingagent.uncertain") : t("startingagent.kept")}</p>
            {editable && <div className="flex w-full flex-col gap-2">
              <label className="text-[12px] text-ink-2">{t("startingagent.launcher_label")}<input value={launcher} onChange={(e) => onLauncherChange(e.target.value)} className="mt-1 w-full rounded-control bg-inset px-3 py-2 font-mono text-[12px] text-ink shadow-hairline outline-none" /></label>
              <label className="text-[12px] text-ink-2">{t("startingagent.first_message")}<textarea value={text} onChange={(e) => onTextChange(e.target.value)} rows={3} className="mt-1 w-full resize-none rounded-control bg-inset px-3 py-2 text-[13px] text-ink shadow-hairline outline-none" /></label>
            </div>}
            {launch.draft && <pre className="max-h-40 max-w-full overflow-auto rounded-control bg-inset px-3 py-2 text-[12px] whitespace-pre-wrap text-ink-2">{launch.draft}</pre>}
            <div className="flex flex-wrap gap-2">
              <Button icon="retry" disabled={retrying || undefined} onClick={onRetry}>{launch.clearable !== undefined ? t("startingagent.clear_and_send") : t("startingagent.retry")}</Button>
              {launch.session && <Button tone="quiet" onClick={onViewSession}>{t("startingagent.view_session")}</Button>}
              {!launch.session && <Button tone="quiet" onClick={onViewList}>{t("startingagent.view_list")}</Button>}
            </div>
          </div> : <div className="flex flex-col gap-2"><Loading label={loadingLabel} /><p className="text-[12px] text-ink-3">{t("startingagent.background")}</p></div>}
        </div>
      </div>
    </div>
  );
}

/** Stable outer component owning scope identity tracking, controlled/uncontrolled resets, and retry fencing across bridge mode switches. */
export function StartingAgent({ launch, onToggleSidebar, deps }: StartingAgentProps) {
  const scopeDep = deps?.scopeKey;
  const readScope = useCallback(() => (typeof scopeDep === "function" ? scopeDep() : (scopeDep ?? "")), [scopeDep]);
  const scopeSubscribe = deps?.subscribeScopeKey && typeof scopeDep === "function" ? deps.subscribeScopeKey : noopSubscribe;
  const trackedScope = useTrackedString(scopeSubscribe, readScope);

  const scopeIdentity = JSON.stringify([trackedScope.token, launch.id, launch.machineId]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: scopeIdentity, epoch: 1 }));
  const [localText, setLocalText] = useState(launch.text);
  const [localLauncher, setLocalLauncher] = useState(launch.launcher);
  const [localRetrying, setLocalRetrying] = useState(false);
  const [retryError, setRetryError] = useState("");

  if (scope.key !== scopeIdentity) {
    setScope({ key: scopeIdentity, epoch: scope.epoch + 1 });
    setLocalText(launch.text);
    setLocalLauncher(launch.launcher);
    setLocalRetrying(false);
    setRetryError("");
  }

  const text = deps?.text !== undefined ? deps.text : localText;
  const launcher = deps?.launcher !== undefined ? deps.launcher : localLauncher;
  const retrying = deps?.retrying !== undefined ? deps.retrying : localRetrying;

  const depsRef = useRef(deps);
  const launchRef = useRef(launch);
  const textRef = useRef(text);
  const launcherRef = useRef(launcher);
  const readScopeTokenRef = useRef(trackedScope.readToken);
  const activeEpochRef = useRef(scope.epoch);
  const lastResetEpoch = useRef(scope.epoch);
  const retrySeqRef = useRef(0);
  const pendingRetryRef = useRef<number | null>(null);

  useLayoutEffect(() => {
    depsRef.current = deps;
    launchRef.current = launch;
    textRef.current = text;
    launcherRef.current = launcher;
    readScopeTokenRef.current = trackedScope.readToken;
  });

  const setTextState = (next: string) => {
    textRef.current = next;
    setLocalText(next);
    depsRef.current?.onTextChange?.(next);
  };
  const setLauncherState = (next: string) => {
    launcherRef.current = next;
    setLocalLauncher(next);
    depsRef.current?.onLauncherChange?.(next);
  };
  const setRetryingState = (next: boolean) => {
    setLocalRetrying(next);
    depsRef.current?.onRetryingChange?.(next);
  };

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    if (lastResetEpoch.current !== scope.epoch) {
      lastResetEpoch.current = scope.epoch;
      pendingRetryRef.current = null;
      retrySeqRef.current += 1;
      setTextState(launchRef.current.text);
      setLauncherState(launchRef.current.launcher);
      setRetryingState(false);
      setRetryError("");
    }
    return () => {
      if (activeEpochRef.current === scope.epoch) activeEpochRef.current = 0;
      pendingRetryRef.current = null;
      retrySeqRef.current += 1;
    };
  }, [scope.epoch]);

  useEffect(() => {
    if (
      launch.phase === "ready" && launch.session
      && activeEpochRef.current === scope.epoch && readScopeTokenRef.current() === trackedScope.token
    ) {
      (depsRef.current?.navigate ?? navigate)({ name: "session", mid: launch.machineId, sid: launch.session.session_id }, true);
    }
  }, [launch, scope.epoch, trackedScope.token]);

  const isScopeCurrent = (capturedEpoch: number, capturedScope: string, id: string, machineId: string) =>
    activeEpochRef.current === capturedEpoch
    && readScopeTokenRef.current() === capturedScope
    && launchRef.current.id === id
    && launchRef.current.machineId === machineId;

  const liveLaunchEligible = (expected: AgentLaunch): AgentLaunch | null => {
    const latest = depsRef.current?.getLaunch ? depsRef.current.getLaunch(expected.id) : launchRef.current;
    if (!latest || latest.id !== expected.id || latest.machineId !== expected.machineId || latest.phase !== "failed") return null;
    return latest;
  };

  const editable = launch.phase === "failed" && !launch.session && !launch.uncertain && !launch.repeatOnly;

  const retry = () => {
    const capturedEpoch = scope.epoch;
    const capturedScope = trackedScope.token;
    if (retrying || pendingRetryRef.current !== null || !isScopeCurrent(capturedEpoch, capturedScope, launch.id, launch.machineId)) return;
    const currentLaunch = liveLaunchEligible(launch);
    if (!currentLaunch) return;
    const expectedOp = retryOperationIdentity(currentLaunch);

    const finish = (seq: number) => {
      if (pendingRetryRef.current === seq) {
        pendingRetryRef.current = null;
        if (activeEpochRef.current === capturedEpoch) setRetryingState(false);
      }
    };

    const stillAdmitted = (seq: number): AgentLaunch | null => {
      if (
        pendingRetryRef.current !== seq
        || retrySeqRef.current !== seq
        || !isScopeCurrent(capturedEpoch, capturedScope, currentLaunch.id, currentLaunch.machineId)
      ) return null;
      const live = liveLaunchEligible(currentLaunch);
      if (!live || retryOperationIdentity(live) !== expectedOp) return null;
      return live;
    };

    const dispatch = (seq: number) => {
      const live = stillAdmitted(seq);
      if (!live) {
        finish(seq);
        return;
      }
      const liveEditable = live.phase === "failed" && !live.session && !live.uncertain && !live.repeatOnly;
      const currentLauncher = launcherRef.current;
      const currentText = textRef.current;
      // the model and effort were picked for the launcher it had: another launcher starts with its own
      const other = currentLauncher.trim() !== live.launcher;
      const retireTextStart = live.agentType === "traex" && live.inlinePrompt && (other || !currentText.trim());
      if (liveEditable) {
        try {
          (depsRef.current?.putLaunch ?? putLaunch)({
            ...live,
            text: currentText.trim(),
            launcher: currentLauncher.trim(),
            outgoing: undefined,
            ...(other && { model: undefined, effort: undefined, displayLauncher: undefined, displayEngine: undefined, displayModel: undefined }),
            ...(retireTextStart && { agentType: undefined, inlinePrompt: undefined }),
          });
        } catch (error) {
          if (pendingRetryRef.current === seq && isScopeCurrent(capturedEpoch, capturedScope, currentLaunch.id, currentLaunch.machineId)) {
            setRetryError((depsRef.current?.formatError ?? errorText)(error));
          }
          finish(seq);
          return;
        }
      }
      // Re-verify scope, epoch, retry sequence, and live launch immediately before delivery in case putLaunch switched scope synchronously.
      const postSaveLive = stillAdmitted(seq);
      if (!postSaveLive) {
        finish(seq);
        return;
      }
      let delivery: void | Promise<unknown>;
      try {
        delivery = (depsRef.current?.deliverLaunch ?? deliverLaunch)(postSaveLive.id, postSaveLive.clearable);
      } catch (error) {
        if (pendingRetryRef.current === seq && isScopeCurrent(capturedEpoch, capturedScope, currentLaunch.id, currentLaunch.machineId)) {
          setRetryError((depsRef.current?.formatError ?? errorText)(error));
        }
        finish(seq);
        return;
      }
      if (delivery && typeof (delivery as PromiseLike<unknown>).then === "function") {
        void Promise.resolve(delivery).then(
          () => finish(seq),
          (error) => {
            if (pendingRetryRef.current === seq && isScopeCurrent(capturedEpoch, capturedScope, currentLaunch.id, currentLaunch.machineId)) {
              setRetryError((depsRef.current?.formatError ?? errorText)(error));
            }
            finish(seq);
          },
        );
      } else {
        finish(seq);
      }
    };

    const seq = ++retrySeqRef.current;
    pendingRetryRef.current = seq;
    setRetryError("");

    if (currentLaunch.uncertain) {
      const message = currentLaunch.session ? t("startingagent.resend_confirm") : t("startingagent.restart_confirm");
      const ask = depsRef.current?.confirm ?? ((msg: string) => confirm(msg));
      let answer: boolean | Promise<boolean>;
      try {
        answer = ask(message);
      } catch {
        finish(seq);
        return;
      }
      if (answer && typeof (answer as PromiseLike<boolean>).then === "function") {
        setRetryingState(true);
        void Promise.resolve(answer).then(
          (approved) => {
            if (!approved) {
              finish(seq);
              return;
            }
            dispatch(seq);
          },
          () => finish(seq),
        );
        return;
      }
      if (!answer) {
        finish(seq);
        return;
      }
    }

    setRetryingState(true);
    dispatch(seq);
  };

  const removeAttachment = (id: string) => {
    if (retrying || pendingRetryRef.current !== null || !isScopeCurrent(scope.epoch, trackedScope.token, launch.id, launch.machineId)) return;
    const live = liveLaunchEligible(launch);
    if (!live || live.session || live.uncertain || live.repeatOnly) return;
    try {
      (depsRef.current?.releaseAttachments ?? releaseAttachments)(live.attachments.filter((a) => a.id === id));
      (depsRef.current?.putLaunch ?? putLaunch)({ ...live, attachments: live.attachments.filter((a) => a.id !== id), outgoing: undefined });
    } catch (error) {
      if (isScopeCurrent(scope.epoch, trackedScope.token, launch.id, launch.machineId)) {
        setRetryError((depsRef.current?.formatError ?? errorText)(error));
      }
    }
  };

  const viewSession = () => {
    if (!isScopeCurrent(scope.epoch, trackedScope.token, launch.id, launch.machineId)) return;
    const live = depsRef.current?.getLaunch ? depsRef.current.getLaunch(launch.id) : launchRef.current;
    if (!live || live.id !== launch.id || live.machineId !== launch.machineId || !live.session) return;
    (depsRef.current?.navigate ?? navigate)({ name: "session", mid: live.machineId, sid: live.session.session_id });
  };

  const viewList = () => {
    if (!isScopeCurrent(scope.epoch, trackedScope.token, launch.id, launch.machineId)) return;
    (depsRef.current?.navigate ?? navigate)({ name: "home" });
  };

  const label = launch.phase === "creating" ? t("startingagent.creating") : launch.phase === "uploading" ? t("startingagent.uploading") : t("startingagent.sending");
  const shellQuestion = launch.phase === "failed" && launch.shellWaiting
    ? (deps?.renderShellQuestion ? deps.renderShellQuestion(launch) : <Suspense fallback={null}><ShellQuestion launch={launch} /></Suspense>)
    : undefined;

  return (
    <MachineNameLeaf
      deps={deps}
      launch={launch}
      editable={editable}
      text={text}
      launcher={launcher}
      loadingLabel={label}
      retrying={retrying}
      retryError={retryError || undefined}
      shellQuestion={shellQuestion}
      onToggleSidebar={onToggleSidebar}
      onTextChange={setTextState}
      onLauncherChange={setLauncherState}
      onRemoveAttachment={editable ? removeAttachment : undefined}
      onRetry={retry}
      onViewSession={viewSession}
      onViewList={viewList}
    />
  );
}

type SubtitleLeafProps = Omit<StartingAgentViewProps, "subtitleText" | "subtitleTooltip"> & {
  deps?: StartingAgentDeps;
};

function MachineNameLeaf(props: SubtitleLeafProps) {
  if (props.deps?.machineName !== undefined) {
    return <ControlledMachineNameLeaf {...props} deps={props.deps} />;
  }
  return <DefaultMachineNameLeaf {...props} />;
}

function ControlledMachineNameLeaf(props: SubtitleLeafProps & { deps: StartingAgentDeps }) {
  const { deps, launch } = props;
  const machineDep = deps.machineName!;
  const readMachine = useCallback(
    () => (typeof machineDep === "function" ? machineDep(launch.machineId) : machineDep),
    [machineDep, launch.machineId],
  );
  const subscribe = deps.subscribeMachine && typeof machineDep === "function" ? deps.subscribeMachine : noopSubscribe;
  const machineName = useSyncExternalStore(subscribe, readMachine, readMachine);
  return <ProviderNamesLeaf {...props} machineName={machineName} />;
}

function DefaultMachineNameLeaf(props: SubtitleLeafProps) {
  const machineName = useStore((s) => s.machines.find((m) => m.id === props.launch.machineId)?.name);
  return <ProviderNamesLeaf {...props} machineName={machineName} />;
}

function ProviderNamesLeaf(props: SubtitleLeafProps & { machineName?: string }) {
  if (props.deps?.providerNames !== undefined) {
    return <ControlledProviderNamesLeaf {...props} deps={props.deps} />;
  }
  return <DefaultProviderNamesLeaf {...props} />;
}

function ControlledProviderNamesLeaf(props: SubtitleLeafProps & { deps: StartingAgentDeps; machineName?: string }) {
  const { deps } = props;
  const namesDep = deps.providerNames!;
  const readNames = useCallback(
    () => (typeof namesDep === "function" ? namesDep() : namesDep),
    [namesDep],
  );
  const subscribe = deps.subscribeProviderNames && typeof namesDep === "function" ? deps.subscribeProviderNames : noopSubscribe;
  const providerNames = useCachedProviderNames(subscribe, readNames);
  return <RenderStartingAgentView {...props} providerNames={providerNames} />;
}

function DefaultProviderNamesLeaf(props: SubtitleLeafProps & { machineName?: string }) {
  const providerNames = useProviderNames();
  return <RenderStartingAgentView {...props} providerNames={providerNames} />;
}

function RenderStartingAgentView({
  deps: _deps,
  machineName,
  providerNames,
  ...viewProps
}: SubtitleLeafProps & { machineName?: string; providerNames: readonly Named[] }) {
  const { launch } = viewProps;
  // the launcher by the provider's display name; the line's tooltip says it with its startup ID (#5029)
  const shownLauncher = launch.launcher || launch.displayLauncher || "";
  const shownModel = launch.model || launch.displayModel;
  const runsOn = namedLauncher(shownLauncher, providerNames);
  const subtitle = (name: string) => [machineName ?? launch.machineId, shortPath(launch.cwd), name, shownModel].filter(Boolean).join(" · ");
  return (
    <StartingAgentView
      {...viewProps}
      subtitleText={subtitle(providerTitle(runsOn))}
      subtitleTooltip={providerIdNote(runsOn) ? subtitle(providerLabel(runsOn)) : undefined}
    />
  );
}
