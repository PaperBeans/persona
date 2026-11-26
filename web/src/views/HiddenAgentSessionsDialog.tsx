import { useCallback, useEffect, useMemo, useSyncExternalStore, type MouseEvent } from "react";
import { t } from "../i18n";
import { useScoped } from "../lib/current-assistant";
import { SessionList } from "../lib/session-list";
import { runtimeIdentity, runtimeOrder } from "../lib/runtime-list";
import { href, onLinkClick } from "../lib/router";
import { onHubEvent } from "../lib/store";
import type { Session } from "../lib/types";
import { Button, Dialog, Loading } from "../ui/atoms";
import { Group, Row, RowFailed } from "./settings/SettingsParts";

export interface HiddenAgentSessionsSnapshot {
  rows: readonly Session[];
  loading: boolean;
  error: string | null;
  hasMore: boolean;
}

export interface HiddenAgentSessionsSource {
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => HiddenAgentSessionsSnapshot;
  load: () => Promise<void>;
  retry: () => Promise<void>;
  more: () => Promise<void>;
  invalidate: () => void;
  reset: () => void;
}

export interface HiddenAgentSessionsDialogDeps {
  /** Create the inspection list source (defaults to a hidden-inclusive `SessionList`). */
  createList?: () => HiddenAgentSessionsSource;
  /** Subscribe to session-list change events (defaults to `onHubEvent("sessions_list", ...)`). */
  onSessionsList?: (listener: () => void) => () => void;
  /** Filter rows for the current assistant scope (supplying this bypasses `useScoped`). */
  scopeRows?: (rows: readonly Session[]) => readonly Session[];
  /** Compute a session's link href (defaults to `router.href`). */
  sessionHref?: (s: Session) => string;
  /** Handle a session link click before closing the dialog (defaults to `router.onLinkClick`). */
  onSessionClick?: (e: MouseEvent, s: Session) => void;
}

export interface HiddenAgentSessionsDialogProps {
  open: boolean;
  onClose: () => void;
  deps?: HiddenAgentSessionsDialogDeps;
}

export interface HiddenAgentSessionsDialogViewProps {
  open: boolean;
  onClose: () => void;
  rows: readonly Session[];
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  onRetry: () => void;
  onMore: () => void;
  sessionHref?: (s: Session) => string;
  onSessionClick?: (e: MouseEvent, s: Session) => void;
}

const defaultCreateList = (): HiddenAgentSessionsSource =>
  new SessionList<Session>("/sessions?include_hidden=1", runtimeIdentity, runtimeOrder, undefined, undefined, { counts: false });
const defaultOnSessionsList = (listener: () => void) => onHubEvent("sessions_list", listener);
const defaultSessionHref = (s: Session) => href({ name: "session", mid: s.machine_id, sid: s.session_id });
const defaultOnSessionClick = (e: MouseEvent, s: Session) => onLinkClick(e, { name: "session", mid: s.machine_id, sid: s.session_id });

// A read that fails rejects only after the list has put its error in the snapshot (SessionList enqueue), which the
// error row shows with Retry: nothing more to do with the rejection here.
const shown = () => {};
const runListAction = (action: () => Promise<void> | void) => {
  try { void Promise.resolve(action()).catch(shown); } catch { /* snapshot owns error state */ }
};

/** Pure presentational view for the hidden-agent inspection dialog. */
export function HiddenAgentSessionsDialogView({
  open, onClose, rows, loading, error, hasMore, onRetry, onMore, sessionHref, onSessionClick,
}: HiddenAgentSessionsDialogViewProps) {
  return <Dialog open={open} onClose={onClose} title={t("hidden-agent-dirs.inspect")} footer={<Button tone="secondary" onClick={onClose}>{t("hidden-agent-dirs.close")}</Button>}>
    {open && <HiddenAgentSessionsListView rows={rows} loading={loading} error={error} hasMore={hasMore} onRetry={onRetry} onMore={onMore} close={onClose} sessionHref={sessionHref} onSessionClick={onSessionClick} />}
  </Dialog>;
}

/** The inspection dialog, its own chunk (HiddenAgentSessions loads it on the first open). */
export function HiddenAgentSessionsDialog({ open, onClose, deps }: HiddenAgentSessionsDialogProps) {
  return <Dialog open={open} onClose={onClose} title={t("hidden-agent-dirs.inspect")} footer={<Button tone="secondary" onClick={onClose}>{t("hidden-agent-dirs.close")}</Button>}>
    {open && <InspectionList close={onClose} deps={deps} />}
  </Dialog>;
}

interface ListViewProps {
  rows: readonly Session[];
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  onRetry: () => void;
  onMore: () => void;
  close: () => void;
  sessionHref?: (s: Session) => string;
  onSessionClick?: (e: MouseEvent, s: Session) => void;
}

function HiddenAgentSessionsListView({
  rows, loading, error, hasMore, onRetry, onMore, close, sessionHref = defaultSessionHref, onSessionClick = defaultOnSessionClick,
}: ListViewProps) {
  return <Group footer={t("hidden-agent-dirs.temporary_view")}>
    {error && <RowFailed text={error} retry={t("hidden-agent-dirs.retry")} onRetry={onRetry} />}
    {loading && <Row label={<Loading />} />}
    {rows.filter((s) => !s.assistant).map((s) => <Row key={runtimeIdentity(s)} label={<a href={sessionHref(s)} onClick={(e) => { onSessionClick(e, s); close(); }}>{s.title || t("lib-format.untitled_session")}</a>} hint={t(s.hidden_by_directory ? "hidden-agent-dirs.hidden" : "hidden-agent-dirs.normal")} />)}
    {hasMore && <Row label={<Button tone="secondary" busy={loading} onClick={onMore}>{t("hidden-agent-dirs.more")}</Button>} />}
  </Group>;
}

function DefaultScopedListView(props: ListViewProps) {
  const rows = useScoped(props.rows as Session[]);
  return <HiddenAgentSessionsListView {...props} rows={rows} />;
}

function InspectionList({ close, deps }: { close: () => void; deps?: HiddenAgentSessionsDialogDeps }) {
  const createList = deps?.createList ?? defaultCreateList;
  const subscribeSessionsList = deps?.onSessionsList ?? defaultOnSessionsList;
  const list = useMemo(() => createList(), [createList]);
  const subscribe = useCallback((listener: () => void) => list.subscribe(listener), [list]);
  const getSnapshot = useCallback(() => list.getSnapshot(), [list]);
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  useEffect(() => {
    runListAction(() => list.load());
    return () => { list.reset(); };
  }, [list]);
  useEffect(() => {
    let active = true;
    const off = subscribeSessionsList(() => { if (active) list.invalidate(); });
    return () => { active = false; off(); };
  }, [list, subscribeSessionsList]);
  const onRetry = useCallback(() => runListAction(() => list.retry()), [list]);
  const onMore = useCallback(() => runListAction(() => list.more()), [list]);
  const shared = { loading: state.loading, error: state.error, hasMore: state.hasMore, onRetry, onMore, close, sessionHref: deps?.sessionHref, onSessionClick: deps?.onSessionClick };
  if (deps?.scopeRows) return <HiddenAgentSessionsListView {...shared} rows={deps.scopeRows(state.rows)} />;
  return <DefaultScopedListView {...shared} rows={state.rows} />;
}
