import { describe, expect, it } from "vitest";
import fixture from "../../../spec/fixtures/entries/cases.json";
import { assistantEntries } from "./assistant-entries";
import type { FeedItem, TaskPlan } from "./feed";
import type { Todo } from "./todos";
import type { Interaction, Session } from "./types";

// Android reads the same fixture (AssistantEntriesFixtureTest): the two entries split the same input the same way.
describe("the Assistant's Needs you and In progress entries (spec/fixtures/entries)", () => {
  for (const c of fixture.cases) {
    it(c.name, () => {
      const input = c.input as unknown as { conversation: string; main: string; now: string; todos: Todo[]; sessions: Session[]; interactions: Interaction[]; plans: Record<string, TaskPlan>; feed: FeedItem[] };
      const got = assistantEntries({ ...input, now: Date.parse(input.now) });
      expect(got.needsYou.map((item) => item.key)).toEqual(c.expected.needs_you);
      expect(got.running.map((item) => item.key)).toEqual(c.expected.running);
      expect(got.shelved.map((item) => item.key)).toEqual(c.expected.shelved);
      const activity = (c.expected as { activity?: Record<string, unknown> }).activity;
      for (const [key, expected] of Object.entries(activity ?? {})) {
        const work = got.running.find((row) => row.key === key)?.activity;
        expect(work ? { tasks: work.tasks, subagents: work.subagents, workflows: work.workflows,
          ...(work.workflow?.phase ? { phase: work.workflow.phase, progress: `${work.workflow.agents_done}/${work.workflow.agents_started}+` } : {}) } : null).toEqual(expected);
      }
      // one entry each: nothing is in both, and no agent of a listed task shows on its own
      const keys = [...got.needsYou, ...got.running, ...got.shelved].map((item) => item.key);
      expect(new Set(keys).size).toBe(keys.length);
      const taskAgents = new Set(input.todos.filter((todo) => !todo.done_at && keys.includes(`task:${todo.id}`))
        .flatMap((todo) => (todo.agents ?? []).filter((a) => !a.released_at).map((a) => `agent:${a.machine_id}/${a.session_id}`)));
      expect(keys.filter((key) => taskAgents.has(key))).toEqual([]);
    });
  }
});

// A task its supervisor parked (#3804) is In progress, however it would be filed otherwise: not Needs you, not folded
// under shelved after two days still, not dropped as finished unseen.
describe("a parked task in the Assistant's entries", () => {
  const now = Date.parse("2026-11-10T09:00:00Z");
  const parked = { reason: "The design team has to pick the icon set first", until: "2026-11-14T09:00:00Z", waiting_on: "someone_else" as const, count: 1 };
  const todo = (state: "parked" | "needs_user" | "", at: string): Todo => ({
    id: "todo_1", title: "Redraw the toolbar icons", origin: "main", plan_id: "plan_1", created_at: at, updated_at: at,
    agents: [{ machine_id: "box", session_id: "s1", linked_at: at }],
    supervision: { supervisor: "main", state, ...(state === "parked" ? { parked } : {}), reviews: [], updated_at: at },
  } as unknown as Todo);
  const entries = (t: Todo, at: string) => assistantEntries({
    conversation: "main", main: "main", todos: [t], sessions: [], interactions: [], plans: {}, feed: [], now,
    cards: [{ id: "plan:plan_1:0", todo_id: t.id, plan_id: "plan_1", index: 0, at, updated_at: at, state: "working" }],
  });
  const keys = (got: ReturnType<typeof assistantEntries>) => ({ needsYou: got.needsYou.map((i) => i.key), running: got.running.map((i) => i.key), shelved: got.shelved.map((i) => i.key) });

  it("is listed with the tasks in progress, not under Needs you", () => {
    const at = "2026-11-10T08:00:00Z";
    expect(keys(entries(todo("parked", at), at))).toEqual({ needsYou: [], running: ["task:todo_1"], shelved: [] });
    // the same task waiting for the owner's decision is theirs
    expect(keys(entries(todo("needs_user", at), at)).needsYou).toEqual(["task:todo_1"]);
  });

  it("stays in progress after days without activity, where an unparked task folds away", () => {
    const at = "2026-11-05T08:00:00Z";
    expect(keys(entries(todo("parked", at), at))).toEqual({ needsYou: [], running: ["task:todo_1"], shelved: [] });
    expect(keys(entries(todo("", at), at)).running).toEqual([]);
  });
});
