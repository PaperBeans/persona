import { useMemo, useState } from "react";
import { formatDate, t } from "../i18n";
import { foldedState } from "../lib/child-sessions";
import { plainMarkdown } from "../lib/plain-markdown";
import { waitName } from "../lib/state-labels";
import { progressLine } from "../lib/supervision";
import { waitsOnOwner, type Triage } from "../lib/todo-triage";
import { mostUrgentAgent, responsibleOf, taskNow, type SessionLookup, type Todo } from "../lib/todos";
import { cx } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { fieldClass, Tag } from "./assistant/parts";

const HOUR_MS = 3_600_000;
export const TODO_BOARD_HOURS_KEY = "persona.todos.board.staleHours";

type Progress = { at: string; text: string };
type BoardRow = { todo: Todo; progress?: Progress; waiting: boolean; stale: boolean; responsible: string };
type BoardGroup = { project: string; name: string; rows: BoardRow[]; waiting: number; stale: number };

function timestamp(value: string | undefined): string | undefined {
  return value && Number.isFinite(Date.parse(value)) ? value : undefined;
}

function oneLine(value: string | undefined): string {
  return (value ?? "").replace(/\s+/g, " ").trim();
}

function firstNoteLine(value: string): string {
  const line = value.split(/\r?\n/).find((part) => part.trim()) ?? "";
  return oneLine(plainMarkdown(line));
}

/** The latest report and its own moment, never a summary paired with an unrelated status change. */
function latestProgress(todo: Todo, lookup: SessionLookup): Progress | undefined {
  const candidates: Progress[] = [];
  const reportAt = timestamp(todo.supervision?.progress?.at);
  const report = oneLine(progressLine(todo.supervision));
  if (reportAt && report) candidates.push({ at: reportAt, text: report });
  let linkedProgress = false;
  for (const agent of todo.agents ?? []) {
    if (agent.released_at) continue;
    const session = lookup(agent.machine_id, agent.session_id);
    const current = taskNow(session, session ? foldedState(session) === "working" : false);
    if (!session || !current) continue;
    const text = oneLine(current.kind === "words" ? plainMarkdown(current.text) : current.text);
    const at = timestamp(current.kind === "step" ? current.since : undefined) ?? timestamp(session.activity_at) ?? timestamp(session.updated_at) ?? timestamp(session.state_since);
    if (at && text) {
      candidates.push({ at, text });
      linkedProgress = true;
    }
  }
  // #12712: with no linked-Agent report, an external responsible party's notes are its progress note.
  const external = responsibleOf(todo);
  const externalAt = timestamp(todo.updated_at);
  const externalNote = firstNoteLine(todo.notes ?? "");
  if (external && !linkedProgress && !(reportAt && report) && externalAt && externalNote) candidates.push({ at: externalAt, text: externalNote });
  return candidates.reduce<Progress | undefined>((latest, candidate) => !latest || Date.parse(candidate.at) > Date.parse(latest.at) ? candidate : latest, undefined);
}

function responsible(todo: Todo, lookup: SessionLookup): string {
  const stored = responsibleOf(todo)?.name;
  if (stored) return stored;
  const urgent = mostUrgentAgent(todo, lookup);
  return oneLine(urgent?.agent.title) || oneLine(urgent?.session?.title) || t("todos.board_unassigned");
}

function hoursValue(raw: string): number | undefined {
  if (!raw.trim()) return undefined;
  const value = Number(raw);
  return Number.isFinite(value) && value > 0 ? value : undefined;
}

function storedHours(): number {
  try {
    return hoursValue(localStorage.getItem(TODO_BOARD_HOURS_KEY) ?? "") ?? 72;
  } catch {
    return 72;
  }
}

function saveHours(value: number) {
  try {
    localStorage.setItem(TODO_BOARD_HOURS_KEY, String(value));
  } catch {
    // Storage can be unavailable or full; the valid choice still applies to this page.
  }
}

