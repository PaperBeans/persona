import { useLayoutEffect, useRef, useState } from "react";
import { base64File } from "../../lib/attachments";
import { MAX_COMMENT_CHARS } from "../../lib/comments";
import { cancelNote, editNote, setCommentImages, setNote, type CommentImage, type DraftQuote } from "../../lib/drafts";
import { AttachmentTray } from "../../ui/AttachmentTray";
import { cx, EASE, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";
import { composing } from "../../lib/send-history";
import { Excerpt, goToComment, NumberDot, omittedOf, whereOf } from "./CommentCard";

export type CommentImagePicker = (id: string, files: File[], signal: AbortSignal) => Promise<void>;

/** The card above the input. `editing`: its note is open for writing (Enter keeps it, Esc cancels). */
export function DraftCommentCard({ draftKey, quote, n, editing, onRemove, onAddImages }: {
  draftKey: string; quote: DraftQuote; n: number; editing: boolean; onRemove: () => void; onAddImages?: CommentImagePicker;
}) {
  const c = quote.comment!;
  const id = quote.id!;
  const box = useRef<HTMLTextAreaElement>(null);
  const before = useRef<{ body: string; images?: DraftQuote["images"] } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const reads = useRef<AbortController | null>(null);
  const [preparing, setPreparing] = useState(0);
  const [previews, setPreviews] = useState<{ id: string; name: string; url: string }[]>([]);
  const previewUrls = useRef(new Map<CommentImage, string>());
  useLayoutEffect(() => {
    const urls = previewUrls.current;
    return () => {
      reads.current?.abort();
      urls.forEach((url) => URL.revokeObjectURL(url));
      urls.clear();
    };
  }, []);
  useLayoutEffect(() => {
    const urls = previewUrls.current;
    const images = quote.images ?? [];
    if (!urls.size && !images.length) return;
    for (const [image, url] of urls) {
      if (images.includes(image)) continue;
      URL.revokeObjectURL(url);
      urls.delete(image);
    }
    setPreviews(images.map((image) => {
      const { id, name, mime, data } = image;
      const url = urls.get(image) ?? URL.createObjectURL(base64File(name, mime, data));
      urls.set(image, url);
      return { id, name, url };
    }));
  }, [quote.images]);

  const addImages = async (files: File[]) => {
    if (!onAddImages || !files.length) return;
    const { signal } = reads.current ??= new AbortController();
    setPreparing((n) => n + 1);
    try {
      await onAddImages(id, files, signal);
    } finally {
      if (!signal.aborted) setPreparing((n) => n - 1);
    }
  };
  const done = useRef(false);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 112)}px`;
  }, [c.body, editing]);
  useLayoutEffect(() => {
    if (!editing) return;
    before.current = { body: c.body, images: quote.images };
    done.current = false;
    const el = box.current;
    if (el) {
      el.focus({ preventScroll: true });
      el.setSelectionRange(el.value.length, el.value.length);
      // Reveal the editor inside the quote list without moving the conversation.
      const scroller = el.closest("[data-quotes]");
      if (scroller) {
        let top = 0;
        for (let node: HTMLElement | null = el; node && node !== scroller; node = node.offsetParent as HTMLElement | null) top += node.offsetTop;
        const bottom = top + el.offsetHeight;
        if (bottom > scroller.scrollTop + scroller.clientHeight) scroller.scrollTop = bottom - scroller.clientHeight;
        else if (top < scroller.scrollTop) scroller.scrollTop = top;
      }
    }
    // only when the writing begins
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);

  const finish = (cancel: boolean) => {
    if (done.current) return;
    done.current = true;
    if (cancel && before.current) {
      reads.current?.abort();
      reads.current = null;
      setPreparing(0);
      cancelNote(draftKey, id, before.current.body, before.current.images);
    } else setNote(draftKey, id, c.body, true);
    before.current = null;
  };
  const where = whereOf(c);
  const omitted = omittedOf(c);
  return (
    <div
      className="flex min-w-0 items-start gap-2 rounded-[10px] bg-inset py-1.5 pr-1 pl-2 shadow-hairline"
      aria-label={t("chat-comments.comment_n", { n })}
      data-comment-card={n}
      style={{ animation: `pop-in 180ms ${EASE} both` }}
    >
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-accent" />
      <div
        className="flex min-w-0 flex-1 flex-col gap-1 py-px"
        onBlur={(e) => {
          if (editing && !e.currentTarget.contains(e.relatedTarget)) finish(false);
        }}
        onKeyDown={(e) => {
          if (!editing || composing(e.nativeEvent)) return;
          if (e.key === "Enter" && e.target === box.current && !e.shiftKey) {
            e.preventDefault();
            finish(false);
          } else if (e.key === "Escape") {
            // Keep the page's Escape shortcut from stopping a running agent.
            e.preventDefault();
            e.stopPropagation();
            finish(true);
          }
        }}
      >
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
          {omitted > 0 && <span className="text-submeta text-ink-3">{t("chat-comments.omitted", { count: omitted })}</span>}
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
              onPaste={onAddImages && ((e) => {
                const images = Array.from(e.clipboardData.files ?? []).filter((file) => file.type.startsWith("image/"));
                if (!images.length) {
                  for (const item of Array.from(e.clipboardData.items ?? [])) {
                    if (item.kind !== "file") continue;
                    const file = item.getAsFile();
                    if (file?.type.startsWith("image/")) images.push(file);
                  }
                }
                if (!images.length) return;
                e.preventDefault();
                void addImages(images);
              })}
              className="w-full resize-none rounded-[8px] bg-surface px-2 py-1 text-meta leading-[1.5] text-ink shadow-hairline outline-none placeholder:text-ink-3 focus:shadow-[0_0_0_1.5px_var(--accent)]"
            />
            <div className="flex items-center gap-1 text-submeta text-ink-3">
              {onAddImages && (
                <>
                  <IconButton icon="image" label={t("chat-comments.add_images")} className="size-7" onClick={() => fileInput.current?.click()} />
                  <input ref={fileInput} type="file" accept="image/*" multiple hidden onChange={(e) => {
                    void addImages(Array.from(e.currentTarget.files ?? []));
                    e.currentTarget.value = "";
                  }} />
                </>
              )}
              <span className="min-w-0 truncate">{t("chat-comments.hint_enter")}</span>
              {c.body.length > MAX_COMMENT_CHARS * 0.8 && <span className="ml-auto tabular-nums">{t("chat-comments.counter", { n: c.body.length, max: MAX_COMMENT_CHARS })}</span>}
            </div>
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
        {preparing > 0 && <span role="status" className="text-submeta text-ink-3">{t("chat-comments.preparing_images")}</span>}
        <AttachmentTray attachments={previews} onRemove={(imageId) => setCommentImages(draftKey, id, (images) => images.filter((image) => image.id !== imageId))} />
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
