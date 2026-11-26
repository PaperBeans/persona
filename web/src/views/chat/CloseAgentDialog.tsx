import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { appArchiveDeps, archiveAgent, closeBusyReason, closeOffer, type CloseBusyReason, type CloseOffer, type CloseResult } from "../../lib/agent-archives";
import { api, errorText, sessionPath } from "../../lib/api";
import { stripBase } from "../../lib/base";
import { sessionMenuActions, sessionTitle, tabLabel } from "../../lib/format";
import { navigate, parse, showsSession } from "../../lib/router";
import { getSession, getState, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import type { WorktreeStatus } from "../../lib/worktree-cleanup";
import { Button, cx, Dialog, toast } from "../../ui/atoms";
import { cleanupReady, KEEP, runCleanup, useAgentWorktree, WorktreeCleanup, type Cleanup } from "./SessionWorktreeCleanup";

export interface CloseAgentArchiveResult {
  ok: boolean;
  busy?: boolean;
  close?: CloseResult;
}

/** Whether `archive?close=1` confirmed the agent closed (or was already not running) so deleting its worktree is safe.
 *  Controlled `onArchiveClose` callers that omit `close` are assumed closed for backwards compatibility; the default
 *  hub `archiveAgent` path fails closed when `close` is omitted (`archived_close_unknown`). */
function archiveClosed(close: CloseResult | undefined, assumeClosedWhenOmitted: boolean): boolean {
  if (!close) return assumeClosedWhenOmitted;
  return close.closed || close.outcome === "not_running";
}

export interface CloseAgentDialogProps {
  open: boolean;
  onClose: () => void;
  onClosed?: () => void;
  session: Session;
  machineName: string;
  /** Live session snapshot (`null` when the session went away); skips the default store when provided. */
  liveSession?: Session | null;
  /** Hub feature flags; skips the default store when provided. */
  features?: readonly string[];
  /** Managed worktree status (`null` for none); skips the default worktree status request when provided. */
  worktree?: WorktreeStatus | null;
  /** Controlled archive-and-close action; replaces `archiveAgent(..., appArchiveDeps)`. */
  onArchiveClose?: (session: Session) => Promise<CloseAgentArchiveResult>;
  /** Controlled plain-close action; replaces `POST .../close`. */
  onCloseSession?: (session: Session) => Promise<void>;
  /** Controlled worktree cleanup action; replaces `runCleanup`. */
  onCleanupWorktree?: (worktree: WorktreeStatus, cleanup: Cleanup) => void | Promise<void>;
  /** Controlled notification callback; replaces the default `toast`. */
  onNotice?: (message: string, tone: "ok" | "error" | "info") => void;
}

interface CloseAgentLiveProps extends CloseAgentDialogProps {
  live: Session | undefined;
  liveControlled: boolean;
}

interface CloseAgentFeaturesProps extends CloseAgentLiveProps {
  resolvedFeatures: readonly string[];
  featuresControlled: boolean;
}

interface CloseAgentResolvedProps extends CloseAgentFeaturesProps {
  wt: WorktreeStatus | null;
}

// "Close…" (#7125): the one way the web closes an Agent. Its row menu, the chat's ⋯ and "Close pane" on its pane all
// ask here (lib/close-agent.ts; views/CloseAgentHost.tsx loads this chunk on that intent), reading the agent's latest row as it changes:
// - not busy, it archives and closes (lib/agent-archives.ts, `archive?close=1`): the chat stays, saying it is archived;
// - busy, it says what closing interrupts, and "Close anyway" closes it through `POST …/close` (the hub refuses to
//   archive a busy agent), then leaves its chat as "Close agent" did (#3405); so does an agent the hub cannot archive and close.
// The conversation stays every way. `onClosed`: what to do once a plain close is done; without it, the page leaves the
// agent's chat only when that chat is open.
export function CloseAgentDialog(props: CloseAgentDialogProps) {
  if (props.liveSession === undefined) return <CloseAgentWithLive {...props} />;
  return <CloseAgentFeatures {...props} live={props.liveSession ?? undefined} liveControlled />;
}

function CloseAgentWithLive(props: CloseAgentDialogProps) {
  const live = useStore(() => getSession(props.session.machine_id, props.session.session_id));
  return <CloseAgentFeatures {...props} live={live} liveControlled={false} />;
}

function CloseAgentFeatures(props: CloseAgentLiveProps) {
  if (props.features === undefined) return <CloseAgentWithFeatures {...props} />;
  return <CloseAgentWorktree {...props} resolvedFeatures={props.features} featuresControlled />;
}

function CloseAgentWithFeatures(props: CloseAgentLiveProps) {
  const resolvedFeatures = useStore((st) => st.features);
  return <CloseAgentWorktree {...props} resolvedFeatures={resolvedFeatures} featuresControlled={false} />;
}

function CloseAgentWorktree(props: CloseAgentFeaturesProps) {
  if (props.worktree === undefined) return <CloseAgentWithWorktree {...props} />;
  const wt = props.worktree?.ours && props.worktree.path ? props.worktree : null;
  return <CloseAgentDialogView key={`${props.session.machine_id}/${props.session.session_id}`} {...props} wt={wt} />;
}

function CloseAgentWithWorktree(props: CloseAgentFeaturesProps) {
  const wt = useAgentWorktree(props.session, props.open);
  return <CloseAgentDialogView key={`${props.session.machine_id}/${props.session.session_id}`} {...props} wt={wt} />;
}

function CloseAgentDialogView({
  open,
  onClose,
  onClosed,
  session,
  machineName,
  live,
  liveControlled,
  resolvedFeatures,
  featuresControlled,
  wt,
  onArchiveClose,
  onCloseSession,
  onCleanupWorktree,
  onNotice,
}: CloseAgentResolvedProps) {
  const [pending, setPending] = useState(false);
  // the hub said it is busy though this page doesn't know why yet (a 409 to the archive): it closes as a busy one does
  const [refused, setRefused] = useState(false);
  const [cleanup, setCleanup] = useState<Cleanup>(KEEP);
  // Track whether this uncontrolled dialog ever observed the session in the live store while open (outside our own
  // archive attempt, whose 409/error `restore()` may briefly insert an off-page snapshot), so a session that genuinely
  // disappeared while the dialog was open still dismisses, while off-page/filtered/grouped/archived-open snapshots
  // remain closable.
  const hadLive = useRef(Boolean(open && live));
  const submitting = useRef(false);
  const archiveRestored = useRef(false);
  useEffect(() => {
    if (!open) {
      hadLive.current = false;
      archiveRestored.current = false;
      return;
    }
    setCleanup(KEEP); // each time: keeping is the default
    setRefused(false);
    archiveRestored.current = false;
    hadLive.current = Boolean(live);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  useEffect(() => {
    if (open && live && !pending && !submitting.current && !archiveRestored.current) hadLive.current = true;
  }, [open, live, pending]);
  const shown = closing(live ?? session, session, resolvedFeatures, refused);
  const notify = (message: string, tone: "ok" | "error" | "info") => {
    if (onNotice) onNotice(message, tone);
    else toast(message, tone);
  };
  const go = async () => {
    if (pending) return;
    // the agent went away while the dialog was open: nothing left to close, so don't leave a button that does nothing
    const liveNow = liveControlled ? live : getSession(session.machine_id, session.session_id);
    const current = liveControlled ? live : (liveNow ?? (hadLive.current ? undefined : session));
    const activeFeatures = featuresControlled ? resolvedFeatures : getState().features;
    const now = current && closing(current, session, activeFeatures, refused);
    if (!current || !now || now.offer === "no" || now.offer === "busy") {
      onClose();
      return;
    }
    submitting.current = true;
    setPending(true);
    try {
      let shouldCleanup = true;
      if (now.offer === "archive") {
        const r = onArchiveClose ? await onArchiveClose(current) : await archiveAgent(current, true, appArchiveDeps);
        if (!r.ok) {
          if (!onArchiveClose && !liveNow) archiveRestored.current = true;
          if (r.busy) setRefused(true); // the toast said why; the dialog now offers to close it anyway
          return;
        }
        shouldCleanup = archiveClosed(r.close, Boolean(onArchiveClose));
        onClose();
      } else {
        if (onCloseSession) await onCloseSession(current);
        else await api(`${sessionPath(session.machine_id, session.session_id)}/close`, { method: "POST" });
        notify(t("close-agent.closed"), "ok");
        onClose();
        if (onClosed) onClosed();
        else if (showsSession(parse(stripBase(location.pathname), location.search), session.machine_id, session.session_id)) navigate({ name: "home" });
      }
      if (shouldCleanup && wt?.path && cleanup.remove) {
        if (onCleanupWorktree) {
          try {
            await onCleanupWorktree(wt, cleanup);
          } catch (e) {
            notify(t("chat-sessiondialogs.wt_remove_failed", { error: errorText(e) }), "error");
          }
        } else {
          await runCleanup(session.machine_id, wt, cleanup, session.host);
        }
      }
    } catch (e) {
      notify(t("close-agent.close_failed", { error: errorText(e) }), "error");
    } finally {
      submitting.current = false;
      setPending(false);
    }
  };
  const row = live ?? session;
  const detail = shown.offer === "archive"
    ? archiveCloseText(row, machineName)
    : t(row.source_kind === "runtime" ? "close-agent.close_runtime_detail" : "close-agent.close_agent_detail", { machine: machineName, title: sessionTitle(row), where: [row.space_name, tabLabel(row)].filter(Boolean).join(" › ") || sessionTitle(row) });
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t("close-agent.close_agent_title")}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          {shown.offer !== "busy" && (
            <Button tone="danger" busy={pending} disabled={!cleanupReady(wt, cleanup)} onClick={go}>
              {shown.why ? t("close-agent.close_anyway") : shown.offer === "archive" ? t("close-agent.archive_close") : t("close-agent.close_agent")}
            </Button>
          )}
        </>
      }
    >
      {shown.why && <p className="text-meta leading-relaxed font-medium text-red" data-close-busy={shown.why}>{closeBusyText(shown.why)}</p>}
      <p className={cx("text-meta leading-relaxed text-ink-2", shown.why && "mt-2")}>{shown.offer === "busy" ? t("agentmenus.close_busy") : detail}</p>
      {wt && shown.offer !== "busy" && <WorktreeCleanup st={wt} value={cleanup} onChange={setCleanup} />}
    </Dialog>
  );
}

