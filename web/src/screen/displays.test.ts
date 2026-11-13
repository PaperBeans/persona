// @vitest-environment jsdom
// Which of a Mac's displays the live screen offers, and where the page puts and keeps the one shown (#5685): the iOS
// app's VNCDisplaysTests cases for the node's arrangement, plus the web's own drawing and kept choice.
import { afterEach, describe, expect, it, vi } from "vitest";
import { clampInto, displayChoice, framing, keepPointerIn, layoutDisplays, machineDisplays, setDisplayChoice, type MachineDisplay } from "./displays";

describe("the displays offered", () => {
  // A laptop left of a larger main display, top edges level, sent at 2× and at 1×.
  it("scales the node's arrangement onto the framebuffer, numbered left to right", () => {
    const node: MachineDisplay[] = [
      { id: 3, x: 0, y: 0, width: 2560, height: 1440, main: true },
      { id: 1, x: -1728, y: 0, width: 1728, height: 1117 },
    ];
    expect(layoutDisplays(node, { w: 8576, h: 2880 })).toEqual([
      { id: "display-1", frame: { x: 0, y: 0, w: 3456, h: 2234 } },
      { id: "display-3", frame: { x: 3456, y: 0, w: 5120, h: 2880 } },
    ]);
    expect(layoutDisplays(node, { w: 4288, h: 1440 }).map((d) => d.frame.x)).toEqual([0, 1728]);
  });

  it("numbers displays stacked one above the other top to bottom", () => {
    const node: MachineDisplay[] = [
      { id: 1, x: 0, y: 0, width: 240, height: 150, main: true },
      { id: 2, x: 0, y: -150, width: 240, height: 150 },
    ];
    expect(layoutDisplays(node, { w: 480, h: 600 }).map((d) => [d.id, d.frame.y])).toEqual([["display-2", 0], ["display-1", 300]]);
  });

  // A display plugged in or out since the node looked, or a server that draws them otherwise, offers nothing; nor do
  // fewer than two displays.
  it("offers nothing for an arrangement that does not fit the framebuffer", () => {
    const node: MachineDisplay[] = [
      { id: 1, x: 0, y: 0, width: 1512, height: 982 },
      { id: 2, x: 1512, y: 0, width: 1920, height: 1080 },
    ];
    expect(layoutDisplays(node, { w: 3432, h: 1080 })).toHaveLength(2);
    expect(layoutDisplays(node, { w: 1512, h: 982 }), "only the laptop is sent").toEqual([]);
    expect(layoutDisplays(node, { w: 6864, h: 2260 }), "4% off its shape").toEqual([]);
    expect(layoutDisplays(node.slice(0, 1), { w: 1512, h: 982 })).toEqual([]);
    expect(layoutDisplays([], { w: 960, h: 300 })).toEqual([]);
    expect(layoutDisplays(node, { w: 0, h: 0 }), "no framebuffer yet").toEqual([]);
  });

  // An older node sends none; a list the page cannot read costs only the display choice.
  it("reads the status's displays, and nothing it cannot read", () => {
    const raw = [{ id: 3, x: 0, y: 0, width: 2560, height: 1440, main: true }, { id: 1, x: -1728, y: 0, width: 1728, height: 1117 }];
    expect(machineDisplays(raw)).toEqual(raw);
    expect(machineDisplays(undefined)).toEqual([]);
    expect(machineDisplays("two")).toEqual([]);
    expect(machineDisplays([{ id: 1, x: 0, y: 0, width: "240", height: 150 }])).toEqual([]);
    expect(machineDisplays([null])).toEqual([]);
  });
});

