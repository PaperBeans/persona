// @vitest-environment jsdom
// Wide-screen File Workbench split mode in FilesPanel (#14926): directory tree on the left, draggable vertical
// splitter in the middle, and FileView on the right with clamping, localStorage persistence, and double-click reset.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.mock("../../lib/use-fs-live", () => ({ useFsLive: () => {} }));
import { setToken } from "../../lib/api";
import { FILES_TREE_WIDTH_KEY, TREE_PANE_DEFAULT, TREE_PANE_MAX, TREE_PANE_MIN } from "../../lib/layout";
import { getState } from "../../lib/store";
import { RESIZE_STEP } from "../../ui/ResizeHandle";
import { FilesPanel } from "./FilesPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
HTMLElement.prototype.scrollTo ??= () => {};

let host: HTMLDivElement;
let root: Root;
const original = { features: getState().features, machines: getState().machines };
const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); });
};
const splitter = () => host.querySelector<HTMLElement>('[data-files-panel] [role="separator"]');

beforeEach(() => {
  localStorage.clear();
  setToken("synthetic-A");
  Object.assign(getState(), {
    features: ["file_edit"],
    machines: [{ id: "m1", name: "Work computer", online: true, home: "/home/me", os: "linux" }],
  });
  // Wide screen: NARROW media query does not match.
  window.matchMedia = ((media: string) => ({
    matches: false,
    media,
    addEventListener() {},
    removeEventListener() {},
  })) as unknown as typeof window.matchMedia;
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const u = new URL(String(input), "http://local.invalid");
    if (u.pathname.endsWith("/git/status")) return Response.json({ is_repo: false, files: [], commits: [] });
    if (u.pathname.endsWith("/fs")) {
      return Response.json({
        path: "/home/me/app",
        entries: [
          { name: "src", dir: true, size: 0 },
          { name: "notes.txt", dir: false, size: 11 },
        ],
        truncated: false,
      });
    }
    if (u.pathname.endsWith("/file")) {
      return Response.json({ path: "/home/me/app/notes.txt", name: "notes.txt", size: 11, text: "hello world", truncated: false, is_dir: false });
    }
    return Response.json({});
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  document.body.removeAttribute("style");
  Object.assign(getState(), original);
  setToken("");
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("FilesPanel resizable directory-tree sidebar (#14926)", () => {
  it("shows a draggable splitter when a file is open on wide screens, persists width, and resets on double-click", async () => {
    await act(async () => root.render(<FilesPanel mid="m1" sid="s1" cwd="/home/me/app" file="/home/me/app/notes.txt" onClose={() => {}} />));
    await flush();

    const el = splitter();
    expect(el).not.toBeNull();
    expect(el!.getAttribute("aria-orientation")).toBe("vertical");
    expect(el!.getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));
    expect(el!.getAttribute("aria-valuemin")).toBe(String(TREE_PANE_MIN));
    expect(el!.getAttribute("aria-valuemax")).toBe(String(TREE_PANE_MAX));

    const controlsId = el!.getAttribute("aria-controls")!;
    const pane = document.getElementById(controlsId)!;
    expect(pane).not.toBeNull();
    expect(pane.style.getPropertyValue("--files-tree-w")).toBe(`${TREE_PANE_DEFAULT}px`);

    // Drag wider by 60px -> 300px
    act(() => void el!.dispatchEvent(new PointerEvent("pointerdown", { clientX: 240, pointerId: 1, button: 0, bubbles: true, cancelable: true })));
    act(() => void window.dispatchEvent(new PointerEvent("pointermove", { clientX: 300, pointerId: 1, button: 0, bubbles: true, cancelable: true })));
    act(() => void window.dispatchEvent(new PointerEvent("pointerup", { clientX: 300, pointerId: 1, button: 0, bubbles: true, cancelable: true })));
    expect(el!.getAttribute("aria-valuenow")).toBe("300");
    expect(pane.style.getPropertyValue("--files-tree-w")).toBe("300px");
    expect(localStorage.getItem(FILES_TREE_WIDTH_KEY)).toBe("300");

    // Keyboard ArrowRight increases by RESIZE_STEP
    act(() => void el!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true })));
    expect(el!.getAttribute("aria-valuenow")).toBe(String(300 + RESIZE_STEP));
    expect(localStorage.getItem(FILES_TREE_WIDTH_KEY)).toBe(String(300 + RESIZE_STEP));

    // Double-click resets back to default 240px and clears storage
    act(() => void el!.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(el!.getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));
    expect(localStorage.getItem(FILES_TREE_WIDTH_KEY)).toBeNull();
  });

  it("omits the splitter when no file is open", async () => {
    await act(async () => root.render(<FilesPanel mid="m1" sid="s1" cwd="/home/me/app" onClose={() => {}} />));
    await flush();
    expect(splitter()).toBeNull();
  });
});
