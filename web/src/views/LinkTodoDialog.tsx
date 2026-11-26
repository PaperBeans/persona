// Dialog to link an agent session or project space to an uncompleted todo
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { errorText } from "../lib/api";
import { ago } from "../lib/format";
import { composing } from "../lib/send-history";
import { useAgentSessions } from "../lib/sessions";
import { refresh, refreshTodos, setSpacesSupported, useStore } from "../lib/store";
import { projectNames, useTodoProjects, type TodoProject } from "../lib/todo-projects";
import {
  createTodo,
  isLegacySpacesRouteError,
  linkAgentToTodo,
  linkSpaceToTodo,
  makeSessionLookup,
  todoStatus,
  TODO_TONE_CLASS,
  unlinkAgentFromTodo,
  unlinkSpaceFromTodo,
  type Todo,
} from "../lib/todos";
import { resolveLinkCurrentTodoId } from "../lib/todo-details";
import { todoLook, waitOf } from "../lib/todo-wait-reason";
import type { Session } from "../lib/types";
import { hasFinePointer } from "../lib/viewport";
import { Button, cx, Dialog, toast, useNow } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { SearchField } from "../ui/SearchField";
import { t } from "../i18n";

/** From this many open to-dos the list gets a search field: its box shows five two-line rows whole (and a peek of the
 *  sixth, so a longer list is seen to scroll), so from six one has to be scrolled to. */
export const LINK_SEARCH_FROM = 6;

/** The open to-dos whose title, notes or project name hold `query` (all of them for an empty one). */
export function matchTodos(todos: readonly Todo[], query: string, project: (slug: string) => string = (slug) => slug): Todo[] {
  const q = query.trim().toLocaleLowerCase();
  if (!q) return [...todos];
  return todos.filter((todo) => [todo.title, todo.notes ?? "", todo.project ? project(todo.project) : ""].some((text) => text.toLocaleLowerCase().includes(q)));
}

export interface LinkTargetSpace {
  machineId: string;
  space: string;
}

export type LinkTodoNotifyTone = "ok" | "error";
export type LinkTodoRefreshScope = "todos" | "all";

export interface LinkTodoDialogProps {
  open: boolean;
  session?: Session;
  space?: LinkTargetSpace;
  onClose: () => void;
  /** Explicit todos skip the default hub store subscription. */
  todos?: readonly Todo[];
  /** Explicit current todo ID (`null` when unlinked) overrides deriving it from `session` and `todos`. */
  currentTodoId?: string | null;
  /** Explicit sessions for status derivation skip `useAgentSessions()`. */
  sessions?: readonly Session[];
  /** Explicit projects for labels and search skip `useTodoProjects()` and its `/kb/projects` fetch. */
  projects?: readonly TodoProject[];
  /** Controlled link callback for a session target, replacing `linkAgentToTodo`. */
  onLinkSession?: (todo: Todo, session: Session) => Promise<void> | void;
  /** Controlled link callback for a project space target, replacing `linkSpaceToTodo`. */
  onLinkSpace?: (todo: Todo, space: LinkTargetSpace) => Promise<void> | void;
  /** Controlled unlink callback for a session target, replacing `unlinkAgentFromTodo`. */
  onUnlinkSession?: (todoId: string, session: Session) => Promise<void> | void;
  /** Controlled unlink callback for a project space target, replacing `unlinkSpaceFromTodo`. */
  onUnlinkSpace?: (todoId: string, space: LinkTargetSpace) => Promise<void> | void;
  /** Controlled create callback for "New to-do…", replacing `createTodo`. */
  onCreateTodo?: (title: string) => Promise<Todo>;
  /** Controlled refresh callback replacing `refreshTodos` / `refresh`. Omitted with controlled mutations skips global store refreshes. */
  onRefresh?: (scope: LinkTodoRefreshScope) => Promise<void> | void;
  /** Controlled legacy-spaces callback replacing `setSpacesSupported(false)`. */
  onSpacesUnsupported?: () => void;
  /** Controlled notification callback replacing `toast(message, tone)`. */
  onNotify?: (message: string, tone: LinkTodoNotifyTone) => void;
}

