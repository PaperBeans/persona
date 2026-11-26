// Thin client consumer for the server-owned `board` projection tree under todos_v2 (#15800).
// Consumes `board.status_groups`, `board.direction_groups`, `board.sidebar_tree`, and `board.counts`
// directly without re-running client-side triage, goal folding, or direction derivation rules.

import { t } from "../../i18n";
import { splitAgentSessions, type AgentMutes } from "../../lib/agent-groups";
import type { Direction } from "../../lib/direction-tree";
import { activityOf, stateOf } from "../../lib/format";
import type { GoalChildEntry, GoalFoldResult, GoalSummary } from "../../lib/todo-goal-fold";
import type { ResolvedDirectionSection } from "../../lib/todo-reorganize";
import {
  agentGroupLabel,
  agentTreeState,
  nestAgents,
  STALE_MS,
  worstTreeState,
  type LooseGroup,
  type TodoTree,
  type TreeState,
  type TreeTodo,
} from "../../lib/todo-tree";
import type { Triage, TriageGroup, TriageResult, TriageRule, TriageSection } from "../../lib/todo-triage";
import { todoLook, waitOf } from "../../lib/todo-wait-reason";
import {
  makeSessionLookup,
  todoStatus,
  type Todo,
  type TodoBoardProjection,
  type TodoBoardSectionKey,
  type TodoStatus,
} from "../../lib/todos";
import type { Machine, Session } from "../../lib/types";

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

function defaultSessionCompare(mutes: AgentMutes) {
  const rank = (s: Session): number => {
    const st = activityOf(s);
    const waiting =
      (st === "idle" || st === "unknown") &&
      Boolean(s.loop || s.background?.length || s.subagents?.some((a) => a.running));
    const isMuted =
      mutes.sessions.includes(`${s.machine_id}/${s.session_id}`) ||
      Boolean(s.space_name && mutes.spaces.includes(`${s.machine_id}/${s.space_name}`));
    if (st === "needs_you") return 0;
    if (isMuted && (st === "working" || st === "unread" || waiting)) return 4;
    if (st === "unread") return 1;
    if (st === "working") return 2;
    if (waiting) return 3;
    return 5;
  };
  const ts = (s: Session): number => {
    const raw = (stateOf(s) === "working" ? s.state_since || s.updated_at : s.updated_at) ?? "";
    return raw ? Date.parse(raw) || 0 : 0;
  };
  return (a: Session, b: Session): number => {
    const rA = rank(a);
    const rB = rank(b);
    if (rA !== rB) return rA - rB;
    const diff = ts(b) - ts(a);
    if (diff !== 0) return diff;
    return a.session_id.localeCompare(b.session_id) || a.machine_id.localeCompare(b.machine_id);
  };
}

function latestSessionActivity(sessions: readonly Session[]): number {
  let at = 0;
  for (const s of sessions) {
    at = Math.max(at, Date.parse(s.updated_at ?? "") || 0, Date.parse(s.state_since ?? "") || 0);
  }
  return at;
}

function sidebarNodeState(
  section: TodoBoardSectionKey,
  needsYouCount: number,
  sessions: readonly Session[],
): TreeState {
  if (section === "needs_you" || needsYouCount > 0) return "needs_you";
  if (section === "in_progress") {
    const states = sessions.map(agentTreeState);
    if (states.includes("needs_you")) return "needs_you";
    return states.includes("bg_waiting") && !states.includes("working") ? "bg_waiting" : "working";
  }
  if (section === "waiting") {
    const states = sessions.map(agentTreeState);
    if (states.includes("needs_you")) return "needs_you";
    return "bg_waiting";
  }
  return worstTreeState(sessions.map(agentTreeState));
}

/**
 * Thinly adapts server `board.sidebar_tree` for `Sidebar.tsx` By-to-do view under `todos_v2 === true`.
 * Preserves server top-level ordering, folded child aggregation (`child_ids`, `folded_count`), and
 * surfaced blockers (`surfaced_child_ids`) without re-running client sorting or goal-folding derivation.
 */
