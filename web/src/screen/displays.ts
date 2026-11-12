// One display of a multi-display Mac on the live screen (docs/extensions/computer-use.md "Displays", #5685), as the iOS
// app's VNCDisplays does it: macOS's Screen Sharing sends one framebuffer with every display, laid out as System
// Settings › Displays arranges them. The node reads that arrangement (`displays` in the computer / browser status) and
// the page lays it onto the framebuffer, then shows one display cropped out of it.

/** One of the Mac's displays as its node read it: points in the global display space (origin at the main display's
 *  top-left corner, y down); `id` is macOS's display number. */
export type MachineDisplay = { id: number; x: number; y: number; width: number; height: number; main?: boolean };
export type Size = { w: number; h: number };
export type Rect = { x: number; y: number; w: number; h: number };
/** A display on the framebuffer, in pixels. `id` stays the same across connections, as the iOS app's does. */
export type ScreenDisplay = { id: string; frame: Rect };

/** How far the arrangement's aspect ratio may be from the framebuffer's (a fraction) before it is not trusted: a display
 *  changed since the node last looked, or the server draws the displays at different scales. */
export const ASPECT_TOLERANCE = 0.02;

/** The status's `displays`, or none when it is missing (an older node) or not a list of displays. */
export function machineDisplays(raw: unknown): MachineDisplay[] {
  if (!Array.isArray(raw)) return [];
  const num = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
  const ok = raw.every((d) => d && typeof d === "object" && num(d.id) && num(d.x) && num(d.y) && num(d.width) && num(d.height));
  return ok ? (raw as MachineDisplay[]) : [];
}

/** The displays to choose from: the node's arrangement, its bounding box scaled onto the framebuffer, numbered left to
 *  right, then top to bottom. None unless at least two displays have a size and the box has the framebuffer's shape. */
export function layoutDisplays(displays: MachineDisplay[], fb: Size): ScreenDisplay[] {
  const sized = displays.filter((d) => d.width > 0 && d.height > 0);
  if (sized.length < 2 || !(fb.w > 0 && fb.h > 0)) return [];
  const minX = Math.min(...sized.map((d) => d.x)), minY = Math.min(...sized.map((d) => d.y));
  const maxX = Math.max(...sized.map((d) => d.x + d.width)), maxY = Math.max(...sized.map((d) => d.y + d.height));
  const sx = fb.w / (maxX - minX), sy = fb.h / (maxY - minY);
  if (Math.abs(sx / sy - 1) > ASPECT_TOLERANCE) return [];
  return sized
    .map((d) => {
      const x = Math.round((d.x - minX) * sx), y = Math.round((d.y - minY) * sy);
      const right = Math.min(fb.w, x + Math.round(d.width * sx)), bottom = Math.min(fb.h, y + Math.round(d.height * sy));
      return { id: `display-${d.id}`, frame: { x, y, w: right - x, h: bottom - y } };
    })
    .filter((d) => d.frame.w > 0 && d.frame.h > 0)
    .sort((a, b) => a.frame.x - b.frame.x || a.frame.y - b.frame.y);
}

/** Where the page draws one display, in the stage's CSS pixels: `clip`, the box that shows it, as large as fits and
 *  centered; `inner`, the whole framebuffer at the same scale, placed in `clip` so that the display fills it. noVNC
 *  scales the framebuffer to its container, so `inner` sized this way makes it draw at exactly that scale. */
export function framing(stage: Size, fb: Size, d: Rect): { clip: Rect; inner: Rect } | null {
  const wide = stage.w / d.w <= stage.h / d.h; // the display fills the stage's width, else its height
  const k = wide ? stage.w / d.w : stage.h / d.h;
  if (!(k > 0 && fb.w > 0 && fb.h > 0)) return null;
  const w = wide ? stage.w : d.w * k, h = wide ? d.h * k : stage.h;
  return {
    clip: { x: (stage.w - w) / 2, y: (stage.h - h) / 2, w, h },
    inner: { x: 0 - d.x * k, y: 0 - d.y * k, w: fb.w * k, h: fb.h * k },
  };
}

/** A point kept inside a box, as noVNC keeps a point inside its canvas. A point outside goes to the nearest whole pixel
 *  inside the box's edges: a browser gives a dispatched mouse event's position as whole pixels, so a point kept on a
 *  fractional edge would land outside it again. */
export function clampInto(box: { left: number; top: number; right: number; bottom: number }, x: number, y: number): { x: number; y: number } {
  const left = Math.ceil(box.left), top = Math.ceil(box.top);
  const right = Math.max(left, Math.floor(box.right) - 1), bottom = Math.max(top, Math.floor(box.bottom) - 1);
  return { x: Math.min(Math.max(x, left), right), y: Math.min(Math.max(y, top), bottom) };
}

/** Keeps a drag that leaves `box` on the display it shows. noVNC follows the pointer while a button is down, and a
 *  finger once it touched, and maps either onto its whole canvas, which here reaches past `box` onto the displays
 *  hidden beside it. A mouse move or release outside `box` is stopped on its way down and sent again at the nearest
 *  point inside; a touch gesture's position (noVNC's gesture events carry it in their detail) is moved there in place.
 *  Returns what undoes it. */
export function keepPointerIn(box: HTMLElement): () => void {
  let sending = false; // the event sent again comes through here too
  const keep = (e: MouseEvent) => {
    if (sending) return;
    const p = clampInto(box.getBoundingClientRect(), e.clientX, e.clientY);
    if (p.x === e.clientX && p.y === e.clientY) return;
    e.stopPropagation();
    const again = new MouseEvent(e.type, {
      bubbles: true, cancelable: true, view: e.view, detail: e.detail, screenX: e.screenX, screenY: e.screenY,
      clientX: p.x, clientY: p.y, ctrlKey: e.ctrlKey, shiftKey: e.shiftKey, altKey: e.altKey, metaKey: e.metaKey,
      button: e.button, buttons: e.buttons, relatedTarget: e.relatedTarget,
    });
    sending = true;
    try {
      if (!e.target?.dispatchEvent(again)) e.preventDefault();
    } finally {
      sending = false;
    }
  };
  const keepGesture = (e: Event) => {
    const d = (e as CustomEvent<{ clientX?: unknown; clientY?: unknown } | null>).detail;
    if (!d || typeof d.clientX !== "number" || typeof d.clientY !== "number") return;
    const p = clampInto(box.getBoundingClientRect(), d.clientX, d.clientY);
    d.clientX = p.x;
    d.clientY = p.y;
  };
  const mouse = ["mousemove", "mouseup"] as const, gestures = ["gesturestart", "gesturemove", "gestureend"];
  for (const type of mouse) box.addEventListener(type, keep, true);
  for (const type of gestures) box.addEventListener(type, keepGesture, true);
  return () => {
    for (const type of mouse) box.removeEventListener(type, keep, true);
    for (const type of gestures) box.removeEventListener(type, keepGesture, true);
  };
}

// The chosen display, kept per Mac in this browser as the iOS app keeps it per Mac on the device; none: all of them. A
// chosen display that is not there now shows all of them and stays chosen for when it is back.
const choiceKey = (mid: string) => `persona.screen.display.${mid}`;

export function displayChoice(mid: string): string | null {
  try {
    return localStorage.getItem(choiceKey(mid));
  } catch {
    return null;
  }
}

export function setDisplayChoice(mid: string, id: string | null): void {
  try {
    if (id === null) localStorage.removeItem(choiceKey(mid));
    else localStorage.setItem(choiceKey(mid), id);
  } catch {
    // not kept: this page still shows it, the next one starts with all of them
  }
}
