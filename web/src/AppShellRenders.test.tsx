// @vitest-environment jsdom
// A sessions push does not re-render the shell (and the sidebar, palette, new-agent dialog and page it draws): the
// attention signals that read every push are a leaf of their own (AttentionSignals), which still puts the "Needs you"
// count in the tab title.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// every render of the shell draws its sidebar: these stand-ins count them and read nothing from the store themselves
const drawn = vi.hoisted(() => ({ sidebar: 0, home: 0 }));
vi.mock("./views/Sidebar", () => ({
  Sidebar: () => {
    drawn.sidebar++;
    return null;
  },
  SidebarRail: () => null,
}));
vi.mock("./views/Home", () => ({
  Home: () => {
    drawn.home++;
    return null;
  },
}));
vi.mock("./views/Palette", () => ({ Palette: () => null }));
vi.mock("./views/NewAgent", () => ({ NewAgent: () => null }));
vi.mock("./views/NewWorktreeHost", () => ({ NewWorktreeHost: () => null }));
vi.mock("./views/SidePanel", () => ({ SidePanel: () => null }));
vi.mock("./views/ShortcutHelp", () => ({ ShortcutHelp: () => null }));
vi.mock("./lib/store", async (load) => ({ ...(await load<typeof import("./lib/store")>()), connect: () => {}, disconnect: () => {} }));
import { getState, refresh, reset, upsertSession } from "./lib/store";
import { setToken } from "./lib/api";
import type { Machine, Session } from "./lib/types";
import { App } from "./App";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }];
const sessions: Session[] = ["s1", "s2"].map((id, i) => ({ machine_id: "mac", session_id: id, title: `任务 ${i + 1}`, cwd: "/code", status: "working", state: "working" }));
const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions }, "/api/v1/machines": { machines } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  localStorage.clear();
  setToken("tok");
  drawn.sidebar = drawn.home = 0;
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
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

describe("a sessions push", () => {
  it("leaves the shell alone and still counts 需要你 in the tab title", async () => {
    await act(() => refresh());
    act(() => root.render(<App />));
    await flush();
    expect(drawn.sidebar).toBeGreaterThan(0);
    expect(document.title).not.toMatch(/^\(\d+\)/);
    const sidebar = drawn.sidebar, home = drawn.home;

    act(() => upsertSession({ ...getState().sessions.mac[0], state: "needs_you", status: "needs_you" }));
    await flush();
    expect(document.title).toMatch(/^\(1\) /);
    act(() => upsertSession({ ...getState().sessions.mac[1], title: "任务 2（改）" }));
    await flush();
    expect(drawn.sidebar).toBe(sidebar);
    expect(drawn.home).toBe(home);

    act(() => upsertSession({ ...getState().sessions.mac[0], state: "working", status: "working" }));
    await flush();
    expect(document.title).not.toMatch(/^\(\d+\)/);
    expect(drawn.sidebar).toBe(sidebar);
  });
});
