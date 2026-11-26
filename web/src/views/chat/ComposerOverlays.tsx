import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { useCloseOnBack } from "../../lib/back-close";
import { enterSends, hasFinePointer, NARROW, useMedia } from "../../lib/viewport";
import { useComposition } from "../../lib/ime";
import { editorText, type InlineDocument, type InlineSelection } from "../../lib/inline-mentions";
import { cx, EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { useModalFocus } from "../../ui/overlay";
import { ComposerField } from "./ComposerField";
import type { InlineMentionInputHandle } from "./InlineMentionInput";

type ExpandedProps = {
  editLabel?: string;
  value: InlineDocument;
  placeholder: string;
  selection: [number, number];
  onChange: (v: InlineDocument) => void;
  onCollapse: (sel: [number, number]) => void;
  canSend: boolean;
  blocked: boolean;
  onSend: (sel: [number, number], repeat?: boolean) => void;
};

type SuggestionProps = { text: string; onFill: () => void; onSend?: () => void };
type PasteProps = {
  pastedBlocks: { id: string; text: string }[];
  onRemove: (id: string) => void;
  onExpand: (id: string, text: string) => void;
};

/** Conditional composer extras share one chunk and one first-screen loader. */
export function ComposerOverlay(props: ExpandedProps | SuggestionProps | PasteProps) {
  if ("pastedBlocks" in props) return <PastedBlocks {...props} />;
  return "value" in props ? <ExpandedComposer {...props} /> : <SuggestionPill {...props} />;
}

function PastedBlocks({ pastedBlocks, onRemove, onExpand }: PasteProps) {
  const [previewId, setPreviewId] = useState<string | null>(null);
  const preview = pastedBlocks.find((block) => block.id === previewId);
  return <>
    <div className="flex flex-wrap items-center gap-1.5">
      {pastedBlocks.map((block) => <span key={block.id} className={cx(
        "inline-flex h-6.5 max-w-full items-center gap-1 rounded-[8px] border px-2 text-label transition-colors",
        preview === block ? "border-accent/40 bg-accent-tint text-accent-ink" : "border-line bg-inset text-ink-2 hover:bg-hover",
      )}>
        <button type="button" onClick={() => setPreviewId((id) => id === block.id ? null : block.id)}
          className="inline-flex min-w-0 items-center gap-1.5 truncate" title={t("chat-composeroverlays.pasted_preview_hint")}>
          <Icon name="paste" size={12} className="shrink-0 opacity-75" />
          <span className="truncate">{t("chat-composeroverlays.pasted_chip", { count: block.text.length })}</span>
        </button>
        <button type="button" aria-label={t("chat-composeroverlays.pasted_remove")} onClick={() => onRemove(block.id)}
          className="ml-0.5 flex size-4 shrink-0 items-center justify-center rounded-[4px] text-ink-3 transition-colors hover:bg-hover hover:text-ink">
          <Icon name="x" size={10} stroke={2.4} />
        </button>
      </span>)}
    </div>
    {preview && <PastedPreview pastedText={preview.text} onExpand={() => onExpand(preview.id, preview.text)} />}
  </>;
}

function PastedPreview({ pastedText, onExpand }: { pastedText: string; onExpand: () => void }) {
  const lines = pastedText.split(/\r?\n/);
  const snippet = lines.slice(0, 6).join("\n") + (lines.length > 6 ? "\n…" : "");
  return (
    <div className="rounded-[8px] border border-line bg-inset p-2 text-label">
      <div className="mb-1 flex items-center justify-between gap-2 text-ink-3">
        <span>{t("chat-composeroverlays.pasted_stats", { lines: lines.length, chars: pastedText.length })}</span>
        <button type="button" onClick={onExpand} className="font-medium text-accent-ink hover:underline">
          {t("chat-composeroverlays.pasted_expand")}
        </button>
      </div>
      <pre className="max-h-28 overflow-hidden font-mono text-submeta leading-[1.45] whitespace-pre-wrap text-ink-2 [overflow-wrap:anywhere]">{snippet}</pre>
    </div>
  );
}

/** The draft full screen on a phone (#5688): collapse at the top right, the text filling the visible height, send at the
 *  bottom right. It edits the box's own draft; closing hands back the caret so the box picks up where this left off. */
function ExpandedComposer({ editLabel, value, placeholder, selection, onChange, onCollapse, canSend, blocked, onSend }: ExpandedProps) {
  const panel = useRef<HTMLDivElement>(null);
  const field = useRef<InlineMentionInputHandle>(null);
  const start = useRef(selection);
  const ime = useComposition();
  useModalFocus(panel);
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.focus({ preventScroll: true });
    el.setSelectionRange(start.current[0], start.current[1]);
  }, []);
  const sel = (): InlineSelection => {
    const end = editorText(value).length;
    return field.current?.selection() ?? [end, end];
  };
  // Back collapses it, as the collapse button does, and stays in the chat
  useCloseOnBack(true, () => onCollapse(sel()), useMedia(NARROW));
  return (
    <div
      ref={panel}
      role="dialog"
      aria-modal="true"
      aria-label={editLabel ?? t("chat-composer.message")}
      tabIndex={-1}
      data-testid="composer-expanded"
      className="fixed inset-x-0 z-50 flex flex-col bg-surface outline-none"
      style={{ top: "var(--kb-top)", height: "var(--kb-h)", animation: `fade-in 160ms ease-out both` }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && !ime.busy(e.nativeEvent)) {
          e.preventDefault();
          e.stopPropagation();
          onCollapse(sel());
        }
      }}
    >
      <div className="flex justify-end px-2 pt-[max(env(safe-area-inset-top),8px)]">
        <button
          type="button"
          data-testid="composer-collapse"
          aria-label={t("chat-composeroverlays.collapse")}
          title={t("chat-composeroverlays.collapse")}
          onClick={() => onCollapse(sel())}
          className="touch-hit flex size-9 items-center justify-center rounded-[10px] text-ink-2 transition-colors hover:bg-hover hover:text-ink"
        >
          <Icon name="collapse" size={18} stroke={2} />
        </button>
      </div>
      <ComposerField
        inputRef={field}
        value={value}
        onChange={onChange}
        {...ime.handlers}
        onBlur={ime.onBlur}
        onKeyDown={(e) => {
          if (ime.busy(e.nativeEvent)) return;
          // The full editor always keeps plain Enter as a newline; an explicit Mod+Enter sends on any pointer.
          if (editLabel || !enterSends(e, false)) return;
          e.preventDefault();
          if (canSend || blocked) onSend(sel(), e.repeat);
        }}
        placeholder={placeholder}
        label={editLabel ?? t("chat-composer.message")}
        className="relative min-h-0 w-full flex-1 resize-none overflow-y-auto bg-transparent px-5 py-1 text-[16px] leading-[24px] text-ink outline-none [overflow-wrap:anywhere] placeholder:text-ink-3"
      />
      {!editLabel && <div className="flex justify-end px-3 pt-2 pb-[max(env(safe-area-inset-bottom),12px)]">
        <button
          type="button"
          data-testid="composer-expanded-send"
          aria-label={t("chat-composer.send")}
          disabled={!canSend}
          onClick={() => onSend(sel())}
          className="touch-hit flex size-9 shrink-0 items-center justify-center rounded-full transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.94]"
          style={{ background: canSend ? "var(--ink)" : "var(--line-strong)", color: canSend ? "var(--surface)" : "var(--ink-2)" }}
        >
          <Icon name="up" size={18} stroke={2.4} />
        </button>
      </div>}
    </div>
  );
}

