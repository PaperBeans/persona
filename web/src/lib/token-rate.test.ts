// Ported from cindy's runningTokenRateHistory tests (apps/desktop/src/renderer/__tests__), on the status line's count
// over time instead of cindy's generation-only clock: the count written exactly under 1k and to the hundred above it,
// read about once a second; rises close enough for one step at the slowest speed are generation, a longer gap or a call
// running is a pause.
import { describe, expect, it } from "vitest";
import { clearRateHistories, countStep, emptyRateHistory, formatRate, FRESH_MS, loadRateHistory, pauseMs, recordTokens, saveRateHistory, shownRate, turnAverage, type RateHistory } from "./token-rate";

/** Readings of one turn: [tokens, ms] pairs, the page's clock equal to the node's. */
function feed(h: RateHistory, readings: [number, number][], turn: string | null = "t1"): RateHistory {
  return readings.reduce((acc, [tokens, at]) => recordTokens(acc, { turn, tokens, at, now: at }), h);
}
/** A turn measuring from `tokens` at 0 ms (its first reading and the rise after it already in). */
const begin = (tokens = 0): RateHistory => ({ ...emptyRateHistory(), turn: "t1", last: { tokens, at: 0 } });
/** Exact counts rising `per` tokens each second, from second `from` to `to`, starting at `base`. */
const each = (from: number, to: number, per: number, base = 0): [number, number][] =>
  Array.from({ length: to - from + 1 }, (_, i) => [base + per * (from + i), (from + i) * 1000]);

describe("the count's step", () => {
  it("is exact under 1k, the hundred to 1M, the 100k past it; a pause is one step at 10 tok/s, 3 to 10 s", () => {
    expect([countStep(999), countStep(1000), countStep(22_800), countStep(1_200_000)]).toEqual([1, 100, 100, 100_000]);
    expect([pauseMs(500), pauseMs(5000), pauseMs(2_000_000)]).toEqual([3000, 10_000, 10_000]);
  });
});

