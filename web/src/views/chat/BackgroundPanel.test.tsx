// @vitest-environment jsdom
// An Agent's Background tasks side panel (#5421, after cindy's Background tasks tab): Running (the /loop, the running
// workflow runs oldest first, the background tasks) and Completed (ended runs newest first); a row's kind mark, status
// mark, title and "status · progress · duration · tokens · tool uses"; a run's detail with Back; a command's output in
// the file panel; Stop for the loop that asks first; "No background tasks in this session yet" when there is nothing.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia = ((query: string) => ({
    matches: query.includes("reduce"),
    media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { provideBackgroundChat, type BackgroundPanel as Asked } from "../../lib/background-panel";
import { closePanel, getPanel } from "../../lib/panel";
import { reset, upsertSession } from "../../lib/store";
import type { BackgroundTask, LoopInfo, Session, Workflow } from "../../lib/types";
import { BackgroundPanel, backgroundSections } from "./BackgroundPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();

const run = (task: string, over: Partial<Workflow> = {}): Workflow => ({
  task_id: task,
  name: `run-${task}`,
  summary: `What ${task} is for`,
  status: "running",
  running: true,
  tool_use_id: `call-${task}`,
  phases: ["Scan"],
  phase: "Scan",
  phase_stats: [{ title: "Scan", started: 5, done: 4, running: 1 }],
  agents_started: 5,
  agents_done: 4,
  agents_running: 1,
  started_at: at(-600),
  tokens: 12_300,
  calls: 7,
  agents: [
    { id: `${task}-a1`, session_id: `s@${task}-a1`, label: "scan:1", phase: "Scan", running: false, status: "done", started_at: at(-500), ended_at: at(-400) },
    { id: `${task}-a2`, session_id: `s@${task}-a2`, label: "scan:2", phase: "Scan", running: true, status: "running", started_at: at(-300) },
  ],
  ...over,
});
const ended = (task: string, end: number, over: Partial<Workflow> = {}) =>
  run(task, { status: "completed", running: false, ended_at: at(end), agents_running: 0, agents_done: 5, phase_stats: [{ title: "Scan", started: 5, done: 5, running: 0 }], ...over });

const loop: LoopInfo = { kind: "interval", prompt: "/loop 30m check the deploy", interval_seconds: 1800, next_at: at(240), reason: "watching the release" };
const shell: BackgroundTask = { id: "b1", kind: "shell", description: "Run the test suite", command: "npm test -- --watch=false", output_file: "/tmp/out/b1.log", started_at: at(-65) };
const sub: BackgroundTask = { id: "b2", kind: "agent", description: "Review the change", started_at: at(-30) };

let host: HTMLDivElement;
let root: Root;
const closed = vi.fn();

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
  vi.setSystemTime(T0);
  closed.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePanel();
  reset();
  vi.useRealTimers();
});

const put = (over: Partial<Session>) => act(() => upsertSession({ machine_id: "m", session_id: "s", cwd: "/work", status: "working", state: "working", ...over } as Session));
const draw = (opened: Asked = { kind: "background", mid: "m", sid: "s" }) => act(() => root.render(<BackgroundPanel mid="m" sid="s" opened={opened} onClose={closed} />));
const q = (sel: string) => host.querySelector<HTMLElement>(sel);
const all = (sel: string) => [...host.querySelectorAll<HTMLElement>(sel)];
/** The rows in order, by what they are. */
const rows = () => all("[data-bg-loop], [data-bg-run], [data-bg-task]").map((el) => el.dataset.bgRun ?? el.dataset.bgTask ?? "loop");
const meta = (sel: string) => q(`${sel} [data-bg-meta]`)!.textContent;

