import { describe, expect, it } from "vitest";
import { activeWindow, splitByActivity, windowStart } from "./session-activity";

type Row = { id: string; at: number; needs?: boolean };
const NOW = Date.UTC(2026, 10, 11, 12);
const HOUR = 3_600_000;
// newest first, as the paged list orders them
const rows: Row[] = [
  { id: "a", at: NOW - HOUR },
  { id: "b", at: NOW - 5 * HOUR, needs: true },
  { id: "c", at: NOW - 30 * HOUR },
  { id: "d", at: NOW - 50 * HOUR, needs: true },
  { id: "e", at: Number.NaN },
];
const split = (list: Row[], start: number, o: { total?: number; hasMore?: boolean; needsYouTotal?: number } = {}) =>
  splitByActivity(list, (r) => r.at, start, { total: o.total ?? list.length, hasMore: o.hasMore ?? false, needsYou: (r) => !!r.needs, needsYouTotal: o.needsYouTotal });

describe("activeWindow", () => {
  it("reads only the known windows; anything else is Any time", () => {
    expect(activeWindow("7d")).toBe("7d");
    expect(activeWindow("")).toBe("");
    expect(activeWindow(null)).toBe("");
    expect(activeWindow("90d")).toBe("");
  });
  it("starts the window that far back from now, and Any time has no start", () => {
    expect(windowStart("1d", NOW)).toBe(NOW - 24 * HOUR);
    expect(windowStart("", NOW)).toBeNull();
  });
});

describe("splitByActivity", () => {
  it("keeps the rows active within the window and counts the rest of the total as earlier", () => {
    expect(split(rows, NOW - 24 * HOUR, { total: 40, needsYouTotal: 3 })).toEqual({ recent: 2, reached: true, earlier: 38, earlierNeedsYou: 2 });
  });
  it("is not reached while every loaded row is recent and more pages remain", () => {
    expect(split(rows.slice(0, 2), NOW - 24 * HOUR, { total: 40, hasMore: true })).toEqual({ recent: 2, reached: false, earlier: 0, earlierNeedsYou: 0 });
  });
  it("folds nothing when every session is recent and the list is complete", () => {
    expect(split(rows.slice(0, 2), NOW - 24 * HOUR)).toEqual({ recent: 2, reached: true, earlier: 0, earlierNeedsYou: 0 });
  });
  it("takes a row without a time for an earlier one", () => {
    expect(split(rows, NOW - 60 * HOUR).recent).toBe(4);
  });
  it("counts the loaded earlier rows that need you when the server gave no count", () => {
    expect(split(rows, NOW - 24 * HOUR, { total: 40 }).earlierNeedsYou).toBe(1);
  });
  it("never counts more earlier sessions needing you than there are earlier sessions", () => {
    expect(split(rows, NOW - 24 * HOUR, { total: 5, needsYouTotal: 9 }).earlierNeedsYou).toBe(3);
  });
});
