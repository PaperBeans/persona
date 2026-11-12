import { describe, expect, it } from "vitest";
import { chatLines, type ChatLine } from "./turns";
import type { Item } from "./types";

// Segment.cut (#5314, cindy's "This reply did not complete"): an exchange that ended in a model API error or an
// interruption after everything it did, so its last words are not a finished reply.
const u = (seq: number, text: string): Item => ({ id: `u${seq}#0`, seq, role: "user", kind: "text", text });
const a = (seq: number, text: string): Item => ({ id: `a${seq}#0`, seq, role: "assistant", kind: "text", text });
const tool = (seq: number): Item => ({ id: `t${seq}#0`, seq, role: "assistant", kind: "tool_use", tool_use_id: `c${seq}`, tool_name: "Bash", tool_input: { command: "ls" } });
const res = (seq: number, of: number): Item => ({ id: `r${seq}#0`, seq, role: "user", kind: "tool_result", tool_use_id: `c${of}`, text: "ok" });
const stop = (seq: number): Item => ({ id: `n${seq}#0`, seq, role: "system", kind: "notice", text: "已中断" }); // i18n-ignore: the transcript's interrupt notice

const cuts = (lines: ChatLine[]) => lines.flatMap((l) => (l.kind === "segment" ? [Boolean(l.seg.cut)] : []));

describe("an exchange cut short", () => {
  it("is not, when it finished", () => {
    expect(cuts(chatLines([u(1, "go"), tool(2), res(3, 2), a(4, "done")]))).toEqual([false]);
  });

  it("is, when it was interrupted after its last words", () => {
    const lines = chatLines([u(1, "go"), tool(2), res(3, 2), a(4, "Looking at the"), stop(5)]);
    expect(lines.map((l) => l.kind)).toEqual(["segment", "notice"]);
    expect(cuts(lines)).toEqual([true]);
  });

  it("is not, when it went on after the interruption", () => {
    expect(cuts(chatLines([u(1, "go"), a(2, "first"), stop(3), a(4, "finished after all")]))).toEqual([false]);
  });

  it("is, when a model API error came after its last words", () => {
    const lines = chatLines([u(1, "go"), tool(2), res(3, 2), a(4, "Half an answer"), a(5, "API Error: 529 overloaded")]);
    expect(lines.map((l) => l.kind)).toEqual(["segment", "apiError"]);
    expect(cuts(lines)).toEqual([true]);
  });

  it("is only the exchange that ended so, not the ones before it", () => {
    expect(cuts(chatLines([u(1, "one"), a(2, "done"), u(3, "two"), a(4, "half"), stop(5)]))).toEqual([false, true]);
  });

  it("is not while it runs", () => {
    expect(cuts(chatLines([u(1, "go"), a(2, "half"), stop(3)], { live: true }))).toEqual([false]);
    expect(cuts(chatLines([u(1, "go"), a(2, "API Error: 529 overloaded")], { live: true }))).toEqual([false]);
  });
});
