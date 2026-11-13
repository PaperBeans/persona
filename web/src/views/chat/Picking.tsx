// Picking messages to forward at once (#3838; iOS PickRow / PickBar, and Inbox's Select, views/inbox/InboxList.tsx): a
// checkbox over each message with a Forward (PickRow, drawn by MessageActions in the row's first place) and the bar in the
// composer's place (PickBar): Cancel, how many are picked, and Forward N, off while none is; Escape cancels too, and
// Cancel leaves the chat as it was, the composer back with its draft. A pick past FORWARD_MOST is refused with a toast
// from the bar. A chunk that comes with Forward to: its Select messages… hands these to the chat (picking-parts.ts) as it
// starts picking, so nothing of it is on the first screen.
import { useEffect, useRef, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { forwardDraftOf, forwardSummary, openForward, type ForwardChat, type ForwardPick } from "../../lib/forward";
import { FORWARD_MOST, type MessagePick } from "../../lib/message-pick";
import { Button, cx, toast } from "../../ui/atoms";
import { CheckMark } from "../../ui/CheckMark";

export interface PickRowProps {
  pick: MessagePick;
  /** The message, as Forward would forward it. */
  message: ForwardPick;
  /** Your own bubble: the tick goes before it, outside it (its row is the bubble's width). */
  side?: boolean;
}

/** Over a message while the chat picks: a transparent checkbox the size of the message (what is under it is inert),
 *  the tick before it, and a tint around it on hover and once picked, as Inbox's picked cards. Its message's row is
 *  `relative isolate` (and leaves the tick room unless `side`). */
export function PickRow({ pick, message, side }: PickRowProps) {
  const read = () => pick.has(message.itemId);
  const on = useSyncExternalStore(pick.subscribe, read, read);
  return (
    <>
      <button
        type="button"
        role="checkbox"
        aria-checked={on}
        aria-label={forwardSummary(message.text).replace(/\n/g, " ").slice(0, 120)}
        onClick={() => void pick.toggle(message)}
        className="peer absolute -inset-1.5 z-10 cursor-pointer rounded-[16px]"
        data-pick={on ? "on" : "off"}
      >
        <CheckMark on={on} className={cx("absolute", side ? "top-5 right-full mr-1.5" : "top-2.5 left-1.5")} />
      </button>
      <span aria-hidden className={cx("pointer-events-none absolute -inset-1.5 -z-10 rounded-[16px] transition-colors duration-150", on ? "bg-accent-tint" : "[@media(hover:hover)]:peer-hover:bg-hover")} />
    </>
  );
}

/** `picks` in the chat's order (several.json "three_then_a_note": the blocks go as the chat has them, not as they were
 *  picked): by where each message is in `items`, the transcript; one it no longer holds goes after, as it was picked. */
export function inChatOrder(picks: readonly ForwardPick[], items: readonly { id: string }[]): ForwardPick[] {
  const at = new Map<string, number>();
  items.forEach((it, i) => {
    if (!at.has(it.id)) at.set(it.id, i);
  });
  const place = (p: ForwardPick) => at.get(p.itemId) ?? Number.MAX_SAFE_INTEGER;
  return picks
    .map((p, i) => ({ p, i }))
    .sort((a, b) => place(a.p) - place(b.p) || a.i - b.i)
    .map((x) => x.p);
}

/** Forward N: Forward to with the picks as drafts, in the chat's order; the chat drops them once they went. */
function forward(from: ForwardChat, picks: ForwardPick[], sent: () => void) {
  const ordered = inChatOrder(picks, from.items);
  if (ordered.length) openForward({ drafts: ordered.map((p) => forwardDraftOf(from, p)), sent });
}

export interface PickBarProps {
  pick: MessagePick;
  /** The chat they are picked in, as it is when Forward is pressed (its transcript gives their order). */
  chat: () => ForwardChat;
  /** Cancel, Escape, or the picks went: the chat drops them. */
  onEnd: () => void;
}

/** The bar in the composer's place while the chat picks: Cancel, "2 selected", and Forward 2 (off with none), which
 *  opens Forward to with them in the chat's order. */
export function PickBar({ pick, chat, onEnd }: PickBarProps) {
  const count = useSyncExternalStore(pick.subscribe, pick.count, pick.count);
  const refusals = useSyncExternalStore(pick.subscribe, pick.refusals, pick.refusals);
  const told = useRef(refusals);
  useEffect(() => {
    if (refusals === told.current) return;
    told.current = refusals;
    toast(t("chat-forwardto.at_most", { count: FORWARD_MOST }), "error");
  }, [refusals]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // a dialog or menu over the chat takes its own Escape
      if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("[role=dialog], [role=menu]")) return;
      onEnd();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onEnd]);
  return (
    <div role="toolbar" aria-label={t("chat-forwardto.selection")} data-pick-bar className="flex shrink-0 items-center gap-2 rounded-[14px] bg-surface p-2 shadow-card">
      <Button tone="quiet" onClick={onEnd} testId="pick-cancel">
        {t("common.cancel")}
      </Button>
      <span className="min-w-0 flex-1 truncate text-center text-xs text-ink-3 tabular-nums" aria-live="polite" data-pick-count>
        {t("chat-forwardto.selected", { count })}
      </span>
      <Button tone="primary" icon="forward" disabled={!count} onClick={() => forward(chat(), pick.picks(), onEnd)} testId="pick-forward">
        {t("chat-forwardto.forward_count", { count })}
      </Button>
    </div>
  );
}
