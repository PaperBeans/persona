// @vitest-environment jsdom
// The "/" palette as cindy draws it (#5325): one row per command with where it comes from, the focused command's
// description in a card beside the panel (inside it when the window has no room beside it), "No matching commands".
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComposerCommand } from "../../lib/extension-prompts";
import { SlashPalette } from "./SlashPalette";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const CONTINUE: ComposerCommand = { word: "/继续", desc: "让它接着做", text: "继续" };
const COMPACT: ComposerCommand = { word: "/compact", desc: "压缩上下文", text: "/compact", agent: true, source: "Claude Code" };
const PROMPT: ComposerCommand = { key: "extension:kit/today", word: "/今天做了什么", desc: "", text: "整理今天的进展。", source: "入门套件" };

let host: HTMLDivElement;
let root: Root;
const within = createRef<HTMLDivElement>();
const onHover = vi.fn();
const onPick = vi.fn();
const onClose = vi.fn();

beforeEach(() => {
  [onHover, onPick, onClose].forEach((f) => f.mockClear());
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  Object.defineProperty(window, "innerWidth", { value: 1024, configurable: true });
});

const render = (rows: ComposerCommand[], active = 0) =>
  act(() => root.render(
    <div ref={within}>
      <textarea />
      <SlashPalette rows={rows} active={active} onHover={onHover} onPick={onPick} onClose={onClose} within={within} />
    </div>,
  ));

const options = () => [...host.querySelectorAll<HTMLButtonElement>("[role=option]")];
// the card beside the panel, or the line inside it
const card = () => host.querySelector<HTMLElement>("[data-slash-card]");

describe("rows", () => {
  it("name the command without its slash and say where it comes from, nothing for Persona's own", () => {
    render([CONTINUE, COMPACT, PROMPT]);
    expect(options().map((o) => o.textContent)).toEqual(["继续", "compactClaude Code", "今天做了什么入门套件"]);
    expect(options()[0].querySelectorAll("span")).toHaveLength(1);
  });

  it("mark the focused one, and hover and click report back while the box keeps the focus", () => {
    render([CONTINUE, COMPACT], 1);
    expect(options().map((o) => o.getAttribute("aria-selected"))).toEqual(["false", "true"]);
    expect(options()[1].className).toContain("font-medium");
    act(() => options()[0].dispatchEvent(new MouseEvent("mouseover", { bubbles: true })));
    expect(onHover).toHaveBeenCalledWith(0);
    const down = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
    act(() => options()[0].dispatchEvent(down));
    expect(down.defaultPrevented).toBe(true);
    act(() => options()[0].click());
    expect(onPick).toHaveBeenCalledWith(CONTINUE);
  });

  it("carry their description for assistive technology; the card is only drawn", () => {
    render([CONTINUE, COMPACT]);
    const described = options().map((o) => document.getElementById(o.getAttribute("aria-describedby")!)?.textContent);
    expect(described).toEqual([`让它接着做${t("chat-slashpalette.types", { text: "继续" })}`, "压缩上下文"]);
    expect(host.querySelector("[role=listbox]")!.getAttribute("aria-label")).toBe(t("chat-slashpalette.label"));
  });
});

describe("the card", () => {
  it("shows the focused command, its description and what a text command types, to the right of the panel", () => {
    render([CONTINUE, COMPACT]);
    expect(card()!.textContent).toBe(`继续让它接着做${t("chat-slashpalette.types", { text: "继续" })}`);
    expect(card()!.style.left).toBe(`${320 + 8}px`);
    render([CONTINUE, COMPACT], 1);
    expect(card()!.textContent).toBe("compact压缩上下文");
  });

  it("shows a quick prompt's words", () => {
    render([PROMPT]);
    expect(card()!.textContent).toBe(`今天做了什么${t("chat-slashpalette.types", { text: "整理今天的进展。" })}`);
  });

  it("goes to the panel's left when the window has no room on its right", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(700, 400, 320, 200));
    render([CONTINUE]);
    expect(card()!.style.left).toBe(`${-(280 + 8)}px`);
  });

  it("is a line inside the panel under the rows when there is room on neither side", () => {
    Object.defineProperty(window, "innerWidth", { value: 390, configurable: true });
    render([CONTINUE, COMPACT], 1);
    const line = card()!;
    expect(line.parentElement).toBe(host.querySelector("[role=listbox]")!.parentElement);
    expect(line.previousElementSibling!.getAttribute("role")).toBe("listbox");
    expect(line.textContent).toBe("压缩上下文");
    expect(line.style.left).toBe("");
  });
});

describe("nothing matching", () => {
  it("says so, with no rows and no card", () => {
    render([]);
    expect(host.textContent).toBe(t("chat-slashpalette.no_match"));
    expect(options()).toHaveLength(0);
    expect(card()).toBeNull();
  });
});

describe("closing", () => {
  it("closes on a press outside the composer, not inside it", () => {
    render([CONTINUE]);
    act(() => host.querySelector("textarea")!.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(onClose).not.toHaveBeenCalled();
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
