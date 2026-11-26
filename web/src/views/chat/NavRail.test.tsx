// @vitest-environment jsdom
// Component contracts: passive cached scroll tracking, gutter clearance, stable selection and accessible input.
import { act, Profiler, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NAV_RAIL_MIN_TICKS, type NavRailTick } from "../../lib/nav-rail";
import { t } from "../../i18n";
import type { QuestionMark } from "../../lib/question-states";
import { NAV_RAIL_METRICS as M } from "../../ui/nav-rail-metrics";

// `matches` is read live and `set` notifies subscribers: viewport.ts caches the MediaQueryList, so a later test (or a
// later flip in one test) changes the pointer the mounted rail already follows.
const finePointer = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  const resizeObservers = new Set<{ cb: ResizeObserverCallback; targets: Set<Element> }>();
  const state = {
    matches: true,
    set(matches: boolean) {
      state.matches = matches;
      for (const l of [...listeners]) l();
    },
    fireResize(entries: Array<{ target: Element; contentRect?: { width: number; height: number } }>) {
      for (const obs of [...resizeObservers]) {
        const matched = entries.filter((e) => obs.targets.has(e.target));
        if (matched.length) obs.cb(matched as unknown as ResizeObserverEntry[], {} as ResizeObserver);
      }
    },
    clearResizeObservers() {
      resizeObservers.clear();
    },
  };
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (query: string) => ({
    get matches() {
      return query === "(any-pointer: fine)" ? state.matches : true;
    },
    addEventListener(_type: string, l: () => void) { listeners.add(l); },
    removeEventListener(_type: string, l: () => void) { listeners.delete(l); },
  });
  g.ResizeObserver = class {
    #entry: { cb: ResizeObserverCallback; targets: Set<Element> };
    constructor(cb: ResizeObserverCallback) {
      this.#entry = { cb, targets: new Set() };
      resizeObservers.add(this.#entry);
    }
    observe(target: Element) { this.#entry.targets.add(target); }
    unobserve(target: Element) { this.#entry.targets.delete(target); }
    disconnect() { this.#entry.targets.clear(); resizeObservers.delete(this.#entry); }
  };
  return state;
});

const { NavRail, NAV_RAIL_OBSERVER_MS } = await import("./NavRail");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const tick = (i: number): NavRailTick => ({ id: `seg:${i}`, preview: `question ${i}` });
// jsdom reports every box as 0: a scroller wide and tall enough for the rail needs real numbers
const scrollerWith = (width: number, height: number, contentWidth = Math.min(width, 500)) => {
  const el = document.createElement("div");
  Object.defineProperty(el, "clientWidth", { value: width, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: height });
  const content = el.appendChild(document.createElement("div"));
  content.dataset.chatContent = "";
  Object.defineProperty(content, "clientWidth", { value: contentWidth });
  return el;
};
// the rail measures on requestAnimationFrame, which jsdom runs as a ~16 ms timer
const settle = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
};

let host: HTMLDivElement;
let root: Root;
let commits: number;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  commits = 0;
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  finePointer.matches = true;
  act(() => root.unmount());
  finePointer.clearResizeObservers();
  host.remove();
});

