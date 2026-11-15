import { backgroundWork, taskBackgroundWork, type BackgroundWork } from "./background-work";
import { cardDismissed, cardFiled, cardSent, startFailure, type FeedItem, type TaskPlan } from "./feed";
import { cardBucket, taskCardSlots, type TaskCardMetadata } from "./task-cards";
import { reviewState, supervisorReviewing } from "./supervision";
import { supersededFailedStarts, triage } from "./todo-triage";
import { awaitsReview, dockAgents, makeSessionLookup, openAsk, todoRank, todoStatus, type SessionLookup, type Todo, type TodoStatus } from "./todos";
import { byTime } from "./times";
import { homeKey } from "./folded-rows";
import type { Interaction, Session } from "./types";

/**
 * The Assistant's two entries above the composer (#3387, docs/assistant/unified-entry.md "Needs you and In progress"):
 * Needs you lists everything waiting on the user, In progress everything running that does not. Each thing is one row in
 * exactly one of them, Needs you first; finished things are in neither (the board has them), and each entry's count is
 * its rows. A task its supervisor parked is In progress, saying until when and why; it is never in Needs you for that. So is
 * a task waiting for its supervisor's review, for the first hour of the wait (`supervisorReviewing`). A task is one row whatever its agents: they are not listed on their own. In the main conversation an
 * Agent that belongs to no task (opened by hand) is a row of its own.
 */
export type NeedsYouItem =
  /** A task whose agent asks something (`asks`, oldest first), or that waits for the user's decision or review.
   *  `countsOpen` says whether the To-dos page's "Not done" counts the to-do (#3331): the row stays visible whatever
   *  it says, the chip badge counts it only when it does. */
  | { kind: "task"; key: string; todo: Todo; status: TodoStatus; asks: Interaction[]; countsOpen: boolean }
  /** An Agent that belongs to no task, asking something. */
  | { kind: "agent"; key: string; session: Session; asks: Interaction[] }
  /** A plan waiting for Start. */
  | { kind: "plan"; key: string; plan: TaskPlan }
  /** A message card waiting for Send. */
  | { kind: "message"; key: string; item: FeedItem };

export type RunningItem =
  /** A dispatched task not finished and not waiting on the user. `countsOpen`: see NeedsYouItem. `supervisorReview`:
   *  it is here because its supervisor's review is under way, and its row says so. */
  | { kind: "task"; key: string; card: TaskCardMetadata; todo: Todo; status: TodoStatus; activity?: BackgroundWork; countsOpen: boolean; supervisorReview?: boolean }
  /** A working Agent that belongs to no task. */
  | { kind: "agent"; key: string; session: Session; activity?: BackgroundWork };

export interface AssistantEntries {
  needsYou: NeedsYouItem[];
  running: RunningItem[];
  /** Open tasks with no progress for 48 hours and nothing running (the To-dos page's shelved, #3331): folded under
   *  In progress, not counted. */
  shelved: RunningItem[];
  /** Fresh open PRs with no active agent, separate from In progress; still open tasks. */
  pr: RunningItem[];
  /** Handed-out tasks that finished in the last 24 hours and that the owner has not opened since (the To-dos page's Just
   *  finished, todo-triage), newest first: a group under In progress, not counted (#8237). How a task ended is told
   *  here, not by a card in the chat. */
  justFinished: RunningItem[];
}

export interface AssistantEntriesInput {
  conversation: string;
  /** The main conversation's id: it watches every task and the Agents opened by hand. */
  main?: string;
  todos: Todo[];
  /** Every session, as the lists show them (answered cards applied). */
  sessions: Session[];
  /** Pending cards. */
  interactions: Interaction[];
  plans: Record<string, TaskPlan>;
  /** The conversation's feed. */
  feed: FeedItem[];
  /** The hub's task card slots for the conversation (`task_cards`). */
  cards?: TaskCardMetadata[];
  /** Message cards and task-decision rounds the user put off locally (`later`). */
  deferred?: Record<string, unknown>;
  /** Looks up a task's agents; defaults to `sessions`. */
  lookup?: SessionLookup;
  now?: number;
  /** Tasks the user brought back from shelved, by id: when (ms). */
  restored?: ReadonlyMap<string, number>;
}

/** Task states that wait on the user: an agent asks, or its turn broke off on an error (a failed start is below). */
const WAITS_ON_YOU: ReadonlySet<TodoStatus> = new Set(["needs_you", "interrupted"]);

const sessionKey = (mid: string, sid: string) => `${mid}/${sid}`;

/** A task's status when some of its Agents ask under another task (`own`: its status from its own Agents alone): it
 *  does not wait on the user for them, so a Needs you that only those bring is the task at work instead. */
function withNeeds(all: TodoStatus, own: TodoStatus): TodoStatus {
  return all === "needs_you" && own !== "needs_you" ? (own === "not_started" ? "working" : own) : all;
}

/** A task's status, failed too when only its plan says the start failed (a hub from before #1077). */
export function entryStatus(todo: Todo, lookup: SessionLookup, plans: Record<string, TaskPlan>): TodoStatus {
  const st = todoStatus(todo, lookup);
  return st === "not_started" && startFailure(todo, plans) ? "failed" : st;
}

