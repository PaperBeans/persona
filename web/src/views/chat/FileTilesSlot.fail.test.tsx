// @vitest-environment jsdom
// A tiles chunk that will not load (offline, or the hub was redeployed under the page) must not take the composer down:
// the slot draws nothing and reports it.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
vi.hoisted(() => {
  (globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;
});
const failed = vi.hoisted(() => ({ loads: 0 }));
vi.mock("./FileTilesBody", () => {
  failed.loads++;
  throw new Error("Failed to fetch dynamically imported module: /assets/FileTilesBody-x.js");
});
const reported = vi.hoisted(() => [] as string[]);
vi.mock("../../lib/error-reports", async (load) => ({ ...(await load<typeof import("../../lib/error-reports")>()), reportBoundary: (_e: unknown, where: string) => reported.push(where) }));
import { FileTiles } from "./FileTiles";

it("draws nothing and keeps the composer when the chunk cannot load", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  const files = [{ id: "a", name: "a.txt", size: 12, sent: 0, state: "ready" as const }];
  await act(async () => root.render(<div><textarea data-testid="input" /><FileTiles draftKey="k" files={files} /></div>));
  for (let i = 0; i < 100 && !reported.length; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
  expect(reported).toEqual(["composer_files"]);
  expect(failed.loads).toBeGreaterThan(0);
  expect(host.querySelector('[data-testid="input"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="composer-files"]')).toBeNull();
  act(() => root.unmount());
  host.remove();
});
