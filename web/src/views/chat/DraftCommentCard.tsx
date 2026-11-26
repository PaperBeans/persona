import { useLayoutEffect, useRef } from "react";
import { MAX_COMMENT_CHARS } from "../../lib/comments";
import { cancelNote, editNote, setNote, type DraftQuote } from "../../lib/drafts";
import { composing } from "../../lib/send-history";
import { cx, EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";
import { Excerpt, goToComment, NumberDot, omittedOf, whereOf } from "./CommentCard";

/** The card above the input. `editing`: its note is open for writing (Enter keeps it, Esc cancels). */
export function CommentCard({ draftKey, quote, n, editing, onRemove }: { draftKey: string; quote: DraftQuote; n: number; editing: boolean; onRemove: () => void }) {
  const c = quote.comment!;
  const id = quote.id!;
  const box = useRef<HTMLTextAreaElement>(null);
  const before = useRef(c.body); // the note as it was when writing began, for Esc
  const done = useRef(false);
  useLayoutEffect(() => {
    if (!editing) return;
    before.current = c.body;
    done.current = false;
    const el = box.current;
    if (el) {
      el.focus({ preventScroll: true });
      el.setSelectionRange(el.value.length, el.value.length);
      const list = el.closest<HTMLElement>("[data-quotes]");
      if (list) {
        let top = 0;
        for (let node: HTMLElement | null = el; node && node !== list; node = node.offsetParent as HTMLElement | null) top += node.offsetTop;
        const bottom = top + el.offsetHeight;
        if (bottom > list.scrollTop + list.clientHeight) list.scrollTop = bottom - list.clientHeight;
        else if (top < list.scrollTop) list.scrollTop = top;
      }
    }
    // only when the writing begins
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 112)}px`;
  }, [c.body, editing]);

  const finish = (cancel: boolean) => {
    if (done.current) return;
    done.current = true;
    if (cancel) cancelNote(draftKey, id, before.current);
    else setNote(draftKey, id, c.body, true);
  };
  const where = whereOf(c);
  return (
    <div
      className="flex min-w-0 items-start gap-2 rounded-[10px] bg-inset py-1.5 pr-1 pl-2 shadow-hairline"
      aria-label={t("chat-comments.comment_n", { n })}
      data-comment-card={n}
      style={{ animation: `pop-in 180ms ${EASE} both` }}
    >
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-accent" />
      <div className="flex min-w-0 flex-1 flex-col gap-1 py-px">
        <button
          type="button"
          onClick={() => goToComment(quote)}
          title={t("chat-comments.jump")}
          className="flex min-w-0 flex-col gap-0.5 rounded-[6px] text-left transition-colors hover:bg-hover-2 focus-visible:outline-2 focus-visible:outline-accent"
        >
          <span className="flex min-w-0 items-center gap-1.5 text-submeta font-medium whitespace-nowrap text-ink-3">
            <NumberDot n={n} />
            <span className="shrink-0">{t("chat-comments.comment_n", { n })}</span>
          </span>
          {where && <span className="truncate font-mono text-submeta text-ink-3">{where}</span>}
          <Excerpt q={quote} lines={2} />
          {omittedOf(c) > 0 && <span className="text-submeta text-ink-3">{t("chat-comments.omitted", { count: omittedOf(c) })}</span>}
        </button>
        {editing ? (
          <div className="flex flex-col gap-0.5">
            <textarea
              ref={box}
              value={c.body}
              rows={1}
              maxLength={MAX_COMMENT_CHARS}
              aria-label={t("chat-comments.note_label", { n })}
              placeholder={t("chat-comments.placeholder")}
              onChange={(e) => setNote(draftKey, id, e.target.value)}
              onKeyDown={(e) => {
                if (composing(e.nativeEvent)) return;
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  finish(false);
                } else if (e.key === "Escape") {
                  // not the page's Esc (it stops a running agent)
                  e.preventDefault();
                  e.stopPropagation();
                  finish(true);
                }
              }}
              onBlur={() => finish(false)}
              className="w-full resize-none rounded-[8px] bg-surface px-2 py-1 text-meta leading-[1.5] text-ink shadow-hairline outline-none placeholder:text-ink-3 focus:shadow-[0_0_0_1.5px_var(--accent)]"
            />
            <span className="flex justify-between text-submeta text-ink-3">
              <span className="min-w-0 truncate">{t("chat-comments.hint_enter")}</span>
              {c.body.length > MAX_COMMENT_CHARS * 0.8 && <span className="tabular-nums">{t("chat-comments.counter", { n: c.body.length, max: MAX_COMMENT_CHARS })}</span>}
            </span>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => editNote(draftKey, id)}
            className={cx("w-full rounded-[6px] text-left text-meta leading-[1.5] [overflow-wrap:anywhere] whitespace-pre-wrap transition-colors hover:bg-hover-2", c.body ? "text-ink" : "text-ink-3")}
          >
            {c.body || t("chat-comments.placeholder")}
          </button>
        )}
      </div>
      <button
        type="button"
        aria-label={t("chat-comments.remove")}
        title={t("chat-comments.remove")}
        onMouseDown={(e) => e.preventDefault()}
        onClick={onRemove}
        className="flex size-6 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
      >
        <Icon name="x" size={12} stroke={2.4} />
      </button>
    </div>
  );
}
