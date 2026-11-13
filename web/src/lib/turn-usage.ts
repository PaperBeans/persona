// What a finished exchange used (#5426, cindy's per-turn usage under a reply): the tokens every item of the exchange
// carries (Item.usage, and a fold's for the work it stands for), collected as they are, and the subagents it launched
// (lib/turns.ts lineUsage). On the first screen through lib/turns.ts, so only that: the sums by model, the prices, the
// card's lines and the bar's text load with the reply bar (lib/turn-cost.ts).

import type { Item, TokenUsage } from "./types";

export interface TurnUsage {
  /** What the exchange's items carry, in order, then each subagent it launched once (an entry with an `id`). */
  entries: TokenUsage[];
  /** No earlier exchange of the loaded chat used any tokens: likely its first round, with nothing in the cache to read
   *  yet (a low hit rate says nothing then). */
  first?: boolean;
}

/** Adds `entries` to an exchange's usage. */
export function addUsage(to: { usage?: TurnUsage }, entries: TokenUsage[]) {
  if (entries.length) (to.usage ??= { entries: [] }).entries.push(...entries);
}

/** What `it` carries, its own and its fold's: `reports` the subagents' (with an `id`), else the rest. */
export const itemUsage = (it: Item, reports = false): TokenUsage[] => [...(it.usage ?? []), ...(it.fold?.usage ?? [])].filter((u) => Boolean(u.id) === reports);

/** Adds what `it` carries, its own and its fold's, to an exchange's usage; not a subagent's report, which counts once in
 *  the exchange that launched it (lib/turns.ts lineUsage). */
export const countUsage = (to: { usage?: TurnUsage }, it: Item) => addUsage(to, itemUsage(it));

/** Marks the first of `segs` that used tokens as the loaded chat's first round. */
export function markFirstUsage(segs: { usage?: TurnUsage }[]) {
  const first = segs.find((s) => s.usage)?.usage;
  if (first) first.first = true;
}

/** The usage as one string, what the reply's props carry: the same usage is the same string, so a finished reply is
 *  not drawn again when its exchange is worked out again (SegmentView's Answer is memo'd). */
export const usageKey = (u: TurnUsage | undefined): string | undefined => (u ? JSON.stringify(u) : undefined);
