// @vitest-environment jsdom
// Hovering a row moves the gliding highlight without re-rendering the sidebar: the row under the pointer is a ref and
// the box is GlideHighlight's own (Sidebar.tsx useGlide), so a sweep over the rows renders only that span.
import { act, Profiler } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// Sidebar's body calls sidebarConversations and AgentsBlock's calls arrangeSpaces on every render: counted
const calls = vi.hoisted(() => ({ sidebar: 0, agents: 0 }));
vi.mock("../lib/conversation-pins", async (load) => {
  const real = await load<typeof import("../lib/conversation-pins")>();
  return { ...real, sidebarConversations: (...a: Parameters<typeof real.sidebarConversations>) => (calls.sidebar++, real.sidebarConversations(...a)) };
});
vi.mock("../lib/agent-filter", async (load) => {
  const real = await load<typeof import("../lib/agent-filter")>();
  return { ...real, arrangeSpaces: (...a: Parameters<typeof real.arrangeSpaces>) => (calls.agents++, real.arrangeSpaces(...a)) };
});
import { refresh, reset } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }];
const sessions: Session[] = Array.from({ length: 12 }, (_, i) => ({ machine_id: "mac", session_id: `s${i + 1}`, title: `task ${i + 1}`, cwd: "/code", status: "idle", state: "idle" }));
const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions }, "/api/v1/machines": { machines } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  await act(async () => { await vi.dynamicImportSettled(); });
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  localStorage.clear();
  calls.sidebar = calls.agents = 0;
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  // jsdom lays nothing out: each link is a 28px row at its place among the links, everything else a tall box
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const i = this.tagName === "A" ? [...document.querySelectorAll("a")].indexOf(this as HTMLAnchorElement) : -1;
    const top = i < 0 ? 0 : i * 30;
    const height = i < 0 ? 10_000 : 28;
    return { top, bottom: top + height, height, left: 0, right: 200, width: 200, x: 0, y: top, toJSON() {} } as DOMRect;
  });
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("hovering sidebar rows", () => {
  it("moves the highlight without re-rendering the sidebar", async () => {
    let commits = 0;
    await act(() => refresh());
    act(() =>
      root.render(
        <Profiler id="sidebar" onRender={() => commits++}>
          <Sidebar route={{ name: "session", mid: "mac", sid: "gone" }} onSearch={() => {}} onNewAgent={() => {}} />
        </Profiler>,
      ),
    );
    await flush();
    const list = host.querySelector<HTMLElement>("[data-sidebar-rows]")!;
    const glide = list.querySelector<HTMLElement>("[data-glide]")!;
    const links = [...list.querySelectorAll<HTMLAnchorElement>("a")].filter((a) => a.textContent?.includes("task ")).slice(0, 10);
    expect(links).toHaveLength(10);
    expect(host.textContent).toContain("task 12");
    calls.sidebar = calls.agents = 0;
    commits = 0;

    const places = new Set<string>();
    for (const a of links) {
      act(() => void a.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null })));
      expect(glide.style.opacity).toBe("1");
      places.add(glide.style.transform);
    }
    // each row put the box at its own place
    expect(places.size).toBe(10);
    console.info(`10 hovers: Sidebar renders ${calls.sidebar}, AgentsBlock renders ${calls.agents}, commits ${commits}`);
    expect(calls.sidebar).toBe(0);
    expect(calls.agents).toBe(0);

    // the pointer leaves the list: the box goes back to the open row; this route has none in the list, so it fades out
    act(() => void links[9].dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(glide.style.opacity).toBe("0");
    expect(calls.sidebar).toBe(0);
  });
});
