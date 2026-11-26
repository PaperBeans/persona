// @vitest-environment jsdom
// Shared directory-tree splitter and width hook (ui/TreePaneSplitter.tsx, #14926): pointer drag with capture,
// clamping to [160, 420] and container room, localStorage persistence, double-click/Enter reset, and arrow keys.
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TREE_PANE_DEFAULT, TREE_PANE_MAX, TREE_PANE_MIN } from "../lib/layout";
import { RESIZE_STEP } from "./ResizeHandle";
import { TreePaneSplitter, useResizableTreeWidth } from "./TreePaneSplitter";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const STORAGE_KEY = "persona.testTreeWidth";
let host: HTMLDivElement;
let root: Root;

function Harness({ minViewerWidth = 200 }: { minViewerWidth?: number }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const pane = useResizableTreeWidth({
    storageKey: STORAGE_KEY,
    minViewerWidth,
    containerRef,
  });
  return (
    <div ref={containerRef} data-container>
      <div id="tree-pane" style={{ width: `${pane.width}px` }} />
      <TreePaneSplitter
        value={pane.width}
        min={pane.min}
        max={pane.max}
        onResize={pane.setWidth}
        onCommit={pane.commitWidth}
        onReset={pane.resetWidth}
        label="Resize tree"
        title="Drag to resize"
        controls="tree-pane"
      />
    </div>
  );
}

const splitter = () => host.querySelector<HTMLElement>('[role="separator"]')!;
const treePane = () => host.querySelector<HTMLElement>("#tree-pane")!;

const pointer = (target: EventTarget, type: string, clientX: number, pointerId = 1) => {
  const e = new PointerEvent(type, { clientX, pointerId, button: 0, bubbles: true, cancelable: true });
  act(() => void target.dispatchEvent(e));
  return e;
};

const key = (k: string) => {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true });
  act(() => void splitter().dispatchEvent(e));
  return e;
};

beforeEach(() => {
  localStorage.removeItem(STORAGE_KEY);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.removeAttribute("style");
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("TreePaneSplitter and useResizableTreeWidth (#14926)", () => {
  it("renders an accessible vertical separator with 240px default and [160, 420] bounds", () => {
    act(() => root.render(<Harness />));
    const el = splitter();
    expect(el.tabIndex).toBe(0);
    expect(el.getAttribute("aria-orientation")).toBe("vertical");
    expect(el.getAttribute("aria-label")).toBe("Resize tree");
    expect(el.getAttribute("aria-controls")).toBe("tree-pane");
    expect(el.getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));
    expect(el.getAttribute("aria-valuemin")).toBe(String(TREE_PANE_MIN));
    expect(el.getAttribute("aria-valuemax")).toBe(String(TREE_PANE_MAX));
    expect(el.className).toContain("cursor-col-resize");
    expect(el.className).toContain("w-1.5");
    expect(el.querySelector("span")?.className).toContain("bg-line");
  });

  it("resizes via pointer drag with capture, clamps to [160, 420], and persists on pointerup", () => {
    act(() => root.render(<Harness />));
    const el = splitter();
    const capture = vi.fn();
    el.setPointerCapture = capture;

    const down = pointer(el, "pointerdown", 240);
    expect(down.defaultPrevented).toBe(true);
    expect(capture).toHaveBeenCalledWith(1);
    expect(document.body.style.cursor).toBe("col-resize");
    expect(document.body.style.userSelect).toBe("none");
    expect(el.hasAttribute("data-dragging")).toBe(true);
    expect(el.querySelector("span")?.className).toContain("bg-accent/50");

    // Drag wider within bounds
    pointer(window, "pointermove", 320);
    expect(el.getAttribute("aria-valuenow")).toBe("320");
    expect(treePane().style.width).toBe("320px");
    // Not persisted until drag finishes
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();

    // Drag past max clamps to 420
    pointer(window, "pointermove", 900);
    expect(el.getAttribute("aria-valuenow")).toBe(String(TREE_PANE_MAX));

    // Drag below min clamps to 160
    pointer(window, "pointermove", -300);
    expect(el.getAttribute("aria-valuenow")).toBe(String(TREE_PANE_MIN));

    // Settle at 300 and release
    pointer(window, "pointermove", 300);
    pointer(window, "pointerup", 300);
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");
    expect(el.hasAttribute("data-dragging")).toBe(false);
    expect(localStorage.getItem(STORAGE_KEY)).toBe("300");

    // Restores persisted width across remount
    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<Harness />));
    expect(splitter().getAttribute("aria-valuenow")).toBe("300");
    expect(treePane().style.width).toBe("300px");
  });

  it("dynamically caps width when the container narrows without overwriting stored width", () => {
    let room = 700;
    let onResize = () => {};
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ width: room }) as DOMRect);
    vi.stubGlobal("ResizeObserver", class {
      constructor(cb: () => void) { onResize = cb; }
      observe() {}
      disconnect() {}
    });

    localStorage.setItem(STORAGE_KEY, "360");
    act(() => root.render(<Harness minViewerWidth={200} />));
    expect(splitter().getAttribute("aria-valuenow")).toBe("360");
    expect(splitter().getAttribute("aria-valuemax")).toBe("420");

    // Narrow container to 440px -> max tree width is 440 - 200 = 240px
    room = 440;
    act(() => onResize());
    expect(splitter().getAttribute("aria-valuemax")).toBe("240");
    expect(splitter().getAttribute("aria-valuenow")).toBe("240");
    expect(localStorage.getItem(STORAGE_KEY)).toBe("360");

    // Expand container back -> stored 360px returns
    room = 700;
    act(() => onResize());
    expect(splitter().getAttribute("aria-valuenow")).toBe("360");
  });

  it("supports keyboard arrow adjustment, Home/End bounds, and double-click or Enter reset", () => {
    act(() => root.render(<Harness />));
    expect(key("ArrowRight").defaultPrevented).toBe(true);
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT + RESIZE_STEP));
    expect(localStorage.getItem(STORAGE_KEY)).toBe(String(TREE_PANE_DEFAULT + RESIZE_STEP));

    key("ArrowLeft");
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));

    key("End");
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_MAX));

    key("Home");
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_MIN));

    // Double-click resets to 240 and removes storage entry
    act(() => void splitter().dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();

    // Enter also resets after keyboard resize
    key("ArrowRight");
    expect(localStorage.getItem(STORAGE_KEY)).toBe(String(TREE_PANE_DEFAULT + RESIZE_STEP));
    key("Enter");
    expect(splitter().getAttribute("aria-valuenow")).toBe(String(TREE_PANE_DEFAULT));
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it("cleans up body cursor and selection styles if unmounted mid-drag", () => {
    act(() => root.render(<Harness />));
    pointer(splitter(), "pointerdown", 240);
    expect(document.body.style.cursor).toBe("col-resize");
    expect(document.body.style.userSelect).toBe("none");

    act(() => root.unmount());
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");
    root = createRoot(host);
  });
});