export function consumeBoardSidebarTree(
  all: Session[],
  machines: Machine[],
  todos: readonly Todo[],
  board: TodoBoardProjection,
  mutes: AgentMutes = { spaces: [], sessions: [] },
  now = Date.now(),
  { keepEmpty = false, compareSessions }: { keepEmpty?: boolean; compareSessions?: (a: Session, b: Session) => number } = {},
): TodoTree {
  const { live } = splitAgentSessions(all, machines);
  const lookup = makeSessionLookup(live);
  const byId = new Map<string, Todo>();
  for (const td of todos) byId.set(td.id, td);

  const liveByKey = new Map<string, Session>();
  for (const s of live) liveByKey.set(`${s.machine_id}/${s.session_id}`, s);

  // Map each live session to its home to-do ID.
  const homeOf = new Map<string, string>();
  for (const [key, s] of liveByKey) {
    const own = s.todo?.id;
    if (own && byId.has(own)) {
      homeOf.set(key, own);
      continue;
    }
    const owner = todos.find(
      (td) => !td.done_at && (td.agents ?? []).some((a) => !a.released_at && `${a.machine_id}/${a.session_id}` === key),
    );
    if (owner) homeOf.set(key, owner.id);
  }

  // Group live sessions by their owning to-do ID.
  const sessionsByTodo = new Map<string, Session[]>();
  for (const [key, todoId] of homeOf) {
    const s = liveByKey.get(key);
    if (!s) continue;
    const list = sessionsByTodo.get(todoId);
    if (list) list.push(s);
    else sessionsByTodo.set(todoId, [s]);
  }

  const byRank = compareSessions ?? defaultSessionCompare(mutes);
  const assignedSessionKeys = new Set<string>();
  const treeTodos: TreeTodo[] = [];

  for (const node of board.sidebar_tree) {
    const todo = byId.get(node.todo_id);
    if (!todo) continue;

    // Aggregate sessions from this node and its folded children (surfaced children are their own top-level nodes).
    const surfacedSet = new Set(node.surfaced_child_ids ?? []);
    const memberIds = [node.todo_id, ...(node.child_ids ?? []).filter((cid) => !surfacedSet.has(cid))];
    const seenKeys = new Set<string>();
    const nodeSessions: Session[] = [];

    for (const id of memberIds) {
      for (const s of sessionsByTodo.get(id) ?? []) {
        const key = `${s.machine_id}/${s.session_id}`;
        if (seenKeys.has(key) || assignedSessionKeys.has(key)) continue;
        seenKeys.add(key);
        nodeSessions.push(s);
      }
    }

    if (nodeSessions.length === 0) continue;
    for (const key of seenKeys) assignedSessionKeys.add(key);
    nodeSessions.sort(byRank);

    const status: TodoStatus = todoStatus(todo, lookup);
    const wait = waitOf(todo, status, lookup);
    const state = sidebarNodeState(node.section, node.needs_you_count, nodeSessions);
    const statusLabel = todoLook(todo, status, wait).label;

    const allChildIds = [...(node.child_ids ?? []), ...(node.surfaced_child_ids ?? [])];
    const totalChildren = Math.max(allChildIds.length, node.folded_count + (node.surfaced_child_ids?.length ?? 0));
    let goalProgress: { done: number; total: number } | undefined;
    if (totalChildren > 0) {
      const doneChildren = allChildIds.reduce((n, cid) => {
        const child = byId.get(cid);
        return n + (child?.done_at || child?.view?.section === "done" ? 1 : 0);
      }, 0);
      goalProgress = { done: doneChildren, total: totalChildren };
    }

    const folded =
      node.section !== "needs_you" &&
      node.needs_you_count === 0 &&
      (status === "done" || (state === "done" && now - latestSessionActivity(nodeSessions) > STALE_MS));

    treeTodos.push({
      key: `todo-tree:todo:${todo.id}`,
      todoId: todo.id,
      name: todo.title,
      status,
      statusLabel,
      ...(wait ? { wait } : {}),
      state,
      ...(goalProgress ? { goalProgress } : {}),
      agents: nestAgents(nodeSessions, todo, now),
      sessions: nodeSessions,
      folded,
    });
  }

  const unassigned = live.filter((s) => !assignedSessionKeys.has(`${s.machine_id}/${s.session_id}`));
  unassigned.sort(byRank);

  const loose: LooseGroup[] = [];
  if (unassigned.length > 0 || keepEmpty) {
    const agents = nestAgents(unassigned, undefined, now);
    const state = worstTreeState(agents.map((a) => a.state));
    const label = agentGroupLabel(unassigned, state);
    loose.push({
      key: "todo:none",
      name: t("lib-todos.unassigned"),
      hint: t("lib-todos.unassigned_hint"),
      state,
      ...(label ? { label } : {}),
      agents,
      sessions: unassigned,
      folded: treeTodos.length > 0 && state !== "needs_you",
    });
  }

  return { projects: [], todos: treeTodos, loose };
}
