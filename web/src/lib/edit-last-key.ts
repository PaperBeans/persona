// ↑ in an empty composer edits your last message (#4503, as in Slack), and so do Esc twice and the safeguards card's
// Edit last message (#2945): all three start the same edit (ChatView.editLastMessage), whose conversation-only rewind runs
// when the edit is sent, so starting or cancelling one changes nothing on the computer. These are the composer's key
// decisions, pure, so they are tested without a DOM.
import { composing, type KeyLike } from "./send-history";

/** How long an edit's send waits after the rewind (#4605): Claude puts the old prompt back into its input box a moment
 *  after the node's rewind has answered, and a message typed before that lands glued after it. Live, the prompt was
 *  back within about 0.3 s. The send also asks the node to clear exactly that prompt first (`clear_draft`). */
export const EDIT_SEND_SETTLE_MS = 1500;

/** What the composer is showing when a key comes. */
export interface EditLastBox {
  /** Edit last message is on offer: an idle Claude session whose last prompt can be edited (ChatView). */
  offered: boolean;
  /** Sending is off or held: disabled, blocked, a rewind running, the agent working or waiting on a card. */
  busy: boolean;
  /** Something waits to go: any text (spaces too), a quote, a picture, a context, a file uploading. */
  holds: boolean;
  /** One of the box's menus (/ commands, @ mentions), the Ctrl+R search, or a dialog or menu of the page is open. */
  menu: boolean;
}

export interface EditKey extends KeyLike {
  repeat?: boolean;
}

/** A plain, fresh press of `key`: no modifier, not held down, not a key an input method is using. */
const plain = (e: EditKey, key: string) => e.key === key && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && !e.repeat && !composing(e);

/** The box may start editing the last message now: it is on offer, nothing is in the way, and nothing would be lost. */
export function canStartEdit(b: EditLastBox): boolean {
  return b.offered && !b.busy && !b.holds && !b.menu;
}

/** ↑ starts editing. Anywhere else ↑ keeps its old job: moving the caret, a menu's rows, the #2434 history. */
export function upStartsEdit(e: EditKey, b: EditLastBox): boolean {
  return plain(e, "ArrowUp") && canStartEdit(b);
}

/** This Esc counts toward Esc Esc (#2945), under the same rules as ↑. */
export function escapeCountsForEdit(e: EditKey, b: EditLastBox): boolean {
  return plain(e, "Escape") && canStartEdit(b);
}

/** Esc while editing cancels the edit. The box's own menus have taken their Esc before this is asked. */
export function escapeCancelsEdit(e: EditKey, editing: boolean): boolean {
  return editing && plain(e, "Escape");
}
