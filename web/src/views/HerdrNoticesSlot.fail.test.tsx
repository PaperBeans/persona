// @vitest-environment jsdom
// A herdr notice chunk that will not load (offline, or the hub was redeployed under the page) must not take the sidebar
// down: the slot draws nothing and reports it (#14339).
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const failed = vi.hoisted(() => ({ loads: 0 }));
vi.mock("./agents/HerdrNotices", () => {
  failed.loads++;
  throw new Error("Failed to fetch dynamically imported module: /assets/HerdrNotices-x.js");
});
const reported = vi.hoisted(() => [] as string[]);
vi.mock("../lib/error-reports", async (load) => ({ ...(await load<typeof import("../lib/error-reports")>()), reportBoundary: (_e: unknown, where: string) => reported.push(where) }));
import { HerdrNotices } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

it("draws nothing and keeps its siblings when the chunk cannot load", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  await act(async () => root.render(<div><span data-testid="sibling">Agents</span><HerdrNotices /></div>));
  for (let i = 0; i < 100 && !reported.length; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
  expect(reported).toEqual(["herdr_notices"]);
  expect(failed.loads).toBeGreaterThan(0);
  expect(host.querySelector('[data-testid="sibling"]')?.textContent).toBe("Agents");
  expect(host.querySelector('[data-testid="herdr-notice"]')).toBeNull();
  act(() => root.unmount());
  host.remove();
});
