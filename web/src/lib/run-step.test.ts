import { describe, expect, it } from "vitest";
import { runStep, sameStep, turnDone } from "./run-step";
import { segments, type ChatLine } from "./turns";
import type { Item, Subagent } from "./types";

let seq = 0;
const item = (kind: Item["kind"], extra: Partial<Item> = {}): Item => ({ id: `i${++seq}#0`, seq, role: kind === "tool_result" ? "user" : "assistant", kind, ...extra });
const user = (text = "Fix the build"): Item => ({ id: `u${++seq}#0`, seq, role: "user", kind: "text", text });
const call = (name: string, id: string, input: Record<string, unknown> = {}) => item("tool_use", { tool_name: name, tool_use_id: id, tool_input: input });
const result = (id: string, text = "ok") => item("tool_result", { tool_use_id: id, text });
const step = (items: Item[], streaming = false, subagents?: Subagent[], answered?: Set<string>) => runStep(segments(items).at(-1), streaming, subagents, answered);

describe("the running turn's step (#5312)", () => {
  it("is the start before anything came, a thought, or the words written last", () => {
    expect(step([user()])).toEqual({ kind: "start" });
    expect(runStep(undefined, false)).toEqual({ kind: "start" });
    expect(step([user(), item("thinking", { text: "" })])).toEqual({ kind: "thinking" });
    expect(step([user(), item("text", { text: "Looking at the build." })])).toEqual({ kind: "text" });
    // a reply coming in word by word says so whatever came before it
    expect(step([user(), call("Bash", "b1")], true)).toEqual({ kind: "streaming" });
  });

  it("names the newest call still running, or says the agent works on a result", () => {
    expect(step([user(), call("Bash", "b1", { command: "make" })])).toEqual({ kind: "tool", tool: "Bash" });
    expect(step([user(), call("Read", "r1"), result("r1")])).toEqual({ kind: "after" });
    // calls made together: the newest one without its result, past the ones that came back
    expect(step([user(), call("Grep", "g1"), call("Read", "r1"), result("r1")])).toEqual({ kind: "tool", tool: "Grep" });
    // words after a call that came back are newer than it
    expect(step([user(), call("Read", "r1"), result("r1"), item("text", { text: "Done reading." })])).toEqual({ kind: "text" });
  });

  it("knows a subagent call: spawning until its subagent says something, then running until the agent has its answer", () => {
    const launch = [user(), call("Agent", "a1", { description: "Check the tests" })];
    expect(step(launch)).toEqual({ kind: "tool", tool: "Agent", spawning: true });
    const listed: Subagent = { id: "x", session_id: "s@x", tool_use_id: "a1", running: true };
    expect(step(launch, false, [listed])).toEqual({ kind: "tool", tool: "Agent", spawning: true });
    const working = { ...listed, last_tool: { name: "Bash", summary: "Running the tests" } };
    expect(step(launch, false, [working])).toEqual({ kind: "tool", tool: "Agent" });
    // an interim report is no answer: the agent still waits on the call
    const reported = [...launch, result("a1", "Still running the tests.")];
    expect(step(reported, false, [working], new Set())).toEqual({ kind: "tool", tool: "Agent" });
    expect(step(reported, false, [working], new Set(["a1"]))).toEqual({ kind: "after" });
    // a background subagent's receipt: the agent goes on without it
    expect(step(reported, false, [{ ...working, background: true }], new Set())).toEqual({ kind: "after" });
  });

  it("keeps the step it had while nothing of it changed", () => {
    const a = { kind: "tool" as const, tool: "Bash" };
    expect(sameStep(a, { kind: "tool", tool: "Bash" })).toBe(a);
    expect(sameStep(a, { kind: "tool", tool: "Read" })).toEqual({ kind: "tool", tool: "Read" });
    expect(sameStep(undefined, a)).toBe(a);
  });
});

describe("whether a turn was done, for the bar's ✓ Done (cindy's, #5312)", () => {
  const items = [user(), call("Read", "r9"), result("r9"), item("text", { text: "All set." })];
  const reply = { kind: "segment", id: "s", live: false, seg: segments(items)[0] } as ChatLine;
  const apiError = { kind: "apiError", id: "e", item: item("notice", { text: "API 错误：overloaded" }), info: {}, count: 1 } as unknown as ChatLine;

  it("is done when the session is back to idle, or unread, with nothing wrong at the end", () => {
    expect(turnDone("idle", {}, reply, items)).toBe(true);
    expect(turnDone("unread", undefined, reply, items)).toBe(true);
  });

  it("is not done while it waits on you (a permission, a question, a plan) or still works", () => {
    expect(turnDone("needs_you", {}, reply, items)).toBe(false);
    expect(turnDone("working", {}, reply, items)).toBe(false);
  });

  it("is not done when it ended on a model error, a failure or the user's stop", () => {
    expect(turnDone("idle", {}, apiError, items)).toBe(false);
    expect(turnDone("unread", { turn_error: "overloaded" }, reply, items)).toBe(false);
    expect(turnDone("idle", { failure_reason: "The host stopped" }, reply, items)).toBe(false);
    expect(turnDone("idle", {}, reply, [...items, item("notice", { text: "已中断" })])).toBe(false);
  });
});
