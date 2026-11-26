// A comment on picked words or on lines of code (#2437, Moshi's "Comment"), as a card in the quote list above the
// composer (DraftCommentCard.tsx: a numbered header, the words or the code, the note to write, a click goes back to the spot) and
// as a card in a sent bubble (CommentBlock). Same family as the quote bar next to it (Quote.tsx): one accent rule, the
// same fill; the number is the one the spot carries in the transcript (CommentMarks.tsx) and the one the agent reads.
import { jumpToComment } from "../../lib/comment-marks";
import { codePreview, diffLabel, openSpot, rangeLabel, type CodeSpot, type ElementSpot } from "../../lib/comments";
import { openPanel } from "../../lib/panel";
import { addressOf } from "../../lib/preview";
import type { DraftQuote } from "../../lib/drafts";
import { cx, toast } from "../../ui/atoms";
import { t } from "../../i18n";

/** `staged`, `unstaged`, `commit 1a2b3c4d`, worded for the reader. */
export function diffName(d: NonNullable<CodeSpot["diff"]>): string {
  if (d.kind === "review") return d.scope === "uncommitted" ? t("chat-comments.review_uncommitted") : t(d.scope === "branch" ? "chat-comments.review_branch" : "chat-comments.review_since", { sha: d.base });
  return d.kind === "commit" ? t("chat-comments.commit", { sha: diffLabel(d).slice(7) }) : t(d.kind === "staged" ? "chat-comments.staged" : "chat-comments.unstaged");
}

/** `src/app.ts:12-14 · staged`, worded for the reader. */
export function spotWhere(s: CodeSpot): string {
  return [`${s.file}:${rangeLabel(s.from, s.to)}`, s.old ? t("chat-comments.side_old") : "", s.diff ? diffName(s.diff) : ""].filter(Boolean).join(" · ");
}

/** `button.save · /settings`: an element comment's element and the page it is on. */
export function elementWhere(e: ElementSpot): string {
  return [e.selector, e.port ? addressOf(e.url, e.port) : e.url].join(" · ");
}

/** Where a comment points, worded for the reader: a file's lines, or an element of a preview. */
export function whereOf(c: NonNullable<DraftQuote["comment"]>): string {
  return c.element ? elementWhere(c.element) : c.spot ? spotWhere(c.spot) : "";
}

/** Lines of code (or of an element's HTML) a comment carries beyond the ones sent. */
export function omittedOf(c: NonNullable<DraftQuote["comment"]>): number {
  return c.element?.omitted ?? c.spot?.omitted ?? 0;
}

/** The round number a comment carries, in the card and next to its words in the transcript. */
export function NumberDot({ n, className }: { n: number; className?: string }) {
  return (
    <span aria-hidden className={cx("flex size-4 shrink-0 items-center justify-center rounded-full bg-accent text-[10px] leading-none font-semibold text-white tabular-nums", className)}>
      {n}
    </span>
  );
}

/** The words or the lines a comment is about: two lines in the card, code in mono, one line of it each. */
export function Excerpt({ q, lines }: { q: DraftQuote; lines: number }) {
  const spot = q.comment?.spot ?? q.comment?.element;
  const text = q.text.trim();
  if (!text) return null;
  if (spot) {
    return (
      <span className="flex min-w-0 flex-col font-mono text-[12px] leading-[1.5] text-ink-2" data-comment-excerpt>
        {codePreview(text, lines).map((l, i) => (
          <span key={i} className="truncate whitespace-pre">
            {l || " "}
          </span>
        ))}
      </span>
    );
  }
  return (
    <span className={cx(lines === 3 ? "line-clamp-3" : "line-clamp-2", "text-[12.5px] leading-[1.5] text-ink-2 [overflow-wrap:anywhere]")} data-comment-excerpt>
      {text}
    </span>
  );
}

export interface CommentPreviewTarget {
  mid: string;
  sid: string;
  cwd: string;
  port: number;
  path: string;
  element: ElementSpot;
}

