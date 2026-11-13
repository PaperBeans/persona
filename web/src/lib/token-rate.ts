// The running bar's generation speed (cindy's runningTokenRateHistory and formatRecentOutputTokenRate, adapted; the
// owner asked on 2026-11-12 that the bar follow cindy). cindy reads a generation-only clock from its host; Persona has
// only the count Claude's status line shows ("↓ 1.2k tokens", read about once a second, `SessionLive.tokens_down`), so
// generation time is worked out from when that count rises. The line writes the count exactly under 1k and to the
// hundred from there ("22.8k"), so above 1k a rise is a step of 100 that comes every second or every few: two rises
// close enough for even slow output (SLOWEST tok/s) to make one step between them are generation, and a longer gap, or a
// call running (the turn's step, lib/run-step.ts), is a pause that counts for nothing and starts the measuring again.
// A sample spans at least two rises and a few seconds (more once the count moves in hundreds), so a step that lands a
// moment early or late moves it little. Every speed is a count over a time that both moved; nothing is guessed, and
// nothing shows before there is a measurement (docs/design/PRINCIPLES.md 5). The times are when each reading arrived:
// the node's `at` is in whole seconds, too coarse for gaps of a second or two, so it counts only when it carries
// fractions of a second.
import { useEffect, useState } from "react";

export interface RateSample {
  /** Measured generation time up to this sample, across turns: the chart's x axis. */
  durationMs: number;
  rate: number;
}

export interface RateHistory {
  /** The turn the counts belong to (its start); a new one starts the counts and the peak again, not the chart. */
  turn: string | null;
  /** The last count seen and when it was reached (-Infinity: not known, so the next rise only starts the measuring). */
  last: { tokens: number; at: number } | null;
  /** Generation since the last sample: its tokens, time and how many rises of the count. */
  window: { tokens: number; ms: number; rises: number };
  /** This turn's measured generation: the turn average. */
  genTokens: number;
  genMs: number;
  samples: RateSample[];
  /** This turn's highest sample. */
  peak: number;
  /** The latest sample, until a pause: after one it waits for the next sample. */
  latestRate: number | null;
  /** When the latest sample was taken, and when the count last rose (this page's clock). */
  latestAt?: number;
  movedAt?: number;
}

/** The slowest output the count's step is allowed for: a gap longer than one step takes at this speed is a pause. */
const SLOWEST = 10;
/** A pause is at least this long (a count read exactly, about once a second) and at most this (a step of 100k, past 1M). */
const MIN_PAUSE_MS = 3000;
const MAX_PAUSE_MS = 10_000;
/** A sample's least generation: a few seconds of an exact count, longer once a step lands up to a reading late. */
const SAMPLE_MS = 3000;
const STEPPED_SAMPLE_MS = 5000;
const SAMPLE_RISES = 2;
/** The latest speed stays the latest for this long (cindy's RATE_SAMPLE_FRESH_MS). */
export const FRESH_MS = 60_000;
const MAX_SAMPLES = 60;
const NO_WINDOW = { tokens: 0, ms: 0, rises: 0 };

/** How the line writes a count: exactly under 1k, to the hundred ("22.8k"), to the 100k ("1.2M"). */
export function countStep(tokens: number): number {
  return tokens >= 1e6 ? 1e5 : tokens >= 1e3 ? 100 : 1;
}

/** How long the count may stand still at `tokens` and still be generating: one step at SLOWEST tok/s. */
export function pauseMs(tokens: number): number {
  return Math.min(MAX_PAUSE_MS, Math.max(MIN_PAUSE_MS, (countStep(tokens) * 1000) / SLOWEST));
}

export function emptyRateHistory(): RateHistory {
  return { turn: null, last: null, window: NO_WINDOW, genTokens: 0, genMs: 0, samples: [], peak: 0, latestRate: null };
}

/** Measuring starts again at the next rise, and the speed before shows no more (`tokens`: the count now, its time not
 *  known). */
function restart(h: RateHistory, tokens: number): RateHistory {
  const quiet = h.last?.tokens === tokens && h.last.at === -Infinity && h.latestRate === null && h.window === NO_WINDOW;
  return quiet ? h : { ...h, last: { tokens, at: -Infinity }, window: NO_WINDOW, latestRate: null, latestAt: undefined };
}

/** One reading of the count. `turn` null (the turn is over) keeps the one before, as cindy's terminal report does;
 *  `paused`: a call is running, so the count stands still for it. The same history comes back when nothing changed, so
 *  a render that reads it does not render again. */