export function getLinkDialogTargetKey(space?: LinkTargetSpace, session?: Session): string {
  if (space) return `sp:${space.machineId}/${space.space}`;
  if (session) return `s:${session.machine_id}/${session.session_id}`;
  return "";
}

export function LinkTodoDialog(props: LinkTodoDialogProps) {
  if (props.todos !== undefined) {
    return <LinkTodoDialogView {...props} todos={props.todos} />;
  }
  return <DefaultLinkTodoDialog {...props} />;
}

function DefaultLinkTodoDialog(props: LinkTodoDialogProps) {
  const todos = useStore((s) => s.todos);
  return <LinkTodoDialogView {...props} todos={todos} />;
}

interface LinkTodoDialogViewProps extends Omit<LinkTodoDialogProps, "todos"> {
  todos: readonly Todo[];
}

function LinkTodoDialogView({
  open,
  session,
  space,
  onClose,
  todos,
  currentTodoId: controlledCurrentTodoId,
  sessions,
  projects,
  onLinkSession,
  onLinkSpace,
  onUnlinkSession,
  onUnlinkSpace,
  onCreateTodo,
  onRefresh,
  onSpacesUnsupported,
  onNotify,
}: LinkTodoDialogViewProps) {
  const uncompleted = useMemo(() => todos.filter((todo) => !todo.done_at), [todos]);
  const [creating, setCreating] = useState(false);
  const [newTitle, setNewTitle] = useState("");
  // Cancelling a new to-do puts the keyboard back on "New to-do…", not on the page under the dialog.
  const refocusNew = useRef(false);
  const cancelCreating = () => {
    refocusNew.current = true;
    setCreating(false);
  };

  // Reset dialog state and invalidate stale in-flight requests when opened or when target changes (#874 item 3).
  const targetKey = getLinkDialogTargetKey(space, session);
  const scopeKey = open ? targetKey : "";
  const mountedRef = useRef(true);
  const requestSeqRef = useRef(0);
  const activeRequestRef = useRef(0);
  const latestRef = useRef({ onClose, onNotify, onRefresh, onSpacesUnsupported });
  latestRef.current = { onClose, onNotify, onRefresh, onSpacesUnsupported };
  const [busyState, setBusyState] = useState<{ scope: string; token: number } | null>(null);
  const busy = busyState !== null && busyState.scope === scopeKey;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestSeqRef.current += 1;
      activeRequestRef.current = 0;
    };
  }, []);

  useLayoutEffect(() => {
    requestSeqRef.current += 1;
    activeRequestRef.current = 0;
    setBusyState(null);
    if (open) {
      setCreating(false);
      setNewTitle("");
    }
  }, [open, targetKey]);

  if (!session && !space) return null;

  const startRequest = (): number | null => {
    if (activeRequestRef.current !== 0) return null;
    const token = ++requestSeqRef.current;
    activeRequestRef.current = token;
    setBusyState({ scope: scopeKey, token });
    return token;
  };
  const isCurrent = (token: number) => mountedRef.current && activeRequestRef.current === token;
  const finishRequest = (token: number) => {
    if (activeRequestRef.current === token) {
      activeRequestRef.current = 0;
      if (mountedRef.current) setBusyState((prev) => (prev?.token === token ? null : prev));
    }
  };
  const notify = (message: string, tone: LinkTodoNotifyTone) => {
    if (latestRef.current.onNotify) latestRef.current.onNotify(message, tone);
    else toast(message, tone);
  };
  const markSpacesUnsupported = (controlledAction: boolean) => {
    if (latestRef.current.onSpacesUnsupported) latestRef.current.onSpacesUnsupported();
    else if (!controlledAction) setSpacesSupported(false);
  };
  const runRefresh = async (scope: LinkTodoRefreshScope, controlledAction: boolean) => {
    if (latestRef.current.onRefresh) {
      await latestRef.current.onRefresh(scope);
      return;
    }
    if (controlledAction) return;
    await refreshTodos();
    if (scope === "all") await refresh();
  };
  const closeDialog = () => latestRef.current.onClose();

  // Keep session.todo?.id as source for sessions so controls don't disappear if /todos hasn't loaded (#874 item 4)
  const currentTodoId =
    controlledCurrentTodoId !== undefined
      ? (controlledCurrentTodoId ?? undefined)
      : resolveLinkCurrentTodoId(space, session, todos as Todo[]);

  const handleSelect = async (todo: Todo) => {
    const token = startRequest();
    if (token === null) return;
    const controlledAction = Boolean(space ? onLinkSpace : onLinkSession);
    try {
      if (space) {
        if (onLinkSpace) await onLinkSpace(todo, space);
        else await linkSpaceToTodo(todo.id, space.machineId, space.space);
        if (!isCurrent(token)) return;
        await runRefresh("all", controlledAction);
        if (!isCurrent(token)) return;
        notify(t("linktododialog.space_linked", { title: todo.title }), "ok");
      } else if (session) {
        if (onLinkSession) await onLinkSession(todo, session);
        else await linkAgentToTodo(todo.id, session.machine_id, session.session_id);
        if (!isCurrent(token)) return;
        await runRefresh("all", controlledAction);
        if (!isCurrent(token)) return;
        notify(t("linktododialog.session_linked", { title: todo.title }), "ok");
      }
      closeDialog();
    } catch (e) {
      if (!isCurrent(token)) return;
      if (space && isLegacySpacesRouteError(e)) {
        markSpacesUnsupported(controlledAction);
        notify(t("linktododialog.space_unsupported"), "error");
      } else {
        notify(space ? t("linktododialog.space_link_failed", { error: errorText(e) }) : t("linktododialog.session_link_failed", { error: errorText(e) }), "error");
      }
      void Promise.resolve(runRefresh("todos", controlledAction)).catch(() => {});
    } finally {
      finishRequest(token);
    }
  };

  const handleUnlink = async () => {
    if (!currentTodoId) return;
    const token = startRequest();
    if (token === null) return;
    const controlledAction = Boolean(space ? onUnlinkSpace : onUnlinkSession);
    try {
      if (space) {
        if (onUnlinkSpace) await onUnlinkSpace(currentTodoId, space);
        else await unlinkSpaceFromTodo(currentTodoId, space.machineId, space.space);
        if (!isCurrent(token)) return;
        await runRefresh("all", controlledAction);
        if (!isCurrent(token)) return;
        notify(t("linktododialog.space_unlinked"), "ok");
      } else if (session) {
        if (onUnlinkSession) await onUnlinkSession(currentTodoId, session);
        else await unlinkAgentFromTodo(currentTodoId, session.machine_id, session.session_id);
        if (!isCurrent(token)) return;
        await runRefresh("all", controlledAction);
        if (!isCurrent(token)) return;
        notify(t("linktododialog.session_unlinked"), "ok");
      }
      closeDialog();
    } catch (e) {
      if (!isCurrent(token)) return;
      if (space && isLegacySpacesRouteError(e)) {
        markSpacesUnsupported(controlledAction);
      }
      notify(space ? t("linktododialog.space_unlink_failed", { error: errorText(e) }) : t("linktododialog.session_unlink_failed", { error: errorText(e) }), "error");
      void Promise.resolve(runRefresh("todos", controlledAction)).catch(() => {});
    } finally {
      finishRequest(token);
    }
  };

  const handleCreateAndLink = async () => {
    const trimmed = newTitle.trim();
    if (!trimmed) return;
    const token = startRequest();
    if (token === null) return;
    const linkControlled = Boolean(space ? onLinkSpace : onLinkSession);
    const anyControlled = Boolean(onCreateTodo) || linkControlled;
    try {
      const created = onCreateTodo ? await onCreateTodo(trimmed) : await createTodo(trimmed);
      if (!isCurrent(token)) return;
      try {
        if (space) {
          if (onLinkSpace) await onLinkSpace(created, space);
          else await linkSpaceToTodo(created.id, space.machineId, space.space);
          if (!isCurrent(token)) return;
          notify(t("linktododialog.space_created_linked", { title: created.title }), "ok");
        } else if (session) {
          if (onLinkSession) await onLinkSession(created, session);
          else await linkAgentToTodo(created.id, session.machine_id, session.session_id);
          if (!isCurrent(token)) return;
          notify(t("linktododialog.session_created_linked", { title: created.title }), "ok");
        }
      } catch (linkErr) {
        if (!isCurrent(token)) return;
        if (space && isLegacySpacesRouteError(linkErr)) {
          markSpacesUnsupported(linkControlled);
          notify(t("linktododialog.created_space_unsupported", { error: errorText(linkErr) }), "error");
        } else {
          notify(space ? t("linktododialog.created_space_link_failed", { error: errorText(linkErr) }) : t("linktododialog.created_session_link_failed", { error: errorText(linkErr) }), "error");
        }
      }
      await runRefresh("all", anyControlled);
      if (!isCurrent(token)) return;
      closeDialog();
    } catch (e) {
      if (!isCurrent(token)) return;
      notify(t("linktododialog.create_failed", { error: errorText(e) }), "error");
    } finally {
      finishRequest(token);
    }
  };

  const dialogTitle = space
    ? currentTodoId
      ? t("linktododialog.title_move")
      : t("linktododialog.title_space_link")
    : currentTodoId
      ? t("linktododialog.title_move")
      : t("linktododialog.title_session_link");

  const description = space
    ? t("linktododialog.space_description", { space: space.space })
    : t("linktododialog.session_description", { name: session?.title || session?.session_id || "" });

  return (
    <Dialog open={open} onClose={onClose} title={dialogTitle} width={420}>
      <div className="flex flex-col gap-3">
        <div className="text-[12.5px] text-ink-3">{description}</div>

        {uncompleted.length === 0 ? (
          <div className="py-4 text-center text-meta text-ink-3">{t("linktododialog.empty")}</div>
        ) : (
          <TodoChoices
            todos={uncompleted}
            currentTodoId={currentTodoId}
            busy={busy}
            resetKey={scopeKey}
            sessions={sessions}
            projects={projects}
            onPick={(todo) => void handleSelect(todo)}
          />
        )}

        {creating ? (
          <div className="flex flex-col gap-2 rounded-control border border-line bg-inset p-2.5">
            <input
              type="text"
              autoFocus
              value={newTitle}
              disabled={busy}
              onChange={(e) => setNewTitle(e.target.value)}
              onKeyDown={(e) => {
                if (composing(e.nativeEvent)) return;
                if (e.key === "Enter") {
                  e.preventDefault();
                  void handleCreateAndLink();
                } else if (e.key === "Escape") {
                  // only the new to-do is cancelled: the dialog, and the list under it, stay for the next Escape
                  e.stopPropagation();
                  cancelCreating();
                }
              }}
              placeholder={t("linktododialog.new_placeholder")}
              className="w-full bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-3"
            />
            <div className="flex items-center justify-end gap-2">
              <Button tone="quiet" className="px-2 py-1 text-[12px] whitespace-nowrap" onClick={cancelCreating} disabled={busy}>
                {t("common.cancel")}
              </Button>
              <Button tone="primary" className="px-2 py-1 text-[12px] whitespace-nowrap" onClick={() => void handleCreateAndLink()} disabled={busy || !newTitle.trim()}>
                {t("linktododialog.create_and_link")}
              </Button>
            </div>
          </div>
        ) : (
          <button
            type="button"
            disabled={busy}
            ref={(el) => {
              if (el && refocusNew.current) {
                refocusNew.current = false;
                el.focus();
              }
            }}
            onClick={() => {
              setNewTitle("");
              setCreating(true);
            }}
            className="flex items-center gap-1.5 rounded-control px-2 py-1.5 text-left text-[12.5px] font-medium text-accent hover:bg-hover whitespace-nowrap"
          >
            <Icon name="plus" size={12} stroke={2.5} />
            <span>{t("linktododialog.new_todo")}</span>
          </button>
        )}

        <div className="mt-2 flex justify-between gap-2">
          {currentTodoId ? (
            <Button tone="quiet" onClick={handleUnlink} disabled={busy} className="text-red hover:bg-red-tint/50 whitespace-nowrap">
              {space ? t("linktododialog.space_unlink") : t("linktododialog.session_unlink")}
            </Button>
          ) : (
            <div />
          )}
          <Button tone="secondary" onClick={onClose} className="whitespace-nowrap">
            {t("common.cancel")}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

interface TodoChoicesProps {
  todos: Todo[];
  currentTodoId?: string;
  busy: boolean;
  resetKey: string;
  sessions?: readonly Session[];
  projects?: readonly TodoProject[];
  onPick: (todo: Todo) => void;
}

interface TodoChoicesWithSessionsProps extends Omit<TodoChoicesProps, "sessions"> {
  sessions: readonly Session[];
}

interface TodoChoicesViewProps extends Omit<TodoChoicesWithSessionsProps, "projects"> {
  projects: readonly TodoProject[];
}

/** The open to-dos to pick from. Each row says, under its title, where the to-do stands (the To-dos page's word), its
 *  project and when it last changed, so two with one title can be told apart; a long list gets a search field. */
function TodoChoices(props: TodoChoicesProps) {
  if (props.sessions !== undefined) {
    return <TodoChoicesProjects {...props} sessions={props.sessions} />;
  }
  return <DefaultTodoChoicesSessions {...props} />;
}

function DefaultTodoChoicesSessions(props: TodoChoicesProps) {
  const sessions = useAgentSessions();
  return <TodoChoicesProjects {...props} sessions={sessions} />;
}

function TodoChoicesProjects(props: TodoChoicesWithSessionsProps) {
  if (props.projects !== undefined) {
    return <TodoChoicesView {...props} projects={props.projects} />;
  }
  return <DefaultTodoChoicesProjects {...props} />;
}

function DefaultTodoChoicesProjects(props: TodoChoicesWithSessionsProps) {
  const projects = useTodoProjects();
  return <TodoChoicesView {...props} projects={projects} />;
}

function TodoChoicesView({ todos, currentTodoId, busy, resetKey, sessions, projects, onPick }: TodoChoicesViewProps) {
  const lookup = useMemo(() => makeSessionLookup(sessions as Session[]), [sessions]);
  const project = useMemo(() => projectNames(projects as TodoProject[]), [projects]);
  const now = useNow(60_000);
  const [query, setQuery] = useState("");
  useLayoutEffect(() => {
    setQuery("");
  }, [resetKey]);
  const searchable = todos.length >= LINK_SEARCH_FROM;
  const shown = searchable ? matchTodos(todos, query, project) : todos;
  return (
    <>
      {searchable && (
        <SearchField
          tone="inset"
          autoFocus={hasFinePointer()}
          value={query}
          onChange={setQuery}
          onKeyDown={(e) => {
            // Escape empties the search first (the dialog stays); with nothing typed it reaches the dialog and closes it
            if (e.key !== "Escape" || !query || composing(e.nativeEvent)) return;
            e.stopPropagation();
            setQuery("");
          }}
          placeholder={t("linktododialog.search")}
        />
      )}
      {shown.length === 0 ? (
        <div className="py-4 text-center text-meta text-ink-3">{t("linktododialog.no_match")}</div>
      ) : (
        <div className="flex max-h-72 flex-col gap-1 overflow-y-auto rounded-control border border-line p-1" data-link-todos>
          {shown.map((todo) => {
            const isCurrent = todo.id === currentTodoId;
            const status = todoStatus(todo, lookup);
            // a waiting one's word says what it waits on, attention only on the owner (#11090)
            const look = todoLook(todo, status, waitOf(todo, status, lookup));
            const where = [todo.project ? project(todo.project) : "", ago(todo.updated_at, now, "narrow")].filter(Boolean);
            return (
              <button
                key={todo.id}
                type="button"
                disabled={busy}
                onClick={() => onPick(todo)}
                className={cx(
                  "flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left transition-colors whitespace-nowrap",
                  isCurrent ? "bg-accent-tint text-accent" : "hover:bg-hover text-ink",
                )}
              >
                <span className="min-w-0 flex-1">
                  <span className={cx("block truncate text-meta", isCurrent && "font-medium")}>{todo.title}</span>
                  <span className="block truncate text-xs text-ink-3">
                    <span className={TODO_TONE_CLASS[look.tone]}>{look.label}</span>
                    {where.map((part) => ` · ${part}`).join("")}
                  </span>
                </span>
                {isCurrent && <Icon name="check" size={13} stroke={2.5} />}
              </button>
            );
          })}
        </div>
      )}
    </>
  );
}
