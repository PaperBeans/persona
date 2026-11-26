// A Persona conversation's page (`/persona/<id>`). With several assistants the conversation may be another assistant's
// than the current one (an inbox item's hand-off, a link, the palette, a to-do's origin), and the page must open it on
// the computer it runs on: what this tab noted at a hand-off (lib/outbox.ts adoptConversation) is gone after a reload
// and in a new tab, and a hub with the paged sessions list pushes no rows to look it up in. So the address names the
// computer (`?machine=`) and, when it is another manager's than the default, the manager (`?manager=`); the page asks the
// hub for that exact row, which the rest (its manager's offline state, its plan buttons) then reads (docs/web/web.md).
// One assistant: the plain address, as before.
import { chosenManager, defaultManager, otherManager } from "./manager-choice";
import { currentRoster, multiManager } from "./managers";
import type { Route } from "./router";
import type { AssistantInfo, Session } from "./types";

/** The page of conversation sid, on computer mid, of manager (none: the default's). Without the computer (a to-do names
 *  only its origin conversation) the manager is named even when it is the default: the page reads where its assistant
 *  runs. */
export function conversationPage(sid: string, mid?: string, manager?: string, at?: string, nameMachine?: boolean): Route {
  const msg = at ? { at } : {};
  if (!nameMachine && !multiManager(currentRoster())) return { name: "persona", sid, ...msg };
  if (!mid) return { name: "persona", sid, manager: manager || defaultManager(), ...msg };
  const other = otherManager(manager);
  return { name: "persona", sid, mid, ...(other ? { manager: other } : {}), ...msg };
}

/** Inbox handling chats can be absent from the assistant's list and can run on a separate computer. */
export function inboxConversationPage(sid: string, mid: string, manager: string | undefined, assistantMachine: string | undefined): Route {
  return conversationPage(sid, mid, manager, undefined, mid !== assistantMachine || Boolean(otherManager(manager)));
}

/** A conversation's page: the assistant's are in Persona (/persona/<sid>, naming its computer and manager with several
 *  assistants), an agent's is /s/<machine>/<sid>. Use a session summary when that's where the caller found the row. */
export function conversationRoute(mid: string, sid: string, sessions: Record<string, Session[]>, assistant: AssistantInfo | null, row = sessions[mid]?.find((x) => x.session_id === sid)): Route {
  if (row?.assistant && row.space) return spacePage(row.space, sid);
  const own = assistant?.machine_id === mid && assistant.conversations.some((c) => c.session_id === sid);
  if (!row?.assistant && !own) return { name: "session", mid, sid };
  const owner = row?.manager ?? (own ? chosenManager() ?? undefined : undefined);
  return row?.origin === "inbox" ? inboxConversationPage(sid, mid, owner, assistant?.machine_id) : conversationPage(sid, mid, owner);
}

/** A space's conversation's page: its track's `/tracks/<slug>/<sid>` (the health space's too, which the health module's
 *  track page opens), where its plan cards, questions and tasks show (the bare `/s/…` chat is an agent's and shows none
 *  of them). */
export function spacePage(space: string, sid: string): Route {
  return { name: "track", slug: space, sid };
}
