// Comments in the transcript (#2437, Moshi's "Comment"): selecting words shows a small "Comment" pill under the
// selection (the C key does the same), which puts the words above the input as a comment card to write the note in;
// while the comment waits there, its words stay lit in the transcript with the card's number beside them. The marks
// are drawn in the scroller's own content, so they scroll with it; they are measured again when the transcript
// re-renders or resizes (streaming, a fold opening), and go with the card when it is removed or sent.
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { anchorOf, onJumpToComment, rangeFor } from "../../lib/comment-marks";
import { textQuote, type TextAnchor } from "../../lib/comments";
import { addQuote, editNote, useQuotes } from "../../lib/drafts";
import { focusKind } from "../../lib/shortcuts";
import { cx, EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { scrollBehavior } from "../../ui/stream/motion";
import { LazyDraftCommentCard, NumberDot } from "./CommentCard";
import { useChatActions } from "./chat-actions";
import { t } from "../../i18n";
import { composing } from "../../lib/send-history";

interface Box {
  id: string;
  n: number;
  rects: { x: number; y: number; w: number; h: number }[];
}

interface HighlightBucket {
  base: Range[];
  pulsed: Range[];
}

const DOT = 16;
const FLASH_MS = 1400;
const HIGHLIGHT_NAME = "persona-comment";
const HIGHLIGHT_FLASH_NAME = "persona-comment-flash";

// CSS.highlights is document-global while CommentMarks mounts per chat scroller (e.g. main chat + SidePanel).
// Track each mounted instance's ranges separately and publish their union so panes never wipe each other.
const highlightBuckets = new Map<symbol, HighlightBucket>();

function supportsCustomHighlight(): boolean {
  return typeof CSS !== "undefined" && Boolean(CSS.highlights) && typeof Highlight !== "undefined";
}

function publishCustomHighlights(): void {
  if (!supportsCustomHighlight()) return;
  const base: Range[] = [];
  const pulsed: Range[] = [];
  for (const bucket of highlightBuckets.values()) {
    for (const r of bucket.base) base.push(r);
    for (const r of bucket.pulsed) pulsed.push(r);
  }
  if (base.length) CSS.highlights.set(HIGHLIGHT_NAME, new Highlight(...base));
  else CSS.highlights.delete(HIGHLIGHT_NAME);
  if (pulsed.length) CSS.highlights.set(HIGHLIGHT_FLASH_NAME, new Highlight(...pulsed));
  else CSS.highlights.delete(HIGHLIGHT_FLASH_NAME);
}

function setInstanceHighlights(owner: symbol, base: Range[], pulsed: Range[]): void {
  if (base.length || pulsed.length) {
    highlightBuckets.set(owner, { base, pulsed });
    publishCustomHighlights();
  } else if (highlightBuckets.delete(owner)) {
    publishCustomHighlights();
  }
}

function clearInstanceHighlights(owner: symbol): void {
  if (highlightBuckets.delete(owner)) publishCustomHighlights();
}

/** `jump`: brings words into view `topOffset` px from the top and keeps them there while the exchanges around them are
 *  drawn (ChatView landRow); without it, the scroller's own smooth scroll. */
export function CommentMarks({ scroller, jump }: { scroller: RefObject<HTMLDivElement | null>; jump?: (words: Range, topOffset: number) => void }) {
  const chat = useChatActions();
  const key = chat?.draftKey ?? "";
  const can = Boolean(chat?.canCompose);
  const { list } = useQuotes(key);

  // ── the pill ──
  const [pill, setPill] = useState<{ x: number; y: number } | null>(null);
  const picked = useRef<{ text: string; anchor: TextAnchor | null } | null>(null);
  const dragging = useRef(false);

  const read = useCallback(() => {
    const el = scroller.current;
    const sel = document.getSelection();
    const hide = () => {
      picked.current = null;
      setPill((p) => (p ? null : p));
    };
    if (!can || !el || !sel || sel.isCollapsed || sel.rangeCount === 0 || dragging.current) return hide();
    const range = sel.getRangeAt(0);
    const text = sel.toString().trim();
    if (!text || !el.contains(range.commonAncestorContainer)) return hide();
    const rects = range.getClientRects();
    const last = rects.length ? rects[rects.length - 1] : range.getBoundingClientRect();
    const bounds = el.getBoundingClientRect();
    // the end of the words is out of sight: nothing to hang the pill under
    if (last.bottom < bounds.top || last.bottom > bounds.bottom - 4) return hide();
    picked.current = { text, anchor: anchorOf(range) };
    const x = Math.min(Math.max(last.right - 36, 8), window.innerWidth - 112);
    const y = last.bottom + 6;
    setPill((p) => (p && p.x === x && p.y === y ? p : { x, y }));
  }, [can, scroller]);

  useEffect(() => {
    let frame = 0;
    const soon = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(read);
    };
    const down = (e: PointerEvent) => {
      if (e.target instanceof Node && scroller.current?.contains(e.target)) {
        dragging.current = true;
        picked.current = null;
        setPill(null);
      }
    };
    const up = () => {
      if (!dragging.current) return;
      dragging.current = false;
      soon();
    };
    const el = scroller.current;
    document.addEventListener("selectionchange", soon);
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("pointerup", up, true);
    document.addEventListener("pointercancel", up, true);
    el?.addEventListener("scroll", soon, { passive: true });
    window.addEventListener("resize", soon);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("selectionchange", soon);
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("pointerup", up, true);
      document.removeEventListener("pointercancel", up, true);
      el?.removeEventListener("scroll", soon);
      window.removeEventListener("resize", soon);
    };
  }, [read, scroller]);

  const add = useCallback(() => {
    const cur = picked.current;
    if (!cur || !key) return;
    addQuote(key, textQuote(cur.text, cur.anchor ?? undefined), true);
    document.getSelection()?.removeAllRanges();
    picked.current = null;
    setPill(null);
  }, [key]);

  // C, with words picked and the keyboard not in a field
  useEffect(() => {
    if (!pill) return;
    if (!LazyDraftCommentCard.ready()) void LazyDraftCommentCard.preload().catch(() => {});
    const press = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "c" || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || e.repeat || composing(e)) return;
      if (focusKind(e.target) !== "page") return;
      e.preventDefault();
      add();
    };
    window.addEventListener("keydown", press);
    return () => window.removeEventListener("keydown", press);
  }, [pill, add]);

  // ── the lit words ──
  const marks = useMemo(() => {
    let n = 0;
    const out: { id: string; n: number; anchor: TextAnchor }[] = [];
    for (const q of list) {
      if (!q.comment) continue;
      n++;
      if (q.comment.anchor && q.id) out.push({ id: q.id, n, anchor: q.comment.anchor });
    }
    return out;
  }, [list]);
  const [boxes, setBoxes] = useState<Box[]>([]);
  const boxesNow = useRef(boxes);
  boxesNow.current = boxes;
  const [flash, setFlash] = useState<string | null>(null);
  const flashNow = useRef(flash);
  flashNow.current = flash;
  const flashTimer = useRef<number | undefined>(undefined);
  const nativeHighlight = useMemo(() => supportsCustomHighlight(), []);
  const [highlightOwner] = useState(() => Symbol());
  const rangesNow = useRef(new Map<string, Range>());

  const syncCustomHighlights = useCallback(
    (activeFlash: string | null) => {
      if (!nativeHighlight) return;
      const base: Range[] = [];
      const pulsed: Range[] = [];
      for (const [id, range] of rangesNow.current) {
        if (id === activeFlash) pulsed.push(range);
        else base.push(range);
      }
      setInstanceHighlights(highlightOwner, base, pulsed);
    },
    [highlightOwner, nativeHighlight],
  );

  const measure = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const base = el.getBoundingClientRect();
    const out: Box[] = [];
    const nextRanges = new Map<string, Range>();
    for (const m of marks) {
      const range = rangeFor(el, m.anchor);
      if (!range) continue;
      nextRanges.set(m.id, range);
      const rects = [...range.getClientRects()]
        .filter((r) => r.width > 0 && r.height > 0)
        .map((r) => ({ x: Math.round(r.left - base.left + el.scrollLeft), y: Math.round(r.top - base.top + el.scrollTop), w: Math.round(r.width), h: Math.round(r.height) }));
      if (rects.length) out.push({ id: m.id, n: m.n, rects });
    }
    rangesNow.current = nextRanges;
    syncCustomHighlights(flashNow.current);
    setBoxes((prev) => (JSON.stringify(prev) === JSON.stringify(out) ? prev : out));
  }, [marks, scroller, syncCustomHighlights]);

  useEffect(() => {
    syncCustomHighlights(flash);
  }, [flash, syncCustomHighlights]);

  useEffect(() => {
    return () => {
      window.clearTimeout(flashTimer.current);
      flashTimer.current = undefined;
      rangesNow.current = new Map();
      if (nativeHighlight) clearInstanceHighlights(highlightOwner);
    };
  }, [highlightOwner, nativeHighlight]);

  // Passive effect so ancestor refs (ChatView's ref={scrollRef} wrapping CommentMarks) are attached before measuring.
  useEffect(() => {
    if (!marks.length) {
      rangesNow.current = new Map();
      window.clearTimeout(flashTimer.current);
      flashTimer.current = undefined;
      setFlash((f) => (f ? null : f));
      syncCustomHighlights(null);
      setBoxes((prev) => (prev.length ? [] : prev));
      return;
    }
    measure();
    const el = scroller.current;
    const content = el?.firstElementChild;
    if (!el || !content) return;
    let frame = 0;
    const soon = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(measure);
    };
    const mutations = new MutationObserver(soon);
    mutations.observe(content, { childList: true, subtree: true, characterData: true });
    const sizes = new ResizeObserver(soon);
    sizes.observe(content);
    sizes.observe(el);
    return () => {
      cancelAnimationFrame(frame);
      mutations.disconnect();
      sizes.disconnect();
    };
  }, [marks, measure, scroller, syncCustomHighlights]);

  // the card's click: scroll to the words and pulse them
  const marksNow = useRef(marks);
  marksNow.current = marks;
  const jumpNow = useRef(jump);
  jumpNow.current = jump;
  useEffect(
    () =>
      onJumpToComment((id) => {
        const box = boxesNow.current.find((b) => b.id === id);
        const el = scroller.current;
        if (!box || !el) return false;
        const anchor = marksNow.current.find((m) => m.id === id)?.anchor;
        const jumpFn = jumpNow.current;
        const words = jumpFn && anchor ? rangeFor(el, anchor) : null;
        if (words && jumpFn) jumpFn(words, -el.clientHeight / 3);
        else el.scrollTo({ top: Math.max(0, box.rects[0].y - el.clientHeight / 3), behavior: scrollBehavior() });
        window.clearTimeout(flashTimer.current);
        setFlash(id);
        flashTimer.current = window.setTimeout(() => {
          flashTimer.current = undefined;
          setFlash((f) => (f === id ? null : f));
        }, FLASH_MS);
        return true;
      }),
    [scroller],
  );

  return (
    <>
      {boxes.length > 0 && (
        <div className="pointer-events-none absolute top-0 left-0 h-0 w-full" data-comment-marks>
          {boxes.map((b) => {
            const last = b.rects[b.rects.length - 1];
            return (
              <div key={b.id} data-comment-mark={b.n}>
                {!nativeHighlight &&
                  b.rects.map((r, i) => (
                    <span
                      key={i}
                      className={cx("absolute rounded-[3px] bg-accent/20 transition-shadow duration-300", flash === b.id && "shadow-[0_0_0_2px_var(--accent)]")}
                      style={{ left: r.x - 1, top: r.y, width: r.w + 2, height: r.h }}
                    />
                  ))}
                <button
                  type="button"
                  tabIndex={-1}
                  aria-label={t("chat-comments.comment_n", { n: b.n })}
                  onClick={() => key && editNote(key, b.id)}
                  className="pointer-events-auto absolute flex cursor-pointer items-center justify-center rounded-full"
                  // like a footnote mark: after the words, a little above their line
                  style={{ left: last.x + last.w - 2, top: last.y - DOT / 2, width: DOT, height: DOT }}
                >
                  <NumberDot n={b.n} />
                </button>
              </div>
            );
          })}
        </div>
      )}
      {pill &&
        createPortal(
          <button
            type="button"
            data-comment-pill
            aria-label={t("chat-comments.pill_label")}
            onPointerDown={(e) => e.preventDefault()}
            onMouseDown={(e) => e.preventDefault()}
            onClick={add}
            className="fixed z-40 flex h-8 items-center gap-1.5 rounded-full bg-tooltip-bg pr-2 pl-3 text-[12.5px] font-medium whitespace-nowrap text-tooltip-fg shadow-overlay"
            style={{ left: pill.x, top: pill.y, animation: `pop-in 140ms ${EASE} both` }}
          >
            <Icon name="chat" size={13} />
            {t("chat-comments.pill")}
            <kbd className="rounded-[4px] border border-tooltip-border px-1 font-sans text-[10.5px] leading-[16px] text-tooltip-muted">C</kbd>
          </button>,
          document.body,
        )}
    </>
  );
}
