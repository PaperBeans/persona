import { Suspense } from "react";
import { t } from "../../i18n";
import type { PlanTask, TaskPlan, TaskWorkspace } from "../../lib/feed";
import { lazyPage } from "../../lib/lazy-page";

const WorkspaceControls = lazyPage(() => import("./TaskWorkspaceControls").then((m) => m.TaskWorkspaceControls));

export function workspaceMode(w?: TaskWorkspace): "new" | "existing" | "none" {
  return typeof w === "object" ? ("existing" in w ? "existing" : "new") : w ?? "none";
}

export function TaskWorkspaceLine({ task }: { task: PlanTask }) {
  if (!task.workspace) return null;
  return <div className="min-w-0 text-[12px] break-all text-ink-2" data-task-workspace>
    {t("assistant-tasks.workspace")} · {t(`assistant-tasks.workspace_${workspaceMode(task.workspace)}`)} · {task.workspace_resolved?.path ?? task.cwd}
  </div>;
}

export interface TaskWorkspaceEditorProps {
  plan: TaskPlan; index: number; disabled?: boolean; onChange: (workspace: TaskWorkspace) => void;
}

/** Only a plan with workspace policy needs the editor; the first screen keeps just its summary. */
export function TaskWorkspaceEditor(props: TaskWorkspaceEditorProps) {
  const task = props.plan.tasks[props.index];
  if (!task.workspace) return null; // Older hubs retain their original card.
  return <Suspense fallback={<TaskWorkspaceLine task={task} />}>
    <WorkspaceControls {...props} mode={workspaceMode(task.workspace)} />
  </Suspense>;
}
