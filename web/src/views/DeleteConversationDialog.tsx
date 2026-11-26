import { deleteConversation } from "../lib/conversation-delete-action";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { errorText } from "../lib/api";
import { isBackgroundSupervisor } from "../lib/background-supervisor";
import { conversationDeleteAllowed, CONVERSATION_DELETE_FEATURE } from "../lib/conversation-delete";
import { getSession, getState, useStore } from "../lib/store";
import { sessionTitle } from "../lib/format";
import type { Session } from "../lib/types";
import { Button, Dialog } from "../ui/atoms";

export interface DeleteConversationDialogDeps {
  /** Read the latest session snapshot (defaults to `store.getSession`). */
  getSession?: (machineId: string, sessionId: string) => Session | undefined;
  /** Subscribe to deletion/session changes (bypasses `useStore` when supplied). */
  subscribe?: (listener: () => void) => () => void;
  /** Explicit controlled deletion state (bypasses `useStore` and `subscribe` when supplied). */
  isDeleted?: boolean;
  /** Determine whether deletion is allowed for `current` (defaults to `store.getState()` + `conversationDeleteAllowed`). */
  canDelete?: (current: Session) => boolean;
  /** Perform the deletion request (defaults to `lib/conversation-delete-action.ts`'s `deleteConversation`). */
  deleteConversation?: (current: Session) => Promise<void>;
  /** Format thrown errors for display (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
}

export interface DeleteConversationDialogProps {
  s: Session;
  onClose: () => void;
  deps?: DeleteConversationDialogDeps;
}

export interface DeleteConversationDialogViewProps {
  s: Session;
  busy: boolean;
  error: string;
  onClose: () => void;
  onConfirm: () => void;
}

function defaultCanDelete(current: Session, target: Session): boolean {
  const state = getState();
  return conversationDeleteAllowed(current, {
    supported: !!state.assistant && state.features.includes(CONVERSATION_DELETE_FEATURE),
    main: state.assistant?.main === target.session_id,
    builtIn: isBackgroundSupervisor(state.assistant, target.session_id),
    pending: state.interactions.some((ix) => ix.machine_id === target.machine_id && ix.session_id === target.session_id),
  });
}

/** Pure presentational view for the conversation deletion confirmation dialog. */
export function DeleteConversationDialogView({ s, busy, error, onClose, onConfirm }: DeleteConversationDialogViewProps) {
  return <Dialog open title={t("conversation-delete.delete_title", { title: sessionTitle(s) })} onClose={() => !busy && onClose()} footer={<>
    <Button tone="quiet" disabled={busy} onClick={onClose}>{t("common.cancel")}</Button>
    <Button tone="danger" icon="trash" busy={busy} onClick={onConfirm}>{t("conversation-delete.delete_confirm")}</Button>
  </>}>
    <p className="text-meta leading-relaxed text-ink-2">{t("conversation-delete.delete_body")}</p>
    {error && <p role="alert" className="mt-3 text-meta text-red">{error}</p>}
  </Dialog>;
}

export function DeleteConversationDialog({ s, onClose, deps }: DeleteConversationDialogProps) {
  if (deps?.isDeleted !== undefined) {
    return <DeleteConversationDialogBody s={s} onClose={onClose} deps={deps} deleted={deps.isDeleted} />;
  }
  if (deps?.subscribe) {
    return <SubscribedDeleteConversationDialog s={s} onClose={onClose} deps={deps} subscribe={deps.subscribe} />;
  }
  if (deps?.getSession) {
    return <DeleteConversationDialogBody s={s} onClose={onClose} deps={deps} deleted={deps.getSession(s.machine_id, s.session_id)?.deleted === true} />;
  }
  return <DefaultStoreDeleteConversationDialog s={s} onClose={onClose} deps={deps} />;
}

function DefaultStoreDeleteConversationDialog({ s, onClose, deps }: DeleteConversationDialogProps) {
  const deleted = useStore(() => getSession(s.machine_id, s.session_id)?.deleted === true);
  return <DeleteConversationDialogBody s={s} onClose={onClose} deps={deps} deleted={deleted} />;
}

function SubscribedDeleteConversationDialog({ s, onClose, deps, subscribe }: DeleteConversationDialogProps & { subscribe: (listener: () => void) => () => void }) {
  const readSession = deps?.getSession ?? getSession;
  const getSnapshot = useCallback(
    () => readSession(s.machine_id, s.session_id)?.deleted === true,
    [readSession, s.machine_id, s.session_id],
  );
  const deleted = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <DeleteConversationDialogBody s={s} onClose={onClose} deps={deps} deleted={deleted} />;
}

function DeleteConversationDialogBody({ s, onClose, deps, deleted }: DeleteConversationDialogProps & { deleted: boolean }) {
  const scopeKey = JSON.stringify([s.machine_id, s.session_id, s.manager ?? ""]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: scopeKey, epoch: 1 }));
  const [busyEpoch, setBusyEpoch] = useState<number | null>(null);
  const [errorState, setErrorState] = useState<{ epoch: number; message: string } | null>(null);
  if (scope.key !== scopeKey) {
    setScope({ key: scopeKey, epoch: scope.epoch + 1 });
    setBusyEpoch(null);
    setErrorState(null);
  }
  const activeEpoch = useRef(0);
  const requestSeq = useRef(0);
  const pending = useRef<{ epoch: number; seq: number } | null>(null);
  const onCloseRef = useRef(onClose);
  useLayoutEffect(() => { onCloseRef.current = onClose; }, [onClose]);
  useLayoutEffect(() => {
    activeEpoch.current = scope.epoch;
    return () => {
      activeEpoch.current = 0;
      if (pending.current?.epoch === scope.epoch) pending.current = null;
    };
  }, [scope.epoch]);
  useEffect(() => { if (deleted) onCloseRef.current(); }, [deleted, scope.epoch]);

  const busy = busyEpoch === scope.epoch;
  const error = errorState?.epoch === scope.epoch ? errorState.message : "";

  const remove = async () => {
    const runEpoch = scope.epoch;
    if (pending.current?.epoch === runEpoch || activeEpoch.current !== runEpoch) return;
    const readSession = deps?.getSession ?? getSession;
    const current = readSession(s.machine_id, s.session_id) ?? s;
    const allowed = deps?.canDelete ? deps.canDelete(current) : defaultCanDelete(current, s);
    if (!allowed) {
      setErrorState({ epoch: runEpoch, message: t("conversation-delete.delete_unavailable") });
      return;
    }
    const runSeq = ++requestSeq.current;
    pending.current = { epoch: runEpoch, seq: runSeq };
    const isCurrent = () => activeEpoch.current === runEpoch && pending.current?.seq === runSeq;
    setBusyEpoch(runEpoch);
    setErrorState(null);
    const runDelete = deps?.deleteConversation ?? deleteConversation;
    const formatError = deps?.formatError ?? errorText;
    try {
      await runDelete(current);
      if (!isCurrent()) return;
      pending.current = null;
      setBusyEpoch(null);
      onCloseRef.current();
    } catch (e) {
      if (!isCurrent()) return;
      pending.current = null;
      setBusyEpoch(null);
      setErrorState({ epoch: runEpoch, message: formatError(e) });
    } finally {
      if (pending.current?.seq === runSeq) pending.current = null;
      if (activeEpoch.current === runEpoch) setBusyEpoch((cur) => (cur === runEpoch ? null : cur));
    }
  };

  return <DeleteConversationDialogView s={s} busy={busy} error={error} onClose={onClose} onConfirm={() => void remove()} />;
}
