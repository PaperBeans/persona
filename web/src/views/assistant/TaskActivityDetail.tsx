import { formatDate, t } from "../../i18n";
import { backgroundWorkLine, type BackgroundWork } from "../../lib/background-work";
import { reviewState } from "../../lib/supervision";
import type { Todo } from "../../lib/todos";
import { workflowCounts } from "../../lib/workflows";

export function TaskActivityDetail({ activity, detail, pr, todo }: { activity?: BackgroundWork; detail?: string; pr?: string; todo?: Todo }) {
  return <>
    <BackgroundWorkDetail activity={activity} detail={detail} pr={pr} />
    {todo && <NextCheck todo={todo} />}
  </>;
}

function NextCheck({ todo }: { todo: Todo }) {
  const parked = todo.supervision?.parked;
  if (!parked || parked.waiting_on !== "date" || reviewState(todo) !== "parked") return null;
  const time = formatDate(parked.until, { dateStyle: "medium", timeStyle: "short" });
  return time ? <div className="truncate text-xs text-ink-2" title={time}>{t("task-wait.recheck", { time })}</div> : null;
}

/** Workflow counts never disappear behind an ellipsized phase, even on a narrow tray or with large text. */
function BackgroundWorkDetail({ activity, detail, pr }: { activity?: BackgroundWork; detail?: string; pr?: string }) {
  if (!activity?.workflow || !activity.workflows) return <div className="truncate text-xs text-ink-2" title={detail}>{[detail, pr].filter(Boolean).join(" · ")}</div>;
  const other = activity.tasks || activity.subagents ? backgroundWorkLine({ ...activity, workflows: 0, workflow: undefined }) : undefined;
  return <>
    <div className="flex min-w-0 items-center gap-1 text-xs text-ink-2" title={detail}>
      <span className="min-w-0 truncate">{activity.workflow.phase || t("chat-runningstatusbar.background_workflow")}</span>
      <span className="shrink-0 whitespace-nowrap tabular-nums"> · {workflowCounts(activity.workflow)}{activity.workflows > 1 ? ` · ${t("chat-runningstatusbar.more_workflows", { count: activity.workflows - 1 })}` : ""}</span>
    </div>
    {(activity.tasks || activity.subagents || pr) ? <div className="truncate text-xs text-ink-2" title={other}>{[activity.tasks || activity.subagents ? other : undefined, pr].filter(Boolean).join(" · ")}</div> : null}
  </>;
}
