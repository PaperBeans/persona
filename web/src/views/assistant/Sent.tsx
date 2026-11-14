// What Persona sent to its tasks' agents (`routed` items it wrote, #1542, docs/assistant/unified-entry.md "Messages to
// agents"): never the user's bubble. A message on its own is one folded line on the left (SentLine); a task's card lists
// the hand-off sent together with a task's card is not drawn on its own (lib/feed takenIn).
import { useState } from "react";
import { field, firstLine, type FeedItem } from "../../lib/feed";
import { Collapse, copyText, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** "Sent to “task”", or "Stopped “task” and sent" when Persona cut in first. */
export function sentTitle(item: FeedItem, title: string): string {
  return item.body?.interrupted === true ? t("assistant-tasks.sent_interrupting", { title }) : t("assistant-tasks.sent_to", { title });
}

/** One of Persona's messages to a task's agent in the conversation: a capsule like the answered and review lines, with its
 *  mark (a stop when Persona cut in first), where it went and the first line. A click opens the whole text, selectable,
 *  with Copy, and again folds it; the task's name opens the task. */
export function SentLine({ item, title, onOpenTask }: { item: FeedItem; title: string; onOpenTask?: () => void }) {
  const [open, setOpen] = useState(false);
  const text = field(item, "text") ?? "";
  const first = firstLine(text);
  const label = sentTitle(item, title);
  return (
    <div data-testid="sent-line" className={cx("flex max-w-full flex-col self-start bg-field text-[12px] text-ink-2", open ? "w-full rounded-card" : "rounded-full")}>
      <div className="flex min-w-0 items-center gap-1.5 px-3 py-1.5">
        <Icon name={item.body?.interrupted === true ? "stop" : "send"} size={12} className="shrink-0" />
        {onOpenTask ? (
          <button type="button" onClick={onOpenTask} className="max-w-[45%] shrink-0 truncate text-left hover:text-ink hover:underline">
            {label}
          </button>
        ) : (
          <span className="max-w-[45%] shrink-0 truncate">{label}</span>
        )}
        <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 text-left">
          <span className="min-w-0 flex-1 truncate">{first && `· ${first}`}</span>
          <Icon name="chevronRight" size={12} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : "none" }} />
        </button>
      </div>
      <Collapse open={open} lazy>
        <SentText text={text} className="px-3 pt-0.5 pb-2.5" />
      </Collapse>
    </div>
  );
}

/** The whole text Persona sent, newlines kept and selectable, with Copy under it. */
function SentText({ text, className }: { text: string; className?: string }) {
  return (
    <div data-testid="sent-detail" className={cx("flex flex-col gap-2", className)}>
      <div className="whitespace-pre-wrap text-[13px] leading-[1.55] text-ink select-text [overflow-wrap:anywhere]">{text}</div>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          void copyText(text);
        }}
        className="inline-flex items-center gap-1 self-start whitespace-nowrap text-[12px] font-semibold text-accent hover:underline"
      >
        <Icon name="copy" size={12} />
        {t("assistant-tasks.copy")}
      </button>
    </div>
  );
}