describe("the list", () => {
  it("says there is nothing yet, under its header", () => {
    put({});
    draw();
    expect(q("header")!.textContent).toContain("后台任务");
    expect(q("[data-background-empty]")!.textContent).toBe("这个会话还没有后台任务");
    expect(q("[data-background-list]")).toBeNull();
  });

  it("lists Running (the loop, runs oldest start first, background tasks) and Completed (newest end first)", () => {
    put({
      loop,
      background: [shell, sub],
      workflows: [
        run("w-new", { started_at: at(-60) }),
        ended("w-early", -3000),
        run("w-old", { started_at: at(-900) }),
        ended("w-late", -100),
        ended("w-gone", -2 * 86_400), // ended over a day ago: the node no longer keeps it
      ],
    });
    draw();
    const headings = all("h3");
    expect(headings.map((h) => h.textContent)).toEqual(["运行中", "已完成"]);
    expect(headings[0]!.className).toContain("uppercase");
    expect(all("section").map((s) => s.getAttribute("aria-labelledby"))).toEqual(headings.map((h) => h.id));
    expect(rows()).toEqual(["loop", "w-old", "w-new", "b1", "b2", "w-late", "w-early"]);
    expect(q('[data-background-section="completed"]')!.querySelectorAll("[data-bg-run]")).toHaveLength(2);
  });

  it("says a run's status, agents, duration, tokens and tool uses, with its kind and status marks", () => {
    put({ workflows: [run("w1"), ended("w2", -100, { status: "failed", agents_failed: 1, agents_done: 4 })] });
    draw();
    const live = q('[data-bg-run="w1"]')!;
    expect(live.querySelector("[data-bg-kind]")!.getAttribute("data-bg-kind")).toBe("workflow");
    expect(live.querySelector("[data-bg-status]")!.getAttribute("data-bg-status")).toBe("running");
    expect(live.querySelector("button")!.textContent).toContain("run-w1");
    expect(meta('[data-bg-run="w1"]')).toBe("运行中 · 4/5 Agent · 10m · 12.3k tokens · 7 次工具调用");
    const failed = q('[data-bg-run="w2"] [data-bg-status]')!;
    expect(failed.getAttribute("data-bg-status")).toBe("failed");
    expect(failed.className).toContain("text-red");
    expect(meta('[data-bg-run="w2"]')).toMatch(/^失败 · 5\/5 Agent · /);
  });

  it("counts a running one's time each second, and moves a run to Completed when it ends", () => {
    put({ workflows: [run("w1", { started_at: at(-5) })] });
    draw();
    expect(q('[data-bg-run="w1"] [data-bg-elapsed]')!.textContent).toBe("5s");
    act(() => vi.advanceTimersByTime(2000));
    expect(q('[data-bg-run="w1"] [data-bg-elapsed]')!.textContent).toBe("7s");
    put({ workflows: [ended("w1", 0, { started_at: at(-5) })] });
    expect(q('[data-background-section="running"]')).toBeNull();
    expect(q('[data-background-section="completed"] [data-bg-run="w1"]')).not.toBeNull();
  });
});

describe("a background task", () => {
  it("opens its output in the file panel, and keeps its command as a line that copies it", () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    put({ background: [shell, sub] });
    draw();
    const row = q('[data-bg-task="b1"]')!;
    expect(row.querySelector("[data-bg-kind]")!.getAttribute("data-bg-kind")).toBe("terminal");
    expect(meta('[data-bg-task="b1"]')).toBe("运行中 · 1m 5s");
    act(() => row.querySelector<HTMLElement>("[data-bg-row-button]")!.click());
    expect(getPanel()).toEqual({ kind: "file", mid: "m", path: "/tmp/out/b1.log", cwd: "/work" });
    const cmd = row.querySelector<HTMLElement>("[data-bg-command]")!;
    expect(cmd.title).toBe("npm test -- --watch=false");
    expect(cmd.getAttribute("aria-label")).toBe("拷贝命令");
    act(() => cmd.click());
    expect(writeText).toHaveBeenCalledWith("npm test -- --watch=false");
  });

  it("is a plain row when it has no output to open", () => {
    put({ background: [sub] });
    draw();
    const row = q('[data-bg-task="b2"]')!;
    expect(row.querySelector("[data-bg-kind]")!.getAttribute("data-bg-kind")).toBe("agent");
    expect(row.querySelector("button")).toBeNull();
    expect(row.textContent).toContain("Review the change");
  });
});

describe("the loop", () => {
  it("names its task, how often and when it wakes next, and stops only once asked twice", () => {
    const stopLoop = vi.fn();
    const take = provideBackgroundChat("m", "s", { stopLoop });
    put({ loop });
    draw();
    const row = q("[data-bg-loop]")!;
    expect(row.querySelector("[data-bg-kind]")!.getAttribute("data-bg-kind")).toBe("loop");
    expect(row.textContent).toContain("check the deploy");
    expect(meta("[data-bg-loop]")).toMatch(/^每 30 分钟 · 下次唤醒 \d\d:\d\d（还有 4 分钟）$/);
    const stop = row.querySelector<HTMLElement>('button[aria-label="停止循环"]')!;
    act(() => stop.click());
    expect(stopLoop).not.toHaveBeenCalled();
    const confirm = q("[data-bg-loop-confirm]")!;
    expect(confirm.textContent).toContain("不再安排下一次唤醒");
    act(() => [...confirm.querySelectorAll("button")].find((b) => b.textContent === "取消")!.click());
    expect(q("[data-bg-loop-confirm]")).toBeNull();
    act(() => stop.click());
    act(() => [...q("[data-bg-loop-confirm]")!.querySelectorAll("button")].find((b) => b.textContent === "停止循环")!.click());
    expect(stopLoop).toHaveBeenCalledTimes(1);
    expect(q("[data-bg-loop-confirm]")).toBeNull();
    take();
  });

  it("offers no Stop while no chat page shows the session, and offers it once one does (#5421 review)", () => {
    put({ loop });
    draw();
    expect(q('[data-bg-loop] button[aria-label="停止循环"]')).toBeNull();
    const stopLoop = vi.fn();
    let take = () => {};
    act(() => void (take = provideBackgroundChat("m", "s", { stopLoop })));
    expect(q('[data-bg-loop] button[aria-label="停止循环"]')).not.toBeNull();
    act(() => take());
    expect(q('[data-bg-loop] button[aria-label="停止循环"]')).toBeNull();
  });

  it("says Loop when it has no task in words", () => {
    put({ loop: { kind: "dynamic", prompt: "/loop" } });
    draw();
    expect(q("[data-bg-loop]")!.textContent).toContain("循环");
  });
});

