// @vitest-environment jsdom
// The scheduled task editor's words: a new task says what is missing only once the user has changed something, its
// time hints name the time zone the hub reads them in (as the list's footer does), and its hints use everyday words
// and the permission option's real name.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { blankTask, withRepeat, type Task } from "../../lib/routines";
import { getState } from "../../lib/store";
import { Toasts } from "../../ui/atoms";
import { TaskEditor } from "./TaskEditor";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
let host: HTMLDivElement;
let root: Root;
const assistantBefore = getState().assistant;

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ error: "not found" }, 404))));
  Object.assign(getState(), { assistant: { machine_id: "server", timezone: "Europe/Paris", conversations: [] } });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  Object.assign(getState(), { assistant: assistantBefore });
  vi.unstubAllGlobals();
});

const open = (task: Task) => act(async () => root.render(<><TaskEditor task={task} onClose={() => {}} onSaved={() => {}} onDeleted={() => {}} /><Toasts /></>));
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const button = (text: string) => [...dialog().parentElement!.querySelectorAll("button")].find((b) => b.textContent === text)!;
const add = () => button("添加");
const title = () => dialog().querySelector<HTMLInputElement>("input:not([type])")!;
const stateNote = () => dialog().querySelector<HTMLTextAreaElement>("details textarea")!;
const type = (el: HTMLInputElement | HTMLTextAreaElement, value: string) => act(() => {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
});

describe("the scheduled task editor", () => {
  it("opens a blank task with a grey Add and no error, and says what is missing once something changed", async () => {
    await open(blankTask());
    expect(add().disabled).toBe(true);
    expect(document.body.textContent).not.toContain("要有标题");

    type(dialog().querySelector("textarea")!, "看看今天的日程");
    expect(document.body.textContent).toContain("要有标题");
    expect(add().disabled).toBe(true);

    type(dialog().querySelector<HTMLInputElement>("input:not([type])")!, "早报");
    expect(document.body.textContent).not.toContain("要有标题");
    expect(add().disabled).toBe(false);
  });

  it("names the hub's time zone for a daily time and a cron expression, not 'your time zone'", async () => {
    await open(blankTask());
    expect(dialog().textContent).toContain("按 Europe/Paris 时区");
    expect(dialog().textContent).not.toContain("你所在的时区");

    act(() => root.unmount());
    root = createRoot(host);
    await open(withRepeat(blankTask(), "cron", new Date()));
    expect(dialog().textContent).toContain("按 Europe/Paris 时区计算");
    expect(dialog().textContent).not.toContain("你的时区");
  });

  it("says 'your time zone' only when the hub doesn't name one", async () => {
    Object.assign(getState(), { assistant: { machine_id: "server", conversations: [] } });
    await open(blankTask());
    expect(dialog().textContent).toContain("按你所在的时区");
  });

  it("hints in everyday words, naming the permission option as the menu above it does", async () => {
    await open(blankTask());
    const text = dialog().textContent ?? "";
    expect(text).not.toContain("schedule_followup");
    const options = [...dialog().querySelectorAll("option")].map((o) => o.textContent);
    expect(options).toContain("自动审批");
    expect(text).toContain("可以选「自动审批」");
    expect(text).not.toContain("「自动」");
  });

  const taskWithNote = (): Task => ({
    ...blankTask(), id: "scheduled", title: "早报", prompt: "看看今天的日程",
    state_note: { text: "older note", revision: 1, updated_at: "2026-11-14T12:00:00Z" },
  });

  it("keeps a run's newer note when saving other fields after a failed Run now", async () => {
    const task = taskWithNote();
    const newerNote = { text: "newer note", revision: 2, updated_at: "2026-11-14T13:00:00Z" };
    const puts: Record<string, unknown>[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path.endsWith("/tasks/scheduled") && init?.method === "PUT") {
        const body = JSON.parse(String(init.body));
        puts.push(body);
        return json({ task: { ...task, title: body.title, state_note: newerNote } });
      }
      if (path.endsWith("/tasks/scheduled/run")) return json({ error: "Already running" }, 409);
      return json({ error: "not found" }, 404);
    }));

    await open(task);
    type(title(), "新早报");
    await act(async () => button("立即运行").click());
    expect(stateNote().value).toBe("newer note");
    type(title(), "工作日早报");
    await act(async () => button("保存").click());

    expect(puts.map((body) => body.title)).toEqual(["新早报", "工作日早报"]);
    for (const body of puts) {
      expect(body).not.toHaveProperty("state_text");
      expect(body).not.toHaveProperty("state_revision");
    }
  });

  it("saves a changed note with its original revision and keeps the draft if the hub rejects it", async () => {
    const puts: Record<string, unknown>[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PUT") {
        puts.push(JSON.parse(String(init.body)));
        return json({ error: "任务状态笔记已更新。请重新打开编辑器后再试。", code: "task_state_changed" }, 409);
      }
      return json({ error: "not found" }, 404);
    }));

    await open(taskWithNote());
    type(stateNote(), "manual note");
    await act(async () => button("保存").click());

    expect(puts).toHaveLength(1);
    expect(puts[0]).toMatchObject({ state_text: "manual note", state_revision: 1 });
    expect(puts[0]).not.toHaveProperty("state_note");
    expect(stateNote().value).toBe("manual note");
    expect(document.body.textContent).toContain("任务状态笔记已更新。请重新打开编辑器后再试。");
  });
});
