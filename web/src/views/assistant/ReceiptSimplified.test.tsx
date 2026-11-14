// The simplified chat draws a conclusion card for every way a task ended (#5569): done, stopped, failed to start, interrupted;
// the reviews and reports stay out of it. A failed start keeps its "Retry": the hand-off card above only says it did not start.
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";
import { getFeedState, type FeedItem } from "../../lib/feed";
import { getState } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import { FeedLineView } from "./Tasks";

const now = new Date().toISOString();
const todo = (o: Partial<Todo>): Todo => ({ id: "todo_1", title: "修连接", origin: "c1", created_at: now, updated_at: now, done_at: null, agents: [], ...o }) as unknown as Todo;
const item = (kind: string, body: Record<string, unknown> = {}): FeedItem => ({ id: "f1", conversation: "c1", at: now, kind, ref: "todo_1", body: { title: "修连接", summary: "测试补好了。", ...body } }) as FeedItem;
const draw = (it: FeedItem) => renderToStaticMarkup(<FeedLineView item={it} simplified />);

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };
const feedBefore = { ...getFeedState() };
afterEach(() => {
  seed(before);
  Object.assign(getFeedState() as unknown as Record<string, unknown>, feedBefore);
});

describe("conclusion cards in the simplified chat", () => {
  it("draws done, stopped, failed and interrupted", () => {
    seed({ todos: [todo({ done_at: now })] });
    expect(draw(item("receipt", { state: "done" }))).toContain("修连接 完成");
    seed({ todos: [todo({})] });
    expect(draw(item("receipt", { state: "done" }))).toContain("修连接 停下了，还没完成");
    seed({ todos: [] });
    const failed = draw(item("receipt", { state: "failed", summary: "rpc: closed" }));
    expect(failed).toContain("修连接没开起来");
    expect(failed).toContain("data-receipt");
    expect(draw(item("receipt", { state: "interrupted" }))).toContain("修连接 中断了");
  });

  it("keeps Retry on a failed start, with no pointer to a card that is not there", () => {
    seed({ todos: [todo({ start_error: "rpc: closed" })] });
    const html = draw(item("receipt", { state: "failed" }));
    expect(html).toContain("重试");
    expect(html).toContain("复制");
  });

  it("keeps reviews and reports out of the chat", () => {
    seed({ todos: [todo({})] });
    expect(draw(item("review", { verdict: "accepted" }))).toBe("");
    expect(draw(item("report", { text: "50%" }))).toBe("");
  });

  it("has the hand-off card say only that the task did not start while a failed card below says why", () => {
    seed({ todos: [todo({ start_error: "rpc: closed" })] });
    const task = item("task", {});
    expect(draw(task)).toContain("没开起来：rpc: closed");
    Object.assign(getFeedState() as unknown as Record<string, unknown>, { feeds: { c1: [task, item("receipt", { state: "failed" })] } });
    const html = draw(task);
    expect(html).toContain("没开起来");
    expect(html).not.toContain("rpc: closed");
  });
});