/** The suggested next message over the box (iOS SuggestionPill): the row fills it in, the arrow sends it as it is; a
 *  risky one (no onSend) has no arrow (#4644). */
function SuggestionPill({ text, onFill, onSend }: SuggestionProps) {
  return (
    <div data-testid="composer-suggestion" className="mb-2 flex items-center gap-1 rounded-full bg-surface p-1 pl-1 shadow-card" style={{ animation: `fade-up 200ms ${EASE} both` }}>
      <button
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={onFill}
        aria-label={t("chat-composeroverlays.suggestion_label", { suggestion: text })}
        title={hasFinePointer() ? t("chat-composeroverlays.suggestion_use_hint") : undefined}
        className="touch-hit flex min-w-0 flex-1 items-center gap-2 rounded-full py-1 pr-1 pl-2.5 text-left transition-colors hover:bg-hover"
      >
        <Icon name="sparkles" size={13} stroke={2} className="shrink-0 text-accent" />
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-2">{text}</span>
        <span className="shrink-0 whitespace-nowrap text-[12px] font-medium text-accent">{t("chat-composeroverlays.suggestion_use")}</span>
      </button>
      {onSend && (
        <button
          type="button"
          onMouseDown={(e) => e.preventDefault()}
          onClick={onSend}
          aria-label={t("chat-composeroverlays.suggestion_send")}
          title={t("chat-composeroverlays.suggestion_send")}
          className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-full text-ink-3 transition-[background-color,color,transform] duration-150 hover:bg-hover hover:text-ink active:scale-[0.94]"
        >
          <Icon name="up" size={15} stroke={2.4} />
        </button>
      )}
    </div>
  );
}