export function recordTokens(h: RateHistory, input: { turn: string | null; tokens: number | undefined; at: number; now: number; paused?: boolean }): RateHistory {
  const turn = input.turn ?? h.turn;
  // a new turn: its counts start from nothing; the chart goes on after the last one's samples
  const cur: RateHistory = turn === h.turn ? h : { ...h, turn, last: null, window: NO_WINDOW, genTokens: 0, genMs: 0, peak: 0, latestRate: null, latestAt: undefined, movedAt: undefined };
  const { tokens, at, now } = input;
  if (tokens === undefined || !Number.isFinite(tokens) || tokens < 0 || !Number.isFinite(at)) return cur;
  const last = cur.last;
  // the first reading (a page opened in the middle of a turn, a turn waiting for its first tokens), a count that went
  // back, or a call running: when the count was reached is not known, so the rise after it is where measuring starts
  if (!last || tokens < last.tokens || input.paused) return restart(cur, tokens);
  // a reading with the same count says nothing of when the next tokens came
  if (tokens === last.tokens) return cur;
  const gap = at - last.at;
  const moved = { ...cur, last: { tokens, at }, movedAt: now };
  // a count corrected at the same moment has no time to divide by: measure again from here
  if (gap <= 0) return { ...moved, window: NO_WINDOW };
  // a pause: the tokens came at some unknown moment in it, and the speed before it is not the speed after
  if (gap > pauseMs(last.tokens)) return { ...moved, window: NO_WINDOW, latestRate: null, latestAt: undefined };
  const delta = tokens - last.tokens;
  const window = { tokens: cur.window.tokens + delta, ms: cur.window.ms + gap, rises: cur.window.rises + 1 };
  const next = { ...moved, genTokens: cur.genTokens + delta, genMs: cur.genMs + gap };
  if (window.rises < SAMPLE_RISES || window.ms < (countStep(last.tokens) > 1 ? STEPPED_SAMPLE_MS : SAMPLE_MS)) return { ...next, window };
  const rate = (window.tokens * 1000) / window.ms;
  const samples = [...cur.samples.slice(-(MAX_SAMPLES - 1)), { durationMs: (cur.samples.at(-1)?.durationMs ?? 0) + window.ms, rate }];
  return { ...next, window: NO_WINDOW, samples, peak: Math.max(cur.peak, rate), latestRate: rate, latestAt: now };
}

/** The latest speed to show at `now`: while it is fresh, no call runs and the count still moves, else null. */
export function shownRate(h: RateHistory, now: number, paused = false): number | null {
  if (paused || h.latestRate === null || h.latestAt === undefined || h.movedAt === undefined) return null;
  return now - h.latestAt < FRESH_MS && now - h.movedAt <= pauseMs(h.last?.tokens ?? 0) ? h.latestRate : null;
}

/** This turn's measured tokens over its measured generation time, once there is a sample's worth of it. */
export function turnAverage(h: RateHistory): number | null {
  return h.genMs >= SAMPLE_MS ? (h.genTokens * 1000) / h.genMs : null;
}

/** cindy's formatRecentOutputTokenRate: `0`, `<0.1`, `4.2`, `42`, `120` (no ".0"; whole from 100). */
export function formatRate(rate: number): string {
  if (rate === 0) return "0";
  if (!Number.isFinite(rate) || rate < 0) return "";
  if (rate < 0.1) return "<0.1";
  return rate >= 100 ? rate.toFixed(0) : rate.toFixed(1).replace(/\.0$/, "");
}

// Kept per conversation while the page is open (cindy's in-process cache): switching to another chat and back keeps
// the chart. Never written anywhere; a reload starts empty.
const MAX_CACHED = 20;
const cache = new Map<string, RateHistory>();

export function loadRateHistory(key: string): RateHistory | null {
  const h = cache.get(key);
  if (!h) return null;
  // the Map iterates in insertion order: what is read moves to the end, the least recently used is first
  cache.delete(key);
  cache.set(key, h);
  return h;
}

export function saveRateHistory(key: string, h: RateHistory): void {
  if (!h.samples.length) return; // no chart to bring back
  cache.delete(key);
  cache.set(key, h);
  while (cache.size > MAX_CACHED) cache.delete(cache.keys().next().value!);
}

export function clearRateHistories(): void {
  cache.clear();
}

/** The speed history of the conversation `key`, fed with each status line while `on` (an Agent's own turn: not in the
 *  Persona conversation, not while only background work goes on). `turn` is the turn's start; `paused`: a call runs. */
export function useRateHistory(key: string, turn: string | undefined, tokens: number | undefined, at: string | undefined, on: boolean, paused = false): RateHistory {
  const [state, setState] = useState(() => ({ key, h: loadRateHistory(key) ?? emptyRateHistory() }));
  // another conversation in the same bar: its own history
  const cur = state.key === key ? state : { key, h: loadRateHistory(key) ?? emptyRateHistory() };
  if (cur !== state) setState(cur);
  useEffect(() => {
    if (!on) return;
    const now = Date.now();
    const node = at && /\.\d/.test(at) ? Date.parse(at) : NaN;
    setState((s) => {
      const h = recordTokens(s.h, { turn: turn ?? null, tokens, at: Number.isFinite(node) ? node : now, now, paused });
      return h === s.h ? s : { key: s.key, h };
    });
    // a reading is a new `at` (or, without one, a new count); a call starting is a pause at once
  }, [on, turn, tokens, at, paused]);
  useEffect(() => saveRateHistory(cur.key, cur.h), [cur.key, cur.h]);
  return cur.h;
}
