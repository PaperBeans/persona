// "Session info" (iOS SessionInfoSheet), a chunk of its own: SessionDialogs.tsx loads it on the dialog's first open, so its
// fetch, its rows and the Persona conversation's standing permissions are not the first screen's, nor their words (the
// chat-sessioninfo namespace, which the first screen's catalog leaves to the rest) (#8261).
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, errorText, sessionPath } from "../../lib/api";
import { ago, externalLink, resumeCommand, shortPath } from "../../lib/format";
import { openLink } from "../../lib/links";
import { effortTitle, listedModeOption, listedModesOf, modeTitle } from "../../lib/session-actions";
import { formatTokens } from "../../lib/tokens";
import type { Session } from "../../lib/types";
import { Button, copyText, cx, Dialog, Loading } from "../../ui/atoms";
import { t } from "../../i18n";
import { AlwaysPermissions } from "./AlwaysPermissions";
import { DelegatedHistory } from "./Delegated";

export interface SessionInfo {
  session_id?: string;
  transcript_path?: string;
  cwd?: string;
  model?: string;
  launcher?: string;
  git_branch?: string;
  version?: string;
  permission_mode?: string;
  effort?: string;
  context_tokens?: number;
  output_tokens?: number;
  compactions?: number;
  turns?: number;
  /** Nobody gives its counts (a runtime's session with no transcript on the computer, #8261): the zeros beside it stand
   *  in for older apps and are not shown. */
  usage_unknown?: boolean;
  started_at?: string;
  last_activity?: string;
}

export type SessionInfoCopyKind = "session_id" | "resume_command";
export type SessionInfoLoader = (session: Session, signal: AbortSignal) => Promise<SessionInfo>;

export interface SessionInfoDialogProps {
  session: Session;
  machineName: string;
  open: boolean;
  onClose: () => void;
  /** Explicit session info (`null` while loading); skips internal fetching when `info` or `error` is supplied. */
  info?: SessionInfo | null;
  /** Explicit loading indicator when controlled; defaults to `!info && !error`. */
  loading?: boolean;
  /** Explicit error message; skips internal fetching when `info` or `error` is supplied. */
  error?: string;
  /** Custom info loader replacing `GET /sessions/:mid/:sid/info` when `info` and `error` are omitted. */
  loadInfo?: SessionInfoLoader;
  /** Replaces global `copyText` for footer copy buttons. */
  onCopy?: (text: string, kind: SessionInfoCopyKind) => void;
  /** Replaces global `openLink` for the external link button. */
  onOpenExternal?: (url: string) => void;
  /** Replaces default `AlwaysPermissions` section for assistant sessions. */
  renderPermissions?: (session: Session) => ReactNode;
  /** Replaces default `DelegatedHistory` section for non-assistant sessions. */
  renderDelegated?: (session: Session) => ReactNode;
}

// Token counts as the Machines page and the apps write them (1.2K, 1.23M: lib/tokens.ts) with a localized unit; "" for a
// count nobody gave.
const tokens = (n?: number) => (n === undefined ? "" : t("chat-sessioninfo.tokens", { value: formatTokens(n) }));

// How it runs, how much it used, where it lives; ids and the resume command copy.
export function SessionInfoDialog(props: SessionInfoDialogProps) {
  if (props.info !== undefined || props.error !== undefined) {
    return (
      <SessionInfoDialogView
        {...props}
        info={props.info ?? null}
        error={props.error ?? ""}
        loading={props.loading ?? (!props.info && !props.error)}
      />
    );
  }
  return <LoadedSessionInfoDialog {...props} />;
}

interface LoadedState {
  key: string;
  seq: number;
  info: SessionInfo | null;
  error: string;
  loading: boolean;
}

function LoadedSessionInfoDialog(props: SessionInfoDialogProps) {
  const { session, open, loadInfo } = props;
  const sessionKey = `${session.machine_id}\0${session.session_id}`;
  const hasLoadInfo = Boolean(loadInfo);
  const loadInfoRef = useRef(loadInfo);
  const sessionRef = useRef(session);
  useEffect(() => {
    loadInfoRef.current = loadInfo;
    sessionRef.current = session;
  }, [loadInfo, session]);
  const requestSeq = useRef(0);
  const [state, setState] = useState<LoadedState>({ key: "", seq: 0, info: null, error: "", loading: false });

  useEffect(() => {
    if (!open) {
      requestSeq.current += 1;
      return;
    }
    let live = true;
    const seq = ++requestSeq.current;
    const ac = new AbortController();
    setState({ key: sessionKey, seq, info: null, error: "", loading: true });
    let request: Promise<SessionInfo>;
    try {
      request = loadInfoRef.current
        ? Promise.resolve(loadInfoRef.current(sessionRef.current, ac.signal))
        : api<SessionInfo>(`${sessionPath(sessionRef.current.machine_id, sessionRef.current.session_id)}/info`, { signal: ac.signal });
    } catch (e) {
      request = Promise.reject(e);
    }
    request
      .then((r) => {
        if (!live || ac.signal.aborted || requestSeq.current !== seq) return;
        setState({ key: sessionKey, seq, info: r, error: "", loading: false });
      })
      .catch((e) => {
        if (!live || ac.signal.aborted || requestSeq.current !== seq) return;
        setState({ key: sessionKey, seq, info: null, error: errorText(e), loading: false });
      });
    return () => {
      live = false;
      ac.abort();
    };
  }, [open, sessionKey, hasLoadInfo]);

  const current = !open || state.key === sessionKey;
  const info = current ? state.info : null;
  const error = current ? state.error : "";
  const loading = props.loading ?? (open ? (current ? state.loading : true) : false);

  return <SessionInfoDialogView {...props} info={info} error={error} loading={loading} />;
}

