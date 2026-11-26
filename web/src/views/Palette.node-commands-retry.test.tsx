// @vitest-environment jsdom
// The palette's node commands (lib/node-palette.ts) load the first time it opens. A load that fails (the hub was
// redeployed under the page, or it is offline) is not kept: the next opening asks again, as a lazy page does.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { closePalette, openPalette, usePalette } from "../lib/palette";
import { Palette } from "./Palette";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  (globalThis as Record<string, unknown>).matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

// Stands in for the node's commands: the first load fails as a chunk that could not be fetched, the next registers one
// command, as the real module does when imported.
const loads = vi.hoisted(() => ({ count: 0 }));
vi.mock("../lib/node-palette", async () => {
  loads.count++;
  if (loads.count === 1) throw new TypeError("Failed to fetch dynamically imported module");
  const { registerContextCommands } = await import("../lib/palette");
  registerContextCommands(() => [{ id: "node:qa-retry", section: "Pane · build", sectionAsWritten: true, title: "Retry this pane…", run: () => {} }]);
  return {};
});

let host: HTMLDivElement;
let root: Root;

function Shell() {
  const { open } = usePalette();
  return <Palette open={open} onClose={closePalette} onNewAgent={() => {}} />;
}

const option = (name: string) => [...document.querySelectorAll<HTMLElement>("[role=option]")].find((o) => o.textContent?.includes(name));
const settle = () => act(() => new Promise((ok) => setTimeout(ok, 50)));

beforeEach(async () => {
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  Element.prototype.scrollIntoView ??= () => {};
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<Shell />));
  await settle();
});

afterEach(async () => {
  act(() => closePalette());
  await settle();
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

it("asks for the node's commands again on the next opening after a failed load", async () => {
  act(() => openPalette("command"));
  await vi.waitFor(() => {
    expect(loads.count).toBe(1);
  });
  await settle();
  expect(option("Retry this pane…")).toBeUndefined();
  act(() => closePalette());
  await settle();
  act(() => openPalette("command"));
  await vi.waitFor(() => {
    expect(loads.count).toBe(2);
    expect(option("Retry this pane…")).toBeDefined();
  });
});
