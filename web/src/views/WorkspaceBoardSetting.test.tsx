// @vitest-environment jsdom
// "Workflow board" is off unless turned on (#2475): no sidebar entry and no discovery polling while off, the entry back once
// Settings › General › Agent turns it on, and a hub that says the board is on (one used before the switch) shows it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// jsdom has no matchMedia, which the sidebar's modules read when they load, and no ResizeObserver, which the
// sidebar's highlight uses (#2490)
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { getWorkspaceBoardSetting, resetWorkspaceBoardSettingForTest } from "../lib/workspace-board-setting";
import { AgentGroup } from "./settings/AgentGroup";
import { Sidebar } from "./Sidebar";
import { WorkspaceBoard } from "./WorkspaceBoard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const board = { revision: 2, columns: [{ id: "todo", title: "待办" }], cards: [], can_write: true };
let enabled = false;
let calls: { method: string; path: string; body?: unknown }[] = [];

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
function hub(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = String(input).split("?")[0]!;
  const method = init?.method ?? "GET";
  calls.push({ method, path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
  if (path === "/api/v1/workspace-board/settings") {
    if (method === "PUT") enabled = (JSON.parse(String(init!.body)) as { enabled: boolean }).enabled;
    return Promise.resolve(json({ enabled, can_write: true }));
  }
  if (path === "/api/v1/workspace-board") return Promise.resolve(json(board));
  if (path === "/api/v1/workspaces") return Promise.resolve(json({ workspaces: [] }));
  return Promise.resolve(json({}));
}

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const discoveryCalls = () => calls.filter((c) => c.path === "/api/v1/workspaces").length;
const sidebarEntry = () => host.querySelector('a[href="/workspace-board"]');

beforeEach(() => {
  enabled = false;
  calls = [];
  resetWorkspaceBoardSettingForTest();
  vi.stubGlobal("fetch", vi.fn(hub));
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("the workflow board switch", () => {
  it("is off by default: no sidebar entry, an 未开启 page with the way to settings, and no discovery polling", async () => {
    act(() => root.render(<><Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} /><WorkspaceBoard /></>));
    await flush();
    expect(getWorkspaceBoardSetting().enabled).toBe(false);
    expect(sidebarEntry()).toBeNull();
    expect(host.textContent).toContain("工作流看板未开启");
    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();
    expect(discoveryCalls()).toBe(0);
    expect(calls.some((c) => c.path === "/api/v1/workspace-board")).toBe(false);

    const open = [...host.querySelectorAll("button")].find((b) => b.textContent === "去设置打开")!;
    act(() => open.click());
    expect(location.pathname + location.search).toBe("/settings?tab=hub");
  });

  it("shows the sidebar entry once 设置 › 通用 › Agent turns it on, and keeps the switch on the hub", async () => {
    act(() => root.render(<><AgentGroup /><Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} /></>));
    await flush();
    const sw = host.querySelector<HTMLButtonElement>('button[role="switch"][aria-label="工作流看板"]')!;
    expect(sw.getAttribute("aria-checked")).toBe("false");
    expect(sidebarEntry()).toBeNull();
    act(() => sw.click());
    await flush();
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/workspace-board/settings", body: { enabled: true } });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect(sidebarEntry()).not.toBeNull();
  });

  it("opens the saved board when the hub says it is on (a board in use before the switch counts as on)", async () => {
    enabled = true;
    act(() => root.render(<><Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} /><WorkspaceBoard /></>));
    await flush();
    expect(sidebarEntry()).not.toBeNull();
    expect(host.textContent).not.toContain("工作流看板未开启");
    expect(calls.some((c) => c.path === "/api/v1/workspace-board")).toBe(true);
    const before = discoveryCalls();
    expect(before).toBeGreaterThan(0);
    await act(async () => { vi.advanceTimersByTime(10_000); });
    await flush();
    expect(discoveryCalls()).toBeGreaterThan(before);
  });
});
