// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const reports = vi.hoisted(() => ({ list: [] as { boundary: string; chunk: boolean }[] }));
const reloads = vi.hoisted(() => ({ calls: 0 }));
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});
vi.mock("../lib/error-reports", () => ({
  reportBoundary: (_error: unknown, boundary: string, chunk: boolean) => void reports.list.push({ boundary, chunk }),
}));
vi.mock("./PageBoundary", async (load) => {
  const real = await load<typeof import("./PageBoundary")>();
  return {
    ...real,
    reloadPage: () => { reloads.calls++; },
    reloadForStaleChunk: (error: unknown) => real.reloadForStaleChunk(error, () => { reloads.calls++; }),
  };
});

import { refresh, reset } from "../lib/store";
import { setToken } from "../lib/api";
import { t } from "../i18n";
import { setAgentReposLoaderForTest, Sidebar } from "./Sidebar";

let host: HTMLDivElement, root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  reports.list = [];
  reloads.calls = 0;
  localStorage.clear();
  sessionStorage.clear();
  reset();
  setToken("chunk-fixture");
  localStorage.setItem("persona.agentGrouping", "repo");
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://fixture").pathname;
    const body = path.endsWith("/machines") ? { machines: [{ id: "m1", name: "Computer A", online: true }] }
      : path.endsWith("/sessions") ? { sessions: [] }
      : path.endsWith("/workspaces") ? { workspaces: [] }
      : {};
    return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  setAgentReposLoaderForTest(null);
  reset();
  setToken("");
  vi.unstubAllGlobals();
});

describe("By repo lazy chunk stale-hash recovery", () => {
  it("reloads once on first stale chunk failure and offers a Reload button on repeat failure", async () => {
    setAgentReposLoaderForTest(() => Promise.reject(new TypeError("Failed to fetch dynamically imported module: /assets/agent-repos-stale.js")));
    await act(() => refresh());
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(reloads.calls).toBe(1);
    expect(reports.list).toEqual([]);
    expect(sessionStorage.getItem("persona.chunkReload")).not.toBeNull();

    // Repeat failure within 60s reports the chunk warning and offers Reload instead of retrying the dead hash.
    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(reloads.calls).toBe(1);
    expect(reports.list).toEqual([{ boundary: "sidebar_agent_repos", chunk: true }]);
    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe(t("pageboundary.chunk_title"));
    const button = [...host.querySelectorAll("button")].find((b) => b.textContent === t("pageboundary.reload"))!;
    expect(button).toBeTruthy();
    act(() => button.click());
    expect(reloads.calls).toBe(2);
  });
});
