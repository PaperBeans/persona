// @vitest-environment jsdom
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InlineMentionInput, type InlineMentionInputHandle } from "../views/chat/InlineMentionInput";
import type { InlineDocument } from "./inline-mentions";
import type { Route } from "./router";
import { currentPlatform } from "./shortcuts";
import { useShortcuts } from "./use-shortcuts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const VISIBLE_RECT = { left: 0, top: 0, right: 200, bottom: 32, width: 200, height: 32, x: 0, y: 0, toJSON: () => ({}) } as DOMRect;
let origGetClientRects: typeof Element.prototype.getClientRects;

function Shell({ route, children }: { route: Route; children: React.ReactNode }) {
  useShortcuts({ route, narrow: false, toggleDrawer: vi.fn() });
  return <main>{children}</main>;
}

function RichComposer({ disabled = false, hidden = false }: { disabled?: boolean; hidden?: boolean }) {
  const ref = useRef<InlineMentionInputHandle>(null);
  const value: InlineDocument = [
    "Check ",
    { id: "file:src/App.tsx", kind: "file", title: "App.tsx", url: "", text: "src/App.tsx", attrs: { path: "src/App.tsx" } },
  ];
  return (
    <div data-hidden={hidden ? "true" : undefined}>
      <InlineMentionInput
        inputRef={ref}
        value={value}
        label="Message"
        placeholder="Ask anything"
        disabled={disabled}
        composerInput
        onChange={vi.fn()}
      />
    </div>
  );
}

function pressModI(): KeyboardEvent {
  const mac = currentPlatform() === "mac";
  const ev = new KeyboardEvent("keydown", {
    key: "i",
    metaKey: mac,
    ctrlKey: !mac,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    window.dispatchEvent(ev);
  });
  return ev;
}

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  origGetClientRects = Element.prototype.getClientRects;
  Element.prototype.getClientRects = function fakeRects() {
    if ((this as Element).closest?.('[data-hidden="true"]')) return [] as unknown as DOMRectList;
    return [VISIBLE_RECT] as unknown as DOMRectList;
  };
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  try {
    act(() => {
      root.unmount();
    });
    host.remove();
  } finally {
    Element.prototype.getClientRects = origGetClientRects;
    document.body.innerHTML = "";
  }
});

describe("Mod+I focus-input shortcut (#15777)", () => {
  it("focuses the InlineMentionInput contentEditable composer on Mod+I", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1" }}>
          <button id="other" type="button">Other</button>
          <RichComposer />
        </Shell>,
      );
    });
    const other = host.querySelector<HTMLButtonElement>("#other")!;
    other.focus();
    expect(document.activeElement).toBe(other);

    const ev = pressModI();
    expect(ev.defaultPrevented).toBe(true);
    const composer = host.querySelector<HTMLElement>("[data-composer-input]")!;
    expect(composer.tagName).toBe("DIV");
    expect(document.activeElement).toBe(composer);
  });

  it("focuses [data-composer-input] instead of an earlier unrelated textarea inside <main>", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "persona" }}>
          <textarea id="comment-note" aria-label="Comment note" />
          <RichComposer />
        </Shell>,
      );
    });
    const ev = pressModI();
    expect(ev.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(host.querySelector("[data-composer-input]"));

    // Also applies when the composer is a plain <textarea data-composer-input>.
    act(() => {
      root.render(
        <Shell route={{ name: "persona" }}>
          <textarea id="comment-note" aria-label="Comment note" />
          <textarea id="plain-composer" data-composer-input="" aria-label="Message" />
        </Shell>,
      );
    });
    const otherBtn = document.createElement("button");
    document.body.append(otherBtn);
    otherBtn.focus();
    pressModI();
    expect(document.activeElement).toBe(host.querySelector("#plain-composer"));
  });

  it("skips a disabled InlineMentionInput without hijacking unrelated textareas, falling back to terminal only when visible", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1" }}>
          <button id="other" type="button">Other</button>
          <textarea id="comment-note" aria-label="Comment note" />
          <RichComposer disabled />
        </Shell>,
      );
    });
    const other = host.querySelector<HTMLButtonElement>("#other")!;
    other.focus();

    const ev = pressModI();
    expect(ev.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(other);

    // When a visible terminal is mounted alongside a disabled composer, fall back to .xterm-helper-textarea.
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1" }}>
          <button id="other" type="button">Other</button>
          <textarea id="comment-note" aria-label="Comment note" />
          <RichComposer disabled />
          <div className="xterm">
            <textarea className="xterm-helper-textarea" aria-label="Terminal" />
          </div>
        </Shell>,
      );
    });
    host.querySelector<HTMLButtonElement>("#other")!.focus();
    const termEv = pressModI();
    expect(termEv.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(host.querySelector(".xterm-helper-textarea"));
  });

  it("skips hidden [data-composer-input] layers and focuses the visible one", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1" }}>
          <RichComposer hidden />
          <div id="active-pane">
            <RichComposer />
          </div>
        </Shell>,
      );
    });
    const ev = pressModI();
    expect(ev.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(host.querySelector("#active-pane [data-composer-input]"));
  });

  it("skips an inert or hidden composer without hijacking unrelated textareas, and ignores visibility:hidden terminals", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1", view: "term" }}>
          <button id="other" type="button">Other</button>
          <textarea id="card-note" aria-label="Card note" />
          <div inert>
            <RichComposer />
          </div>
          <div className="xterm" style={{ visibility: "hidden" }}>
            <textarea className="xterm-helper-textarea" aria-label="Hidden terminal" />
          </div>
        </Shell>,
      );
    });
    const other = host.querySelector<HTMLButtonElement>("#other")!;
    other.focus();
    const hiddenTermEv = pressModI();
    expect(hiddenTermEv.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(other);

    // Once the terminal layer is visible, Mod+I focuses the active terminal rather than #card-note or the inert composer.
    act(() => {
      root.render(
        <Shell route={{ name: "session", mid: "m1", sid: "s1", view: "term" }}>
          <button id="other" type="button">Other</button>
          <textarea id="card-note" aria-label="Card note" />
          <div inert>
            <RichComposer />
          </div>
          <div className="xterm">
            <textarea className="xterm-helper-textarea" aria-label="Terminal" />
          </div>
        </Shell>,
      );
    });
    host.querySelector<HTMLButtonElement>("#other")!.focus();
    const ev = pressModI();
    expect(ev.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(host.querySelector(".xterm-helper-textarea"));
  });

  it("falls back to non-composer main textarea when no [data-composer-input] is visible and to terminal on non-chat routes", () => {
    act(() => {
      root.render(
        <Shell route={{ name: "persona-new" }}>
          <textarea id="fallback-box" aria-label="Prompt" />
        </Shell>,
      );
    });
    pressModI();
    expect(document.activeElement).toBe(host.querySelector("#fallback-box"));

    act(() => {
      root.render(
        <Shell route={{ name: "machine", mid: "m1" }}>
          <textarea id="machine-note" aria-label="Note" />
          <div className="xterm">
            <textarea className="xterm-helper-textarea" aria-label="Terminal" />
          </div>
        </Shell>,
      );
    });
    pressModI();
    expect(document.activeElement).toBe(host.querySelector(".xterm-helper-textarea"));
  });
});
