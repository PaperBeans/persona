// An Agent's Background tasks side panel (#5421, after cindy's right-sidebar Background tasks tab, BackgroundTasksBody;
// lib/background-panel.ts opens it): what keeps the Agent going besides its turn. A list in two sections: Running (the
// /loop first, Persona's own; then the workflow runs, oldest start first, and the background commands, Monitors and
// subagents) and Completed (the ended workflow runs the node still lists, newest end first). A row is a round kind mark,
// a status mark and the title, with "status · progress · duration · tokens · tool uses" under it. A workflow row opens
// its detail in the panel (Back, the run's head, progress and phases: WorkflowCard); a background task with an output
// file opens that output in the side panel. The panel reads the session from the store, live; clocks tick only while
// something runs.
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { useBackgroundChat, type BackgroundPanel as BackgroundPanelObject } from "../../lib/background-panel";
import { openPanel } from "../../lib/panel";
import { getSession, useStore } from "../../lib/store";
import { backgroundTitle, loopCadence, loopNext, loopTask } from "../../lib/tasks";
import { compactTokens } from "../../lib/tokens";
import type { BackgroundTask, LoopInfo, Session, Workflow } from "../../lib/types";
import { mediaMatches, NARROW } from "../../lib/viewport";
import { workDuration, type AgentStatus } from "../../lib/work-groups";
import { otherBackground, recentWorkflows, workflowDurationMs, workflowName, workflowProgress, workflowTaskStatus } from "../../lib/workflows";
import { Button, copyText, cx, IconButton, Spinner, useNow } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { PanelHeader } from "../../ui/PanelHeader";
import { taskStatusWord } from "./TaskCard";
import { WorkflowCard } from "./WorkflowCard";

/** When a run started, or 0 when the hub does not say. */
const startOf = (w: Workflow) => Date.parse(w.started_at ?? "") || 0;
/** When a run ended, or its last sign of life. */
const endOf = (w: Workflow) => Date.parse(w.ended_at ?? "") || Date.parse(w.updated_at ?? "") || startOf(w);

/** What the list shows, in its order: the running runs oldest start first, the background tasks as the hub lists them,
 *  the ended runs newest end first. */
export function backgroundSections(s: Session | undefined, now: number): { loop?: LoopInfo; running: Workflow[]; tasks: BackgroundTask[]; completed: Workflow[] } {
  const recent = recentWorkflows(s, now);
  return {
    loop: s?.loop,
    running: recent.filter((w) => w.running).sort((a, b) => startOf(a) - startOf(b)),
    tasks: otherBackground(s),
    completed: recent.filter((w) => !w.running).sort((a, b) => endOf(b) - endOf(a)),
  };
}

/** A background task's round kind mark: a command's terminal, a Monitor's, a subagent's, a run an older hub lists. */
function taskIcon(task: BackgroundTask): IconName {
  if (task.kind === "shell") return "terminal";
  if (task.kind === "agent") return "agent";
  if (task.kind === "monitor") return "monitor";
  if (task.kind === "workflow") return "workflow";
  return "run";
}

/** `opened`: the panel object as last asked for (lib/panel.ts): one naming a run opens that run's detail, every time it
 *  is asked for again (a new object), as cindy consumes focusTaskId once. */
