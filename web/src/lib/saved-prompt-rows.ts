// The user's saved prompts as rows of the composer's "/" list (#7329, docs/web/web.md "Saved prompts"). The list itself,
// its reads, changes and words are lib/saved-prompts.ts, a chunk of its own the first time a composer has the focus
// (or a "/" in it), so none of that is on the first screen; this file only holds the rows it gives the composers.
import { useEffect, useSyncExternalStore } from "react";
import type { ComposerCommand } from "./extension-prompts";

let rows: readonly ComposerCommand[] = [];
// one request for the chunk however many boxes want it at once: each then gets the module once it has run
let chunk: Promise<typeof import("./saved-prompts")> | null = null;
const listeners = new Set<() => void>();
let interested = 0;
const subscribe = (l: () => void) => (listeners.add(l), () => void listeners.delete(l));
const snapshot = () => rows;

/** lib/saved-prompts.ts: the rows the composers show now. */
export function setSavedPromptRows(next: readonly ComposerCommand[]) {
  if (next === rows) return;
  rows = next;
  listeners.forEach((l) => l());
}

/** The saved prompts' rows, followed from the first time they are `wanted` (a composer has the focus, or a "/" in it;
 *  starting again does nothing). A chunk that would not load (offline, the hub redeployed) is asked for the next time. */
export function useSavedPromptRows(wanted: boolean): readonly ComposerCommand[] {
  useEffect(() => {
    if (!wanted) return;
    interested++;
    chunk ??= import("./saved-prompts");
    const request = chunk;
    let live = true;
    // A chunk can arrive after this composer has unmounted. Only a remaining consumer may start the shared
    // subscription: starting it here would retain credentials and hub listeners after the last interested UI is gone.
    request.then((m) => { if (live && interested > 0) m.startSavedPrompts(); }, () => { if (request === chunk) chunk = null; });
    return () => {
      live = false;
      interested--;
    };
  }, [wanted]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
