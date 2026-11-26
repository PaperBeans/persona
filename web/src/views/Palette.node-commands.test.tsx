// @vitest-environment jsdom
// The node the user is on adds its menu to the palette's `>` commands (lib/node-palette.ts). That module is not on the
// first screen: the sidebar's dialogs read only lib/node-ask.ts, and the palette loads the commands the first time it
// opens, then lists them.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { closePalette, openPalette, usePalette } from "../lib/palette";
import { Palette } from "./Palette";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  (globalThis as Record<string, unknown>).matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

// Stands in for the node's commands: counts its loads and registers one command, as the real module does when imported.
const loads = vi.hoisted(() => ({ count: 0 }));
vi.mock("../lib/node-palette", async () => {
  const { registerContextCommands } = await import("../lib/palette");
  loads.count++;
  registerContextCommands(() => [{ id: "node:qa-rename", section: "Pane · build", sectionAsWritten: true, title: "Rename this pane…", run: () => {} }]);
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

describe("the node's commands in the palette", () => {
  it("load the first time the palette opens, once, and are listed in command mode", async () => {
    expect(loads.count).toBe(0); // mounted closed (as the idle preload does): nothing loaded yet
    act(() => openPalette("command"));
    await settle();
    expect(loads.count).toBe(1);
    expect(option("Rename this pane…")).toBeDefined();
    act(() => closePalette());
    await settle();
    act(() => openPalette("command"));
    await settle();
    expect(loads.count).toBe(1);
    expect(option("Rename this pane…")).toBeDefined();
  });

  it("are not imported by the first screen's sidebar, its node dialogs or the app shell", () => {
    for (const file of ["./Sidebar.tsx", "./NodePrompt.tsx", "../App.tsx"]) {
      const source = readFileSync(new URL(file, import.meta.url), "utf8");
      expect(source, file).not.toMatch(/["']\.\.?\/(?:lib\/)?node-palette["']/);
    }
  });
});