export function BackgroundPanel({ mid, sid, opened, onClose }: { mid: string; sid: string; opened: BackgroundPanelObject; onClose: () => void }) {
  const session = useStore(() => getSession(mid, sid));
  // the run whose detail shows, and whether its row opened it (the row is gone: Back takes the focus); Back gives the
  // focus to the run's row
  const asks = (p: BackgroundPanelObject) => (p.run ? { run: p.run, fromRow: false } : null);
  const [asked, setAsked] = useState(opened);
  const [shown, setShown] = useState<{ run: string; fromRow: boolean } | null>(() => asks(opened));
  if (asked !== opened) {
    setAsked(opened);
    setShown(asks(opened));
  }
  const back = useRef<string | null>(null);
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (shown || !back.current) return;
    const row = [...(list.current?.querySelectorAll<HTMLElement>("[data-bg-run]") ?? [])].find((el) => el.dataset.bgRun === back.current);
    row?.querySelector<HTMLElement>("[data-bg-row-button]")?.focus();
    back.current = null;
  }, [shown]);
  // a run gone from the session (the node no longer lists it), or not there when asked for: the list, and the ask is
  // dropped, so the panel never jumps to it later
  const run = shown ? session?.workflows?.find((w) => w.task_id === shown.run) : undefined;
  useEffect(() => {
    if (shown && !run) setShown(null);
  }, [shown, run]);
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-background-panel>
      <PanelHeader icon="list" title={t("sidepanel.background")} onClose={onClose} />
      {run ? (
        <WorkflowDetail
          key={run.task_id}
          run={run}
          mid={mid}
          sid={sid}
          focus={Boolean(shown?.fromRow)}
          onBack={() => {
            back.current = run.task_id;
            setShown(null);
          }}
          onClose={onClose}
        />
      ) : (
        <div ref={list} className="flex min-h-0 flex-1 flex-col">
          <BackgroundList session={session} mid={mid} sid={sid} onOpenRun={(task) => setShown({ run: task, fromRow: true })} />
        </div>
      )}
    </div>
  );
}

function BackgroundList({ session, mid, sid, onOpenRun }: { session: Session | undefined; mid: string; sid: string; onOpenRun: (task: string) => void }) {
  // an ended run leaves the list a day after it ended (recentWorkflows): the list's clock moves while something runs
  const busy = Boolean(session?.loop) || (session?.background?.length ?? 0) > 0 || (session?.workflows ?? []).some((w) => w.running);
  const now = useNow(60_000, busy);
  const { loop, running, tasks, completed } = backgroundSections(session, now);
  const runningId = useId();
  const completedId = useId();
  if (!loop && !running.length && !tasks.length && !completed.length) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-6 text-center" data-background-empty>
        <Icon name="list" size={20} className="text-ink-3" />
        <p className="text-[12px] text-ink-3">{t("chat-activity.empty")}</p>
      </div>
    );
  }
  const openOutput = (task: BackgroundTask) => openPanel({ kind: "file", mid, path: task.output_file!, cwd: session?.cwd });
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-1 pb-2" data-background-list>
      {(loop || running.length > 0 || tasks.length > 0) && (
        <section aria-labelledby={runningId} data-background-section="running">
          <SectionHeader id={runningId} label={t("chat-activity.section_running")} />
          {loop && <LoopRow loop={loop} mid={mid} sid={sid} />}
          {running.map((w) => (
            <RunRow key={w.task_id} run={w} onOpen={onOpenRun} />
          ))}
          {tasks.map((task) => {
            const title = backgroundTitle(task);
            const command = task.command && task.command !== title ? task.command : "";
            const since = Date.parse(task.started_at ?? "");
            return (
              <TaskRow
                key={task.id}
                icon={taskIcon(task)}
                title={title}
                status="running"
                meta={[<span key="status">{taskStatusWord("running")}</span>, ...(Number.isFinite(since) ? [<LiveDuration key="time" since={since} />] : [])]}
                command={command}
                onClick={task.output_file ? () => openOutput(task) : undefined}
                data={{ "data-bg-task": task.id }}
              />
            );
          })}
        </section>
      )}
      {completed.length > 0 && (
        <section aria-labelledby={completedId} data-background-section="completed">
          <SectionHeader id={completedId} label={t("chat-activity.section_completed")} />
          {completed.map((w) => (
            <RunRow key={w.task_id} run={w} onOpen={onOpenRun} />
          ))}
        </section>
      )}
    </div>
  );
}

function SectionHeader({ id, label }: { id: string; label: string }) {
  return (
    <h3 id={id} className="px-2 pt-3 pb-1 text-[11px] font-medium tracking-[0.5px] text-ink-3 uppercase">
      {label}
    </h3>
  );
}

