// "Close…" (#7125): the dialog lib/close-agent.ts opens for every menu that closes an Agent: its row's, the chat's ⋯, and
// "Close pane" on its pane in the Agents tree, the Terminal page and the command palette. The dialog is its own chunk,
// loaded when one of those actions opens it, so it stays off both the first screen and the idle preload.
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { onOpenCloseAgent } from "../lib/close-agent";
import { machineWithHub } from "../lib/mesh";
import { sameValue } from "../lib/stable-lines";
import { useStore } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import type { CloseAgentDialogProps } from "./chat/CloseAgentDialog";
import { lazyDialog } from "./LazyOverlay";

const CloseAgentDialog = lazyDialog(() => import("./chat/CloseAgentDialog").then((m) => m.CloseAgentDialog));

export type CloseAgentDialogExtraProps = Partial<Omit<CloseAgentDialogProps, "open" | "session" | "machineName" | "onClose">>;

export interface CloseAgentHostRenderProps {
  open: true;
  session: Session;
  machineName: string;
  onClose: () => void;
  dialogKey: string;
  dialogProps?: CloseAgentDialogExtraProps;
}

export interface CloseAgentHostViewProps {
  session: Session | null;
  machineName: string;
  dialogKey?: string;
  onClose: () => void;
  dialogProps?: CloseAgentDialogExtraProps;
  renderDialog?: (props: CloseAgentHostRenderProps) => ReactNode;
}

export interface CloseAgentHostDeps {
  /** Optional scope partition (value or getter); switching it resets local state and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled active session or reader (`null` for closed; bypasses `onOpenCloseAgent` when supplied). */
  session?: Session | null | (() => Session | null | undefined);
  /** Subscribe to controlled session updates when `session` is a getter. */
  subscribeSession?: (listener: () => void) => () => void;
  /** Custom opener event source (replaces `onOpenCloseAgent` when `session` is omitted). */
  onOpenRequest?: (listener: (session: Session) => void) => () => void;
  /** Invoked when the active close dialog dismisses. */
  onClose?: (session: Session) => void;
  /** Controlled machine list or reader (bypasses `useStore` when supplied). */
  machines?: readonly Machine[] | (() => readonly Machine[]);
  /** Subscribe to machine list updates when `machines` is a getter. */
  subscribeMachines?: (listener: () => void) => () => void;
  /** Controlled machine display name or per-session reader (bypasses `useStore` and `machineWithHub`). */
  machineName?: string | ((session: Session) => string);
  /** Subscribe to machine name updates when `machineName` is a reader. */
  subscribeMachineName?: (listener: () => void) => () => void;
  /** Custom machine-name formatter (defaults to `mesh.machineWithHub`). */
  formatMachineName?: (machineId: string, machines: readonly Machine[]) => string;
  /** Extra controlled props forwarded to `CloseAgentDialog`. */
  dialogProps?: CloseAgentDialogExtraProps | ((session: Session) => CloseAgentDialogExtraProps);
  /** Custom dialog renderer (bypasses loading the lazy `CloseAgentDialog` chunk). */
  renderDialog?: (props: CloseAgentHostRenderProps) => ReactNode;
  /** Custom outer view renderer (defaults to `<CloseAgentHostView>`). */
  renderView?: (props: CloseAgentHostViewProps) => ReactNode;
}

export interface CloseAgentHostProps {
  deps?: CloseAgentHostDeps;
}

const noopSubscribe = () => () => {};
const EMPTY_MACHINES: readonly Machine[] = [];

function useTrackedScope(
  source: string | (() => string) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
) {
  const isGetter = typeof source === "function";
  const read = useCallback(() => (typeof source === "function" ? source() : (source ?? "")), [source]);
  const committedRef = useRef<{ value: string; version: number } | null>(null);
  const snapRef = useRef<{ value: string; version: number } | null>(null);
  const sub = useCallback((listener: () => void) => {
    if (!subscribe || !isGetter) return noopSubscribe();
    return subscribe(() => {
      const next = read();
      const cur = committedRef.current;
      if (!cur || next !== cur.value) {
        committedRef.current = { value: next, version: (cur?.version ?? 0) + 1 };
      }
      listener();
    });
  }, [subscribe, isGetter, read]);
  const getSnapshot = useCallback(() => {
    const next = read();
    const cur = committedRef.current;
    if (cur && next === cur.value) return cur;
    const nextVersion = cur ? cur.version + 1 : 0;
    if (snapRef.current && snapRef.current.value === next && snapRef.current.version === nextVersion) {
      return snapRef.current;
    }
    snapRef.current = { value: next, version: nextVersion };
    return snapRef.current;
  }, [read]);
  const snap = useSyncExternalStore(sub, getSnapshot, getSnapshot);
  useLayoutEffect(() => {
    committedRef.current = snap;
  }, [snap]);
  const readToken = useCallback(() => {
    const cur = read();
    const committed = committedRef.current;
    return committed && committed.value === cur ? `${committed.version}:${cur}` : `drift:${cur}`;
  }, [read]);
  return { value: snap.value, token: `${snap.version}:${snap.value}`, readToken };
}

