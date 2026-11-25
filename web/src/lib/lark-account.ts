// The signed-in Feishu user of each account (#13404): what the Feishu chat view (views/inbox/LarkThread.tsx) sends an
// add-Pin write with. The Messages page's lists (the lark module) say it as they read; otherwise the hub is asked.
import { t } from "../i18n";
import { api, ApiError } from "./api";
import type { LarkConversationPage } from "./lark-history";
import { larkPath } from "./lark-path";
import { itemHeaders } from "./manager-choice";

const known = new Map<number | undefined, string>();

/** What a list of account `ol` (undefined: the hub's own) last said its account is; undefined forgets it. */
export function rememberLarkAccount(ol: number | undefined, account: string | undefined): void {
  if (account) known.set(ol, account);
  else known.delete(ol);
}

/** The signed-in Feishu user of account `ol` as the hub last said (an add-Pin write carries it); another manager's
 *  (an inbox item's, docs/assistant/managers.md "Clients") is asked of it, as the Messages page's lists are the default's. */
export async function larkAccount(ol?: number, manager?: string): Promise<string> {
  const kept = itemHeaders(manager) ? undefined : known.get(ol);
  if (kept) return kept;
  const p = await api<LarkConversationPage>(larkPath("/lark/conversations?scope=chats&limit=1&account_bound=1", ol, manager));
  if (!p.account_id) throw new ApiError(426, t("lib-lark-pins.hub_upgrade"), { code: "lark_hub_upgrade_required" });
  return p.account_id;
}