/** The 20 px round kind mark at a row's start (and the detail bar's). */
function KindChip({ icon, className }: { icon: IconName; className?: string }) {
  return (
    <span className={cx("inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-field text-ink-2", className)} data-bg-kind={icon}>
      <Icon name={icon} size={12} />
    </span>
  );
}

/** The status mark before a row's title, as the task cards draw it (TaskCard): spinning while it runs, else the
 *  outcome's icon; only a failure is red. */
function RowStatus({ status }: { status: AgentStatus }) {
  return (
    <span className={cx("flex size-[13px] shrink-0 items-center justify-center", status === "failed" ? "text-red" : "text-ink-2")} data-bg-status={status}>
      {status === "running" ? <Spinner size={11} /> : <Icon name={status === "failed" ? "warning" : status === "stopped" ? "stop" : "check"} size={13} />}
    </span>
  );
}

/** How long it has run so far, counted each second in its own leaf (the list does not redraw for it). */
function LiveDuration({ since }: { since: number }) {
  const now = useNow(1000);
  return (
    <span className="tabular-nums" data-bg-elapsed>
      {workDuration(Math.max(0, now - since))}
    </span>
  );
}

/** The meta line's pieces joined with " · ". */
function joined(parts: ReactNode[]): ReactNode[] {
  return parts.flatMap((p, i) => (i ? [<span key={`sep${i}`} aria-hidden>{" · "}</span>, p] : [p]));
}

/** cindy's TaskRow: a button when it opens something (a run's detail, a command's output), else a plain row. A
 *  background command keeps its command as a third line that copies it. */
function TaskRow({ icon, title, status, meta, hint, command, onClick, trailing, data }: {
  icon: IconName;
  title: string;
  status?: AgentStatus;
  meta: ReactNode[];
  /** The title's hover text, when the row says less than there is. */
  hint?: string;
  command?: string;
  onClick?: () => void;
  trailing?: ReactNode;
  data?: Record<string, string>;
}) {
  const body = (
    <>
      <KindChip icon={icon} className="mt-px" />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          {status && <RowStatus status={status} />}
          <span className="min-w-0 truncate text-[13px] leading-5 text-ink" title={hint}>
            {title}
          </span>
        </span>
        {meta.length > 0 && (
          <span className="mt-0.5 block truncate text-[11px] leading-4 text-ink-3" data-bg-meta>
            {joined(meta)}
          </span>
        )}
      </span>
    </>
  );
  const row = "flex min-w-0 flex-1 items-start gap-2 rounded-[8px] px-2 py-1.5 text-left";
  return (
    <div className="flex flex-col" {...data}>
      <div className="flex items-start gap-1">
        {onClick ? (
          <button type="button" onClick={onClick} className={cx(row, "transition-colors hover:bg-hover")} data-bg-row-button>
            {body}
          </button>
        ) : (
          <div className={row}>{body}</div>
        )}
        {trailing}
      </div>
      {command && (
        <button
          type="button"
          onClick={() => void copyText(command)}
          aria-label={t("chat-activity.copy_command")}
          title={command}
          className="-mt-1 mr-2 mb-1 ml-9 truncate text-left font-mono text-[11px] leading-4 text-ink-3 hover:text-ink-2"
          data-bg-command
        >
          {command}
        </button>
      )}
    </div>
  );
}

/** A workflow run's row: it opens the run's detail. */
function RunRow({ run, onOpen }: { run: Workflow; onOpen: (task: string) => void }) {
  const status = workflowTaskStatus(run);
  const parts: ReactNode[] = [<span key="status">{taskStatusWord(status)}</span>];
  const progress = workflowProgress(run);
  if (progress) parts.push(<span key="agents" className="tabular-nums">{t("chat-subagentcard.workflow_progress_count", { done: progress.done, total: progress.total })}</span>);
  const start = Date.parse(run.started_at ?? "");
  const took = workflowDurationMs(run);
  if (run.running && Number.isFinite(start)) parts.push(<LiveDuration key="time" since={start} />);
  else if (took !== null) parts.push(<span key="time" className="tabular-nums">{workDuration(took)}</span>);
  if (run.tokens) parts.push(<span key="tokens">{t("chat-subagentcard.tokens", { value: compactTokens(run.tokens) })}</span>);
  if (run.calls) parts.push(<span key="calls">{t("chat-subagentcard.tool_uses", { count: run.calls })}</span>);
  return (
    <TaskRow
      icon="workflow"
      title={workflowName(run)}
      hint={run.summary}
      status={status}
      meta={parts}
      onClick={() => onOpen(run.task_id)}
      data={{ "data-bg-run": run.task_id }}
    />
  );
}

