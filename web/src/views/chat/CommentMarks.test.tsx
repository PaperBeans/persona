// @vitest-environment jsdom
import { act, StrictMode, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { anchorOf, jumpToComment } from "../../lib/comment-marks";
import { textQuote } from "../../lib/comments";
import { addQuote, removeQuote, setQuotes } from "../../lib/drafts";
import { ChatActionsContext, type ChatActions } from "./chat-actions";
import { CommentMarks } from "./CommentMarks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeHighlight {
  ranges: AbstractRange[];
  constructor(...ranges: AbstractRange[]) {
    this.ranges = ranges;
  }
}

let highlightsMap: Map<string, FakeHighlight>;
let origCSSDesc: PropertyDescriptor | undefined;
let origHighlightDesc: PropertyDescriptor | undefined;
let origResizeObserverDesc: PropertyDescriptor | undefined;
let origGetClientRects: typeof Range.prototype.getClientRects;
let origScrollTo: typeof Element.prototype.scrollTo;
let resizeObserverCount = 0;

function restoreGlobalProp(name: string, desc: PropertyDescriptor | undefined) {
  if (desc) Object.defineProperty(globalThis, name, desc);
  else Reflect.deleteProperty(globalThis, name);
}

function makeActions(draftKey: string): ChatActions {
  return {
    draftKey,
    title: `Chat ${draftKey}`,
    canCompose: true,
    speaker: "Assistant",
    images: () => [],
  };
}

function makeAnchor(itemId: string, fullText: string, target: string) {
  const host = document.createElement("div");
  host.setAttribute("data-items", itemId);
  const p = document.createElement("p");
  p.textContent = fullText;
  host.append(p);
  const start = fullText.indexOf(target);
  if (start < 0) throw new Error(`missing target ${target}`);
  const range = document.createRange();
  range.setStart(p.firstChild!, start);
  range.setEnd(p.firstChild!, start + target.length);
  return anchorOf(range)!;
}

function ChatPane({
  draftKey,
  itemId,
  text,
  jump,
}: {
  draftKey: string;
  itemId: string;
  text: string;
  jump?: (words: Range, topOffset: number) => void;
}) {
  const scroller = useRef<HTMLDivElement | null>(null);
  return (
    <ChatActionsContext.Provider value={makeActions(draftKey)}>
      <div ref={scroller} style={{ position: "relative" }}>
        <div>
          <div data-items={itemId}>
            <p>{text}</p>
          </div>
        </div>
        <CommentMarks scroller={scroller} jump={jump} />
      </div>
    </ChatActionsContext.Provider>
  );
}

function highlightTexts(name: string): string[] {
  const h = highlightsMap.get(name);
  if (!h) return [];
  return h.ranges.map((r) => (r as Range).toString());
}

const usedKeys: string[] = [];
const mounted: { root: Root; host: HTMLDivElement }[] = [];

function mountPane(node: React.ReactNode): { root: Root; host: HTMLDivElement } {
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  act(() => {
    root.render(node);
  });
  const entry = { root, host };
  mounted.push(entry);
  return entry;
}

function unmountPane(entry: { root: Root; host: HTMLDivElement }) {
  act(() => {
    entry.root.unmount();
  });
  entry.host.remove();
  const idx = mounted.indexOf(entry);
  if (idx >= 0) mounted.splice(idx, 1);
}

function seedComment(draftKey: string, itemId: string, fullText: string, target: string): string {
  usedKeys.push(draftKey);
  const anchor = makeAnchor(itemId, fullText, target);
  let id = "";
  act(() => {
    id = addQuote(draftKey, textQuote(target, anchor), false);
  });
  return id;
}

