// @vitest-environment jsdom
/// <reference types="node" />
// The "herdr isn't running" rows (#1916) draw nothing until a computer's herdr is read as stopped, so their code and
// words are not on the first screen: the sidebar's slot loads them with its first render (#14339).
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import startup from "../i18n/startup-namespaces.json";
import { startupMatcher } from "../i18n/startup-keys";
import { refresh, reset } from "../lib/store";
import { HerdrNotices } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
// Lets the chunk and the herdr read land inside act, a few ticks at a time, until `done`.
async function settle(done: () => boolean) {
  for (let i = 0; i < 100 && !done(); i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}
let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  reset();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const path = new URL(url, "http://hub.example.com").pathname;
    const body = path === "/api/v1/machines" ? { machines: [{ id: "m1", name: "Laptop", online: true }] }
      : path === "/api/v1/machines/m1/herdr" ? { autostart: false, running: false, known: true } : {};
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  }));
  await refresh();
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

describe("the sidebar's herdr notice slot", () => {
  it("loads the notices from their own chunk, with their words", () => {
    const sidebar = read("./Sidebar.tsx");
    expect(sidebar).not.toMatch(/^import[^;]*from\s+["']\.\/agents\/HerdrNotices["']/m);
    expect(sidebar).not.toMatch(/^import[^;]*from\s+["'][./]*lib\/herdr-notice["']/m);
    expect(sidebar).toMatch(/lazyPage\(\(\) => import\("\.\/agents\/HerdrNotices"\)\.then\(\(m\) => m\.HerdrNotices\)\)/);
    const inStartup = startupMatcher(startup);
    const words = [...new Set([...read("./agents/HerdrNotices.tsx"), ...read("../lib/herdr-notice.ts")].join("").match(/herdr-notice\.[a-z_]+/g) ?? [])];
    expect(words.length).toBeGreaterThan(10);
    for (const k of words) expect(inStartup(k), k).toBe(false);
  });

  it("asks the computer's herdr once its chunk is in and shows the row with its Start button", async () => {
    await act(async () => root.render(<HerdrNotices />));
    await settle(() => host.querySelector('[data-testid="herdr-notice"]') !== null);
    const row = host.querySelector('[data-testid="herdr-notice"]')!;
    expect(row.textContent).toContain("Laptop");
    expect(row.querySelector("button:not([aria-label])")?.textContent).toBeTruthy();
    expect(vi.mocked(fetch).mock.calls.filter(([u]) => String(u).endsWith("/machines/m1/herdr"))).toHaveLength(1);
  });
});
