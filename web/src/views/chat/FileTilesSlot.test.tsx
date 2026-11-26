// @vitest-environment jsdom
/// <reference types="node" />
// Most messages carry no file, so the tiles' code (FileTilesBody.tsx) is not on the first screen: the composer's slot
// (FileTiles.tsx) loads it the first time a message has a file, and draws nothing before.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComposerFile } from "../../lib/composer-files";

const body = vi.hoisted(() => ({ loads: 0 }));
vi.mock("./FileTilesBody", async (original) => {
  body.loads++;
  return original();
});
vi.hoisted(() => {
  (globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;
});
import { FileTiles } from "./FileTiles";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const file = (state: ComposerFile["state"]): ComposerFile => ({ id: state, name: `${state}.txt`, size: 12, sent: 0, state });
// Lets the chunk land inside act, a few ticks at a time, until `done`.
async function settle(done: () => boolean) {
  for (let i = 0; i < 100 && !done(); i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("the composer's file tiles slot", () => {
  it("imports the tiles only through its lazy chunk", () => {
    const staticImport = (from: string) => new RegExp(`^import[^;]*from\\s+["']${from}["']`, "m");
    expect(read("./FileTiles.tsx")).not.toMatch(staticImport("\\./FileTilesBody"));
    expect(read("./FileTiles.tsx")).toMatch(/lazyPage\(\(\) => import\("\.\/FileTilesBody"\)\.then\(\(m\) => m\.FileTiles\)\)/);
    expect(read("./Composer.tsx")).not.toMatch(staticImport("\\./FileTilesBody"));
  });

  it("loads nothing while the message has no file, and draws the tiles once it has one", async () => {
    await act(async () => root.render(<FileTiles draftKey="k" files={[]} />));
    await act(async () => new Promise((r) => setTimeout(r, 20)));
    expect(body.loads).toBe(0);
    expect(host.innerHTML).toBe("");

    await act(async () => root.render(<FileTiles draftKey="k" files={[file("uploading"), file("failed")]} />));
    await settle(() => host.children.length === 2);
    expect(body.loads).toBe(1);
    expect(Array.from(host.children, (tile) => tile.getAttribute("data-state"))).toEqual(["uploading", "failed"]);

    // the last one removed: nothing drawn, and the chunk is not asked for again
    await act(async () => root.render(<FileTiles draftKey="k" files={[]} />));
    expect(host.innerHTML).toBe("");
    await act(async () => root.render(<FileTiles draftKey="k" files={[file("ready")]} />));
    expect(host.querySelectorAll("[data-remove]")).toHaveLength(1);
    expect(body.loads).toBe(1);
  });
});