describe("a run's detail", () => {
  it("opens from its row, with Back, the run's mark and name over its card, and Back gives the row the focus", () => {
    put({ workflows: [run("w1"), run("w2", { started_at: at(-30) })] });
    draw();
    act(() => q('[data-bg-run="w2"] [data-bg-row-button]')!.click());
    const detail = q('[data-background-detail="w2"]')!;
    expect(q("[data-background-list]")).toBeNull();
    const back = detail.querySelector<HTMLElement>('button[aria-label="返回"]')!;
    expect(document.activeElement).toBe(back);
    expect(detail.querySelector("h3")!.textContent).toBe("run-w2");
    expect(detail.querySelector<HTMLElement>('[data-workflow-card="running"]')!.dataset.workflowTask).toBe("w2");
    expect(detail.querySelectorAll("[data-phase]")).toHaveLength(1);
    act(() => back.click());
    expect(q("[data-background-detail]")).toBeNull();
    expect(document.activeElement).toBe(q('[data-bg-run="w2"] [data-bg-row-button]'));
  });

  it("opens at the run the panel was asked for, every time it is asked; a new ask without one is the list", () => {
    put({ workflows: [run("w1"), run("w2")] });
    draw({ kind: "background", mid: "m", sid: "s", run: "w1" });
    expect(q('[data-background-detail="w1"]')).not.toBeNull();
    act(() => q('button[aria-label="返回"]')!.click());
    expect(q("[data-background-list]")).not.toBeNull();
    draw({ kind: "background", mid: "m", sid: "s", run: "w1" }); // a card asks again: a new object
    expect(q('[data-background-detail="w1"]')).not.toBeNull();
    draw({ kind: "background", mid: "m", sid: "s", run: "w2" });
    expect(q('[data-background-detail="w2"]')).not.toBeNull();
    draw({ kind: "background", mid: "m", sid: "s" });
    expect(q("[data-background-list]")).not.toBeNull();
  });

  it("falls back to the list when the run is gone from the session", () => {
    put({ workflows: [run("w1")] });
    draw({ kind: "background", mid: "m", sid: "s", run: "w9" });
    expect(q("[data-background-detail]")).toBeNull();
    expect(q('[data-bg-run="w1"]')).not.toBeNull();
  });

  it("drops an ask for a run it does not have: the run coming later does not take the list away (#5421 review)", () => {
    put({ workflows: [run("w1")] });
    draw({ kind: "background", mid: "m", sid: "s", run: "w9" });
    put({ workflows: [run("w1"), run("w9")] });
    expect(q("[data-background-detail]")).toBeNull();
    expect(q('[data-bg-run="w9"]')).not.toBeNull();
  });

  it("shows the call in the chat only while the chat page can, and says so to it", () => {
    put({ workflows: [run("w1")] });
    draw({ kind: "background", mid: "m", sid: "s", run: "w1" });
    expect(q("[data-workflow-jump]")).toBeNull();
    const jumpToCall = vi.fn();
    // a page that comes while the detail shows: the detail offers it without being drawn again for it
    let take = () => {};
    act(() => void (take = provideBackgroundChat("m", "s", { stopLoop: () => {}, jumpToCall })));
    act(() => q("[data-workflow-jump]")!.click());
    expect(jumpToCall).toHaveBeenCalledWith("call-w1");
    expect(closed).not.toHaveBeenCalled(); // wide: the chat is beside the panel
    act(() => take());
    expect(q("[data-workflow-jump]")).toBeNull();
  });
});

describe("backgroundSections", () => {
  it("is empty for no session", () => {
    expect(backgroundSections(undefined, T0)).toEqual({ loop: undefined, running: [], tasks: [], completed: [] });
  });
});
