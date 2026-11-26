// Reader-controlled expansion survives live remounts and history paging; a run's completion collapses its summary.
import { useCallback, useEffect, useLayoutEffect, useState } from "react";

export const MAX_EXPANDED_ENTRIES = 500;

const opened = new Map<string, boolean>();
/** The drawn groups, told when something outside them opened theirs (`openExpanded`). */
const openListeners = new Set<(keys: ReadonlySet<string>) => void>();

/** Opens these keys from outside their groups ("Jump to the step that started it"): the ones on screen redraw open. */
export function openExpanded(keys: string[]) {
  if (!keys.length) return;
  for (const k of keys) setCapped(opened, k, true);
  const set = new Set(keys);
  openListeners.forEach((l) => l(set));
}

/** Listens for keys something outside opened (`openExpanded`), told in the same update as the groups' own redraw; returns
 *  how to stop. An open work summary draws all its steps then (work-budget.ts), so the row asked for is there at once. */
export function onOpenedOutside(listen: (keys: ReadonlySet<string>) => void): () => void {
  openListeners.add(listen);
  return () => void openListeners.delete(listen);
}
/** Folded ranges (seq … through_seq) the reader opened, by chat: the groups their steps land in start open, whatever
 *  key they get once the range is unfolded (a plan card taken out of it moves the first step). */
const foldRanges = new Map<string, [number, number][]>();

/** Forget what was opened: for tests, whose rows reuse the same ids. */
export function forgetExpanded() {
  opened.clear();
  liveOverrides.clear();
  foldRanges.clear();
}

function setCapped<K, V>(map: Map<K, V>, key: K, value: V, limit = MAX_EXPANDED_ENTRIES) {
  if (map.has(key)) map.delete(key);
  map.set(key, value);
  while (map.size > limit) {
    const oldest = map.keys().next().value;
    if (oldest === undefined) break;
    map.delete(oldest);
  }
}

export function openFoldRange(scope: string, from: number, through: number) {
  const prev = foldRanges.get(scope) ?? [];
  setCapped(foldRanges, scope, [...prev, [from, through]]);
}

export function inOpenedFold(scope: string, seq?: number): boolean {
  return seq !== undefined && (foldRanges.get(scope) ?? []).some(([a, b]) => seq >= a && seq <= b);
}

export function useExpanded(key: string, initial = false): [boolean, (next: boolean | ((v: boolean) => boolean)) => void] {
  const [, bump] = useState(0);
  const value = opened.get(key) ?? initial;
  useEffect(() => {
    const listen = (keys: ReadonlySet<string>) => keys.has(key) && bump((n) => n + 1);
    openListeners.add(listen);
    return () => void openListeners.delete(listen);
  }, [key]);
  const set = useCallback(
    (next: boolean | ((v: boolean) => boolean)) => {
      const v = typeof next === "function" ? next(opened.get(key) ?? initial) : next;
      setCapped(opened, key, v);
      bump((n) => n + 1);
    },
    [key, initial],
  );
  return [value, set];
}

/** Manual run overrides for every conversation style: dropped at completion, retained across live remounts. */
const liveOverrides = new Map<string, boolean>();

/** A run's manual override (initially collapsed), retained across remounts and dropped on completion. */
export function useLiveOverride(key: string, running: boolean): [boolean | undefined, (next: boolean) => void] {
  const [, bump] = useState(0);
  useLayoutEffect(() => {
    if (running) {
      // Mark even an untouched run: a previously opened historical summary must fold again when this run ends.
      if (!liveOverrides.has(key)) setCapped(liveOverrides, key, false);
      // Jump-to-call and search reveal use openExpanded, not the header's own toggle.
      const listen = (keys: ReadonlySet<string>) => {
        if (!keys.has(key)) return;
        setCapped(liveOverrides, key, true);
        bump((n) => n + 1);
      };
      openListeners.add(listen);
      return () => void openListeners.delete(listen);
    }
    if (!liveOverrides.has(key)) return;
    liveOverrides.delete(key);
    // An external reveal may also have saved `opened`: completion still collapses the run before paint.
    setCapped(opened, key, false);
    const keys = new Set([key]);
    openListeners.forEach((l) => l(keys));
  }, [key, running]);
  const set = useCallback(
    (next: boolean) => {
      setCapped(liveOverrides, key, next);
      bump((n) => n + 1);
    },
    [key],
  );
  return [running ? liveOverrides.get(key) : undefined, set];
}
