// Thin client consumer for the server-owned `board` projection tree under todos_v2 (#15800).
// Consumes `board.status_groups`, `board.direction_groups`, `board.sidebar_tree`, and `board.counts`
// directly without re-running client-side triage, goal folding, or direction derivation rules.

import type { Direction } from "../../lib/direction-tree";
import type { GoalChildEntry, GoalFoldResult, GoalSummary } from "../../lib/todo-goal-fold";
import type { ResolvedDirectionSection } from "../../lib/todo-reorganize";
import type { Triage, TriageGroup, TriageResult, TriageRule, TriageSection } from "../../lib/todo-triage";
import {
  type Todo,
  type TodoBoardProjection,
  type TodoBoardSectionKey,
} from "../../lib/todos";

const ALL_TRIAGE_SECTIONS: readonly TriageSection[] = [
  "waiting",
  "working",
  "parked",
  "other",
  "just_done",
  "shelved",
  "done",
  "cancelled",
];

/** Maps a server `board.status_groups[*].key` to the Web To-dos section identifier. */
export function boardSectionToTriageSection(key: TodoBoardSectionKey): TriageSection {
  switch (key) {
    case "needs_you":
      return "waiting";
    case "in_progress":
      return "working";
    case "pending_review":
      return "just_done";
    case "waiting":
      return "parked";
    case "someday":
      return "shelved";
    case "done":
      return "done";
    default:
      return "other";
  }
}

function boardSectionDefaultRule(key: TodoBoardSectionKey): TriageRule {
  switch (key) {
    case "needs_you":
      return "owner_decides";
    case "in_progress":
      return "agent_running";
    case "pending_review":
      return "finished_unseen";
    case "waiting":
      return "parked";
    case "someday":
      return "stale";
    case "done":
      return "closed";
    default:
      return "rest";
  }
}

function emptySections(): Record<TriageSection, TriageGroup[]> {
  return {
    waiting: [],
    working: [],
    parked: [],
    other: [],
    just_done: [],
    shelved: [],
    done: [],
    cancelled: [],
  };
}

function todoTimestamp(todo: Todo): number {
  const ts = Date.parse(todo.updated_at || todo.created_at || "");
  return Number.isNaN(ts) ? 0 : ts;
}

function isChildDoneFromBoard(todo: Todo, boardSections: ReadonlyMap<string, TriageSection>): boolean {
  const sec = boardSections.get(todo.id);
  if (sec !== undefined) return sec === "done" || sec === "cancelled";
  return Boolean(todo.done_at) || todo.view?.section === "done";
}

/** Builds a lookup map of `todo_id -> TriageSection` directly from `board.status_groups`. */
export function boardSectionMap(board: TodoBoardProjection, byId?: ReadonlyMap<string, Todo>): Map<string, TriageSection> {
  const map = new Map<string, TriageSection>();
  // First pass: every top-level entry (including surfaced blocked children) gets its exact server section.
  for (const group of board.status_groups) {
    const sec = boardSectionToTriageSection(group.key);
    for (const entry of group.entries) {
      if (!map.has(entry.todo_id)) map.set(entry.todo_id, sec);
    }
  }
  // Second pass: folded children not listed at top level inherit done if closed, else their parent's section.
  for (const group of board.status_groups) {
    const parentSec = boardSectionToTriageSection(group.key);
    for (const entry of group.entries) {
      for (const cid of entry.folded_child_ids ?? []) {
        if (map.has(cid)) continue;
        const child = byId?.get(cid);
        map.set(cid, child?.done_at || child?.view?.section === "done" ? "done" : parentSec);
      }
    }
  }
  return map;
}

export interface BoardStatusConsumption {
  /** GoalFoldResult-compatible projection consumed directly by `Todos.tsx`. */
  goalFold: GoalFoldResult;
  /** Exact status-section display order from `board.status_groups`. */
  sectionOrder: TriageSection[];
  /** Displayed count per section (server `visible_count` when unfiltered, filtered row count otherwise). */
  sectionCounts: Record<TriageSection, number>;
  /** Header "Needs you" count (server `board.counts.needs_you` when unfiltered, filtered count otherwise). */
  needsYouCount: number;
}

/**
 * Thinly adapts server `board.status_groups` and `board.counts` for `Todos.tsx` under `todos_v2 === true`.
 * Preserves server section ordering, entry ordering, top-level goal folding (`folded_child_ids` / `folded_count`),
 * and surfaced blocked children (`surfaced_child_ids`) without re-running client triage or folding heuristics.
 */