/** What archiving and closing row does, in the dialog's words. An Antigravity CLI conversation (#14520) is closed, from
 *  its own row (the companion's), or loses the pane it runs in, and a message brings it back from Archived agents; any
 *  other agent's tab and process close, and it comes back with Resume on its terminal page. */
function archiveCloseText(row: Session, machine: string): string {
  const title = sessionTitle(row);
  if (row.agent !== "agy") return t("close-agent.archive_close_detail", { title, machine });
  return row.source_kind ? t("close-agent.archive_close_agy_detail", { title, machine }) : t("close-agent.archive_close_agy_pane_detail", { title, machine });
}

/** What closing a busy agent interrupts, in the dialog's words. */
function closeBusyText(why: CloseBusyReason): string {
  if (why === "waiting") return t("close-agent.busy_waiting");
  if (why === "loop") return t("close-agent.busy_loop");
  if (why === "server") return t("close-agent.busy_server");
  return t("close-agent.busy_working");
}

/** What "Close…" does with row now (lib/agent-archives.ts closeOffer) and, busy, what closing interrupts. `asked` is the
 *  row the dialog was opened for: an archived chat's (the menus' snapshot says so) is only closed. `refused`: the hub
 *  refused to archive it as busy, so it closes as a busy one does. */
function closing(now: Session, asked: Session, features: readonly string[], refused: boolean): { offer: CloseOffer; why?: CloseBusyReason } {
  const row = asked.archived ? { ...now, archived: true } : now;
  const why = closeBusyReason(row) ?? (refused ? "working" : undefined);
  let offer = closeOffer(row, features);
  if (offer === "archive" && why) offer = sessionMenuActions(row).includes("close") ? "close" : "busy";
  return { offer, why: offer === "close" ? why : undefined };
}
