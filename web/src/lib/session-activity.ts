// "Active within" (#4236): how far back a session list keeps its rows in view. The rows last active before the window
// stay in the list, folded into one Earlier group at its end (views/agents/PagedSessionList.tsx).

export const ACTIVE_WINDOWS = ["", "1d", "7d", "30d"] as const;
/** "" is Any time: nothing folds. */
export type ActiveWindow = (typeof ACTIVE_WINDOWS)[number];

const DAY = 86_400_000;
const SPAN: Record<Exclude<ActiveWindow, "">, number> = { "1d": DAY, "7d": 7 * DAY, "30d": 30 * DAY };

/** A stored or passed value as a window: anything unknown reads as Any time. */
export function activeWindow(value: string | null | undefined): ActiveWindow {
  const v = value ?? "";
  return (ACTIVE_WINDOWS as readonly string[]).includes(v) ? (v as ActiveWindow) : "";
}

/** The earliest activity (ms) the window keeps in view; null for Any time. */
export function windowStart(window: ActiveWindow, now: number): number | null {
  return window ? now - SPAN[window] : null;
}

export interface ActivitySplit {
  /** Rows [0, recent) of the loaded prefix were active within the window. */
  recent: number;
  /** The loaded rows reach past the window (or there is nothing more to load), so `earlier` is exact. */
  reached: boolean;
  /** The sessions before the window: the list's total minus the recent ones, loaded or not. 0 until `reached`. */
  earlier: number;
  /** How many of `earlier` need the user. 0 until `reached`. */
  earlierNeedsYou: number;
}

/**
 * Splits a newest-first list at the window's start. `rows` must be in the order of `activeAt`, newest first (the
 * paged list's transport order), so once one loaded row is older than `start` every recent row is loaded and the rest
 * of `total` is earlier. `needsYouTotal` is the server's count of the list's sessions that need the user (the Needs
 * you filter's number); without it only the loaded earlier rows are counted.
 */
export function splitByActivity<T>(
  rows: readonly T[],
  activeAt: (row: T) => number,
  start: number,
  { total, hasMore, needsYou, needsYouTotal }: { total: number; hasMore: boolean; needsYou: (row: T) => boolean; needsYouTotal?: number },
): ActivitySplit {
  const first = rows.findIndex((row) => !(activeAt(row) >= start));
  const recent = first === -1 ? rows.length : first;
  const reached = first !== -1 || !hasMore;
  if (!reached) return { recent, reached, earlier: 0, earlierNeedsYou: 0 };
  const earlier = Math.max(0, total - recent, rows.length - recent);
  let earlierNeedsYou: number;
  if (needsYouTotal === undefined) {
    earlierNeedsYou = rows.slice(recent).filter(needsYou).length;
  } else {
    const recentNeedsYou = rows.slice(0, recent).filter(needsYou).length;
    earlierNeedsYou = Math.min(earlier, Math.max(0, needsYouTotal - recentNeedsYou));
  }
  return { recent, reached, earlier, earlierNeedsYou };
}