/** A read-only projection of the To-dos already read by the page. It starts no requests of its own. */
export function TodoBoard({
  todos,
  lookup,
  triages,
  projectName,
  tick,
  onOpen,
}: {
  todos: readonly Todo[];
  lookup: SessionLookup;
  triages: ReadonlyMap<string, Triage>;
  projectName: (slug: string) => string;
  tick: number;
  onOpen: (id: string) => void;
}) {
  const [hours, setHours] = useState(storedHours);
  const [draft, setDraft] = useState(() => String(storedHours()));
  const [invalid, setInvalid] = useState(false);
  const groups = useMemo(() => {
    const now = Date.now();
    const rows: BoardRow[] = todos.map((todo) => {
      const progress = latestProgress(todo, lookup);
      const baseline = Date.parse(progress?.at ?? todo.created_at);
      const triage = triages.get(todo.id);
      return {
        todo,
        progress,
        waiting: triage ? waitsOnOwner(todo, triage) === "owner_decides" : false,
        stale: Number.isFinite(baseline) && now - baseline > hours * HOUR_MS,
        responsible: responsible(todo, lookup),
      };
    });
    const byProject = new Map<string, BoardRow[]>();
    for (const row of rows) {
      const project = row.todo.project?.trim() ?? "";
      byProject.set(project, [...(byProject.get(project) ?? []), row]);
    }
    const named: BoardGroup[] = [...byProject].map(([project, rows]) => ({
      project,
      name: project ? projectName(project) : t("todos.board_no_project"),
      rows,
      waiting: rows.filter((row) => row.waiting).length,
      stale: rows.filter((row) => row.stale).length,
    }));
    return named.sort((a, b) => !a.project ? 1 : !b.project ? -1 : a.name.localeCompare(b.name));
  }, [todos, lookup, triages, projectName, hours, tick]);

  const changeHours = (raw: string) => {
    setDraft(raw);
    const value = hoursValue(raw);
    if (value === undefined) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    setHours(value);
    saveHours(value);
  };

  return (
    <div data-testid="todos-board" className="flex flex-col gap-3 p-1">
      <div className="flex flex-wrap items-end justify-between gap-2 px-1">
        <p className="max-w-prose text-meta text-ink-3">{t("todos.board_stale_explanation")}</p>
        <label className="flex items-center gap-2 whitespace-nowrap text-label text-ink-2">
          <span>{t("todos.board_stale_hours")}</span>
          <input
            type="number"
            step="any"
            inputMode="decimal"
            data-testid="todo-board-stale-hours"
            aria-label={t("todos.board_stale_hours")}
            aria-invalid={invalid || undefined}
            aria-describedby={invalid ? "todo-board-hours-error" : undefined}
            className={cx(fieldClass, "w-24 max-md:h-11")}
            value={draft}
            onChange={(event) => changeHours(event.target.value)}
          />
        </label>
      </div>
      {invalid && <p id="todo-board-hours-error" role="alert" className="px-1 text-label text-red">{t("todos.board_invalid_hours")}</p>}
      {groups.length === 0 ? (
        <p className="px-1 py-8 text-center text-meta text-ink-3">{t("todos.board_empty")}</p>
      ) : groups.map((group) => (
        <section
          key={group.project}
          data-testid="todo-board-group"
          data-project={group.project}
          data-open-count={group.rows.length}
          data-waiting-count={group.waiting}
          data-stale-count={group.stale}
          className="overflow-hidden rounded-card border border-line bg-surface"
        >
          <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-2">
            <span className="flex min-w-0 items-center gap-1.5 text-card-title font-semibold text-ink">
              <Icon name="folder" size={13} className="shrink-0 text-ink-3" />
              <span className="truncate">{group.name}</span>
            </span>
            <span className="flex flex-wrap gap-x-2 text-label text-ink-3 tabular-nums">
              <span>{t("todos.board_open_count", { count: group.rows.length })}</span>
              <span>{t("todos.board_waiting_count", { count: group.waiting, state: waitName("owner") })}</span>
              <span>{t("todos.board_stale_count", { count: group.stale })}</span>
            </span>
          </header>
          <div className="divide-y divide-line">
            {group.rows.map((row) => (
              <div
                key={row.todo.id}
                role="button"
                tabIndex={0}
                data-todo={row.todo.id}
                onClick={() => onOpen(row.todo.id)}
                onKeyDown={(event) => {
                  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
                  event.preventDefault();
                  onOpen(row.todo.id);
                }}
                className="flex cursor-pointer flex-col gap-1.5 px-3 py-2.5 transition-colors hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent"
              >
                <span className="flex flex-wrap items-center gap-1.5">
                  <span className="min-w-0 flex-1 truncate text-card-title font-semibold text-ink">{row.todo.title}</span>
                  {row.waiting && <Tag tone="accent"><span data-testid="board-owner">{waitName("owner")}</span></Tag>}
                  {row.stale && <Tag tone="orange"><span data-testid="board-stale">{t("todos.board_stale")}</span></Tag>}
                </span>
                <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-label text-ink-2">
                  <span data-testid="board-responsible">{row.responsible}</span>
                  {row.progress ? (
                    <time className="text-ink-3" dateTime={row.progress.at}>{formatDate(row.progress.at, { dateStyle: "medium", timeStyle: "short" })}</time>
                  ) : null}
                </span>
                <span data-testid="board-progress" className="truncate text-meta text-ink-3">
                  {row.progress?.text ?? t("todos.board_no_progress")}
                </span>
              </div>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