/** The task waits for the user's decision or review: an open ask (a decided one only while the task is open), or its
 *  supervisor's `needs_user` on an open task, or a `pending_review` that is the owner's: one waiting an hour or more,
 *  or one the hub gives no start for (`supervisorReviewing`). A younger review is its supervisor's to do. */
export function taskAwaitsDecision(todo: Todo, now: number = Date.now()): boolean {
  const ask = openAsk(todo);
  if (ask) return !(todo.done_at && ask.outcome === "decide");
  if (todo.done_at) return false;
  return todo.supervision?.state === "needs_user" || (todo.supervision?.state === "pending_review" && !supervisorReviewing(todo, now));
}

/** The message card still waits for Send: not sent, filed, dismissed, or put off. */
export function messageWaits(it: FeedItem, deferred: Record<string, unknown> = {}): boolean {
  return it.kind === "message" && !cardSent(it) && !cardFiled(it) && !cardDismissed(it) && !deferred[it.id];
}

/** The review round, not the task id alone: a later decision must remain visible. */
export function decisionRound(todo: Todo): string {
  return todo.supervision?.reviews?.at(-1)?.at || todo.decision_review?.review_at || todo.ask?.asked_at || "";
}

export function decisionLaterKey(todo: Todo): string {
  return `decision:${todo.manager || ""}:${todo.id}:${decisionRound(todo)}`;
}