function useCachedExternalValue<T>(
  source: T | (() => T) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
): T | undefined {
  const cache = useRef<{ has: boolean; value: T | undefined }>({ has: false, value: undefined });
  const getSnapshot = useCallback((): T | undefined => {
    const next = typeof source === "function" ? (source as () => T)() : source;
    if (cache.current.has && sameValue(cache.current.value, next)) {
      return cache.current.value;
    }
    cache.current = { has: true, value: next };
    return next;
  }, [source]);
  const sub = subscribe && typeof source === "function" ? subscribe : noopSubscribe;
  return useSyncExternalStore(sub, getSnapshot, getSnapshot);
}

function DefaultMachinesBridge({ render }: { render: (machines: readonly Machine[]) => ReactNode }) {
  const machines = useStore((st) => st.machines);
  return <>{render(machines)}</>;
}

/** Pure presentational host view for the Close Agent dialog. */
export function CloseAgentHostView({
  session,
  machineName,
  dialogKey,
  onClose,
  dialogProps,
  renderDialog,
}: CloseAgentHostViewProps) {
  if (!session) return null;
  const key = dialogKey ?? `${session.machine_id}/${session.session_id}`;
  if (renderDialog) {
    return <>{renderDialog({ open: true, session, machineName, onClose, dialogKey: key, dialogProps })}</>;
  }
  return (
    <CloseAgentDialog
      key={key}
      {...dialogProps}
      open
      session={session}
      machineName={machineName}
      onClose={onClose}
    />
  );
}