export interface CommentNavigationHandlers {
  /** Opens a preview panel for an element comment instead of the default side-panel singleton. */
  openPreview?: (target: CommentPreviewTarget) => boolean | void;
  /** Opens a code or diff spot instead of the default `openSpot` singleton. */
  openSpot?: (spot: CodeSpot) => boolean;
  /** Scrolls to a text comment in the transcript instead of the default `jumpToComment` singleton. */
  jumpToText?: (id: string) => boolean;
  /** Reports a jump-unavailable notice instead of the default `toast` singleton. */
  onNotice?: (message: string) => void;
}

/** Goes back to where the comment was made: the words in the transcript, or the file / diff at its lines. */
export function goToComment(q: DraftQuote, handlers?: CommentNavigationHandlers): boolean {
  const c = q.comment;
  if (!c) return false;
  const notice = handlers?.onNotice ?? toast;
  if (c.element) {
    const e = c.element;
    // the preview of that session, at the page the element was on
    if (!e.mid || !e.sid || !e.cwd || !e.port) {
      notice(t("chat-comments.jump_code_gone"));
      return false;
    }
    const rawPath = addressOf(e.url, e.port);
    const path = rawPath.startsWith("/") ? rawPath : "/";
    if (handlers?.openPreview) {
      const ok = handlers.openPreview({ mid: e.mid, sid: e.sid, cwd: e.cwd, port: e.port, path, element: e }) !== false;
      if (!ok) notice(t("chat-comments.jump_code_gone"));
      return ok;
    }
    openPanel({ kind: "preview", mid: e.mid, sid: e.sid, cwd: e.cwd, port: e.port, path });
    return true;
  }
  if (c.spot) {
    const ok = handlers?.openSpot ? handlers.openSpot(c.spot) : openSpot(c.spot);
    if (!ok) notice(t("chat-comments.jump_code_gone"));
    return ok;
  }
  if (q.id && (handlers?.jumpToText ?? jumpToComment)(q.id)) return true;
  notice(t("chat-comments.jump_text_gone"));
  return false;
}

export interface CommentBlockProps {
  quote: DraftQuote;
  n: number;
  /** Explicit click handler for jumping back to the commented spot. */
  open?: () => void;
  /** Controlled navigation handlers used when `open` is omitted. */
  navigation?: CommentNavigationHandlers;
}

/** A comment in a sent bubble: its number and where, the words or the code, the note. The code one opens its file or
 *  diff when the session's folder is known (`cwd`). */
export function CommentBlock({ quote, n, open, navigation }: CommentBlockProps) {
  const c = quote.comment!;
  const where = whereOf(c);
  const onOpen = open ?? (navigation ? () => { goToComment(quote, navigation); } : undefined);
  const head = (
    <>
      <NumberDot n={n} />
      <span className="shrink-0">{t("chat-comments.comment_n", { n })}</span>
    </>
  );
  const headClass = "flex max-w-full min-w-0 items-center gap-1.5 text-[12px] font-medium whitespace-nowrap text-ink-3";
  return (
    <div className="flex min-w-0 gap-2 rounded-[8px] bg-ink/[0.05] py-1.5 pr-2.5 pl-2 whitespace-normal" data-quote data-comment-block={n}>
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-accent/60" />
      <div className="flex min-w-0 flex-1 flex-col items-start gap-1">
        {onOpen ? (
          <button type="button" onClick={onOpen} title={t("chat-comments.jump")} className={cx(headClass, "rounded-[6px] transition-colors hover:bg-hover-2")}>
            {head}
          </button>
        ) : (
          <span className={headClass}>{head}</span>
        )}
        {where && <span className="max-w-full truncate font-mono text-[11px] text-ink-3">{where}</span>}
        <Excerpt q={quote} lines={3} />
        {omittedOf(c) > 0 && <span className="text-[11px] text-ink-3">{t("chat-comments.omitted", { count: omittedOf(c) })}</span>}
        {c.body && <span className="text-[13px] leading-[1.55] whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{c.body}</span>}
      </div>
    </div>
  );
}