describe("the speed history", () => {
  it("measures each stretch of at least two rises and three seconds, not the turn's running average", () => {
    const first = feed(begin(), each(1, 3, 100));
    expect(first.samples.map((s) => s.rate)).toEqual([100]);
    const next = feed(first, each(4, 6, 50, 150));
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50]);
    expect(next.peak).toBe(100);
    expect(next.latestRate).toBe(50);
    // the same reading again changes nothing, and is the same history
    expect(recordTokens(next, { turn: "t1", tokens: 450, at: 6000, now: 6500 })).toBe(next);
  });

  it("does not know when a first reading's count was reached: measuring starts at the rise after it", () => {
    // a page opened in the middle of a turn, or a turn waiting for its first tokens
    const first = feed(emptyRateHistory(), [[500, 10_000], [550, 11_000], [600, 12_000], [650, 13_000]]);
    expect(first.samples).toEqual([]);
    expect(first.latestRate).toBeNull();
    expect(feed(first, [[700, 14_000]]).samples.map((s) => s.rate)).toEqual([50]);
  });

  it("needs two rises for a sample, however long one gap is", () => {
    const one = feed(begin(), [[90, 3000]]);
    expect(one.samples).toEqual([]);
    expect(feed(one, [[120, 4000]]).samples.map((s) => s.rate)).toEqual([30]);
  });

  it("takes a gap longer than one step at the slowest speed as a pause, and shows no speed from before it again", () => {
    const first = feed(begin(), each(1, 3, 100));
    // an exact count: 3 s still are generation, 3.5 s are not
    const afterPause = feed(first, [[400, 6500]]);
    expect(afterPause.samples).toEqual(first.samples);
    expect(afterPause.latestRate).toBeNull();
    expect(shownRate(afterPause, 6500)).toBeNull();
    const next = feed(afterPause, [[450, 7500], [500, 8500], [550, 9500]]);
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50]);
    expect(next.samples.map((s) => s.durationMs)).toEqual([3000, 6000]);
    // in hundreds, a step every 8 s is still slow output, one after 11 s is a pause
    const slow = feed(begin(5000), [[5100, 8000], [5200, 16_000]]);
    expect(slow.samples.map((s) => s.rate)).toEqual([12.5]);
    expect(feed(slow, [[5300, 27_000]]).latestRate).toBeNull();
  });

  it("takes a running call as a pause at once: measuring starts at the rise after it", () => {
    const first = feed(begin(), each(1, 3, 100));
    const paused = recordTokens(first, { turn: "t1", tokens: 300, at: 3500, now: 3500, paused: true });
    expect(paused.latestRate).toBeNull();
    expect(paused.last).toEqual({ tokens: 300, at: -Infinity });
    // read again while the call runs: nothing changes
    expect(recordTokens(paused, { turn: "t1", tokens: 300, at: 4500, now: 4500, paused: true })).toBe(paused);
    const next = feed(paused, [[350, 9000], [400, 10_000], [450, 11_000], [500, 12_000]]);
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50]);
    expect(next.genMs).toBe(6000);
  });

  it("starts the counts again for a new turn or a count that went back, keeping the chart", () => {
    const first = feed(begin(), each(1, 3, 100));
    const newTurn = feed(first, [[0, 5000]], "t2");
    expect(newTurn.samples).toEqual(first.samples);
    expect(newTurn.latestRate).toBeNull();
    expect(newTurn.peak).toBe(0);
    expect(newTurn.genMs).toBe(0);
    const back = feed(first, [[50, 3500]]);
    expect(back.samples).toEqual(first.samples);
    expect(feed(back, [[100, 4500], [150, 5500], [200, 6500], [250, 7500]]).samples.map((s) => s.rate)).toEqual([100, 50]);
  });

  it("does not turn a count corrected with no time between into a spike", () => {
    const first = feed(begin(), each(1, 3, 100));
    const correction = feed(first, [[350, 3000]]);
    expect(correction.samples).toEqual(first.samples);
    expect(correction.peak).toBe(100);
    expect(feed(correction, [[400, 4000], [450, 5000], [500, 6000]]).samples.at(-1)?.rate).toBe(50);
  });

  it("keeps 60 samples for the chart and the turn's peak", () => {
    let h = feed(begin(), each(1, 3, 300));
    for (let n = 4; n <= 300; n++) h = feed(h, [[900 + (n - 3) * 0.3, n * 1000]]);
    expect(h.samples).toHaveLength(60);
    expect(h.peak).toBe(300);
  });

  it("records the reading that ends the turn with the turn's own counts, and the next turn goes on along the chart", () => {
    const active = feed(begin(), each(1, 3, 100));
    const end = feed(active, each(4, 6, 50, 150), null);
    expect(end.turn).toBe("t1");
    expect(end.samples.map((s) => s.rate)).toEqual([100, 50]);
    const next = feed(end, [[0, 20_000], [10, 21_000], [50, 22_000], [90, 23_000], [130, 24_000]], "t2");
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50, 40]);
    expect(next.samples.map((s) => s.durationMs)).toEqual([3000, 6000, 9000]);
    expect(next.latestRate).toBe(40);
    expect(next.peak).toBe(40);
  });

  it("ignores readings without a count", () => {
    const h = begin();
    expect(recordTokens(h, { turn: "t1", tokens: undefined, at: 1000, now: 1000 })).toBe(h);
    expect(recordTokens(h, { turn: "t1", tokens: Number.NaN, at: 1000, now: 1000 })).toBe(h);
    expect(recordTokens(h, { turn: "t1", tokens: 0, at: 1000, now: 1000 })).toBe(h);
  });
});

/** Steady output at `rate` tok/s from 5,000 tokens, as Claude's line writes it (to the hundred) and the page gets it:
 *  about once a second, each reading up to 100 ms early or late. What the bar would show after each reading. */
