// The conversations opened in this browser, newest first, by when they were opened rather than by their last activity
// (#2191): ⌘K lists them first, its first row (the one before the conversation on the page) already picked, so ⌘K ⏎ goes
// back to it; "Go back to the previous conversation" does the same from the commands. The rules are the ones iOS follows
// (spec/fixtures/recents/README.md, checked against recent_conversations.json). Only keys are kept, never a title or a
// message; in this browser, for this hub (the page is the hub's own, so its localStorage is too); never sent anywhere.
import { useSyncExternalStore } from "react";
import { BASE } from "./base";
import { removeStoredValue, storedValue, storeValue } from "./compatibility";

export const MAX_RECENT_CHATS = 10;
/** How many ⌘K lists. */
export const PALETTE_RECENT_CHATS = 5;

/** An Agent's conversation, keyed as ⌘K keys its rows. */
export const agentChatKey = (machine: string, session: string) => `s:${machine}/${session}`;
/** One of Persona's conversations. */
export const personaChatKey = (session: string) => `j:${session}`;

/** The conversation shown goes first; one already there moves (no second entry); the ten newest stay. */
export function openChat(before: string[], key: string): string[] {
  if (!key) return before;
  return [key, ...before.filter((k) => k !== key)].slice(0, MAX_RECENT_CHATS);
}

/** The entries to offer: those that still exist, without the one on the page, in order, at most `limit`. */
export function shownChats(entries: string[], current: string, exists: (key: string) => boolean, limit: number): string[] {
  return entries.filter((k) => k !== current && exists(k)).slice(0, limit);
}

// ── this browser's list ─────────────────────────────────

/** One list per hub: a hub served under a sub-path of an origin another one shares keeps its own. */
export const recentChatsKey = (base = BASE) => (base ? `persona.recentChats.${base}` : "persona.recentChats");

export function loadRecentChats(store: Storage = localStorage): string[] {
  try {
    const raw = storedValue(recentChatsKey(), store);
    const list: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(list) ? list.filter((k): k is string => typeof k === "string" && /^[sj]:./.test(k)).slice(0, MAX_RECENT_CHATS) : [];
  } catch {
    return [];
  }
}

type Snapshot = { entries: string[]; current: string };
let snapshot: Snapshot | null = null;
const listeners = new Set<() => void>();

function read(): Snapshot {
  snapshot ??= { entries: loadRecentChats(), current: "" };
  return snapshot;
}

function publish(next: Snapshot) {
  snapshot = next;
  for (const l of listeners) l();
}

/**
 * The page now shows `key` (an Agent's or a Persona conversation), or no conversation (""): an opened one goes first
 * in the list and becomes the current one, which ⌘K leaves out.
 */
export function noteChatShown(key: string, store: Storage = localStorage) {
  const now = read();
  if (!key) {
    if (now.current) publish({ ...now, current: "" });
    return;
  }
  if (now.current === key && now.entries[0] === key) return;
  const entries = openChat(now.entries, key);
  try {
    if (entries.length) storeValue(recentChatsKey(), JSON.stringify(entries), store);
    else removeStoredValue(recentChatsKey(), store);
  } catch {
    // storage full or blocked: the list is a convenience
  }
  publish({ entries, current: key });
}

/** The list and the conversation on the page, kept up to date. */
export function useRecentChats(): Snapshot {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    read,
    read,
  );
}

/** Tests only: forget what was read. */
export function resetRecentChatsForTests() {
  snapshot = null;
}
