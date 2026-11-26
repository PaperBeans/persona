import type { MouseEvent as ReactMouseEvent } from "react";
import { t } from "../../i18n";
import { useKnownDefault } from "../../lib/manager-choice";
import { href, onLinkClick, type Route } from "../../lib/router";
import { useStore } from "../../lib/store";
import type { InboxItem, InboxTodoForward } from "../../lib/inbox";
import type { Todo } from "../../lib/todos";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export function todoForwardLabel(status: InboxTodoForward["status"]): string {
  switch (status) {
    case "queued":
      return t("inbox-inboxdetail.forward_queued");
    case "resumed":
      return t("inbox-inboxdetail.forward_resumed");
    case "fallback":
      return t("inbox-inboxdetail.forward_fallback");
    case "failed":
      return t("inbox-inboxdetail.forward_failed");
    default:
      return t("inbox-inboxdetail.forward_forwarded");
  }
}

export type InboxRouteClick = (event: ReactMouseEvent, route: Route) => void;

export interface InboxTaskLinksProps {
  item: InboxItem;
  /** Explicit to-dos let a controlled panel own the data without reading the default store. */
  todos?: Todo[];
  /** Explicit manager identity for a controlled panel without the default manager-choice store. */
  defaultManager?: string;
  /** Builds a link address without using the default router. */
  resolveHref?: (route: Route) => string;
  /** Receives link activation while preserving the original event for modifier-key handling. */
  onRouteClick?: InboxRouteClick;
}

interface InboxTaskLinksViewProps extends InboxTaskLinksProps {
  todos: Todo[];
  defaultManager: string;
  resolveHref: (route: Route) => string;
  onRouteClick: InboxRouteClick;
}

function isControlled(props: InboxTaskLinksProps): props is Required<Pick<InboxTaskLinksProps, "todos" | "defaultManager" | "resolveHref" | "onRouteClick">> & InboxTaskLinksProps {
  return props.todos !== undefined && props.defaultManager !== undefined && Boolean(props.resolveHref) && Boolean(props.onRouteClick);
}

/** The business task and its current workers, separate from the Inbox's draft-processing chat. */
export function InboxTaskLinks(props: InboxTaskLinksProps) {
  if (isControlled(props)) {
    return <InboxTaskLinksView item={props.item} todos={props.todos} defaultManager={props.defaultManager} resolveHref={props.resolveHref} onRouteClick={props.onRouteClick} />;
  }
  return <DefaultInboxTaskLinks {...props} />;
}

function DefaultInboxTaskLinks(props: InboxTaskLinksProps) {
  const knownDefault = useKnownDefault();
  const storedTodos = useStore((s) => s.todos);
  return (
    <InboxTaskLinksView
      {...props}
      defaultManager={props.defaultManager ?? knownDefault}
      todos={props.todos ?? storedTodos}
      resolveHref={props.resolveHref ?? href}
      onRouteClick={props.onRouteClick ?? onLinkClick}
    />
  );
}

function InboxTaskLinksView({ item, todos, defaultManager, resolveHref, onRouteClick }: InboxTaskLinksViewProps) {
  const id = item.agent?.todo_id;
  const fwd = item.todo_forward;
  const linked = todos.filter((x) => (x.manager ?? defaultManager) === (item.manager ?? defaultManager) && (x.inbox_item === item.id || x.id === id || (fwd && x.id === fwd.todo_id)));
  const tasks: { id: string; todo?: Todo }[] = linked.map((todo) => ({ id: todo.id, todo }));
  if (id && !tasks.some((x) => x.id === id)) tasks.push({ id });
  if (fwd?.todo_id && !tasks.some((x) => x.id === fwd.todo_id)) tasks.push({ id: fwd.todo_id });
  if (!tasks.length && !fwd) return null;
  const link = "flex min-w-0 max-w-full items-center gap-1.5 rounded-control text-xs text-accent hover:underline focus-visible:outline-2 focus-visible:outline-accent";
  const warn = fwd?.status === "fallback" || fwd?.status === "failed";
  return (
    <div className="mt-3 flex flex-col items-start gap-1.5" data-inbox-task-links>
      {fwd && (
        <div className="flex min-w-0 max-w-full flex-wrap items-center gap-1.5 text-xs" data-inbox-todo-forward={fwd.status}>
          <span className={cx("shrink-0 rounded-chip px-1.5 py-0.5 text-submeta font-medium", warn ? "bg-field text-red" : "bg-field text-ink-2")}>
            {todoForwardLabel(fwd.status)}
          </span>
          {fwd.reason && <span className="min-w-0 truncate text-ink-3" title={fwd.reason}>{fwd.reason}</span>}
        </div>
      )}
      {tasks.map(({ id: taskID, todo }) => {
        const current = todo?.lineage?.filter((x) => x.current);
        // A current lineage supersedes previous workers, even while the next Agent is starting.
        const agents = current?.length ? current.flatMap((x) => x.machine && x.session_id ? [{
          machine: x.machine, session: x.session_id,
          title: todo?.agents?.find((a) => a.session_id === x.session_id && a.machine_id === x.machine)?.title || x.agent || x.session_id,
        }] : []) : (todo?.agents ?? []).filter((x) => !x.released_at && !x.start_error).map((x) => ({ machine: x.machine_id, session: x.session_id, title: x.title || x.session_id }));
        const taskRoute: Route = { name: "todos", id: taskID };
        const title = todo?.title || (fwd?.todo_id === taskID ? fwd.todo_title : undefined) || taskID;
        return <div key={taskID} className="contents">
          <a href={resolveHref(taskRoute)} onClick={(e) => onRouteClick(e, taskRoute)} className={link}>
            <Icon name="todo" size={13} /><span className="shrink-0">{t("inbox-inboxdetail.linked_task")}</span>
            <span className="truncate">{title}</span>
          </a>
          {agents.map((agent) => {
            const route: Route = { name: "session", mid: agent.machine, sid: agent.session };
            return <a key={`${agent.machine}/${agent.session}`} href={resolveHref(route)} onClick={(e) => onRouteClick(e, route)} className={link}>
              <Icon name="agent" size={13} /><span className="shrink-0">{t("inbox-inboxdetail.linked_agent")}</span>
              <span className="truncate">{agent.title}</span>
            </a>;
          })}
        </div>;
      })}
    </div>
  );
}
