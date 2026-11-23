import { describe, expect, it } from "vitest";
import { chatLines } from "./turns";
import { splitBackgroundActivity } from "./quiet-activity";
import type { FeedItem } from "./feed";
import type { Item } from "./types";

// i18n-ignore: fixed synthetic supervision protocol input.
const raw = '📋 督办：你派出去的任务有 1 个要你处理\n<persona-supervision level="triage" />\n这是 Persona 的督办消息\nCheck the fictional task.';
const at = (seconds: number) => new Date(Date.parse("2026-11-01T10:00:00Z") + seconds * 1000).toISOString();
const item = (seq: number, role: Item["role"], kind: Item["kind"], extra: Partial<Item> = {}): Item => ({ id: `i${seq}#0`, seq, ts: at(seq), role, kind, ...extra });
const input = item(1, "user", "text", { text: raw });
const finish = item(2, "assistant", "tool_use", { tool_name: "mcp__persona__supervision_finish", tool_use_id: "finish", tool_input: { visibility: "silent" } });
const receipt = item(3, "user", "tool_result", { tool_use_id: "finish", text: '{"visibility":"silent","level":"triage"}' });
const answer = item(4, "assistant", "text", { text: "The fictional checks are complete." });
const next = item(8, "user", "text", { text: "A separate real request" });
const feed = (id: string, kind: FeedItem["kind"], seconds: number): FeedItem => ({ id, kind, conversation: "main", at: at(seconds) });

describe("Background activity projection", () => {
  const lines = chatLines([input, finish, receipt, answer, next], { main: true });
  const quiet = lines[0];

  it("marks only the canonical completed round and uses the next input as its exclusive boundary", () => {
    expect(quiet).toMatchObject({ kind: "supervision", completedSilent: true, start: at(1), until: at(8) });
    expect(chatLines([input, finish, receipt, answer], { main: true, live: true })[0]).not.toMatchObject({ completedSilent: true });
    expect(chatLines([input, finish, receipt, answer])[0]).not.toMatchObject({ completedSilent: true });
    expect(chatLines([input, finish, { ...receipt, is_error: true }, answer], { main: true })[0]).not.toMatchObject({ completedSilent: true });
  });

  it("keeps every promoted action visible on either side of a silent finish", () => {
    const cases: { name: string; input: Record<string, unknown>; receipt: string }[] = [
      { name: "ask_user", input: {}, receipt: "{}" },
      { name: "notify_user", input: {}, receipt: "{}" },
      { name: "task_review", input: { verdict: "accepted" }, receipt: "{}" },
      { name: "task_review", input: { verdict: "needs_user" }, receipt: "{}" },
      { name: "supervision_finish", input: { visibility: "user" }, receipt: '{"visibility":"user"}' },
    ];
    const ordered = (items: Item[]) => items.map((entry, index) => ({ ...entry, id: `i${index + 1}#0`, seq: index + 1, ts: at(index + 1) }));
    for (const test of cases) for (const promotionFirst of [true, false]) {
      const use = item(0, "assistant", "tool_use", { tool_name: `mcp__persona__${test.name}`, tool_use_id: "promotion", tool_input: test.input });
      const result = item(0, "user", "tool_result", { tool_use_id: "promotion", text: test.receipt });
      const items = ordered(promotionFirst ? [input, use, result, finish, receipt, answer, next] : [input, finish, receipt, use, result, answer, next]);
      const lines = chatLines(items, { main: true });
      expect(splitBackgroundActivity(lines, []).activity, `${test.name}, promotionFirst=${promotionFirst}`).toEqual([]);
    }
  });

  it("splits the round and only its task/routed/answered feed cards before timeline disclosure", () => {
    const all = [
      feed("before", "task", 0), feed("task", "task", 2), feed("routed", "routed", 3), feed("answered", "answered", 4),
      feed("plan", "plan", 2), feed("message", "message", 3), feed("notice", "notice", 4), feed("brief", "brief", 5),
      feed("review", "review", 5), feed("at-next-input", "task", 8), feed("later", "task", 9),
    ];
    const split = splitBackgroundActivity(lines, all);
    expect(split.timeline).toEqual([lines[1]]);
    expect(split.activity).toHaveLength(1);
    expect(split.activity[0].line).toBe(quiet);
    expect(split.activity[0].feed.map((item) => item.id)).toEqual(["task", "routed", "answered"]);
    expect(split.timelineFeed.map((item) => item.id)).toEqual(["before", "plan", "message", "notice", "brief", "review", "at-next-input", "later"]);
  });

  it("keeps later feed in the final round's open-ended interval and never associates invalid times", () => {
    const open = chatLines([input, finish, receipt, answer], { main: true });
    const split = splitBackgroundActivity(open, [feed("later", "task", 30), { ...feed("bad", "task", 2), at: "not-a-time" }]);
    expect(split.activity[0].feed.map((item) => item.id)).toEqual(["later"]);
    expect(split.timelineFeed.map((item) => item.id)).toEqual(["bad"]);
  });
});