beforeEach(() => {
  highlightsMap = new Map();
  resizeObserverCount = 0;

  origCSSDesc = Object.getOwnPropertyDescriptor(globalThis, "CSS");
  origHighlightDesc = Object.getOwnPropertyDescriptor(globalThis, "Highlight");
  origResizeObserverDesc = Object.getOwnPropertyDescriptor(globalThis, "ResizeObserver");
  origGetClientRects = Range.prototype.getClientRects;
  origScrollTo = Element.prototype.scrollTo;

  Object.defineProperty(globalThis, "CSS", {
    configurable: true,
    writable: true,
    value: { highlights: highlightsMap },
  });
  Object.defineProperty(globalThis, "Highlight", {
    configurable: true,
    writable: true,
    value: FakeHighlight,
  });

  Object.defineProperty(globalThis, "ResizeObserver", {
    configurable: true,
    writable: true,
    value: class FakeResizeObserver {
      constructor() {
        resizeObserverCount++;
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  });

  Range.prototype.getClientRects = function fakeRects() {
    const rect = { left: 10, top: 20, right: 60, bottom: 36, width: 50, height: 16, x: 10, y: 20, toJSON: () => ({}) } as DOMRect;
    return [rect] as unknown as DOMRectList;
  };
  Element.prototype.scrollTo = vi.fn();
});

afterEach(() => {
  while (mounted.length) {
    const entry = mounted.pop()!;
    act(() => {
      entry.root.unmount();
    });
    entry.host.remove();
  }
  while (usedKeys.length) {
    setQuotes(usedKeys.pop()!, []);
  }
  vi.useRealTimers();

  restoreGlobalProp("CSS", origCSSDesc);
  restoreGlobalProp("Highlight", origHighlightDesc);
  restoreGlobalProp("ResizeObserver", origResizeObserverDesc);
  Range.prototype.getClientRects = origGetClientRects;
  Element.prototype.scrollTo = origScrollTo;
  document.body.innerHTML = "";
});

describe("CommentMarks multi-instance CSS.highlights coordination", () => {
  it("preserves main chat draft highlights when an empty side chat opens and closes", () => {
    const mainKey = "test-main-open-close";
    const sideKey = "test-side-empty";

    mountPane(<ChatPane draftKey={mainKey} itemId="m-main" text="Use the cache first." />);
    seedComment(mainKey, "m-main", "Use the cache first.", "cache");
    expect(highlightTexts("persona-comment")).toEqual(["cache"]);

    // Opening a SidePanel chat with no draft comments must not wipe main chat highlights.
    const side = mountPane(<ChatPane draftKey={sideKey} itemId="m-side" text="Side chat output." />);
    expect(highlightTexts("persona-comment")).toEqual(["cache"]);

    // Closing the SidePanel chat must also leave main chat highlights intact.
    unmountPane(side);
    expect(highlightTexts("persona-comment")).toEqual(["cache"]);
  });

  it("merges simultaneous highlights across two chats, isolates flash, and cleans up only the closed chat", () => {
    vi.useFakeTimers();
    const mainKey = "test-main-both";
    const sideKey = "test-side-both";

    const main = mountPane(<ChatPane draftKey={mainKey} itemId="m1" text="Check alpha behavior." />);
    const side = mountPane(<ChatPane draftKey={sideKey} itemId="s1" text="Check beta behavior." />);
    const mainId = seedComment(mainKey, "m1", "Check alpha behavior.", "alpha");
    seedComment(sideKey, "s1", "Check beta behavior.", "beta");

    expect(highlightTexts("persona-comment")).toEqual(["alpha", "beta"]);
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);

    // Jumping to main's comment flashes only alpha while keeping beta in the base highlight.
    act(() => {
      expect(jumpToComment(mainId)).toBe(true);
    });
    expect(highlightTexts("persona-comment-flash")).toEqual(["alpha"]);
    expect(highlightTexts("persona-comment")).toEqual(["beta"]);

    // Unmounting side removes only beta; alpha remains flashed and then returns to base.
    unmountPane(side);
    expect(highlightTexts("persona-comment-flash")).toEqual(["alpha"]);
    expect(highlightsMap.has("persona-comment")).toBe(false);

    act(() => {
      vi.advanceTimersByTime(1400);
    });
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
    expect(highlightTexts("persona-comment")).toEqual(["alpha"]);

    // Unmounting the last chat deletes the global highlight entries completely.
    unmountPane(main);
    expect(highlightsMap.has("persona-comment")).toBe(false);
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
  });

  it("clearing draft comments in one chat leaves another chat's highlights untouched", () => {
    const mainKey = "test-main-clear";
    const sideKey = "test-side-clear";

    mountPane(<ChatPane draftKey={mainKey} itemId="m1" text="Keep primary token." />);
    mountPane(<ChatPane draftKey={sideKey} itemId="s1" text="Clear secondary token." />);
    seedComment(mainKey, "m1", "Keep primary token.", "primary");
    seedComment(sideKey, "s1", "Clear secondary token.", "secondary");
    expect(highlightTexts("persona-comment")).toEqual(["primary", "secondary"]);

    act(() => {
      setQuotes(sideKey, []);
    });
    expect(highlightTexts("persona-comment")).toEqual(["primary"]);
  });

  it("resets the flash timer on repeated jumps and does not recreate ResizeObserver while flashing", () => {
    vi.useFakeTimers();
    const mainKey = "test-flash-timer";

    mountPane(<ChatPane draftKey={mainKey} itemId="m1" text="Pulse repeated target." />);
    const id = seedComment(mainKey, "m1", "Pulse repeated target.", "repeated");
    const observersAfterMount = resizeObserverCount;
    expect(observersAfterMount).toBe(1);

    act(() => {
      expect(jumpToComment(id)).toBe(true);
    });
    expect(highlightTexts("persona-comment-flash")).toEqual(["repeated"]);
    expect(resizeObserverCount).toBe(observersAfterMount);

    // Jumping again at 1000ms must cancel the first 1400ms timeout so flash stays active at 1500ms.
    act(() => {
      vi.advanceTimersByTime(1000);
      expect(jumpToComment(id)).toBe(true);
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(highlightTexts("persona-comment-flash")).toEqual(["repeated"]);

    // Full 1400ms after the second jump clears the flash without recreating observers.
    act(() => {
      vi.advanceTimersByTime(900);
    });
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
    expect(highlightTexts("persona-comment")).toEqual(["repeated"]);
    expect(resizeObserverCount).toBe(observersAfterMount);
  });

  it("highlights pre-existing persisted draft comments on initial mount inside the scroller", () => {
    const key = "test-persisted-on-mount";
    seedComment(key, "m1", "Persisted draft highlight.", "Persisted");

    const entry = mountPane(<ChatPane draftKey={key} itemId="m1" text="Persisted draft highlight." />);
    expect(highlightTexts("persona-comment")).toEqual(["Persisted"]);
    expect(resizeObserverCount).toBe(1);

    unmountPane(entry);
    expect(highlightsMap.has("persona-comment")).toBe(false);
  });

  it("clears flash after FLASH_MS even when the jump callback identity changes mid-flash", () => {
    vi.useFakeTimers();
    const key = "test-jump-prop-change";
    const firstJump = vi.fn();
    const secondJump = vi.fn();
    const entry = mountPane(<ChatPane draftKey={key} itemId="m1" text="Watch flash clear." jump={firstJump} />);
    const id = seedComment(key, "m1", "Watch flash clear.", "flash");

    act(() => {
      expect(jumpToComment(id)).toBe(true);
    });
    expect(firstJump).toHaveBeenCalledTimes(1);
    expect(highlightTexts("persona-comment-flash")).toEqual(["flash"]);

    // Re-rendering with a new jump function mid-flash must not cancel the clear timer.
    act(() => {
      entry.root.render(<ChatPane draftKey={key} itemId="m1" text="Watch flash clear." jump={secondJump} />);
    });
    act(() => {
      vi.advanceTimersByTime(1400);
    });
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
    expect(highlightTexts("persona-comment")).toEqual(["flash"]);

    // Subsequent jumps use the latest jump callback.
    act(() => {
      expect(jumpToComment(id)).toBe(true);
    });
    expect(secondJump).toHaveBeenCalledTimes(1);
  });

  it("removes persona-comment-flash immediately when the flashed comment is deleted while another stays", () => {
    vi.useFakeTimers();
    const key = "test-delete-flashed";
    mountPane(<ChatPane draftKey={key} itemId="m1" text="Keep first and drop second." />);
    seedComment(key, "m1", "Keep first and drop second.", "first");
    const secondId = seedComment(key, "m1", "Keep first and drop second.", "second");

    act(() => {
      expect(jumpToComment(secondId)).toBe(true);
    });
    expect(highlightTexts("persona-comment-flash")).toEqual(["second"]);
    expect(highlightTexts("persona-comment")).toEqual(["first"]);

    act(() => {
      removeQuote(key, secondId);
    });
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
    expect(highlightTexts("persona-comment")).toEqual(["first"]);
  });

  it("falls back to overlay rect spans when CSS.highlights is unavailable", () => {
    Reflect.deleteProperty(globalThis, "CSS");
    Reflect.deleteProperty(globalThis, "Highlight");
    const key = "test-fallback-overlay";
    seedComment(key, "m1", "Fallback overlay words.", "overlay");

    const entry = mountPane(<ChatPane draftKey={key} itemId="m1" text="Fallback overlay words." />);
    const mark = entry.host.querySelector('[data-comment-mark="1"]');
    expect(mark).not.toBeNull();
    expect(mark!.querySelectorAll("span.bg-accent\\/20").length).toBeGreaterThan(0);
  });

  it("survives React StrictMode double-mount and cleans up on unmount", () => {
    const key = "test-strict-mode";
    seedComment(key, "m1", "Strict mode highlight.", "highlight");

    const entry = mountPane(
      <StrictMode>
        <ChatPane draftKey={key} itemId="m1" text="Strict mode highlight." />
      </StrictMode>,
    );
    expect(highlightTexts("persona-comment")).toEqual(["highlight"]);

    unmountPane(entry);
    expect(highlightsMap.has("persona-comment")).toBe(false);
    expect(highlightsMap.has("persona-comment-flash")).toBe(false);
  });
});