export function CloseAgentHost({ deps }: CloseAgentHostProps = {}) {
  const trackedScope = useTrackedScope(deps?.scopeKey, deps?.subscribeScopeKey);
  const hostScope = trackedScope.token;
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: hostScope, epoch: 1 }));
  const effectiveEpoch = scope.key === hostScope ? scope.epoch : scope.epoch + 1;
  if (scope.key !== hostScope) {
    setScope({ key: hostScope, epoch: effectiveEpoch });
  }

  const controlledSessionMode = deps?.session !== undefined;
  const customOpenRequest = deps?.onOpenRequest;
  const [sessionSource, setSessionSource] = useState<{
    epoch: number;
    controlled: boolean;
    opener: CloseAgentHostDeps["onOpenRequest"];
    gen: number;
  }>(() => ({
    epoch: effectiveEpoch,
    controlled: controlledSessionMode,
    opener: customOpenRequest,
    gen: 1,
  }));
  const sessionSourceChanged = sessionSource.epoch !== effectiveEpoch
    || sessionSource.controlled !== controlledSessionMode
    || (!controlledSessionMode && sessionSource.opener !== customOpenRequest);
  const sessionGen = sessionSourceChanged ? sessionSource.gen + 1 : sessionSource.gen;
  if (sessionSourceChanged) {
    setSessionSource({
      epoch: effectiveEpoch,
      controlled: controlledSessionMode,
      opener: customOpenRequest,
      gen: sessionGen,
    });
  }

  const [localSessionState, setLocalSessionState] = useState<{
    epoch: number;
    gen: number;
    session: Session | null;
  }>(() => ({ epoch: effectiveEpoch, gen: sessionGen, session: null }));

  const controlledSession = useCachedExternalValue(deps?.session, deps?.subscribeSession);
  const controlledMachines = useCachedExternalValue(deps?.machines, deps?.subscribeMachines);

  const session: Session | null = controlledSessionMode
    ? (controlledSession ?? null)
    : (localSessionState.epoch === effectiveEpoch && localSessionState.gen === sessionGen
        ? localSessionState.session
        : null);

  const machineNameDep = deps?.machineName;
  const readControlledMachineName = useCallback((): string | undefined => {
    if (machineNameDep === undefined) return undefined;
    if (typeof machineNameDep === "function") return session ? machineNameDep(session) : "";
    return machineNameDep;
  }, [machineNameDep, session]);
  const machineNameSub = typeof machineNameDep === "function" ? (deps?.subscribeMachineName ?? noopSubscribe) : noopSubscribe;
  const controlledMachineName = useSyncExternalStore(machineNameSub, readControlledMachineName, readControlledMachineName);

  const sessionIdentity = session ? `${session.machine_id}/${session.session_id}` : "";
  const activeEpochRef = useRef(effectiveEpoch);
  const activeGenRef = useRef(sessionGen);
  const activeSessionKeyRef = useRef(sessionIdentity);
  const depsRef = useRef(deps);
  const readScopeTokenRef = useRef(trackedScope.readToken);

  useLayoutEffect(() => {
    depsRef.current = deps;
    activeSessionKeyRef.current = sessionIdentity;
    readScopeTokenRef.current = trackedScope.readToken;
  });

  useLayoutEffect(() => {
    activeEpochRef.current = effectiveEpoch;
    activeGenRef.current = sessionGen;
    return () => {
      if (activeEpochRef.current === effectiveEpoch) activeEpochRef.current = 0;
      if (activeGenRef.current === sessionGen) activeGenRef.current = 0;
    };
  }, [effectiveEpoch, sessionGen]);

  useEffect(() => {
    if (controlledSessionMode) return;
    const runEpoch = effectiveEpoch;
    const runGen = sessionGen;
    const runScopeToken = trackedScope.token;
    let active = true;
    const listen = customOpenRequest ?? onOpenCloseAgent;
    const unsubscribe = listen((next) => {
      if (
        !active
        || activeEpochRef.current !== runEpoch
        || activeGenRef.current !== runGen
        || readScopeTokenRef.current() !== runScopeToken
      ) return;
      setLocalSessionState({ epoch: runEpoch, gen: runGen, session: next });
    });
    return () => {
      active = false;
      unsubscribe?.();
    };
  }, [controlledSessionMode, customOpenRequest, effectiveEpoch, sessionGen, trackedScope.token]);

  const capturedEpoch = effectiveEpoch;
  const capturedGen = sessionGen;
  const capturedScopeToken = trackedScope.token;
  const capturedSession = session;
  const capturedSessionKey = sessionIdentity;
  const handleClose = () => {
    if (
      !capturedSession
      || activeEpochRef.current !== capturedEpoch
      || activeGenRef.current !== capturedGen
      || activeSessionKeyRef.current !== capturedSessionKey
      || readScopeTokenRef.current() !== capturedScopeToken
    ) return;
    const curSessionDep = depsRef.current?.session;
    if (curSessionDep !== undefined) {
      const live = typeof curSessionDep === "function" ? curSessionDep() : curSessionDep;
      const liveKey = live ? `${live.machine_id}/${live.session_id}` : "";
      if (liveKey !== capturedSessionKey) return;
    }
    try {
      depsRef.current?.onClose?.(capturedSession);
    } finally {
      if (
        depsRef.current?.session === undefined
        && activeEpochRef.current === capturedEpoch
        && activeGenRef.current === capturedGen
        && activeSessionKeyRef.current === capturedSessionKey
      ) {
        setLocalSessionState({ epoch: capturedEpoch, gen: capturedGen, session: null });
      }
    }
  };

  const renderResolved = (machines: readonly Machine[]): ReactNode => {
    const machineName = session
      ? controlledMachineName !== undefined
        ? controlledMachineName
        : deps?.formatMachineName
          ? deps.formatMachineName(session.machine_id, machines)
          : machineWithHub(session.machine_id, machines as Machine[])
      : "";
    const dialogKey = session
      ? (deps?.scopeKey !== undefined ? `${effectiveEpoch}:${sessionIdentity}` : sessionIdentity)
      : undefined;
    const extraDialogProps = session && deps?.dialogProps
      ? (typeof deps.dialogProps === "function" ? deps.dialogProps(session) : deps.dialogProps)
      : undefined;
    const viewProps: CloseAgentHostViewProps = {
      session,
      machineName,
      dialogKey,
      onClose: handleClose,
      dialogProps: extraDialogProps,
      renderDialog: deps?.renderDialog,
    };
    if (deps?.renderView) return <>{deps.renderView(viewProps)}</>;
    return <CloseAgentHostView {...viewProps} />;
  };

  const needDefaultMachines = machineNameDep === undefined && deps?.machines === undefined;
  if (needDefaultMachines) {
    return <DefaultMachinesBridge render={renderResolved} />;
  }
  return renderResolved(controlledMachines ?? EMPTY_MACHINES);
}
