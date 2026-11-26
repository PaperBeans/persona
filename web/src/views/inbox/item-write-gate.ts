import type { InboxItem } from "../../lib/inbox";
import { mailItemReadOnly, mailReading, type MailReading } from "../../lib/mail";
import { useStore } from "../../lib/store";
import { useLarkItemReadOnly } from "./lark-write-gate";

/** How mail is read on this hub (lib/mail.ts mailReading), following the hub's hello. */
export function useMailReading(): MailReading {
  return useStore((s) => mailReading(s));
}

/** Whether a reply to `item` cannot be sent because the hub turned off the module that sends it: Lark's for a Lark
 *  item (lark-write-gate), mail's for a mail (#15995). What the item already holds stays readable; only the controls
 *  that would send are left out. */
export function useItemReadOnly(item?: InboxItem): boolean {
  const lark = useLarkItemReadOnly(item);
  return mailItemReadOnly(useMailReading(), item) || lark;
}
