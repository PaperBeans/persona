// What each leader conversation's direction is called (docs/assistant/unified-entry.md "Leader conversations"): the
// sidebar's Directions group shows that name, not the conversation's title. `GET /api/v1/assistant` lists only the
// leaders' ids (`assistant.leaders`); the names come from `GET /api/v1/assistant/leaders` (#3519, #3695), which the hub
// works out (the name given, else the project's, else the title without its "MMDD | kind | " prefix).
// Asked again whenever the leaders or their titles change; until it answers, or on a hub that cannot, a row shows its title.
import { useEffect, useSyncExternalStore } from "react";
import { api } from "./api";

export const LEADERS_PATH = "/assistant/leaders";

export interface LeaderInfo {
  id: string;
  title?: string;
  name?: string;
  project?: string;
  ended?: boolean;
}

/** The names of the current leaders, by conversation id (ended ones and blank names left out). */
export function directionNames(list: readonly LeaderInfo[] | undefined): Map<string, string> {
  const out = new Map<string, string>();
  for (const l of list ?? []) {
    const name = l.name?.trim();
    if (l.id && name && !l.ended) out.set(l.id, name);
  }
  return out;
}

/** What a leader row says: its direction's name, else the conversation's own title. */
export function directionLabel(names: ReadonlyMap<string, string>, id: string, title: string): string {
  return names.get(id) ?? title;
}

const listeners = new Set<() => void>();
let names: Map<string, string> = new Map();
let askedFor = "";
let asked = 0;

function set(next: Map<string, string>) {
  names = next;
  listeners.forEach((l) => l());
}

/** Asks the hub for the names when `key` (the leaders and their titles) differs from the last ask. */
function ask(key: string) {
  if (key === askedFor) return;
  askedFor = key;
  const n = ++asked;
  if (!key) return set(new Map());
  api<{ leaders?: LeaderInfo[] }>(LEADERS_PATH).then(
    (res) => n === asked && set(directionNames(res.leaders)),
    // an older hub or a failed read: the rows keep their titles; the next change asks again
    () => n === asked && (askedFor = ""),
  );
}

/** Forget the names (another login, tests). */
export function resetDirectionNames() {
  askedFor = "";
  asked++;
  names = new Map();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
const snapshot = () => names;

/** The direction names of `leaders` (ids), asked for again when `key` changes: pass the leaders with their titles,
 *  so a rename that changes a fallback name is picked up. */
export function useDirectionNames(key: string): ReadonlyMap<string, string> {
  useEffect(() => ask(key), [key]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
