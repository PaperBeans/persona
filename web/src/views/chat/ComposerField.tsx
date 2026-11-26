import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState } from "react";
import { editorText, replaceInlineRange, visibleInlineText, type InlineSelection } from "../../lib/inline-mentions";
import { lazyPage } from "../../lib/lazy-page";
import type { InlineMentionInputHandle, InlineMentionInputProps } from "./InlineMentionInput";

const InlineField = lazyPage(() => import("./InlineMentionInput").then((m) => m.InlineMentionInput));

export const preloadInlineField = () => void InlineField.preload().catch(() => {});

/** Plain drafts use the native field. When the first token arrives, keep focus and logical offsets through the swap. */
export function ComposerField({ inputRef, value, label, placeholder, disabled, readOnly, composerInput, onChange,
  onSelectionChange, onFocus, onBlur, onKeyDown, onPaste, onCompositionStart, onCompositionEnd, ...appearance
}: InlineMentionInputProps) {
  const hasTokens = value.some((part) => typeof part !== "string");
  const [inline, setInline] = useState(() => hasTokens && InlineField.ready());
  const plain = useRef<HTMLTextAreaElement>(null);
  const rich = useRef<InlineMentionInputHandle>(null);
  const cursor = useRef<InlineSelection>([0, 0]);
  const focused = useRef(false);
  const replacing = useRef(false);

  const selection = (): InlineSelection => {
    if (rich.current) return rich.current.selection();
    if (plain.current && !hasTokens) return [plain.current.selectionStart, plain.current.selectionEnd];
    return cursor.current;
  };
  useImperativeHandle(inputRef, () => ({
    get element() { return rich.current?.element ?? plain.current; },
    focus: (options) => (rich.current?.element ?? plain.current)?.focus(options),
    blur: () => (rich.current?.element ?? plain.current)?.blur(),
    selection,
    setSelectionRange: (start, end) => {
      cursor.current = [start, end];
      if (rich.current) rich.current.setSelectionRange(start, end);
      else if (!hasTokens) plain.current?.setSelectionRange(start, end);
    },
    replaceRange: ([start, end], insert) => {
      if (disabled || readOnly) return;
      (rich.current?.element ?? plain.current)?.focus({ preventScroll: true });
      if (rich.current) rich.current.replaceRange([start, end], insert);
      else {
        const at = start + editorText(insert).length;
        cursor.current = [at, at];
        replacing.current = true;
        onChange(replaceInlineRange(value, start, end, insert), cursor.current);
      }
    },
  }));
  useLayoutEffect(() => {
    if (!replacing.current) return;
    replacing.current = false;
    if (!hasTokens) plain.current?.setSelectionRange(...cursor.current);
  }, [value]);
  useLayoutEffect(() => {
    if (inline || !hasTokens || !InlineField.ready()) return;
    focused.current = document.activeElement === plain.current;
    setInline(true);
  }, [inline, hasTokens]);
  useEffect(() => {
    if (inline || !hasTokens) return;
    let live = true;
    void InlineField.preload().then(() => {
      if (!live) return;
      focused.current = document.activeElement === plain.current;
      setInline(true);
    }).catch(() => {});
    return () => { live = false; };
  }, [inline, hasTokens]);
  useLayoutEffect(() => {
    if (!inline) return;
    if (focused.current) rich.current?.focus({ preventScroll: true });
    rich.current?.setSelectionRange(...cursor.current);
  }, [inline]);

  if (inline) return <InlineField inputRef={rich} value={value} label={label} placeholder={placeholder}
    disabled={disabled} readOnly={readOnly} composerInput={composerInput} onChange={onChange}
    onSelectionChange={onSelectionChange} onFocus={onFocus} onBlur={onBlur} onKeyDown={onKeyDown} onPaste={onPaste}
    onCompositionStart={onCompositionStart} onCompositionEnd={onCompositionEnd} {...appearance} />;

  return <textarea
    {...appearance}
    ref={plain}
    value={hasTokens ? visibleInlineText(value) : editorText(value)}
    rows={1}
    data-composer-input={composerInput ? "" : undefined}
    disabled={disabled}
    readOnly={readOnly || hasTokens}
    aria-busy={readOnly || hasTokens || undefined}
    aria-label={label}
    placeholder={placeholder}
    onKeyDown={onKeyDown}
    onPaste={onPaste}
    onFocus={onFocus}
    onBlur={onBlur}
    onCompositionStart={onCompositionStart}
    onCompositionEnd={onCompositionEnd}
    onSelect={() => onSelectionChange?.(selection())}
    onClick={() => onSelectionChange?.(selection())}
    onChange={(event) => {
      const el = event.currentTarget;
      onChange(el.value ? [el.value] : [], [el.selectionStart, el.selectionEnd]);
    }}
  />;
}
