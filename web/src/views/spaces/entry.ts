// Where entering a space lands (#4329, docs/assistant/health.md "Entering Health"; the health module and domain spaces):
// like Codex, back in the latest conversation with its context; a space with none opens the new conversation page.
// Pure, so it is tested without a DOM.
import type { Session } from "../../lib/types";
import { newestFirst } from "../assistant/conversations";

export type SpaceEntry = { kind: "wait" } | { kind: "open"; sid: string } | { kind: "new" };

/** `list`: the space's conversations as read (null: not read yet); `error`: why they could not be read. The newest open
 *  one by last activity (`updated_at`, the order the list shows); none, or a list that could not be read, the new
 *  conversation page, which says why the list is missing. */
export function spaceEntry(list: readonly Session[] | null, error = ""): SpaceEntry {
  if (list === null) return error ? { kind: "new" } : { kind: "wait" };
  const latest = newestFirst(list.filter((s) => !s.archived))[0];
  return latest ? { kind: "open", sid: latest.session_id } : { kind: "new" };
}