export function assistantEntries(input: AssistantEntriesInput): AssistantEntries {
  const { conversation, main, todos, sessions, interactions, plans, feed } = input;
  const now = input.now ?? Date.now();
  const lookup = input.lookup ?? makeSessionLookup(sessions);
  const isMain = !!main && main === conversation;
  const byTodo = new Map(todos.map((todo) => [todo.id, todo]));
  // failed starts whose work finished in a related to-do: closed, as the To-dos page has them (#3713)
  const superseded = supersededFailedStarts(todos);
  const asksOf = new Map<string, Interaction[]>();
  for (const ix of [...interactions].sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))) {
    // a child session's card is asked under its parent (#5120): the parent's task or Agent row holds it
    const k = homeKey(ix);
    asksOf.set(k, [...(asksOf.get(k) ?? []), ix]);
  }

  const asking: NeedsYouItem[] = [];
  const deciding: { item: NeedsYouItem; rank: number; at: string }[] = [];
  const open: { item: RunningItem; rank: number; at: string }[] = [];
  const shelved: { item: RunningItem; at: string }[] = [];
  const pr: { item: RunningItem; at: string }[] = [];
  const finished: { item: RunningItem; at: number }[] = [];
  const folded = new Set<string>();
  const slots = taskCardSlots(conversation, input.cards ?? [], feed, plans, todos, isMain);
  const activeAgents = (todo: Todo) => (todo.done_at ? [] : (todo.agents ?? []).filter((a) => !a.released_at));
  // One home per request (docs/design/DESIGN_SYSTEM.md 4.8): an Agent several of these tasks list asks under one of them,
  // the task the hub ties its session to (`todo`), else the first that lists it. The others neither repeat its cards nor
  // wait on the user for them; they are in progress.
  const listing = new Map<string, string[]>();
  for (const card of slots) {
    const todo = card.todo_id ? byTodo.get(card.todo_id) : undefined;
    if (!todo) continue;
    for (const a of activeAgents(todo)) {
      const k = sessionKey(a.machine_id, a.session_id);
      const ids = listing.get(k) ?? [];
      if (!ids.includes(todo.id)) listing.set(k, [...ids, todo.id]);
    }
  }
  const homeOf = (mid: string, sid: string): string | undefined => {
    const ids = listing.get(sessionKey(mid, sid));
    const tied = lookup(mid, sid)?.todo?.id;
    return tied && ids?.includes(tied) ? tied : ids?.[0];
  };
  for (const card of slots) {
    const todo = card.todo_id ? byTodo.get(card.todo_id) : undefined;
    // a plan task with no to-do yet is its plan's row (below); a to-do the hub no longer sends is finished
    if (!todo) continue;
    const active = activeAgents(todo);
    const own = active.filter((a) => homeOf(a.machine_id, a.session_id) === todo.id);
    // what the task waits on the user for is its own Agents' (an Agent another task holds asks there)
    const status = own.length < active.length ? withNeeds(entryStatus(todo, lookup, plans), todoStatus({ ...todo, agents: own }, lookup)) : entryStatus(todo, lookup, plans);
    const asks = own.flatMap((a) => asksOf.get(sessionKey(a.machine_id, a.session_id)) ?? []);
    const key = `task:${todo.id}`;
    const bucket = cardBucket(card, todo, plans, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id));
    const countsOpen = bucket === "open";
    // a failed start carries done_at (the hub ends it), yet it is the user's to retry while its bucket says open
    const waits = status === "failed" ? bucket === "open" : !todo.done_at && WAITS_ON_YOU.has(status);
    const decision = taskAwaitsDecision(todo, now);
    // its supervisor's review is under way (not the owner's yet): in progress, whatever the To-dos page files it under
    const reviewing = !decision && supervisorReviewing(todo, now);
    if (asks.length) asking.push({ kind: "task", key, todo, status, asks, countsOpen });
    else if (waits || decision) {
      // Only the review decision is hidden; a live Agent question above still has its home.
      if (!(status !== "needs_you" && decision && (todo.decision_review?.dismissal_id && todo.decision_review.review_at === decisionRound(todo) || input.deferred?.[decisionLaterKey(todo)]))) {
        deciding.push({ item: { kind: "task", key, todo, status, asks, countsOpen }, rank: todoRank(todo, status), at: card.at });
      }
    }
    // a parked task waits on purpose (#3804): with the tasks in progress however long it has been still, never folded away
    // a supervisor's review under way is Persona's own work, not a wait on the upstream maintainer: In progress, even
    // with a fresh open PR; once reviewed (accepted, or parked on the PR) the PR rule below files it
    else if (!reviewing && triage(todo, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id)).section === "pr") pr.push({ item: { kind: "task", key, card, todo, status, countsOpen }, at: card.at });
    else if (bucket === "open" || reviewState(todo) === "parked" || reviewing) open.push({ item: { kind: "task", key, card, todo, status, activity: taskBackgroundWork(todo, lookup), countsOpen, ...(reviewing && awaitsReview(todo, status) ? { supervisorReview: true } : {}) }, rank: todoRank(todo, status), at: card.at });
    else if (bucket === "shelved") shelved.push({ item: { kind: "task", key, card, todo, status, countsOpen: false }, at: card.at });
    else {
      // finished work a conversation handed out (a failed start a related to-do finished is history, not news)
      const tr = triage(todo, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id));
      if (tr.section === "just_done" && status !== "failed" && todo.origin) finished.push({ item: { kind: "task", key, card, todo, status, countsOpen: false }, at: tr.last });
      continue;
    }
    for (const agent of active) folded.add(sessionKey(agent.machine_id, agent.session_id));
  }

  // Agents that belong to no task, the main conversation's only (dockAgents): asking ones wait on the user, working ones run.
  const agentsAsking: NeedsYouItem[] = [];
  const agentsRunning: RunningItem[] = [];
  for (const { session, state } of dockAgents(sessions.map((s) => lookup(s.machine_id, s.session_id) ?? s), todos, conversation, main)) {
    if (folded.has(sessionKey(session.machine_id, session.session_id))) continue;
    const key = `agent:${sessionKey(session.machine_id, session.session_id)}`;
    if (state === "needs_you") agentsAsking.push({ kind: "agent", key, session, asks: asksOf.get(sessionKey(session.machine_id, session.session_id)) ?? [] });
    else agentsRunning.push({ kind: "agent", key, session, activity: backgroundWork(session) });
  }

  const plansWaiting: NeedsYouItem[] = Object.values(plans)
    .filter((plan) => plan.conversation === conversation && plan.state === "open")
    .sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))
    .map((plan) => ({ kind: "plan", key: `plan:${plan.id}`, plan }));
  const messages: NeedsYouItem[] = feed.filter((it) => messageWaits(it, input.deferred)).map((item) => ({ kind: "message", key: `message:${item.id}`, item }));

  const newest = (a: { at: string; rank?: number }, b: { at: string; rank?: number }) => (a.rank ?? 0) - (b.rank ?? 0) || byTime(b.at, a.at);
  const firstAsk = (item: NeedsYouItem) => (item.kind === "task" || item.kind === "agent" ? item.asks[0]?.created_at : undefined);
  return {
    needsYou: [
      ...[...asking, ...agentsAsking].sort((a, b) => byTime(firstAsk(a), firstAsk(b))),
      ...plansWaiting,
      ...deciding.sort(newest).map((row) => row.item),
      ...messages,
    ],
    running: [...open.sort(newest).map((row) => row.item), ...agentsRunning],
    shelved: shelved.sort(newest).map((row) => row.item),
    pr: pr.sort(newest).map((row) => row.item),
    justFinished: finished.sort((a, b) => b.at - a.at).map((row) => row.item),
  };
}

/** Needs you's rows by the conversation supervising them (#4314, docs/assistant/assistant.md "Background rounds"): a
 *  task's `origin`, else `conversation` (agents, plans, message cards, hand-made to-dos). This conversation's group comes
 *  first, then the others in the order their first row appears; the rows and their order within a group are as given,
 *  so the entry's count is unchanged. One group when every row is this conversation's. */
export function needsYouBySupervisor(items: readonly NeedsYouItem[], conversation: string): { supervisor: string; items: NeedsYouItem[] }[] {
  const groups = new Map<string, NeedsYouItem[]>([[conversation, []]]);
  for (const item of items) {
    const supervisor = item.kind === "task" && item.todo.origin ? item.todo.origin : conversation;
    const group = groups.get(supervisor);
    if (group) group.push(item);
    else groups.set(supervisor, [item]);
  }
  return [...groups].map(([supervisor, rows]) => ({ supervisor, items: rows }));
}
