import { useImperativeHandle, useLayoutEffect, useRef, useState, type HTMLAttributes, type Ref } from "react";
import { createPortal } from "react-dom";
import {
  INLINE_TOKEN, editorText, joinInline, mentionLabel, replaceInlineRange, sliceInline, visibleInlineText, writeInlineDraft,
  type InlineDocument, type InlineSelection,
} from "../../lib/inline-mentions";
import { composing } from "../../lib/send-history";
import { COMPOSITION_TAIL_MS } from "../../lib/ime";
import type { ContextRef } from "../../lib/links";
import { Icon } from "../../ui/icons";
import { referenceIcon } from "./reference-icon";

export interface InlineMentionInputHandle {
  element: HTMLElement | null;
  focus: (options?: FocusOptions) => void;
  blur: () => void;
  selection: () => InlineSelection;
  setSelectionRange: (start: number, end: number) => void;
  /** Replace a typed query or a selection. An empty range inserts at the caret. */
  replaceRange: (range: InlineSelection, insert: InlineDocument) => void;
}

export interface InlineMentionInputProps extends Pick<HTMLAttributes<HTMLElement>,
  "className" | "style" | "title" | "onFocus" | "onBlur" | "onKeyDown" | "onPaste" | "onCompositionStart" | "onCompositionEnd"> {
  inputRef: Ref<InlineMentionInputHandle>;
  value: InlineDocument;
  label: string;
  placeholder: string;
  disabled?: boolean;
  readOnly?: boolean;
  composerInput?: boolean;
  onChange: (doc: InlineDocument, selection: InlineSelection) => void;
  onSelectionChange?: (selection: InlineSelection) => void;
}

const TOKEN = "data-inline-mention";
const END = "data-inline-end";

function readNodes(container: Node, refs: readonly ContextRef[]): InlineDocument {
  const result: (string | ContextRef)[] = [];
  const visit = (parent: Node) => {
    let previousBlock = false;
    for (const node of parent.childNodes) {
      const block = node.nodeName === "DIV" || node.nodeName === "P";
      if (node.previousSibling && (block || previousBlock)) result.push("\n");
      previousBlock = block;
      if (node.nodeType === Node.TEXT_NODE) {
        result.push(node.nodeValue ?? "");
      } else if (node instanceof Element) {
        const index = node.getAttribute(TOKEN);
        if (index !== null && refs[Number(index)]) result.push(refs[Number(index)]);
        else if (node.tagName !== "BR") visit(node);
        else if (!node.hasAttribute(END) && node.nextSibling) result.push("\n");
      }
    }
  };
  visit(container);
  return joinInline(result);
}

function readSelection(el: HTMLElement, refs: readonly ContextRef[]): InlineSelection | undefined {
  const selection = el.ownerDocument.getSelection();
  if (!selection?.rangeCount) return;
  const range = selection.getRangeAt(0);
  if (!el.contains(range.startContainer) || !el.contains(range.endContainer)) return;
  const offset = (node: Node, at: number, end: boolean) => {
    const before = el.ownerDocument.createRange();
    before.setStart(el, 0);
    const token = (node instanceof Element ? node : node.parentElement)?.closest(`[${TOKEN}]`);
    if (token && el.contains(token)) {
      if (end) before.setEndAfter(token);
      else before.setEndBefore(token);
    } else before.setEnd(node, at);
    return editorText(readNodes(before.cloneContents(), refs)).length;
  };
  const start = offset(range.startContainer, range.startOffset, false);
  return [start, range.collapsed ? start : offset(range.endContainer, range.endOffset, true)];
}

