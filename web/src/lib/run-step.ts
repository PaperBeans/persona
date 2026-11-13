// What the running turn is doing right now, read from its exchange's items for the running bar's state words (cindy's
// status line and its plain phases, which the owner asked the bar to follow on 2026-11-12). cindy's host tells it
// ("Thinking...", "Bash running...", "Spawning agent..."); Persona works it out from the newest steps it has.
import { canonicalTool } from "./compatibility";
import { subagentFor } from "./tasks";
import type { ChatLine, Segment, Step } from "./turns";
import type { AgentState, Item, Session, Subagent } from "./types";

export interface RunStep {
  /** `start`: nothing yet; `streaming`: a reply is coming in word by word; `text`: its newest words are the last thing;
   *  `thinking`: a thought is; `tool`: a call is running; `after`: something else came last (a call's result). */
  kind: "start" | "streaming" | "text" | "thinking" | "tool" | "after";
  /** The running call's tool (`tool`). */
  tool?: string;
  /** A subagent call whose subagent has not said anything yet (cindy's "Spawning agent..."). */
  spawning?: boolean;
}

const SUBAGENT_TOOLS = new Set(["Agent", "Task"]);

/** Item order as the transcript has it (lib/turns.ts blockOrder): its line, then its block in the line. */
const after = (a: Item, b: Item) => a.seq !== b.seq ? a.seq > b.seq : Number(a.id.split("#")[1] ?? 0) > Number(b.id.split("#")[1] ?? 0);

const stepItem = (s: Step): Item => (s.kind === "tool" ? s.use : s.item);

/**
 * The live exchange's step. Among the calls after its newest words or thought, the newest still running: one without a
 * result, or a subagent call the agent still waits on (`answered`: the calls with a terminal result, lib/tasks.ts
 * terminalCalls; a background subagent's launch receipt answers nothing, but the agent is not waiting on it).
 */
export function runStep(seg: Segment | undefined, streaming: boolean, subagents?: Subagent[], answered?: ReadonlySet<string>): RunStep {
  if (streaming) return { kind: "streaming" };
  const steps = seg?.steps ?? [];
  const answer = seg?.answer;
  for (let i = steps.length - 1; i >= 0; i--) {
    const s = steps[i];
    if (s.kind !== "tool" || (answer && after(answer, s.use))) break;
    const name = canonicalTool(s.use.tool_name ?? "");
    const call = s.use.tool_use_id;
    const agent = SUBAGENT_TOOLS.has(name) && subagents ? subagentFor(subagents, s.use) : undefined;
    const waited = Boolean(agent?.running && !agent.background && call && answered && !answered.has(call));
    if (s.result && !waited) continue;
    return { kind: "tool", tool: name, ...(SUBAGENT_TOOLS.has(name) && !(agent && (agent.steps || agent.last_tool || agent.last_text)) ? { spawning: true } : {}) };
  }
  const last = steps.at(-1);
  if (answer && (!last || after(answer, stepItem(last)))) return { kind: "text" };
  if (!last) return { kind: "start" };
  return { kind: last.kind === "thinking" ? "thinking" : "after" };
}

/** The same step as `prev` when nothing of it changed, so the bar's props stay as they were. */
export function sameStep(prev: RunStep | undefined, next: RunStep): RunStep {
  return prev && prev.kind === next.kind && prev.tool === next.tool && prev.spawning === next.spawning ? prev : next;
}

/** The notice the hub makes of a turn the user stopped (transcript.InterruptedNotice, "已中断"). */
const isInterrupt = (it: Item | undefined) => it?.kind === "notice" && !it.task && (it.text ?? "").includes("中断"); // i18n-ignore: the hub's notice

/** Whether the turn that just ran was done, for the bar's ✓ Done (cindy says it only when its host reports the turn
 *  done): the session back to idle and not waiting on you, no failure, and the chat ending neither on a model error nor
 *  on the user's stop. */
export function turnDone(state: AgentState, s: Pick<Session, "failure_reason" | "turn_error"> | undefined, lastLine: ChatLine | undefined, items: Item[]): boolean {
  return (state === "idle" || state === "unread") && !s?.failure_reason && !s?.turn_error && lastLine?.kind !== "apiError" && !isInterrupt(items.at(-1));
}