interface SessionInfoDialogViewProps extends Omit<SessionInfoDialogProps, "info" | "error" | "loading"> {
  info: SessionInfo | null;
  error: string;
  loading: boolean;
}

function SessionInfoDialogView({
  session,
  machineName,
  open,
  onClose,
  info,
  error,
  loading,
  onCopy,
  onOpenExternal,
  renderPermissions,
  renderDelegated,
}: SessionInfoDialogViewProps) {
  // counts nobody gives (#8261) are left out, not shown as zeros; a session that lists its own modes runs in one of them
  const counted = info && !info.usage_unknown ? info : undefined;
  const listed = listedModesOf(session);
  const mode = listed ? listedModeOption(info?.permission_mode && listed.includes(info.permission_mode) ? info.permission_mode : "default").title : modeTitle(info?.permission_mode);
  const externalUrl = externalLink(session);
  const resume = !session.assistant ? resumeCommand(session) : "";
  const handleCopy = (text: string, kind: SessionInfoCopyKind) => {
    if (onCopy) onCopy(text, kind);
    else void copyText(text);
  };
  const handleOpenExternal = (url: string) => {
    if (onOpenExternal) onOpenExternal(url);
    else openLink(url);
  };
  const groups: { title: string; rows: [string, string | undefined, boolean?][] }[] = info
    ? [
        { title: t("chat-sessioninfo.group_run"), rows: [[t("chat-sessioninfo.model"), info.model], [t("chat-sessioninfo.launcher"), info.launcher || session.launcher], [t("chat-sessiondialogs.permission_mode"), mode], [t("chat-sessioninfo.effort"), effortTitle(info.effort)], [t("chat-sessioninfo.version"), info.version, true]] },
        { title: t("chat-sessioninfo.group_usage"), rows: [[t("chat-sessioninfo.context"), counted?.context_tokens ? tokens(counted.context_tokens) : ""], [t("chat-sessioninfo.output"), tokens(counted?.output_tokens)], [t("chat-sessioninfo.turns"), counted?.turns !== undefined ? String(counted.turns) : ""], [t("chat-sessioninfo.compaction"), counted?.compactions ? t("chat-sessioninfo.compactions", { count: counted.compactions }) : ""], [t("chat-sessioninfo.started"), info.started_at && ago(info.started_at)], [t("chat-sessioninfo.last_activity"), info.last_activity && ago(info.last_activity)]] },
        { title: t("chat-sessioninfo.group_location"), rows: [[t("chat-sessioninfo.machine"), machineName], [t("chat-sessiondialogs.folder"), info.cwd ? shortPath(info.cwd) : undefined, true], [t("chat-sessioninfo.branch"), info.git_branch, true], [t("chat-sessioninfo.transcript"), info.transcript_path ? shortPath(info.transcript_path) : undefined, true]] },
      ]
    : [];

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t("chat-sessioninfo.info_title")}
      width={480}
      footer={
        <>
          <Button tone="quiet" icon="copy" onClick={() => handleCopy(session.session_id, "session_id")}>
            {t("chat-sessioninfo.copy_session_id")}
          </Button>
          {externalUrl && (
            <Button tone="secondary" icon="external" onClick={() => handleOpenExternal(externalUrl)}>
              {t("chat-sessioninfo.open_in_lark")}
            </Button>
          )}
          {/* none for a runtime's session (resumeCommand): its runtime keeps it */}
          {resume && (
            <Button tone="secondary" icon="terminal" onClick={() => handleCopy(resume, "resume_command")}>
              {t("chat-sessioninfo.copy_resume_command")}
            </Button>
          )}
        </>
      }
    >
      {loading && (
        <div className="flex justify-center py-6">
          <Loading />
        </div>
      )}
      {error && <p className="py-4 text-[13px] text-red">{error}</p>}
      <div className="flex flex-col gap-3">
        {open && session.assistant && (
          renderPermissions ? renderPermissions(session) : <AlwaysPermissions key={session.session_id} session={session.session_id} />
        )}
        {open && !session.assistant && (
          renderDelegated ? renderDelegated(session) : <DelegatedHistory key={`${session.machine_id}/${session.session_id}`} session={session} />
        )}
        {groups.map((g) => {
          const rows = g.rows.filter(([, v]) => v);
          if (!rows.length) return null;
          return (
            <section key={g.title}>
              <h3 className="mb-1 text-submeta font-medium text-ink-3">{g.title}</h3>
              <div className="divide-y divide-line overflow-hidden rounded-[10px] bg-inset shadow-hairline">
                {rows.map(([k, v, mono]) => (
                  <div key={k} className="flex items-center gap-3 px-3 py-1.5 text-[12.5px]">
                    <span className="w-16 shrink-0 text-ink-3">{k}</span>
                    <span title={v} className={cx("min-w-0 flex-1 truncate text-ink", mono && "font-mono text-[12px]")}>
                      {v}
                    </span>
                  </div>
                ))}
              </div>
            </section>
          );
        })}
      </div>
    </Dialog>
  );
}