/** The /loop driving the Agent (Persona's own; cindy lists none): what it repeats, how often and when it wakes next, and
 *  a Stop that asks first (the loop ends by a message the chat sends, lib/tasks.ts STOP_LOOP_MESSAGE). */
function LoopRow({ loop, mid, sid }: { loop: LoopInfo; mid: string; sid: string }) {
  const now = useNow(30_000);
  const [confirming, setConfirming] = useState(false);
  // Stop goes through the chat page showing the session: none (the panel outlived it), no Stop rather than one that does nothing
  const chat = useBackgroundChat(mid, sid);
  const task = loopTask(loop);
  const meta = [loopCadence(loop), loopNext(loop, now)].filter(Boolean).map((m, i) => <span key={i}>{m}</span>);
  const hint = [task, loop.reason].filter(Boolean).join("\n") || undefined;
  return (
    <div className="flex flex-col" data-bg-loop>
      <TaskRow
        icon="loop"
        title={task || t("chat-activity.loop")}
        hint={hint}
        meta={meta}
        trailing={
          chat && <span className="pt-1.5">
            <IconButton icon="square" label={t("chat-activity.stop_loop")} size={24} iconSize={12} active={confirming} onClick={() => setConfirming((c) => !c)} />
          </span>
        }
      />
      {confirming && chat && (
        <div role="group" aria-label={t("chat-activity.stop_loop")} className="mr-2 mb-1.5 ml-9 flex flex-col gap-2 rounded-[8px] bg-surface px-3 py-2.5 shadow-hairline" data-bg-loop-confirm>
          <p className="text-[12.5px] leading-relaxed text-ink-2">{t("chat-activity.stop_loop_explain")}</p>
          <div className="flex justify-end gap-2">
            <Button tone="quiet" size="sm" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              tone="danger"
              size="sm"
              onClick={() => {
                setConfirming(false);
                chat.stopLoop();
              }}
            >
              {t("chat-activity.stop_loop")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** cindy's WorkflowDetail: Back, the run's mark and name, then the run's own card (its head, where it is or what it was
 *  for, the progress bar and the phases) and "Show in chat" while the chat page can bring its call into view. */
function WorkflowDetail({ run, mid, sid, onBack, onClose, focus }: { run: Workflow; mid: string; sid: string; onBack: () => void; onClose: () => void; focus: boolean }) {
  const bar = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (focus) bar.current?.querySelector<HTMLElement>("button")?.focus();
    // only as it opens
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const jumpToCall = useBackgroundChat(mid, sid)?.jumpToCall;
  const jump = jumpToCall
    ? (call: string) => {
        jumpToCall(call);
        // narrow, the panel covers the chat: the call shows only once it is gone
        if (mediaMatches(NARROW)) onClose();
      }
    : undefined;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-background-detail={run.task_id}>
      <div ref={bar} className="flex shrink-0 items-center gap-1.5 border-b border-line px-2 py-1.5">
        <IconButton icon="arrowLeft" label={t("chat-activity.back")} size={24} iconSize={14} onClick={onBack} />
        <KindChip icon="workflow" />
        <h3 className="min-w-0 flex-1 truncate text-[13px] leading-5 font-medium text-ink" title={run.summary}>
          {workflowName(run)}
        </h3>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <WorkflowCard run={run} scope={`${mid}/${sid}:`} onJump={jump} />
      </div>
    </div>
  );
}
