// Goal folding for the To-dos status list under todos_v2 (#14661):
// collapses non-urgent `part_of` child tasks under their top-level parent goal,
// tracks compact done/total progress, and surfaces `needs_you` / active-ask child blockers
// at the top in `waiting` (`needs_you`) so no urgent child is ever hidden inside a fold.

import { checksAsOpen, TRIAGE_SECTIONS, type Triage, type TriageGroup, type TriageResult, type TriageSection } from "./todo-triage";
import { openAsk, type Todo } from "./todos";

export interface GoalChildEntry {
  todo: Todo;
  section: TriageSection;
  done: boolean;
  blocker: boolean;
}

export interface GoalSummary {
  parentId: string;
  done: number;
  total: number;
  blockedCount: number;
  children: GoalChildEntry[];
  blockers: GoalChildEntry[];
}

export interface GoalFoldResult {
  /** Status-section triage with non-urgent children folded under their top-level goal and blockers surfaced in `waiting`. */
  triaged: TriageResult;
  /** Top-level parent goal id -> child progress summary & ordered child list (only parents with >= 1 visible child). */
  goals: ReadonlyMap<string, GoalSummary>;
  /** Surfaced blocker child id -> its top-level parent goal (used to show parent context in `needs_you`). */
  surfacedParent: ReadonlyMap<string, Todo>;
}

/** Whether a child task counts as completed, consistent with row checkbox state (`checksAsOpen`). */
export function isChildDone(todo: Todo, section: TriageSection): boolean {
  return !checksAsOpen(todo, section);
}

/** Whether a child task is urgent (`needs_you` or has an active owner ask) and must surface in `waiting`. */
export function isChildBlocker(todo: Todo, section: TriageSection): boolean {
  if (section === "waiting") return true;
  if (todo.view?.section === "needs_you" || todo.view?.phase === "needs_you") return true;
  if (!isChildDone(todo, section) && Boolean(openAsk(todo))) return true;
  return false;
}

/** Resolves the highest ancestor goal id present in `byId` via `part_of`.
 *  Returns undefined for top-level items, orphan children whose parent is not in `byId`, self-references, or cycles. */
function resolveRootGoalId(id: string, byId: ReadonlyMap<string, Todo>): string | undefined {
  const start = byId.get(id);
  if (!start?.part_of || start.part_of === id || !byId.has(start.part_of)) return undefined;
  const visited = new Set<string>([id]);
  let current = start.part_of;
  while (current) {
    if (visited.has(current)) return undefined;
    visited.add(current);
    const node = byId.get(current);
    if (!node) return undefined;
    const next = node.part_of;
    if (!next || next === current || !byId.has(next)) return current;
    current = next;
  }
  return undefined;
}

/**
 * Folds non-urgent `part_of` child tasks under their top-level parent goal in the status-section list.
 * - Children whose parent is not in the current filtered `triaged` set stay top-level (never dropped).
 * - Multi-level descendants fold under their top-level ancestor goal in the current set.
 * - Cycles in `part_of` are left unfolded so nothing disappears.
 * - Children in `needs_you` (`waiting`) or with an active owner ask surface at the top in `waiting`
 *   with a reference to their parent goal, while still counting toward the parent's `done/total` and `blockedCount`.
 */
export function foldGoalTasks(triaged: TriageResult): GoalFoldResult {
  const byId = new Map<string, Todo>();
  const memberSection = new Map<string, TriageSection>();
  const memberTriage = new Map<string, Triage>();
  const order: string[] = [];

  for (const sec of TRIAGE_SECTIONS) {
    for (const g of triaged.sections[sec]) {
      for (const m of g.members) {
        if (byId.has(m.id)) continue;
        byId.set(m.id, m);
        memberSection.set(m.id, sec);
        const tr = g.triage.get(m.id);
        if (tr) memberTriage.set(m.id, tr);
        order.push(m.id);
      }
    }
  }

  const childrenByParent = new Map<string, GoalChildEntry[]>();
  const foldedIds = new Set<string>();
  const promotedToWaiting = new Set<string>();
  const surfacedParent = new Map<string, Todo>();

  for (const id of order) {
    const parentId = resolveRootGoalId(id, byId);
    if (!parentId) continue;
    const todo = byId.get(id)!;
    const section = memberSection.get(id)!;
    const done = isChildDone(todo, section);
    const blocker = isChildBlocker(todo, section);
    const entry: GoalChildEntry = { todo, section, done, blocker };
    const list = childrenByParent.get(parentId);
    if (list) list.push(entry);
    else childrenByParent.set(parentId, [entry]);

    if (blocker) {
      surfacedParent.set(id, byId.get(parentId)!);
      if (section !== "waiting") promotedToWaiting.add(id);
    } else {
      foldedIds.add(id);
    }
  }

  const goals = new Map<string, GoalSummary>();
  for (const [parentId, entries] of childrenByParent) {
    const ordered = [...entries].sort((a, b) => Number(b.blocker) - Number(a.blocker));
    const blockers = ordered.filter((c) => c.blocker);
    const done = ordered.reduce((n, c) => n + (c.done ? 1 : 0), 0);
    goals.set(parentId, {
      parentId,
      done,
      total: ordered.length,
      blockedCount: blockers.length,
      children: ordered,
      blockers,
    });
  }

  if (foldedIds.size === 0 && promotedToWaiting.size === 0) {
    return { triaged, goals, surfacedParent };
  }

  const sections = Object.fromEntries(TRIAGE_SECTIONS.map((s) => [s, [] as TriageGroup[]])) as Record<TriageSection, TriageGroup[]>;
  for (const sec of TRIAGE_SECTIONS) {
    for (const g of triaged.sections[sec]) {
      const keptMembers = g.members.filter(
        (m) => !foldedIds.has(m.id) && !(sec !== "waiting" && promotedToWaiting.has(m.id)),
      );
      if (keptMembers.length === 0) continue;
      if (keptMembers.length === g.members.length) {
        sections[sec].push(g);
      } else {
        const nextTriage = new Map<string, Triage>();
        for (const m of keptMembers) {
          const tr = g.triage.get(m.id);
          if (tr) nextTriage.set(m.id, tr);
        }
        sections[sec].push({
          ...g,
          lead: keptMembers[0],
          members: keptMembers,
          triage: nextTriage,
        });
      }
    }
  }

  for (const id of promotedToWaiting) {
    const todo = byId.get(id)!;
    const orig = memberTriage.get(id);
    const tr: Triage = orig ? { ...orig, section: "waiting" } : { section: "waiting", rule: "owner_decides", last: 0 };
    sections.waiting.push({
      key: id,
      section: "waiting",
      lead: todo,
      members: [todo],
      triage: new Map([[id, tr]]),
      last: tr.last,
    });
  }

  const todoCounts = Object.fromEntries(
    TRIAGE_SECTIONS.map((sec) => [sec, sections[sec].reduce((n, g) => n + g.members.length, 0)]),
  ) as Record<TriageSection, number>;

  return {
    triaged: {
      ...triaged,
      sections,
      todoCounts,
    },
    goals,
    surfacedParent,
  };
}