describe("one display on the page", () => {
  // Display 2 of a 960 × 300 framebuffer in a 240 × 200 stage: it fills the width, centered in the height, and the
  // framebuffer is drawn so that it lies under the box.
  it("draws the display as large as fits and lands the pointer on it", () => {
    const f = framing({ w: 240, h: 200 }, { w: 960, h: 300 }, { x: 480, y: 0, w: 480, h: 300 })!;
    expect(f.clip).toEqual({ x: 0, y: 25, w: 240, h: 150 });
    expect(f.inner).toEqual({ x: -240, y: 0, w: 480, h: 150 });
    // noVNC scales the framebuffer to `inner` and maps a point by its canvas's left / top and that scale: a click in the
    // middle of the box reaches the Mac at the middle of display 2.
    const k = f.inner.w / 960;
    const at = (x: number, y: number) => ({ x: (x - (f.clip.x + f.inner.x)) / k, y: (y - (f.clip.y + f.inner.y)) / k });
    expect(at(f.clip.x + f.clip.w / 2, f.clip.y + f.clip.h / 2)).toEqual({ x: 720, y: 150 });
    expect(at(f.clip.x, f.clip.y)).toEqual({ x: 480, y: 0 });
  });

  it("scales a display up to the room there is, and draws nothing without room", () => {
    const f = framing({ w: 1000, h: 1000 }, { w: 960, h: 300 }, { x: 0, y: 0, w: 480, h: 300 })!;
    expect(f.clip).toEqual({ x: 0, y: 187.5, w: 1000, h: 625 });
    expect(f.inner.w).toBeCloseTo(2000);
    expect(framing({ w: 0, h: 0 }, { w: 960, h: 300 }, { x: 0, y: 0, w: 480, h: 300 })).toBeNull();
  });

  it("keeps a point inside the box, on its last pixel at most", () => {
    const box = { left: 100, top: 50, right: 340, bottom: 200 };
    expect(clampInto(box, 20, 300)).toEqual({ x: 100, y: 199 });
    expect(clampInto(box, 400, 10)).toEqual({ x: 339, y: 50 });
    expect(clampInto(box, 150, 100)).toEqual({ x: 150, y: 100 });
  });

  // A centered box rarely has whole-pixel edges, and a dispatched mouse event's position comes out as whole pixels: a
  // point kept on the edge itself (81.5) would come back as 81, outside, and be sent again without end.
  it("keeps a point on whole pixels inside a box with fractional edges", () => {
    const box = { left: 100.5, top: 81.5, right: 340.5, bottom: 231.5 };
    expect(clampInto(box, 20, 20)).toEqual({ x: 101, y: 82 });
    expect(clampInto(box, 400, 300)).toEqual({ x: 339, y: 230 });
    expect(clampInto(box, 150.25, 100.75)).toEqual({ x: 150.25, y: 100.75 });
  });

  // While a button is down noVNC hears the pointer anywhere on the page: a drag out of the box must not reach the
  // display hidden next to it.
  it("keeps a drag that leaves the box on the display shown", () => {
    const box = document.createElement("div");
    const canvas = document.createElement("canvas");
    box.append(canvas);
    document.body.append(box);
    box.getBoundingClientRect = () => ({ left: 100, top: 50, right: 340, bottom: 200, width: 240, height: 150, x: 100, y: 50, toJSON() {} });
    const heard: [string, number, number, number][] = [];
    canvas.addEventListener("mousemove", (e) => heard.push([e.type, e.clientX, e.clientY, e.buttons]));
    canvas.addEventListener("mouseup", (e) => heard.push([e.type, e.clientX, e.clientY, e.buttons]));
    const send = (type: string, x: number, y: number, buttons = 1) =>
      canvas.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, buttons }));

    const undo = keepPointerIn(box);
    send("mousemove", 150, 100);
    send("mousemove", 20, 300);
    send("mouseup", 500, 120, 0);
    expect(heard).toEqual([["mousemove", 150, 100, 1], ["mousemove", 100, 199, 1], ["mouseup", 339, 120, 0]]);

    undo();
    heard.length = 0;
    send("mousemove", 20, 300);
    expect(heard).toEqual([["mousemove", 20, 300, 1]]);
    box.remove();
  });

  it("sends a drag out of a box with fractional edges again once, inside it", () => {
    const box = document.createElement("div");
    const canvas = document.createElement("canvas");
    box.append(canvas);
    document.body.append(box);
    box.getBoundingClientRect = () => ({ left: 98.9, top: 81.5, right: 1182.1, bottom: 758.5, width: 1083.2, height: 677, x: 98.9, y: 81.5, toJSON() {} });
    const heard: [number, number][] = [];
    canvas.addEventListener("mouseup", (e) => heard.push([e.clientX, e.clientY]));
    const undo = keepPointerIn(box);
    canvas.dispatchEvent(new MouseEvent("mouseup", { bubbles: true, cancelable: true, clientX: 10, clientY: 20 }));
    expect(heard).toEqual([[99, 82]]);
    undo();
    box.remove();
  });

  // noVNC turns touches into gesture events whose detail carries the position it then reads.
  it("keeps a touch gesture on the display shown", () => {
    const box = document.createElement("div");
    const canvas = document.createElement("canvas");
    box.append(canvas);
    document.body.append(box);
    box.getBoundingClientRect = () => ({ left: 100.5, top: 50, right: 340, bottom: 200, width: 239.5, height: 150, x: 100.5, y: 50, toJSON() {} });
    const heard: [string, number, number][] = [];
    for (const type of ["gesturestart", "gesturemove", "gestureend"]) {
      canvas.addEventListener(type, (e) => {
        const d = (e as CustomEvent<{ clientX: number; clientY: number }>).detail;
        heard.push([type, d.clientX, d.clientY]);
      });
    }
    const gesture = (type: string, x: number, y: number) => canvas.dispatchEvent(new CustomEvent(type, { bubbles: true, detail: { type: "drag", clientX: x, clientY: y } }));
    const undo = keepPointerIn(box);
    gesture("gesturestart", 150, 100);
    gesture("gesturemove", 20, 300);
    gesture("gestureend", 500, 120);
    expect(heard).toEqual([["gesturestart", 150, 100], ["gesturemove", 101, 199], ["gestureend", 339, 120]]);
    undo();
    box.remove();
  });
});

describe("the chosen display", () => {
  afterEach(() => localStorage.clear());

  it("is kept per Mac until All Displays is chosen", () => {
    expect(displayChoice("macbook")).toBeNull();
    setDisplayChoice("macbook", "display-2");
    expect(displayChoice("macbook")).toBe("display-2");
    expect(displayChoice("macstudio")).toBeNull();
    setDisplayChoice("macbook", null);
    expect(displayChoice("macbook")).toBeNull();
  });

  it("is only this page's when the browser keeps nothing", () => {
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    expect(() => setDisplayChoice("macbook", "display-2")).not.toThrow();
    expect(displayChoice("macbook")).toBeNull();
    set.mockRestore();
  });
});