describe("NavRail", () => {
  it("renders a dense viewport of a 678-input history and keeps global ordinals", async () => {
    const ticks = Array.from({ length: 678 }, (_, i) => ({ ...tick(i), ordinal: i + 1 }));
    act(() => root.render(<NavRail ticks={ticks} total={678} scroller={scrollerWith(1000, 800)} onJump={() => {}} reset="example/global" />));
    await settle();
    const marks = [...host.querySelectorAll<HTMLElement>("button[data-tick]")];
    expect(marks.length).toBeGreaterThan(28);
    expect(marks.length).toBeLessThan(120);
    expect(marks.at(-1)?.getAttribute("aria-label")).toContain("678");
    expect(marks[0].style.height).toBe("7px");
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    expect(host.querySelector('[data-tick="0"]')?.getAttribute("aria-label")).toContain("1");
    expect(host.querySelector('[aria-live="polite"]')?.textContent).toContain("678");
  });

  it("uses the natural left gutter and removes the rail and its tab stop when the pane shrinks", async () => {
    const sc = scrollerWith(1200, 600, 880);
    let width = 1200;
    Object.defineProperty(sc, "clientWidth", { get: () => width });
    Object.defineProperty(sc, "offsetWidth", { get: () => width + 10 });
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/gutter" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    expect(rail.style.left).toBe(`${M.edgeGap}px`);
    act(() => rail.focus());
    expect(host.querySelector("[data-nav-preview]")).not.toBeNull();
    width = 1000;
    finePointer.fireResize([{ target: sc }]);
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    expect(document.activeElement).not.toBe(rail);
    expect(sc.style.cssText).toBe("");
    width = 1200;
    finePointer.fireResize([{ target: sc }]);
    await settle();
    expect(host.querySelector("[data-nav-rail]")).not.toBeNull();
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("rearms a frame cancelled by StrictMode's initial effect replay", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<StrictMode><NavRail ticks={[]} scroller={sc} onJump={() => {}} reset="example/strict" /></StrictMode>));
    await settle();
    act(() => root.render(<StrictMode><NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/strict" /></StrictMode>));
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(2);
  });

  it("draws a tick per question in a wide chat once measured", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={[tick(1), tick(2), tick(3), tick(4)]} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const buttons = [...host.querySelectorAll("[data-nav-rail] button[data-tick]")];
    expect(buttons).toHaveLength(4);
    expect(commits).toBeGreaterThan(1); // the mount, then the measure that draws it
  });

  it("restores calm marks and a distinct current input after hover or focus", async () => {
    const sc = scrollerWith(700, 600);
    const row = document.createElement("div");
    row.dataset.line = "seg:1";
    row.append(document.createElement("div"));
    sc.firstElementChild!.append(row);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2), tick(3)]} scroller={sc} onJump={() => {}} reset="example/idle-width" />));
    await settle();
    const marks = () => [...host.querySelectorAll<HTMLElement>("[data-nav-mark]")];
    const short = () => {
      expect(marks().map((mark) => mark.style.width)).toEqual([M.currentWidth, M.tickWidth, M.tickWidth].map((n) => `${n}px`));
      expect(marks().map((mark) => mark.style.height)).toEqual([M.currentHeight, M.tickHeight, M.tickHeight].map((n) => `${n}px`));
      expect(marks()[0].dataset.navMark).toBe("current");
      expect(marks()[0].classList.contains("bg-ink")).toBe(true);
      expect(marks()[0].style.opacity).toBe("1");
    };
    short();
    act(() => sc.dispatchEvent(new Event("scroll")));
    await settle();
    short();
    const button = host.querySelector('[data-tick="1"]')!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(marks()[1].style.width).toBe(`${M.hoverWidth}px`);
    expect(parseFloat(marks()[0].style.width)).toBeCloseTo(M.tickWidth + (M.hoverWidth - M.tickWidth) * Math.exp(-0.5));
    expect(marks()[0].classList.contains("bg-ink")).toBe(true);
    expect(host.querySelector('[data-tick="0"]')?.getAttribute("aria-current")).toBe("true");
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    short();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.focus());
    expect(marks()[0].style.width).toBe(`${M.currentHoverWidth}px`);
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 1");
    act(() => rail.blur());
    short();
  });

  it("highlights the pending jump while preserving the current scroll position marker", async () => {
    const sc = scrollerWith(700, 600);
    const row = document.createElement("div");
    row.dataset.line = "seg:1";
    row.append(document.createElement("div"));
    sc.firstElementChild!.append(row);
    let finish!: () => void;
    const jump = new Promise<void>((resolve) => { finish = resolve; });
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => jump} reset="example/pending" />));
    await settle();

    const target = host.querySelector<HTMLElement>('[data-tick="1"]')!;
    const mark = target.querySelector<HTMLElement>("[data-nav-mark]")!;
    act(() => target.click());
    expect(mark.style.width).toBe(`${M.currentHoverWidth}px`);
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("0");
    await act(async () => finish());
    expect(mark.style.width).toBe(`${M.tickWidth}px`);
  });

  it("measures nothing for a chat below the tick minimum", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: NAV_RAIL_MIN_TICKS - 1 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    const mounted = commits;
    await settle();
    expect(commits).toBe(mounted); // no measure state, no redraw — whatever the scroller's size
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
  });

  it("measures nothing on a touch screen, including while the chat scrolls", async () => {
    finePointer.matches = false;
    let reads = 0;
    const sc = document.createElement("div");
    Object.defineProperty(sc, "clientWidth", { get() { reads++; return 1200; } });
    Object.defineProperty(sc, "clientHeight", { get() { reads++; return 600; } });
    const boxes = vi.spyOn(sc, "getBoundingClientRect");
    const lines = vi.spyOn(sc, "querySelectorAll");
    const listen = vi.spyOn(sc, "addEventListener");
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const mounted = commits;
    sc.dispatchEvent(new Event("scroll"));
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    expect(commits).toBe(mounted); // scrolling writes no state
    expect(reads).toBe(0); // no clientWidth / clientHeight
    expect(boxes).not.toHaveBeenCalled();
    expect(lines).not.toHaveBeenCalled();
    expect(listen).not.toHaveBeenCalled();
  });

  it("does not paint a frozen rail when a precise pointer comes back", async () => {
    const width = { value: 1200 };
    const sc = scrollerWith(width.value, 600, 880);
    Object.defineProperty(sc, "clientWidth", { get() { return width.value; } });
    let watch = false;
    let flashed = false;
    act(() => root.render(
      <Profiler id="rail" onRender={() => { if (watch && host.querySelector("[data-nav-rail]")) flashed = true; }}>
        <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8);
    // the chat then loses its margin (a rotation, a panel) while the pointer is gone
    width.value = 950;
    act(() => { finePointer.set(false); });
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    watch = true;
    act(() => { finePointer.set(true); });
    expect(flashed).toBe(false); // the commit that learns the pointer is back must not draw the old geometry
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
  });

  it("does not restore a hover preview after the precise pointer comes back", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />,
    ));
    await settle();
    const button = host.querySelector("button");
    expect(button).not.toBeNull();
    act(() => { button!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect(host.querySelector("[data-nav-rail-preview]")).not.toBeNull();
    act(() => { finePointer.set(false); });
    await settle();
    act(() => { finePointer.set(true); });
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8);
    expect(host.querySelector("[data-nav-rail-preview]")).toBeNull();
  });

  it("dismisses a bold query and reply preview immediately on pointer exit", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<NavRail ticks={[{ ...tick(1), answerExcerpt: "A short response." }, tick(2)]} scroller={sc} onJump={() => {}} reset="example/exit" />));
    await settle();
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    const surface = host.querySelector<HTMLElement>("[data-nav-preview-surface]")!;
    expect(surface.style.padding).toBe(`${M.previewPadding}px`);
    expect(surface.classList.contains("rounded-nav-preview")).toBe(true);
    expect(host.querySelector("[data-nav-preview-title]")?.textContent).toBe("question 1");
    expect(host.querySelector("[data-nav-preview-title]")?.classList.contains("truncate")).toBe(true);
    expect(host.querySelector("[data-nav-preview-title]")?.classList.contains("font-semibold")).toBe(true);
    expect(host.querySelector("[data-nav-preview-excerpt]")?.textContent).toBe("A short response.");
    expect(host.querySelector("[data-nav-preview-excerpt]")?.classList.contains("line-clamp-3")).toBe(true);
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    expect(host.querySelector("[data-nav-rail]")).not.toBeNull();
  });

  it("describes attachment-only and answer-only inputs, and uses an ordinal for bare positions", async () => {
    const ticks: NavRailTick[] = [
      { id: "image", preview: "", ordinal: 1, attachmentsOnly: 2 },
      { id: "answer", preview: "", ordinal: 2, answerExcerpt: "An indexed reply." },
      { id: "position", preview: "", ordinal: 3 },
    ];
    act(() => root.render(<NavRail ticks={ticks} scroller={scrollerWith(700, 600)} onJump={() => {}} reset="example/fallbacks" />));
    await settle();
    const buttons = host.querySelectorAll("button");
    const hover = (i: number) => act(() => buttons[i].dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    hover(0);
    expect(host.querySelector("[data-nav-preview-title]")?.textContent).toBe(t("chat-navrail.attachments", { count: 2 }));
    hover(1);
    expect(host.querySelector("[data-nav-preview-title]")).toBeNull();
    expect(host.querySelector("[data-nav-preview-excerpt]")?.textContent).toBe("An indexed reply.");
    expect(buttons[1].getAttribute("aria-label")).toContain("An indexed reply.");
    hover(2);
    expect(host.querySelector("[data-nav-preview-title]")?.textContent).toBe(t("chat-navrail.input", { index: 3 }));
    expect(host.querySelector("[data-nav-preview-excerpt]")).toBeNull();
  });

  it("keeps a preview across the rail/card hit zones but closes immediately when both are left", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={jump} reset="example/card" />));
    await settle();
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    const card = host.querySelector<HTMLElement>("[data-nav-preview]")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: card })));
    expect(host.querySelector("[data-nav-preview]")).toBe(card);
    expect(card.style.left).toBe(`${M.width}px`);
    expect(card.style.paddingLeft).toBe(`${M.previewGap}px`);
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    rail.setPointerCapture = vi.fn();
    const press = new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 });
    Object.defineProperty(press, "pointerId", { value: 7 });
    act(() => card.dispatchEvent(press));
    expect(press.defaultPrevented).toBe(false); // selecting card text must not scrub or jump
    expect(rail.setPointerCapture).not.toHaveBeenCalled();
    expect(jump).not.toHaveBeenCalled();
    act(() => card.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("retains a keyboard-owned preview until focus leaves, independently of mouse exit", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/keyboard" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    act(() => rail.focus());
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 2");
    expect(host.querySelector<HTMLElement>('[data-tick="1"] [data-nav-mark]')!.style.width).toBe(`${M.hoverWidth}px`);
    act(() => rail.blur());
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    expect([...host.querySelectorAll<HTMLElement>("[data-nav-mark]")].every((mark) => mark.style.width === `${M.tickWidth}px`)).toBe(true);
  });

  it("does not move a keyboard selection out of the window when entering its card", async () => {
    const sc = scrollerWith(700, 120);
    act(() => root.render(<NavRail ticks={Array.from({ length: 50 }, (_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/card-window" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.focus());
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    const card = host.querySelector<HTMLElement>("[data-nav-preview]")!;
    act(() => card.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector('[data-tick="0"]')).not.toBeNull();
    expect(card.textContent).toBe("question 0");
  });

  it("holds a captured drag preview outside the rail and clears it at outside release", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={[tick(1), tick(2), tick(3)]} scroller={sc} onJump={jump} reset="example/drag-exit" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const button = host.querySelector("button")!;
    const track = rail.querySelector<HTMLElement>("[data-nav-track]")!;
    track.setPointerCapture = vi.fn();
    track.releasePointerCapture = vi.fn();
    track.getBoundingClientRect = () => ({ top: 288 } as DOMRect);
    const pointer = (type: string) => {
      const e = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY: 300 });
      Object.defineProperty(e, "pointerId", { value: 7 });
      act(() => track.dispatchEvent(e));
    };
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    pointer("pointerdown");
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).not.toBeNull();
    expect(host.querySelector<HTMLElement>('[data-tick="1"] [data-nav-mark]')!.style.width).toBe(`${M.hoverWidth}px`);
    pointer("pointerup");
    expect(jump).toHaveBeenCalledWith("seg:2");
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    expect([...host.querySelectorAll<HTMLElement>("[data-nav-mark]")].every((mark) => mark.style.width === `${M.tickWidth}px`)).toBe(true);
  });

  it("stays visible at rest without a body scroll or hover to reveal it", async () => {
    const sc = scrollerWith(700, 600);
    vi.useFakeTimers();
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/s1" />));
    act(() => vi.advanceTimersByTime(32));
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    expect(getComputedStyle(rail).opacity || "1").toBe("1");
    act(() => rail.focus());
    act(() => rail.blur());
    act(() => vi.advanceTimersByTime(3000));
    expect(host.querySelector("[data-nav-rail]")).toBe(rail);
    expect(getComputedStyle(rail).opacity || "1").toBe("1");
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("scrolls the 240-input track by native pixels and keeps it independent of the transcript", async () => {
    const sc = scrollerWith(700, 600);
    sc.scrollBy = vi.fn();
    act(() => root.render(<NavRail ticks={Array.from({ length: 240 }, (_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/wheel" />));
    await settle();
    const el = host.querySelector<HTMLElement>("[data-nav-track]")!;
    const first = () => Number(el.querySelector("button")?.dataset.tick);
    const tail = first();
    expect(tail).toBeGreaterThan(100);
    const wheel = (deltaY: number) => act(() => {
      el.dispatchEvent(new WheelEvent("wheel", { deltaY, bubbles: true }));
      // jsdom dispatches the wheel event without applying the browser's default scrolling.
      el.scrollTop += deltaY;
      el.dispatchEvent(new Event("scroll"));
    });
    wheel(-14);
    expect(first()).toBe(tail - 2);
    wheel(-el.scrollTop);
    expect(first()).toBe(0);
    wheel(800);
    expect(first()).toBe(110);
    expect(el.querySelectorAll("button").length).toBeLessThan(100);
    expect(sc.scrollBy).not.toHaveBeenCalled();
    expect(sc.scrollTop).toBe(0);
  });

  it("follows the reading position only when the rail is idle", async () => {
    const sc = scrollerWith(700, 156);
    const ticks = Array.from({ length: 240 }, (_, i) => tick(i));
    for (const [id, top] of [[0, 0], [200, 1800]]) {
      const line = sc.firstElementChild!.appendChild(document.createElement("div"));
      line.dataset.line = `seg:${id}`;
      const row = line.appendChild(document.createElement("div"));
      row.getBoundingClientRect = () => ({ top: top - sc.scrollTop } as DOMRect);
    }
    act(() => root.render(<NavRail ticks={ticks} scroller={sc} onJump={() => {}} reset="example/active" />));
    await settle();
    const first = host.querySelector('[data-tick="0"]')!;
    const track = host.querySelector<HTMLElement>("[data-nav-track]")!;
    act(() => first.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    act(() => { sc.scrollTop = 1800; sc.dispatchEvent(new Event("scroll")); });
    await settle();
    expect(track.scrollTop).toBe(0);
    act(() => first.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    act(() => sc.dispatchEvent(new Event("scroll")));
    await settle();
    expect(track.scrollTop).toBeGreaterThan(1000);
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("200");
  });

  it("selects every loaded input with the keyboard in a bounded window without jumping until Enter", async () => {
    const sc = scrollerWith(700, 120);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={Array.from({ length: 50 }, (_, i) => tick(i))} scroller={sc} onJump={jump} reset="example/s1" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const press = (key: string) => act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })));
    act(() => rail.focus());
    press("Home");
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 0");
    for (let i = 1; i < 50; i++) {
      press("ArrowDown");
      expect(host.querySelector("[data-nav-preview]")?.textContent).toBe(`question ${i}`);
      expect(host.querySelector('[aria-live="polite"]')?.textContent).toContain(`question ${i}`);
      expect(host.querySelector(`[data-tick="${i}"]`)).not.toBeNull();
    }
    expect(host.querySelectorAll("button").length).toBeLessThan(20);
    expect(jump).not.toHaveBeenCalled();
    press("Enter");
    expect(jump).toHaveBeenLastCalledWith("seg:49");
    press("Escape");
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("keeps preview identity when older inputs are prepended", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    const render = (ticks: NavRailTick[]) => act(() => root.render(<NavRail ticks={ticks} scroller={sc} onJump={jump} reset="example/s1" />));
    render([tick(2), tick(3)]);
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    render([tick(0), tick(1), tick(2), tick(3)]);
    await settle();
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 3");
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(jump).toHaveBeenCalledWith("seg:3");
  });

  it("freezes drag identities across prepends, commits once on release and never on cancellation", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    const render = (ticks: NavRailTick[]) => act(() => root.render(<NavRail ticks={ticks} scroller={sc} onJump={jump} reset="example/s1" />));
    render([tick(2), tick(3), tick(4)]);
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const track = rail.querySelector<HTMLElement>("[data-nav-track]")!;
    track.setPointerCapture = vi.fn();
    track.releasePointerCapture = vi.fn();
    track.getBoundingClientRect = () => ({ top: 288 } as DOMRect);
    const pointer = (type: string, y: number) => {
      const e = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY: y });
      Object.defineProperty(e, "pointerId", { value: 7 });
      act(() => track.dispatchEvent(e));
    };
    pointer("pointerdown", 291);
    render([tick(0), tick(1), tick(2), tick(3), tick(4)]);
    await settle();
    pointer("pointermove", 300);
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 3");
    expect(jump).not.toHaveBeenCalled();
    pointer("pointerup", 300);
    act(() => host.querySelector('[data-tick="3"]')!.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 })));
    expect(jump).toHaveBeenCalledTimes(1);
    expect(jump).toHaveBeenLastCalledWith("seg:3");
    pointer("pointerdown", 291);
    pointer("pointercancel", 291);
    pointer("pointerup", 291);
    expect(jump).toHaveBeenCalledTimes(1);
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("reads rows from the content's own children, and does no per-message layout reads on scroll frames", async () => {
    const sc = scrollerWith(700, 600);
    const content = sc.firstElementChild!;
    const boxes = Array.from({ length: 40 }, (_, i) => {
      const row = document.createElement("div");
      row.dataset.line = `seg:${i}`;
      const child = document.createElement("div");
      child.getBoundingClientRect = vi.fn(() => ({ top: i * 100 - sc.scrollTop } as DOMRect));
      row.append(child);
      content.append(row);
      return child;
    });
    const query = vi.spyOn(sc, "querySelectorAll");
    act(() => root.render(<NavRail ticks={boxes.map((_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/s1" />));
    await settle();
    expect(query).not.toHaveBeenCalled(); // the measure walks the content's children, not the transcript subtree
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();
    act(() => { sc.scrollTop = 530; sc.dispatchEvent(new Event("scroll")); });
    await settle();
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("5");
    expect(query).not.toHaveBeenCalled();
    for (const child of boxes) expect(child.getBoundingClientRect).not.toHaveBeenCalled();
  });

  describe("question states (#13021)", () => {
    const marks = new Map<string, QuestionMark>([
      ["seg:1", { state: "read", key: "u1" }],
      ["seg:2", { state: "unread", key: "u2" }],
      ["seg:3", { state: "unread", key: "u3", follows: "seg:2" }],
      ["seg:4", { state: "running", key: "u4" }],
      ["seg:5", { state: "pending", key: "u5" }],
    ]);
    const five = () => Array.from({ length: 5 }, (_, i) => tick(i + 1));

    it("says each tick's state in its label, marks unread with a dot and draws a follow-up shorter", async () => {
      act(() => root.render(<NavRail ticks={five()} scroller={scrollerWith(1200, 600)} onJump={() => {}} reset="example/states" marks={marks} />));
      await settle();
      const buttons = [...host.querySelectorAll<HTMLElement>("button[data-tick]")];
      expect(buttons.map((b) => b.dataset.questionState)).toEqual(["read", "unread", "unread", "running", "pending"]);
      expect(buttons[0].getAttribute("aria-label")).toContain(t("chat-navrail.state_read"));
      expect(buttons[1].getAttribute("aria-label")).toContain(t("chat-navrail.state_unread"));
      expect(buttons[2].getAttribute("aria-label")).toBe(t("chat-navrail.jump_follow_state", { label: t("chat-navrail.jump", { index: 3, preview: "question 3" }), state: t("chat-navrail.state_unread") }));
      expect(buttons[3].getAttribute("aria-label")).toContain(t("chat-navrail.state_running"));
      expect(buttons[4].getAttribute("aria-label")).toContain(t("chat-navrail.state_pending"));
      expect(buttons.map((b) => Boolean(b.querySelector("[data-question-dot]")))).toEqual([false, true, true, false, false]);
      const width = (b: HTMLElement) => parseFloat(b.querySelector<HTMLElement>("[data-nav-mark]")!.style.width);
      expect(width(buttons[2])).toBeCloseTo(width(buttons[1]) * M.followWidth);
      act(() => buttons[2].dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
      const state = host.querySelector("[data-nav-preview-state]");
      expect(state?.textContent).toBe(t("chat-navrail.follow_state", { state: t("chat-navrail.state_unread") }));
      expect(state?.parentElement?.querySelector("[data-nav-preview-title]")?.textContent).toBe("question 3");
    });

    it("announces the state with a keyboard selection", async () => {
      act(() => root.render(<NavRail ticks={five()} scroller={scrollerWith(1200, 600)} onJump={() => {}} reset="example/states-keys" marks={marks} />));
      await settle();
      const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
      act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
      expect(host.querySelector('[aria-live="polite"]')?.textContent).toContain(t("chat-navrail.state_pending"));
    });

    it("keeps touch navigation with states in wide and compact long chats", async () => {
      finePointer.matches = false;
      const jumps: string[] = [];
      act(() => root.render(<NavRail ticks={five()} scroller={scrollerWith(390, 700)} onJump={(id) => { jumps.push(id); }} reset="example/touch" />));
      await settle();
      expect(host.querySelector("[data-nav-rail]")).toBeNull();
      act(() => root.render(<NavRail ticks={five()} scroller={scrollerWith(390, 700)} onJump={(id) => { jumps.push(id); }} reset="example/touch" marks={marks} />));
      await settle();
      expect(host.querySelector("[data-nav-rail]")).toBeNull();
      act(() => root.render(<NavRail ticks={five()} scroller={scrollerWith(1200, 700, 880)} onJump={(id) => { jumps.push(id); }} reset="example/touch" marks={marks} />));
      await settle();
      const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
      expect(rail.dataset.navTouch).toBe("true");
      expect(rail.querySelector<HTMLElement>("[data-nav-track]")?.style.touchAction).toBe("none");
      act(() => host.querySelectorAll<HTMLElement>("button[data-tick]")[1].click());
      expect(jumps).toEqual(["seg:2"]);

      const compact = scrollerWith(390, 700);
      const long = Array.from({ length: 120 }, (_, i) => tick(i + 1));
      act(() => root.render(<NavRail ticks={long} scroller={compact} onJump={(id) => { jumps.push(id); }} reset="example/touch" marks={marks} />));
      await settle();
      expect(compact.hasAttribute("data-nav-compact")).toBe(true);
      const strip = host.querySelector<HTMLElement>("[data-nav-track]")!;
      expect(strip.style.touchAction).toBe("none");
      act(() => strip.querySelector<HTMLElement>('[data-tick="119"]')!.click());
      expect(jumps).toEqual(["seg:2", "seg:120"]);
    });
  });

  it("keeps cached geometry during tail mutations and text-only tick updates while scroll, hover and jump still work", async () => {
    const sc = scrollerWith(700, 600);
    const content = sc.firstElementChild!;
    const boxes = Array.from({ length: 12 }, (_, i) => {
      const row = document.createElement("div");
      row.dataset.line = `seg:${i}`;
      const child = document.createElement("div");
      child.getBoundingClientRect = vi.fn(() => ({ top: i * 100 - sc.scrollTop } as DOMRect));
      row.append(child);
      content.append(row);
      return child;
    });
    const jump = vi.fn();
    const ticks = boxes.map((_, i) => tick(i));
    const render = (positions: NavRailTick[]) => act(() => root.render(<NavRail ticks={positions} scroller={sc} onJump={jump} reset="example/stream-tail" />));
    render(ticks);
    await settle();
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();

    // Stream DOM nodes into the last tick's body, an untracked assistant `[data-line]` reply after it,
    // a newly appended `[data-line]` block, and a trailing feedback indicator.
    const lastBody = boxes[boxes.length - 1];
    const replyLine = document.createElement("div");
    replyLine.dataset.line = "reply:11";
    const replyBody = document.createElement("div");
    replyLine.append(replyBody);
    content.append(replyLine);
    const tailFeedback = document.createElement("div");
    content.append(tailFeedback);
    for (let n = 0; n < 10; n++) {
      const p = document.createElement("p");
      p.textContent = `streaming chunk ${n}`;
      replyBody.append(p);
      const q = document.createElement("span");
      q.textContent = `tail ${n}`;
      lastBody.append(q);
      tailFeedback.textContent = `chunk ${n}`;
    }
    const nextReplyLine = document.createElement("div");
    nextReplyLine.dataset.line = "reply:12";
    nextReplyLine.append(document.createElement("div"));
    content.append(nextReplyLine);
    render(ticks.map((tick) => ({ ...tick, answerExcerpt: "New excerpt." })));
    await settle();

    for (const child of boxes) expect(child.getBoundingClientRect).not.toHaveBeenCalled();

    // Scroll highlighting, hover preview, and clicking to jump still work accurately.
    act(() => { sc.scrollTop = 420; sc.dispatchEvent(new Event("scroll")); });
    await settle();
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("4");
    const target = host.querySelector<HTMLElement>('[data-tick="2"]')!;
    act(() => target.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview-title]")?.textContent).toBe("question 2");
    expect(host.querySelector("[data-nav-preview-excerpt]")?.textContent).toBe("New excerpt.");
    act(() => target.click());
    expect(jump).toHaveBeenCalledWith("seg:2");
  });

  it("throttles height-only content resizes, skips full row scans when the tail anchor stays put, and re-measures on earlier shifts or scroller resize", async () => {
    vi.useFakeTimers();
    let scWidth = 700;
    let scHeight = 600;
    let contentWidth = 500;
    const sc = document.createElement("div");
    Object.defineProperty(sc, "clientWidth", { get: () => scWidth });
    Object.defineProperty(sc, "clientHeight", { get: () => scHeight });
    Object.defineProperty(sc, "offsetWidth", { get: () => scWidth });
    const content = document.createElement("div");
    content.dataset.chatContent = "";
    Object.defineProperty(content, "clientWidth", { get: () => contentWidth });
    sc.append(content);

    const tops = Array.from({ length: 8 }, (_, i) => i * 100);
    const boxes = tops.map((_, i) => {
      const row = document.createElement("div");
      row.dataset.line = `seg:${i}`;
      const child = document.createElement("div");
      child.getBoundingClientRect = vi.fn(() => ({ top: tops[i] - sc.scrollTop } as DOMRect));
      row.append(child);
      content.append(row);
      return child;
    });

    act(() => root.render(<NavRail ticks={tops.map((_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/resize-throttle" />));
    const paddedWidth = 64;
    const resizeContent = (height: number) => finePointer.fireResize([{ target: content, contentRect: { width: contentWidth - paddedWidth, height } }]);
    resizeContent(800);
    act(() => { vi.advanceTimersByTime(32); });
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8);

    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();

    // Burst of height-only content growth (streaming) + auto-scroll within the throttle window.
    for (let n = 1; n <= 5; n++) {
      resizeContent(800 + n * 24);
      act(() => { sc.scrollTop = 700 + n * 24; sc.dispatchEvent(new Event("scroll")); });
      act(() => { vi.advanceTimersByTime(32); });
    }
    // No full row scan happened during the burst.
    for (let i = 0; i < boxes.length - 1; i++) expect(boxes[i].getBoundingClientRect).not.toHaveBeenCalled();

    // After the observer throttle fires, only the tail anchor is spot-checked and earlier rows stay untouched.
    act(() => { vi.advanceTimersByTime(NAV_RAIL_OBSERVER_MS + 32); });
    for (let i = 0; i < boxes.length - 1; i++) expect(boxes[i].getBoundingClientRect).not.toHaveBeenCalled();
    expect(boxes[boxes.length - 1].getBoundingClientRect).toHaveBeenCalledTimes(1);

    // An earlier row shifts (e.g., image load above the tail): the spot-check detects the tail moved and re-measures all rows.
    for (let i = 3; i < tops.length; i++) tops[i] += 150;
    resizeContent(1100);
    act(() => { vi.advanceTimersByTime(NAV_RAIL_OBSERVER_MS + 32); });
    for (const child of boxes) expect(child.getBoundingClientRect).toHaveBeenCalled();
    act(() => { sc.scrollTop = 440; sc.dispatchEvent(new Event("scroll")); });
    act(() => { vi.advanceTimersByTime(32); });
    // Row 3 is now at top 450, so scrollTop 440 + activeOffset 40 = 480 selects tick 3 instead of tick 4.
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("3");

    // A scroller resize refreshes the offsets on the next frame.
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();
    scWidth = 1200;
    scHeight = 500;
    contentWidth = 880;
    finePointer.fireResize([{ target: sc, contentRect: { width: 1200, height: 500 } }]);
    act(() => { vi.advanceTimersByTime(32); });
    for (const child of boxes) expect(child.getBoundingClientRect).toHaveBeenCalledOnce();
  });

  it("re-measures when an earlier disclosure mutates and coalesces bursts inside earlier rows", async () => {
    vi.useFakeTimers();
    const sc = scrollerWith(700, 600);
    const content = sc.firstElementChild!;
    const tops = [0, 100, 200, 300];
    const boxes = tops.map((_, i) => {
      const row = document.createElement("div");
      row.dataset.line = `seg:${i}`;
      const child = document.createElement("div");
      child.getBoundingClientRect = vi.fn(() => ({ top: tops[i] - sc.scrollTop } as DOMRect));
      row.append(child);
      content.append(row);
      return child;
    });

    act(() => root.render(<NavRail ticks={tops.map((_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/earlier-mutation" />));
    act(() => { vi.advanceTimersByTime(32); });
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();

    // Mutate an earlier row (seg:1) and shift rows below it: first mutation updates on the next frame.
    tops[2] = 350;
    tops[3] = 450;
    boxes[1].append(document.createElement("div"));
    await Promise.resolve(); // deliver MutationObserver microtask
    act(() => { vi.advanceTimersByTime(32); });
    expect(boxes[2].getBoundingClientRect).toHaveBeenCalledTimes(1);

    // Rapid follow-up mutations in seg:1 within NAV_RAIL_OBSERVER_MS coalesce into a single trailing measure.
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();
    for (let n = 0; n < 6; n++) {
      boxes[1].append(document.createElement("span"));
      await Promise.resolve();
      act(() => { vi.advanceTimersByTime(20); });
    }
    expect(boxes[2].getBoundingClientRect).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(NAV_RAIL_OBSERVER_MS); });
    expect(boxes[2].getBoundingClientRect).toHaveBeenCalledTimes(1);
  });
});