function placeSelection(el: HTMLElement, [start, end]: InlineSelection) {
  // Our own writes use only top-level text, token spans and the terminal line-break sentinel.
  const point = (position: number): [Node, number] => {
    let i = 0;
    for (const node of el.childNodes) {
      if (node instanceof Element && node.hasAttribute(END)) return [el, i];
      if (node.nodeType === Node.TEXT_NODE) {
        const length = node.textContent?.length ?? 0;
        if (position <= length) return [node, position];
        position -= length;
      } else if (position-- <= 0) return [el, i];
      i++;
    }
    return [el, i];
  };
  const range = el.ownerDocument.createRange();
  range.setStart(...point(start));
  range.setEnd(...point(end));
  const selection = el.ownerDocument.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function preview(ref: ContextRef): string {
  const { cwd, path, branch } = ref.attrs ?? {};
  if (ref.kind === "file" && path) return cwd && !/^(?:[~/\\]|[A-Za-z]:)/.test(path) ? `${cwd.replace(/[\\/]$/, "")}/${path}` : path;
  if (ref.kind === "workspace") return [cwd, branch].filter(Boolean).join(" · ");
  if (ref.kind === "repository") return ref.url || path || ref.title;
  return ref.text.split("\n").slice(0, ref.kind === "skill" ? 1 : 2).join(" · ") || ref.title;
}

/** The browser owns draft text and composition nodes. React only renders icons in the atomic spans we create. */
export function InlineMentionInput({ inputRef, label, placeholder, value, onChange, onSelectionChange, onFocus, onBlur,
  onKeyDown, onPaste, onCompositionStart, onCompositionEnd, disabled, readOnly, composerInput, className, style, title,
}: InlineMentionInputProps) {
  const root = useRef<HTMLDivElement>(null);
  const current = useRef<InlineDocument>([]);
  const composingNow = useRef(false);
  const composedAt = useRef(-Infinity);
  const deferred = useRef<InlineDocument | null>(null);
  const selected = useRef<InlineSelection>([0, 0]);
  const tokens = useRef<ContextRef[]>([]);
  const [portals, setPortals] = useState<{ ref: ContextRef; node: HTMLSpanElement }[]>([]);
  const [inComposition, setInComposition] = useState(false);

  const selection = () => {
    const el = root.current;
    if (el && el.ownerDocument.activeElement === el) selected.current = readSelection(el, tokens.current) ?? selected.current;
    return selected.current;
  };
  const notifySelection = () => {
    if (!composingNow.current) onSelectionChange?.(selection());
  };
  const showCaret = () => {
    const el = root.current!;
    if (el.clientHeight >= el.scrollHeight) return;
    const range = el.ownerDocument.getSelection()?.rangeCount && el.ownerDocument.getSelection()!.getRangeAt(0);
    if (!range || !el.contains(range.endContainer)) return;
    const caret = range.getClientRects()[0];
    if (!caret) return;
    const rect = el.getBoundingClientRect();
    if (caret.bottom > rect.bottom) el.scrollTop += caret.bottom - rect.bottom;
    else if (caret.top < rect.top) el.scrollTop -= rect.top - caret.top;
  };
  const setSelectionRange = (start: number, end: number) => {
    const el = root.current;
    if (!el || composingNow.current) return;
    selected.current = [start, end];
    if (el.ownerDocument.activeElement === el) {
      placeSelection(el, selected.current);
      showCaret();
    }
    notifySelection();
  };
  const paint = (doc: InlineDocument) => {
    const el = root.current!;
    const fragment = el.ownerDocument.createDocumentFragment();
    const marks: typeof portals = [];
    for (const part of doc) {
      if (typeof part === "string") fragment.append(part);
      else {
        const node = el.ownerDocument.createElement("span");
        node.setAttribute("contenteditable", "false");
        node.setAttribute(TOKEN, String(marks.length));
        node.className = "inline-flex max-w-full items-baseline gap-1 rounded-sm align-baseline font-medium text-accent-ink hover:bg-accent-tint";
        node.title = preview(part);
        marks.push({ ref: part, node });
        fragment.append(node);
      }
    }
    const last = doc.at(-1);
    if (last === undefined || typeof last === "string" && last.endsWith("\n")) {
      const br = el.ownerDocument.createElement("br");
      br.setAttribute(END, "");
      fragment.append(br);
    }
    el.replaceChildren(fragment);
    tokens.current = marks.map((mark) => mark.ref);
    current.current = doc;
    setPortals(marks);
  };
  const sync = (doc: InlineDocument) => {
    const [start, end] = selection();
    paint(doc);
    setSelectionRange(start, end);
  };
  useLayoutEffect(() => {
    if (current.current === value || writeInlineDraft(current.current) === writeInlineDraft(value)) {
      deferred.current = null;
      return;
    }
    if (composingNow.current) deferred.current = value;
    else sync(value);
  }, [value]);
  useLayoutEffect(() => {
    const el = root.current!;
    const changed = () => {
      if (el.ownerDocument.activeElement === el) notifySelection();
    };
    el.ownerDocument.addEventListener("selectionchange", changed);
    return () => el.ownerDocument.removeEventListener("selectionchange", changed);
  }, [onSelectionChange]);

  const replaceRange = ([start, end]: InlineSelection, insert: InlineDocument) => {
    if (composingNow.current || disabled || readOnly) return;
    const next = replaceInlineRange(current.current, start, end, insert);
    const at = start + editorText(insert).length;
    paint(next);
    setSelectionRange(at, at);
    onChange(next, [at, at]);
  };
  const replace = (start: number, end: number, insert: string) => replaceRange([start, end], [insert]);
  useImperativeHandle(inputRef, () => ({
    get element() { return root.current; },
    focus: (options) => root.current?.focus(options),
    blur: () => root.current?.blur(),
    selection, setSelectionRange, replaceRange,
  }));
  const deleteTokens = (direction: "backward" | "forward", start: number, end: number) => {
    const from = start === end && direction === "backward" ? start - 1 : start;
    const to = start === end && direction === "forward" ? end + 1 : end;
    if (from < 0 || !sliceInline(current.current, from, to).some((p) => typeof p !== "string")) return false;
    replace(from, to, "");
    return true;
  };
  const publish = () => {
    const el = root.current;
    if (!el || composingNow.current) return;
    const next = readNodes(el, tokens.current);
    if (writeInlineDraft(current.current) === writeInlineDraft(next)) return;
    current.current = next;
    onChange(next, selection());
  };
  const finishComposition = () => {
    if (!root.current || composingNow.current) return;
    if (deferred.current) {
      const next = deferred.current;
      deferred.current = null;
      sync(next);
    } else publish();
  };
  const editable = !disabled && !readOnly;
  useLayoutEffect(() => {
    if (disabled) root.current?.blur();
  }, [disabled]);
  useLayoutEffect(() => {
    const el = root.current!;
    const beforeInput = (event: InputEvent) => {
      if (!editable || !event.cancelable || composingNow.current || event.isComposing) return;
      const [start, end] = selection();
      switch (event.inputType) {
        case "insertParagraph":
        case "insertLineBreak":
          event.preventDefault();
          if (performance.now() - composedAt.current >= COMPOSITION_TAIL_MS) replace(start, end, "\n");
          break;
        case "deleteContentBackward":
        case "deleteContentForward":
          if (deleteTokens(event.inputType === "deleteContentBackward" ? "backward" : "forward", start, end)) event.preventDefault();
          break;
      }
    };
    el.addEventListener("beforeinput", beforeInput);
    return () => el.removeEventListener("beforeinput", beforeInput);
  });

  return <>
    <div
      ref={root}
      role="textbox"
      aria-label={label}
      aria-placeholder={placeholder}
      aria-multiline="true"
      aria-disabled={disabled || undefined}
      aria-readonly={readOnly || undefined}
      aria-busy={readOnly || undefined}
      tabIndex={disabled ? -1 : 0}
      contentEditable={editable}
      data-composer-input={composerInput ? "" : undefined}
      data-empty={value.length === 0 && !inComposition ? "" : undefined}
      data-placeholder={placeholder}
      title={title}
      className={`${className ?? ""} whitespace-pre-wrap data-[empty]:before:pointer-events-none data-[empty]:before:absolute data-[empty]:before:text-ink-3 data-[empty]:before:content-[attr(data-placeholder)]`}
      style={style}
      onFocus={onFocus}
      onBlur={(event) => {
        selected.current = readSelection(event.currentTarget, tokens.current) ?? selected.current;
        if (composingNow.current) {
          composingNow.current = false;
          setInComposition(false);
          queueMicrotask(finishComposition);
        }
        onBlur?.(event);
      }}
      onClick={notifySelection}
      onCompositionStart={(event) => {
        composingNow.current = true;
        setInComposition(true);
        onCompositionStart?.(event);
      }}
      onCompositionEnd={(event) => {
        composingNow.current = false;
        setInComposition(false);
        composedAt.current = performance.now();
        onCompositionEnd?.(event);
        queueMicrotask(finishComposition);
      }}
      onInput={(event) => {
        if (!(event.nativeEvent as InputEvent).isComposing && !deferred.current) publish();
      }}
      onKeyDown={(event) => {
        onKeyDown?.(event);
        if (event.defaultPrevented || !editable || composingNow.current || composing(event.nativeEvent)) return;
        const [start, end] = selection();
        const text = editorText(current.current);
        const { key, shiftKey, metaKey, ctrlKey, altKey } = event;
        if (key === "Enter" && !metaKey && !ctrlKey) {
          event.preventDefault();
          if (performance.now() - composedAt.current >= COMPOSITION_TAIL_MS) replace(start, end, "\n");
          return;
        }
        if (key === "Backspace" || key === "Delete") {
          if (deleteTokens(key === "Backspace" ? "backward" : "forward", start, end)) event.preventDefault();
        } else if (!shiftKey && !metaKey && !ctrlKey && !altKey && start === end) {
          const left = key === "ArrowLeft" && text[start - 1] === INLINE_TOKEN;
          const right = key === "ArrowRight" && text[start] === INLINE_TOKEN;
          if (left || right) {
            event.preventDefault();
            setSelectionRange(start + (left ? -1 : 1), start + (left ? -1 : 1));
          }
        }
      }}
      onCopy={(event) => {
        const [start, end] = selection();
        if (start === end) return;
        event.clipboardData.setData("text/plain", visibleInlineText(sliceInline(current.current, start, end)));
        event.preventDefault();
      }}
      onCut={(event) => {
        const [start, end] = selection();
        if (!editable || composingNow.current || start === end) return;
        event.clipboardData.setData("text/plain", visibleInlineText(sliceInline(current.current, start, end)));
        event.preventDefault();
        replace(start, end, "");
      }}
      onPaste={(event) => {
        onPaste?.(event);
        if (event.defaultPrevented || !editable) return;
        event.preventDefault();
        const pasted = event.clipboardData.getData("text/plain");
        const [start, end] = selection();
        if (pasted && !composingNow.current) replace(start, end, pasted.replace(/\r\n?/g, "\n"));
      }}
    />
    {portals.map(({ ref, node }, i) => createPortal(<>
      <Icon name={referenceIcon(ref)} size={14} className="relative top-0.5 shrink-0" />
      <span className="min-w-0 truncate">{mentionLabel(ref)}</span>
    </>, node, String(i)))}
  </>;
}
