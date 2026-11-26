// The sidebar's By-to-do tree from the server-owned `board.sidebar_tree` under todos_v2 (#15800). Split from
// board-consumer.ts: the sidebar is on the first screen, while the Todos page that reads the rest is a chunk of its own.

import { t } from "../../i18n";
import { splitAgentSessions, type AgentMutes } from "../../lib/agent-groups";
import { activityOf, stateOf } from "../../lib/format";
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