export function consumeBoardStatusGroups(
  todos: readonly Todo[],
  board: TodoBoardProjection,
  matchesFilter?: (todo: Todo) => boolean,
): BoardStatusConsumption {
  const byId = new Map<string, Todo>();
  for (const td of todos) byId.set(td.id, td);

  const sectionByTodo = boardSectionMap(board, byId);
  const sections = emptySections();
  const sectionOrder: TriageSection[] = [];
  const goals = new Map<string, GoalSummary>();
  const surfacedParent = new Map<string, Todo>();
  const sectionCounts = Object.fromEntries(ALL_TRIAGE_SECTIONS.map((s) => [s, 0])) as Record<TriageSection, number>;

  let allNeedsYouResolved = true;

  for (const group of board.status_groups) {
    const sec = boardSectionToTriageSection(group.key);
    const rule = boardSectionDefaultRule(group.key);
    if (!sectionOrder.includes(sec)) sectionOrder.push(sec);

    let allEntriesResolved = true;

    for (const entry of group.entries) {
      const todo = byId.get(entry.todo_id);
      if (!todo) {
        allEntriesResolved = false;
        if (group.key === "needs_you") allNeedsYouResolved = false;
        continue;
      }

      const rawFolded = (entry.folded_child_ids ?? [])
        .map((cid) => byId.get(cid))
        .filter((td): td is Todo => Boolean(td));
      const rawSurfaced = (entry.surfaced_child_ids ?? [])
        .map((cid) => byId.get(cid))
        .filter((td): td is Todo => Boolean(td));

      const folded = matchesFilter ? rawFolded.filter(matchesFilter) : rawFolded;
      const surfaced = matchesFilter ? rawSurfaced.filter(matchesFilter) : rawSurfaced;
      const parentMatches = !matchesFilter || matchesFilter(todo);

      if (!parentMatches) {
        // When a local UI filter (e.g. search) matches a folded child but not its parent, keep the child visible.
        for (const child of folded) {
          const childSec = sectionByTodo.get(child.id) ?? sec;
          const tr: Triage = { section: childSec, rule, last: todoTimestamp(child) };
          sections[childSec].push({
            key: child.id,
            section: childSec,
            lead: child,
            members: [child],
            triage: new Map([[child.id, tr]]),
            last: tr.last,
          });
        }
        continue;
      }

      const tr: Triage = { section: sec, rule, last: todoTimestamp(todo) };
      sections[sec].push({
        key: todo.id,
        section: sec,
        lead: todo,
        members: [todo],
        triage: new Map([[todo.id, tr]]),
        last: tr.last,
      });

      for (const sc of surfaced) {
        surfacedParent.set(sc.id, todo);
      }

      const serverFoldedCount = entry.folded_count ?? 0;
      if (folded.length > 0 || surfaced.length > 0 || (!matchesFilter && serverFoldedCount > 0)) {
        const children: GoalChildEntry[] = [
          ...surfaced.map((c) => ({
            todo: c,
            section: sectionByTodo.get(c.id) ?? "waiting",
            done: isChildDoneFromBoard(c, sectionByTodo),
            blocker: true,
          })),
          ...folded.map((c) => ({
            todo: c,
            section: sectionByTodo.get(c.id) ?? sec,
            done: isChildDoneFromBoard(c, sectionByTodo),
            blocker: false,
          })),
        ];
        const done = children.reduce((n, c) => n + (c.done ? 1 : 0), 0);
        const total = matchesFilter
          ? children.length
          : Math.max(children.length, serverFoldedCount + surfaced.length);
        goals.set(todo.id, {
          parentId: todo.id,
          done,
          total,
          blockedCount: surfaced.length,
          children,
          blockers: children.filter((c) => c.blocker),
        });
      }
    }

    sectionCounts[sec] =
      !matchesFilter && allEntriesResolved && typeof group.visible_count === "number"
        ? group.visible_count
        : sections[sec].length;
  }

  for (const sec of ALL_TRIAGE_SECTIONS) {
    if (!sectionOrder.includes(sec)) sectionOrder.push(sec);
  }

  const todoCounts = Object.fromEntries(
    ALL_TRIAGE_SECTIONS.map((sec) => [sec, sections[sec].length]),
  ) as Record<TriageSection, number>;

  const open = !matchesFilter
    ? board.counts.open_total
    : todoCounts.waiting + todoCounts.working + todoCounts.just_done + todoCounts.parked + todoCounts.other;

  const needsYouCount =
    !matchesFilter && allNeedsYouResolved
      ? board.counts.needs_you
      : sections.waiting.length;

  const triaged: TriageResult = {
    sections,
    open,
    shelved: !matchesFilter ? board.counts.someday : todoCounts.shelved,
    finished: !matchesFilter ? board.counts.done : todoCounts.done,
    cancelled: todoCounts.cancelled,
    todoCounts,
  };

  return {
    goalFold: {
      triaged,
      goals,
      surfacedParent,
    },
    sectionOrder,
    sectionCounts,
    needsYouCount,
  };
}

/**
 * Thinly adapts server `board.direction_groups` for `Todos.tsx` By-direction view under `todos_v2 === true`.
 * Preserves server group order, member order, and precomputed direction counts without client re-grouping.
 */
export function consumeBoardDirectionGroups({
  todos,
  board,
  matchesFilter,
  includeDone = false,
  resolveFallbackName,
  ungroupedLabel,
}: {
  todos: readonly Todo[];
  board: TodoBoardProjection;
  matchesFilter?: (todo: Todo) => boolean;
  includeDone?: boolean;
  resolveFallbackName?: (id: string) => string | undefined;
  ungroupedLabel: string;
}): ResolvedDirectionSection[] {
  const byId = new Map<string, Todo>();
  for (const td of todos) byId.set(td.id, td);

  const sectionByTodo = boardSectionMap(board, byId);
  const out: ResolvedDirectionSection[] = [];

  for (const group of board.direction_groups) {
    const members: Todo[] = [];
    for (const id of group.todo_ids) {
      const td = byId.get(id);
      if (!td) continue;
      if (!includeDone && sectionByTodo.get(id) === "done") continue;
      if (matchesFilter && !matchesFilter(td)) continue;
      members.push(td);
    }
    if (members.length === 0) continue;

    const isUngrouped = !group.direction_id;
    const title = isUngrouped
      ? (group.label?.trim() || ungroupedLabel)
      : (group.label?.trim() || resolveFallbackName?.(group.direction_id)?.trim() || group.direction_id);

    const d: Direction = {
      id: isUngrouped ? "__ungrouped__" : group.direction_id,
      name: title,
      ...(isUngrouped ? { secretary: true } : {}),
      counts: {
        working: group.counts.in_progress,
        needs_you: group.counts.needs_you,
        stuck: 0,
        review: group.counts.pending_review,
      },
      running: group.counts.in_progress > 0,
    };

    out.push({ d, todos: members, title });
  }

  return out;
}

export { consumeBoardSidebarTree } from "./board-sidebar-tree";
