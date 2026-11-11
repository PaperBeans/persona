// @vitest-environment jsdom
// The Agents block's grouping (#2812) against the store: By to-do waits for the first GET /todos instead of drawing every
// agent under No to-do and moving it, and a hub without todos hides To-dos and shows By machine while keeping the choice.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// jsdom has no matchMedia (the sidebar's modules read it when they load) and no ResizeObserver (its highlight)
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { connect, refresh, reset } from "../lib/store";
import type { Todo } from "../lib/todos";
import type { Machine, Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  constructor() {
    FakeSocket.all.push(this);
  }
  send() {}
  close() {
    this.readyState = 3;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  push(msg: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
}

const mac: Machine = { id: "mac", name: "工作电脑", online: true };
const agent: Session = { machine_id: "mac", session_id: "s1", title: "改发布脚本", cwd: "/code/weather", status: "working", state: "working" };
const todo: Todo = { id: "td1", title: "天气 App 发 1.3 版", created_at: "2026-11-05T00:00:00Z", updated_at: "2026-11-05T00:00:00Z", agents: [{ machine_id: "mac", session_id: "s1", linked_at: "2026-11-05T00:00:00Z" }] };

const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
/** fetch for a hub with `mac` and its one agent; GET /todos answers with `todos`. */
const hub = (todos: () => Promise<Response>) =>
  vi.fn((url: string) => {
    if (url.endsWith("/api/v1/todos")) return todos();
    if (url.endsWith("/api/v1/sessions")) return json({ sessions: [agent] });
    if (url.endsWith("/api/v1/machines")) return json({ machines: [mac] });
    return json({});
  });

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const grouping = () => host.querySelector<HTMLElement>('[role="radiogroup"][aria-label="Agent 分组"]')!;
const segments = () => [...grouping().querySelectorAll<HTMLButtonElement>('[role="radio"]')];
const checked = () => segments().find((b) => b.getAttribute("aria-checked") === "true")?.getAttribute("aria-label");
// the Agents block's body: the segments, the quick filter and the groups (the personal agents' Loading... is elsewhere)
const agentsBlock = () => grouping().closest<HTMLElement>("[data-sidebar-agents]")!;
const render = async () => {
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
};

beforeEach(() => {
  localStorage.clear();
  reset();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("按待办 before the todos have loaded", () => {
  it("says 加载中… until GET /todos answers, then draws the agent under its todo, never under 没挂待办的 first", async () => {
    let answer!: (r: Response) => void;
    const todos = new Promise<Response>((r) => (answer = r));
    vi.stubGlobal("fetch", hub(() => todos));
    // the socket pushes the machines and sessions while GET /todos (asked for on hello) is still on its way
    connect();
    const sock = FakeSocket.all[FakeSocket.all.length - 1];
    sock.open();
    sock.push({ type: "hello", server_version: "v1" });
    sock.push({ type: "machines", machines: [mac] });
    sock.push({ type: "sessions", machine_id: "mac", sessions: [agent] });
    await render();
    expect(checked()).toBe("按待办");
    expect(agentsBlock().textContent).toContain("加载中…");
    expect(agentsBlock().textContent).not.toContain("没挂待办的");
    expect(agentsBlock().textContent).not.toContain("改发布脚本");

    await act(async () => answer(new Response(JSON.stringify({ todos: [todo] }), { status: 200 })));
    await flush();
    expect(agentsBlock().textContent).not.toContain("加载中…");
    expect(agentsBlock().textContent).toContain("天气 App 发 1.3 版");
    expect(agentsBlock().textContent).toContain("改发布脚本");
    expect(agentsBlock().textContent).not.toContain("没挂待办的");
  });

  it("draws the todos as soon as GET /todos answers, while GET /sessions is still on its way", async () => {
    const slow = vi.fn((url: string) => {
      if (url.endsWith("/api/v1/todos")) return json({ todos: [todo] });
      if (url.endsWith("/api/v1/sessions")) return new Promise<Response>(() => {});
      if (url.endsWith("/api/v1/machines")) return json({ machines: [mac] });
      return json({});
    });
    vi.stubGlobal("fetch", slow);
    connect();
    const sock = FakeSocket.all[FakeSocket.all.length - 1];
    sock.open();
    sock.push({ type: "hello", server_version: "v1" });
    sock.push({ type: "machines", machines: [mac] });
    sock.push({ type: "sessions", machine_id: "mac", sessions: [agent] });
    await render();
    expect(agentsBlock().textContent).not.toContain("加载中…");
    expect(agentsBlock().textContent).toContain("天气 App 发 1.3 版");
    expect(agentsBlock().textContent).toContain("改发布脚本");
  });

  it("does not wait under another grouping", async () => {
    localStorage.setItem("persona.agentGrouping", "machine");
    vi.stubGlobal("fetch", hub(() => new Promise<Response>(() => {})));
    connect();
    const sock = FakeSocket.all[FakeSocket.all.length - 1];
    sock.open();
    sock.push({ type: "hello", server_version: "v1" });
    sock.push({ type: "machines", machines: [mac] });
    sock.push({ type: "sessions", machine_id: "mac", sessions: [agent] });
    await render();
    expect(checked()).toBe("按机器");
    expect(agentsBlock().textContent).not.toContain("加载中…");
    expect(agentsBlock().textContent).toContain("工作电脑");
  });
});

describe("group controls in every Agents view", () => {
  it.each(["todo", "machine", "repo", "space", "state"])("opens and folds all levels in %s without folding the block", async (mode) => {
    localStorage.setItem("persona.agentGrouping", mode);
    vi.stubGlobal("fetch", hub(() => json({ todos: [todo] })));
    await refresh();
    await render();
    const action = (label: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
    const disclosures = () => [...agentsBlock().querySelectorAll<HTMLButtonElement>("[data-sidebar-group-heading] button[aria-expanded]")].filter((b) => !b.closest("[data-sidebar-agents-controls]"));
    expect(disclosures().length).toBeGreaterThan(0);
    expect(host.querySelectorAll("[data-sidebar-agents-controls] button[title=\"全部折叠\"], [data-sidebar-agents-controls] button[title=\"全部展开\"]")).toHaveLength(1);
    await act(async () => { action("全部折叠").click(); await Promise.resolve(); });
    expect(disclosures().every((b) => b.getAttribute("aria-expanded") === "false")).toBe(true);
    expect(grouping().closest("[inert]")).toBeNull();
    await flush();
    await act(async () => { action("全部展开").click(); await Promise.resolve(); });
    expect(disclosures().every((b) => b.getAttribute("aria-expanded") === "true")).toBe(true);
    await flush();
    const first = disclosures()[0]!;
    await act(async () => { action("全部折叠").click(); await Promise.resolve(); });
    await flush();
    await act(async () => { first.click(); await Promise.resolve(); });
    await flush();
    expect(first.getAttribute("aria-expanded")).toBe("true");
    expect(action("全部折叠")).not.toBeNull();
    await act(async () => { action("全部折叠").click(); await Promise.resolve(); });
    expect(first.getAttribute("aria-expanded")).toBe("false");
  });
});

describe("a hub without todos (GET /todos 404)", () => {
  it("hides 待办 and shows 按机器 for a browser that never chose", async () => {
    vi.stubGlobal("fetch", hub(() => json({ error: "not found" }, 404)));
    await refresh();
    await render();
    expect(segments().map((b) => b.textContent)).toEqual(["机器", "仓库", "项目", "状态"]);
    expect(checked()).toBe("按机器");
    expect(agentsBlock().textContent).toContain("工作电脑");
    expect(agentsBlock().textContent).not.toContain("加载中…");
    expect(localStorage.getItem("persona.agentGrouping")).toBeNull();
  });

  it("keeps a 按待办 chosen before and shows it again once the hub has todos", async () => {
    localStorage.setItem("persona.agentGrouping", "todo");
    vi.stubGlobal("fetch", hub(() => json({ error: "not found" }, 404)));
    await refresh();
    await render();
    expect(checked()).toBe("按机器");
    expect(localStorage.getItem("persona.agentGrouping")).toBe("todo");

    vi.stubGlobal("fetch", hub(() => json({ todos: [todo] })));
    await act(async () => { await refresh(); });
    await flush();
    expect(segments()[0].getAttribute("aria-label")).toBe("按待办");
    expect(checked()).toBe("按待办");
    expect(agentsBlock().textContent).toContain("天气 App 发 1.3 版");
  });
});