function steady(rate: number, seconds = 180, phase = 0.37): { h: RateHistory; shown: (number | null)[] } {
  let h = emptyRateHistory();
  const shown: (number | null)[] = [];
  for (let k = 0; k <= seconds; k++) {
    const at = (k + phase) * 1000 + Math.round(Math.sin(k * 7.3) * 100);
    const tokens = Math.round((5000 + (rate * at) / 1000) / 100) * 100;
    h = recordTokens(h, { turn: "t1", tokens, at, now: at });
    shown.push(shownRate(h, at));
  }
  return { h, shown };
}

describe("a steady speed above 1k, its count in steps of 100", () => {
  it.each([25, 40, 70, 120])("%i tok/s: the latest stays within a fifth, the peak is not inflated, and it never flickers to the tokens", (rate) => {
    for (const phase of [0, 0.37, 0.71]) {
      const { h, shown } = steady(rate, 180, phase);
      // the first sample needs two rises and five seconds after the rise that starts the measuring
      const settled = shown.slice(20);
      expect(settled.every((v) => v !== null), `${rate} tok/s shown ${settled.join(",")}`).toBe(true);
      for (const v of settled) expect(Math.abs(v! / rate - 1), `${rate} tok/s showed ${v}`).toBeLessThanOrEqual(0.2);
      expect(h.peak / rate).toBeLessThanOrEqual(1.2);
      expect(Math.abs(turnAverage(h)! / rate - 1)).toBeLessThanOrEqual(0.05);
    }
  });
});

describe("what the bar shows of it", () => {
  it("shows the latest speed only while it is fresh, no call runs and the count moves", () => {
    const h = feed(begin(), each(1, 3, 100));
    expect(shownRate(h, 3000)).toBe(100);
    expect(shownRate(h, 3000, true)).toBeNull();
    // an exact count standing still past 3 s: a pause
    expect(shownRate(h, 6000)).toBe(100);
    expect(shownRate(h, 6001)).toBeNull();
    // a count in hundreds may stand still up to 10 s
    const stepped = feed(begin(5000), [[5100, 2000], [5200, 4000], [5300, 6000]]);
    expect(shownRate(stepped, 16_000)).toBe(50);
    expect(shownRate(stepped, 16_001)).toBeNull();
    // still moving but every rise too far apart to measure: the last sample goes stale
    const stale = { ...h, movedAt: 3000 + FRESH_MS };
    expect(shownRate(stale, 3000 + FRESH_MS)).toBeNull();
    expect(shownRate(begin(), 0)).toBeNull();
  });

  it("averages the turn over its measured generation only", () => {
    expect(turnAverage(begin())).toBeNull();
    const h = feed(begin(), [...each(1, 3, 100), [600, 7000], ...each(8, 10, 100, -100)]);
    // 300 tokens in the first three seconds, 300 in the last three: the pause's tokens and time count for nothing
    expect(turnAverage(h)).toBe(100);
  });

  it("writes a speed as cindy's formatRecentOutputTokenRate does", () => {
    expect(formatRate(0)).toBe("0");
    expect(formatRate(0.05)).toBe("<0.1");
    expect(formatRate(4.25)).toBe("4.3");
    expect(formatRate(42)).toBe("42");
    expect(formatRate(99.96)).toBe("100");
    expect(formatRate(120.4)).toBe("120");
  });
});

describe("the per-conversation cache", () => {
  it("brings back a history that has samples, and only one that has", () => {
    clearRateHistories();
    saveRateHistory("a", emptyRateHistory());
    expect(loadRateHistory("a")).toBeNull();
    saveRateHistory("a", feed(begin(), each(1, 3, 100)));
    expect(loadRateHistory("a")?.samples.map((s) => s.rate)).toEqual([100]);
  });

  it("drops the least recently used conversation past 20", () => {
    clearRateHistories();
    const h = feed(begin(), each(1, 3, 100));
    for (let n = 0; n < 20; n++) saveRateHistory(`c${n}`, h);
    expect(loadRateHistory("c0")).not.toBeNull(); // read: now the most recent
    saveRateHistory("c20", h);
    expect(loadRateHistory("c1")).toBeNull();
    expect(loadRateHistory("c0")).not.toBeNull();
    expect(loadRateHistory("c20")).not.toBeNull();
    clearRateHistories();
  });
});
