// @vitest-environment jsdom
// The command palette and the new-agent dialog are lazy chunks (App.tsx): neither is mounted before its first open,
// and once opened each stays mounted when it closes, so its exit animation and focus restore run as before.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// the stand-ins record each render's open prop (undefined: not mounted)
const seen = vi.hoisted(() => ({ help: undefined as boolean | undefined, helpMounts: 0, palette: undefined as boolean | undefined, newAgent: undefined as boolean | undefined, mounts: 0, fail: false, failNewAgent: false, failed: 0 }));
vi.mock("./views/Palette", async () => {
  const { useEffect } = await import("react");
  return {
    Palette: ({ open }: { open: boolean }) => {
      if (seen.fail && ++seen.failed) throw new Error("Failed to fetch dynamically imported module");
      seen.palette = open;
      useEffect(() => void seen.mounts++, []);
      return null;
    },
  };
});
vi.mock("./views/NewAgent", () => ({
  NewAgent: ({ open }: { open: boolean }) => {
    if (seen.failNewAgent && ++seen.failed) throw new Error("Failed to fetch dynamically imported module");
    seen.newAgent = open;
    return null;
  },
}));
vi.mock("./views/Sidebar", () => ({ Sidebar: () => null, SidebarRail: () => null }));
vi.mock("./views/Home", () => ({ Home: () => null }));
vi.mock("./views/NewWorktreeHost", () => ({ NewWorktreeHost: () => null }));
vi.mock("./views/SidePanel", () => ({ SidePanel: () => null }));
vi.mock("./views/ShortcutHelp", async load => {
  const real = await load<typeof import("./views/ShortcutHelp")>();
  const { useEffect } = await import("react");
  const { useHelpOpen } = await import("./lib/shortcut-help");
  return { ...real, ShortcutHelp: () => {
    seen.help = useHelpOpen();
    useEffect(() => void seen.helpMounts++, []);
    return <real.ShortcutHelp />;
  } };
});
const reported = vi.hoisted(() => ({ list: [] as { boundary: string; chunk: boolean }[] }));
vi.mock("./lib/error-reports", async (load) => ({
  ...(await load<typeof import("./lib/error-reports")>()),
  reportBoundary: (_e: unknown, boundary: string, chunk: boolean) => void reported.list.push({ boundary, chunk }),
}));
vi.mock("./lib/store", async (load) => ({ ...(await load<typeof import("./lib/store")>()), connect: () => {}, disconnect: () => {} }));
import { refresh, reset } from "./lib/store";
import { setToken } from "./lib/api";
import { closePalette, openPalette } from "./lib/palette";
import { openNewAgent } from "./lib/new-agent";
import { closeHelp } from "./lib/shortcut-help";
import { App } from "./App";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions: [] }, "/api/v1/machines": { machines: [{ id: "mac", name: "MacBook", online: true }] } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
/**
 * Waits inside act until `check` passes (5 s at most): a lazy chunk's import can take a while on a busy machine, and
 * it has to finish inside an act for React to draw what it suspended on.
 */
const until = async (check: () => void) => {
  for (let i = 0; i < 500; i++) {
    await act(async () => { await new Promise((ok) => setTimeout(ok, 10)); });
    try {
      return check();
    } catch {
      // not yet
    }
  }
  check();
};

beforeEach(() => {
  localStorage.clear();
  setToken("tok");
  seen.palette = seen.newAgent = seen.help = undefined;
  seen.helpMounts = 0;
  seen.mounts = seen.failed = 0;
  seen.fail = seen.failNewAgent = false;
  reported.list = [];
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePalette();
  closeHelp();
  reset();
  vi.unstubAllGlobals();
});

describe("the palette and the new-agent dialog", () => {
  it("keeps the help shortcut live before the dialog loads and retains its mount across close and reopen", async () => {
    await act(() => refresh());
    await act(async () => root.render(<App />)); await flush();
    expect(seen.help).toBeUndefined(); expect(seen.helpMounts).toBe(0);
    const open = async () => {
      const key = new KeyboardEvent("keydown", { key: "/", ctrlKey: true, bubbles: true, cancelable: true });
      await act(async () => { dispatchEvent(key); });
      expect(key.defaultPrevented).toBe(true);
      await until(() => expect(seen.help).toBe(true));
      expect(document.querySelector('[role="dialog"] ul li')).not.toBeNull();
    };
    await open();
    await act(async () => { dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })); }); await flush();
    expect(seen.help).toBe(false);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    await open();
    expect(seen.helpMounts).toBe(1);
  });

  it("mount on their first open and stay mounted when closed", async () => {
    await act(() => refresh());
    act(() => root.render(<App />));
    await flush();
    expect(seen.palette).toBeUndefined();
    expect(seen.newAgent).toBeUndefined();

    await act(async () => openPalette("jump"));
    await flush();
    await until(() => expect(seen.palette).toBe(true));
    await act(async () => closePalette());
    await flush();
    expect(seen.palette).toBe(false);
    await act(async () => openPalette("command"));
    await flush();
    expect(seen.palette).toBe(true);
    expect(seen.mounts).toBe(1);

    await act(async () => openNewAgent({}));
    await flush();
    await until(() => expect(seen.newAgent).toBe(true));
  });

  // the open entry points (sidebar Search, the New agent buttons) only ever ask to open: a failed overlay must not be
  // left open, or the next ask changes nothing and the button stays dead until a reload
  it("keep a failed palette from taking the shell down, and try again on the next open", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    await act(() => refresh());
    act(() => root.render(<App />));
    await flush();
    seen.fail = true;
    await act(async () => openPalette("jump"));
    // the chunk has loaded and its render failed
    await until(() => expect(seen.failed).toBeGreaterThan(0));
    await flush();
    expect(seen.palette).toBeUndefined();
    // reported as a warning (a chunk that would not load); the overlay still closes as before
    expect(reported.list[0]).toEqual({ boundary: "overlay_boundary", chunk: true });
    // the rest of the shell still works
    await act(async () => openNewAgent({}));
    await until(() => expect(seen.newAgent).toBe(true));

    seen.fail = false;
    await act(async () => openPalette("jump"));
    await until(() => expect(seen.palette).toBe(true));
    error.mockRestore();
  });

  it("keep a failed new-agent dialog from taking the shell down, and try again on the next open", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    await act(() => refresh());
    act(() => root.render(<App />));
    await flush();
    seen.failNewAgent = true;
    await act(async () => openNewAgent({}));
    await until(() => expect(seen.failed).toBeGreaterThan(0));
    await flush();
    expect(seen.newAgent).toBeUndefined();

    seen.failNewAgent = false;
    await act(async () => openNewAgent({}));
    await until(() => expect(seen.newAgent).toBe(true));
    error.mockRestore();
  });
});
