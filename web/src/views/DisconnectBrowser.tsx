// "Disconnect this browser" in Settings › General and the account menu (#1873, lib/disconnect.ts): it asks first, since
// getting back in takes pairing again (DisconnectConfirm.tsx); the confirm button is busy while the hub revokes the
// credential; a revoke that fails, or a credential the hub cannot revoke from here, asks before signing out instead of
// signing out as if it had worked.
import { Suspense, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { errorText, handleUnauthorized } from "../lib/api";
import { revokeThisBrowser, signOutHere, type Revoke } from "../lib/disconnect";
import { lazyPage } from "../lib/lazy-page";
import { toast } from "../ui/atoms";
import { t } from "../i18n";
import { OverlayBoundary } from "./LazyOverlay";
import type { DisconnectProblem } from "./DisconnectConfirm";

export type { DisconnectProblem } from "./DisconnectConfirm";

export interface DisconnectDialogViewProps {
  open: boolean;
  busy: boolean;
  problem: DisconnectProblem | null;
  onCancel: () => void;
  onConfirm: () => void;
  onProblemClose: () => void;
  onSignOut: () => void;
}

export interface DisconnectBrowserDialogProps extends DisconnectDialogViewProps {
  mounted?: boolean;
  onChunkError: () => void;
  renderDialog?: (props: DisconnectDialogViewProps) => ReactNode;
}

export interface DisconnectBrowserDeps {
  revoke?: () => Promise<Revoke>;
  signOut?: (unrevoked?: string) => void;
  handleUnauthorized?: (fn: () => void) => () => void;
  notify?: (message: string, tone: "ok" | "error" | "info") => void;
  scopeKey?: string;
  asking?: boolean;
  onAskingChange?: (asking: boolean) => void;
  busy?: boolean;
  onBusyChange?: (busy: boolean) => void;
  problem?: DisconnectProblem | null;
  onProblemChange?: (problem: DisconnectProblem | null) => void;
  renderDialog?: (props: DisconnectDialogViewProps) => ReactNode;
}

// Only a browser that is disconnecting needs the confirmation dialog.
const DisconnectConfirm = lazyPage(() => import("./DisconnectConfirm").then((m) => m.DisconnectConfirm));

export function DisconnectBrowserDialog({
  mounted = true, open, busy, problem, onCancel, onConfirm, onProblemClose, onSignOut, onChunkError, renderDialog,
}: DisconnectBrowserDialogProps) {
  const viewProps: DisconnectDialogViewProps = { open, busy, problem, onCancel, onConfirm, onProblemClose, onSignOut };
  if (renderDialog) return <>{renderDialog(viewProps)}</>;
  if (!mounted) return null;
  return (
    <OverlayBoundary open={open || problem !== null} onClose={onChunkError}>
      <Suspense fallback={null}>
        <DisconnectConfirm {...viewProps} />
      </Suspense>
    </OverlayBoundary>
  );
}

export function useDisconnect(deps?: DisconnectBrowserDeps): { start: () => void; busy: boolean; dialog: ReactNode } {
  const depsRef = useRef(deps);
  useLayoutEffect(() => {
    depsRef.current = deps;
  });

  const [localBusy, setLocalBusy] = useState(false);
  const [localProblem, setLocalProblem] = useState<DisconnectProblem | null>(null);
  // Keep the dialogs mounted after the first opening so they can animate closed and retry their chunk.
  const [asked, setAsked] = useState(false);
  const [localAsking, setLocalAsking] = useState(false);
  const opSeq = useRef(0);
  const inFlight = useRef<{ seq: number; restoreUnauthorized: () => void } | null>(null);
  const abortedInFlight = useRef(false);
  const scopeKey = deps?.scopeKey;
  const lastScopeKey = useRef(scopeKey);

  const busy = deps?.busy !== undefined ? deps.busy : localBusy;
  const problem = deps?.problem !== undefined ? deps.problem : localProblem;
  const asking = deps?.asking !== undefined ? deps.asking : localAsking;
  if ((asking || problem !== null) && !asked) setAsked(true);

  const setAskingState = (next: boolean) => {
    if (depsRef.current?.asking === undefined) setLocalAsking(next);
    depsRef.current?.onAskingChange?.(next);
  };
  const setBusyState = (next: boolean) => {
    if (depsRef.current?.busy === undefined) setLocalBusy(next);
    depsRef.current?.onBusyChange?.(next);
  };
  const setProblemState = (next: DisconnectProblem | null) => {
    if (depsRef.current?.problem === undefined) setLocalProblem(next);
    depsRef.current?.onProblemChange?.(next);
  };

  useLayoutEffect(() => {
    if (lastScopeKey.current !== scopeKey) {
      lastScopeKey.current = scopeKey;
      setAskingState(false);
      if (depsRef.current?.busy === undefined) setLocalBusy(false);
      else if (abortedInFlight.current || depsRef.current.busy) depsRef.current.onBusyChange?.(false);
      setProblemState(null);
      abortedInFlight.current = false;
    }
    return () => {
      const active = inFlight.current;
      if (active) {
        inFlight.current = null;
        abortedInFlight.current = true;
        active.restoreUnauthorized();
      }
      opSeq.current += 1;
    };
  }, [scopeKey]);

  const ask = () => {
    if (busy || inFlight.current !== null) return;
    setAsked(true);
    setAskingState(true);
  };
  const cancel = () => {
    if (busy || inFlight.current !== null) return;
    setAskingState(false);
  };
  const revoke = async () => {
    if (busy || inFlight.current !== null) return;
    const seq = ++opSeq.current;
    const setUnauthorized = depsRef.current?.handleUnauthorized ?? handleUnauthorized;
    const revokeFn = depsRef.current?.revoke ?? revokeThisBrowser;
    // signing out on purpose: a request that gets a 401 meanwhile must not show "this browser was signed out"
    const before = setUnauthorized(() => {});
    inFlight.current = { seq, restoreUnauthorized: () => setUnauthorized(before) };
    setBusyState(true);
    let r: Revoke;
    try {
      r = await revokeFn();
    } catch (failure) {
      r = { ok: false, error: errorText(failure), id: "" };
    }
    if (inFlight.current?.seq !== seq) return;
    inFlight.current = null;
    if (r.ok) {
      (depsRef.current?.signOut ?? signOutHere)();
      return;
    }
    setUnauthorized(before); // still signed in here until the user says otherwise
    setBusyState(false);
    setAskingState(false);
    setProblemState(r);
  };
  const close = () => {
    if (!busy && inFlight.current === null) setProblemState(null);
  };
  const signOut = () => {
    if (busy || inFlight.current !== null) return;
    (depsRef.current?.handleUnauthorized ?? handleUnauthorized)(() => {});
    (depsRef.current?.signOut ?? signOutHere)(problem?.id ?? "");
  };
  const onChunkError = () => {
    setAskingState(false);
    setProblemState(null);
    if (depsRef.current?.notify) depsRef.current.notify(t("pageboundary.chunk_body"), "error");
    else toast(t("pageboundary.chunk_body"), "error");
  };

  const mounted = asked || asking || problem !== null;
  // Both dialogs share the same chunk and stay mounted after first opening so they can animate closed.
  const dialog = (deps?.renderDialog || mounted) && (
    <DisconnectBrowserDialog mounted={mounted} open={asking} busy={busy} problem={problem} onCancel={cancel}
      onConfirm={() => void revoke()} onProblemClose={close} onSignOut={signOut} onChunkError={onChunkError}
      renderDialog={deps?.renderDialog} />
  );
  return { start: ask, busy, dialog };
}
