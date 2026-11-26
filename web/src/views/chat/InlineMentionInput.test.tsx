// @vitest-environment jsdom
import { act, useRef, useState, type RefObject } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { editorText, type InlineDocument } from "../../lib/inline-mentions";
import { skillContext } from "../../lib/skill-mentions";
import { InlineMentionInput, type InlineMentionInputHandle } from "./InlineMentionInput";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const skill = skillContext({ id: "review", name: "Skill Creator", source: "user · ~/.skills" });
const initial = ["介绍 ", skill, " 相关信息"];
let host: HTMLDivElement, root: Root;
let input: RefObject<InlineMentionInputHandle | null>;
let changed: ReturnType<typeof vi.fn<(doc: InlineDocument) => void>>;

function Harness({ placeholder = "Message" }: { placeholder?: string }) {
  input = useRef<InlineMentionInputHandle>(null);
  const [value, setValue] = useState<InlineDocument>(initial);
  return <InlineMentionInput inputRef={input} value={value} label="Message" placeholder={placeholder} onChange={(next) => {
    changed(next);
    setValue(next);
  }} />;
}

const box = () => host.querySelector<HTMLElement>('[role="textbox"]')!;
const token = () => box().querySelector<HTMLElement>("[data-inline-mention]");
const select = (start: number, end = start) => act(() => {
  input.current!.focus();
  input.current!.setSelectionRange(start, end);
});
const key = (key: string, options?: KeyboardEventInit) => {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...options });
  act(() => box().dispatchEvent(event));
  return event;
};

beforeEach(() => {
  changed = vi.fn();
  host = document.body.appendChild(document.createElement("div"));
  root = createRoot(host);
  act(() => root.render(<Harness />));
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

it.each([["Backspace", 4], ["Delete", 3]] as const)("%s removes the whole adjacent reference", (pressed, at) => {
  expect(token()?.getAttribute("contenteditable")).toBe("false");
  expect(token()?.title).toBe("user · ~/.skills");
  select(at);
  expect(key(pressed).defaultPrevented).toBe(true);
  expect(changed).toHaveBeenLastCalledWith(["介绍  相关信息"]);
  expect(input.current!.selection()).toEqual([3, 3]);
  expect(token()).toBeNull();
});

it("crosses a token with one arrow press in either direction and deletes mixed selections", () => {
  select(3);
  key("ArrowRight");
  expect(input.current!.selection()).toEqual([4, 4]);
  key("ArrowLeft");
  expect(input.current!.selection()).toEqual([3, 3]);
  select(1, 6);
  key("Delete");
  expect(changed).toHaveBeenLastCalledWith(["介关信息"]);
});

it("inserts and replaces queries through the editor handle", () => {
  select(4);
  act(() => input.current!.replaceRange([4, 4], [skill, " "]));
  expect(box().querySelectorAll("[data-inline-mention]")).toHaveLength(2);
  expect(input.current!.selection()).toEqual([6, 6]);
  act(() => input.current!.replaceRange([0, 2], ["请问"]));
  expect(changed).toHaveBeenLastCalledWith(["请问 ", skill, skill, "  相关信息"]);
});

it("handles mobile beforeinput deletion and line breaks without keyboard events", () => {
  const beforeInput = (inputType: string) => {
    const event = new InputEvent("beforeinput", { inputType, cancelable: true, bubbles: true });
    act(() => box().dispatchEvent(event));
    return event;
  };
  select(3);
  expect(beforeInput("deleteContentForward").defaultPrevented).toBe(true);
  expect(token()).toBeNull();
  expect(beforeInput("insertLineBreak").defaultPrevented).toBe(true);
  expect(changed).toHaveBeenLastCalledWith(["介绍 \n 相关信息"]);
});

it("leaves the composing text node, token and selection in place and publishes committed CJK once", async () => {
  select(2);
  const text = box().firstChild as Text;
  const ref = token();
  act(() => box().dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true })));
  act(() => {
    text.insertData(2, "中文");
    getSelection()!.collapse(text, 4);
    box().dispatchEvent(new InputEvent("input", { isComposing: true, bubbles: true }));
    root.render(<Harness placeholder="Other label" />);
  });
  expect(changed).not.toHaveBeenCalled();
  expect(key("Backspace", { isComposing: true }).defaultPrevented).toBe(false);
  expect(box().firstChild).toBe(text);
  expect(token()).toBe(ref);
  expect(getSelection()!.anchorNode).toBe(text);
  expect(getSelection()!.anchorOffset).toBe(4);
  await act(async () => box().dispatchEvent(new CompositionEvent("compositionend", { bubbles: true, data: "中文" })));
  expect(changed).toHaveBeenCalledTimes(1);
  expect(editorText(changed.mock.calls[0][0])).toBe("介绍中文 \uFFFC 相关信息");
  expect(input.current!.selection()).toEqual([4, 4]);
  expect(token()).toBe(ref);
});

it("copies readable text and pastes only plain text over a selection, with line breaks", () => {
  select(1, 5);
  const setData = vi.fn();
  const clipboard = (type: string) => {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: { setData, getData: (mime: string) => mime === "text/plain" ? "<b>你好</b>\r\n" : "<i>ignored</i>" } });
    act(() => box().dispatchEvent(event));
    return event;
  };
  expect(clipboard("copy").defaultPrevented).toBe(true);
  expect(setData).toHaveBeenCalledWith("text/plain", "绍 Skill Creator ");
  expect(clipboard("paste").defaultPrevented).toBe(true);
  expect(changed).toHaveBeenLastCalledWith(["介<b>你好</b>\n相关信息"]);
  expect(box().querySelector("b")).toBeNull();
});
