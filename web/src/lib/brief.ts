// A task's brief (#3822, docs/assistant/unified-entry.md "Briefs"): one record per piece of work, made when it is
// handed out and never rewritten. The hub sends it on a plan's task before it starts (`tasks[i].brief`) and on a
// to-do (`brief`, with the `lineage` of the to-dos that worked for it). Types and the reading of them only: nothing
// here says a word, so the first screen can name the types without the strings (views/assistant/TaskBrief.tsx draws).

/** The owner's message that led to the task, as they wrote it (`truncated`: the hub kept its first 2000 characters). */
export interface BriefRequest {
  text: string;
  truncated?: boolean;
  at: string;
  conversation?: string;
  message_id?: string;
}

/** One entry of a brief's log: a scope change (what changed and why, the criteria it added and dropped, the expected
 *  time it left) or the mark that the work passed its clock. */
export interface BriefEvent {
  at: string;
  kind: "scope" | "overdue" | (string & {});
  todo_id?: string;
  by?: string;
  change?: string;
  why?: string;
  done_when?: string[];
  expect_minutes?: number;
  added?: string[];
  dropped?: string[];
  approval?: BriefRequest;
}

export type CriterionState = "met" | "not_met" | "unchecked" | "dropped";

/** One criterion when the work ended (or, on a receipt, as it stood when the agent stopped). */
export interface BriefCriterion {
  criterion: string;
  state: CriterionState | (string & {});
  evidence?: string;
  /** dropped: the scope change that dropped it, and when. */
  change?: string;
  changed_at?: string;
}

/** How the work ended against its brief: each criterion, then what was done beyond it. */
export interface BriefResult {
  at: string;
  todo_id?: string;
  by?: string;
  verdict?: string;
  criteria: BriefCriterion[];
  also_done?: string[];
}

export type BriefStarter = "owner" | "assistant" | "leader" | "routine" | "hub" | "unknown";

export interface TaskBrief {
  id: string;
  created_at: string;
  request?: BriefRequest;
  started_by: BriefStarter | (string & {});
  trigger?: string;
  trigger_title?: string;
  reason?: string;
  conversation?: string;
  goal: string;
  done_when?: string[];
  expect_minutes?: number;
  out_of_bounds?: string[];
  /** Made later for a task from before briefs: `handover` is the prompt its agent got, not the owner's words. */
  reconstructed?: boolean;
  handover?: string;
  log?: BriefEvent[];
  result?: BriefResult;
  /** `done_when` and `expect_minutes` as the scope changes left them. */
  criteria?: string[];
  expect_minutes_now?: number;
  clock_at?: string;
  due_at?: string;
  /** It ran past its clock and its supervisor was told. */
  overdue_at?: string;
}

/** One to-do of a brief's lineage: who worked on it, from when to when, and why it stopped. */
export interface LineageStep {
  todo_id: string;
  title?: string;
  agent?: string;
  machine?: string;
  session_id?: string;
  started: string;
  ended?: string;
  why?: string;
  continues?: string;
  superseded_by?: string;
  /** The to-do the lineage was asked for. */
  current?: boolean;
}

/** Who asked, as the brief's first line says it: the owner's own words; who started it when it was not them, with why;
 *  a brief made later, which says so; or nothing known. */
export type BriefAsked =
  | { kind: "owner"; at: string; text: string; truncated: boolean }
  | { kind: "started"; by: "assistant" | "leader" | "routine" | "hub"; at: string; why: string }
  | { kind: "reconstructed" }
  | { kind: "unknown"; at: string };

const STARTERS = ["assistant", "leader", "routine", "hub"] as const;

export function briefAsked(brief: TaskBrief): BriefAsked {
  if (brief.request?.text?.trim()) return { kind: "owner", at: brief.request.at, text: brief.request.text.trim(), truncated: brief.request.truncated === true };
  if (brief.reconstructed) return { kind: "reconstructed" };
  const by = STARTERS.find((s) => s === brief.started_by);
  if (by) return { kind: "started", by, at: brief.created_at, why: (brief.reason || brief.trigger_title || "").trim() };
  return { kind: "unknown", at: brief.created_at };
}

/** The criteria in effect now (a hub that sends none: as the brief was made). */
export function briefCriteria(brief: TaskBrief): string[] {
  return brief.criteria ?? brief.done_when ?? [];
}

/** The expected minutes in effect now; 0 when none was given. */
export function briefExpect(brief: TaskBrief): number {
  return brief.expect_minutes_now || brief.expect_minutes || 0;
}

/** The scope changes, oldest first. */
export function scopeChanges(brief: TaskBrief | undefined): BriefEvent[] {
  return (brief?.log ?? []).filter((e) => e.kind === "scope");
}

/** How the work of to-do `id` ended against its brief: the brief's result when it is this to-do's; a to-do another
 *  took the work over from has none (the result is that one's). */
export function resultOf(todo: { id: string; brief?: TaskBrief } | undefined): BriefResult | undefined {
  const r = todo?.brief?.result;
  return r && Array.isArray(r.criteria) && (!r.todo_id || r.todo_id === todo?.id) ? r : undefined;
}

/** What a finished to-do's result says that its criteria list (the supervision ledger's, #3635) cannot: the criteria a
 *  scope change dropped, each with the change, and what was done beyond the brief. Undefined when there is neither. */
export function resultExtras(todo: { id: string; brief?: TaskBrief } | undefined): { dropped: BriefCriterion[]; alsoDone: string[] } | undefined {
  const r = resultOf(todo);
  const dropped = r?.criteria.filter((c) => c.state === "dropped") ?? [];
  const alsoDone = r?.also_done ?? [];
  return dropped.length || alsoDone.length ? { dropped, alsoDone } : undefined;
}

/** Whether the lineage has something to tell: more than the one to-do. */
export function lineageTells(lineage: LineageStep[] | undefined): lineage is LineageStep[] {
  return !!lineage && lineage.length > 1;
}
