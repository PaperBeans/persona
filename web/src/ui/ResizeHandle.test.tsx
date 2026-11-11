// @vitest-environment jsdom
// The drag handle on a column's edge (ui/ResizeHandle.tsx): a pointer drag reports each step and keeps the width once at
// the end, clamped; it captures the pointer and stops text selection while it goes; keys and a double-click work too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RESIZE_STEP, ResizeHandle } from "./ResizeHandle";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const calls = { resize: vi.fn(), commit: vi.fn(), reset: vi.fn() };

const draw = (edge: "left" | "right", value = 300) =>
  act(() =>
    root.render(
      <ResizeHandle edge={edge} value={value} min={240} max={480} onResize={calls.resize} onCommit={calls.commit} onReset={calls.reset} label="Resize" controls="col" />,
    ),
  );
const handle = () => host.querySelector<HTMLElement>('[role="separator"]')!;
const pointer = (target: EventTarget, type: string, clientX: number, pointerId = 1) => {
  const e = new PointerEvent(type, { clientX, pointerId, button: 0, bubbles: true, cancelable: true });
  act(() => void target.dispatchEvent(e));
  return e;
};
const key = (k: string) => {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true });
  act(() => void handle().dispatchEvent(e));
  return e;
};

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  Object.values(calls).forEach((f) => f.mockReset());
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.removeAttribute("style");
});

describe("the resize handle", () => {
  it("is a focusable vertical separator with its width and bounds", () => {
    draw("right");
    const h = handle();
    expect(h.tabIndex).toBe(0);
    expect(h.getAttribute("aria-orientation")).toBe("vertical");
    expect(h.getAttribute("aria-label")).toBe("Resize");
    expect(h.getAttribute("aria-controls")).toBe("col");
    expect([h.getAttribute("aria-valuenow"), h.getAttribute("aria-valuemin"), h.getAttribute("aria-valuemax")]).toEqual(["300", "240", "480"]);
    expect(h.className).toContain("cursor-col-resize");
    expect(h.className).toContain("touch-none");
  });

  it("drags a right edge: each step drawn, clamped, kept once at the end", () => {
    draw("right");
    const capture = vi.fn();
    handle().setPointerCapture = capture;
    const down = pointer(handle(), "pointerdown", 100);
    expect(down.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(handle());
    expect(capture).toHaveBeenCalledWith(1);
    // no text selection and the resize cursor everywhere while it goes
    expect(document.body.style.userSelect).toBe("none");
    expect(document.body.style.cursor).toBe("col-resize");
    pointer(window, "pointermove", 140);
    expect(calls.resize).toHaveBeenLastCalledWith(340);
    expect(handle().getAttribute("aria-valuenow")).toBe("340");
    // past the end: held at the bound
    pointer(window, "pointermove", 900);
    expect(calls.resize).toHaveBeenLastCalledWith(480);
    pointer(window, "pointermove", -900);
    expect(calls.resize).toHaveBeenLastCalledWith(240);
    pointer(window, "pointermove", 60);
    expect(calls.resize).toHaveBeenLastCalledWith(260);
    expect(calls.commit).not.toHaveBeenCalled();
    pointer(window, "pointerup", 60);
    expect(calls.commit).toHaveBeenCalledTimes(1);
    expect(calls.commit).toHaveBeenCalledWith(260);
    expect(document.body.style.userSelect).toBe("");
    expect(document.body.style.cursor).toBe("");
    // the drag is over: moving the pointer does nothing more
    pointer(window, "pointermove", 200);
    expect(calls.resize).toHaveBeenCalledTimes(4);
  });

  it("drags a left edge the other way", () => {
    draw("left");
    pointer(handle(), "pointerdown", 500);
    pointer(window, "pointermove", 450);
    expect(calls.resize).toHaveBeenLastCalledWith(350);
    pointer(window, "pointerup", 450);
    expect(calls.commit).toHaveBeenCalledWith(350);
  });

  it("ignores another pointer, and keeps nothing when the drag is cancelled or did not move", () => {
    draw("right");
    pointer(handle(), "pointerdown", 100);
    pointer(window, "pointermove", 160, 2);
    expect(calls.resize).not.toHaveBeenCalled();
    pointer(window, "pointerup", 100);
    expect(calls.commit).not.toHaveBeenCalled();

    pointer(handle(), "pointerdown", 100);
    pointer(window, "pointermove", 150);
    pointer(window, "pointercancel", 150);
    // a cancelled pointer (the browser took it for a scroll) commits what it reached: the column is already drawn there
    expect(calls.commit).toHaveBeenCalledWith(350);
  });

  it("lets go of the window when it goes away mid-drag", () => {
    draw("right");
    pointer(handle(), "pointerdown", 100);
    act(() => root.unmount());
    expect(document.body.style.userSelect).toBe("");
    pointer(window, "pointermove", 200);
    pointer(window, "pointerup", 200);
    expect(calls.resize).not.toHaveBeenCalled();
    expect(calls.commit).not.toHaveBeenCalled();
    root = createRoot(host);
  });

  it("moves by RESIZE_STEP with the arrows (Right widens a right edge), to the bounds with Home / End", () => {
    draw("right");
    expect(key("ArrowRight").defaultPrevented).toBe(true);
    expect(calls.commit).toHaveBeenLastCalledWith(300 + RESIZE_STEP);
    key("ArrowLeft");
    expect(calls.commit).toHaveBeenLastCalledWith(300 - RESIZE_STEP);
    key("Home");
    expect(calls.commit).toHaveBeenLastCalledWith(240);
    key("End");
    expect(calls.commit).toHaveBeenLastCalledWith(480);
    draw("left");
    key("ArrowLeft");
    expect(calls.commit).toHaveBeenLastCalledWith(300 + RESIZE_STEP);
    expect(key("Tab").defaultPrevented).toBe(false);
  });

  it("goes back to the default on a double-click or Enter", () => {
    draw("right");
    act(() => void handle().dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(calls.reset).toHaveBeenCalledTimes(1);
    key("Enter");
    expect(calls.reset).toHaveBeenCalledTimes(2);
  });
});
