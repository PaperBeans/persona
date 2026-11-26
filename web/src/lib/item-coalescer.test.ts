import { describe, expect, it } from "vitest";
import { createItemCoalescer, isCriticalItem, ITEM_COALESCE_MS, type CoalescerClock } from "./item-coalescer";
import type { Item } from "./types";

function fakeClock() {
  let now = 1000;
  let nextId = 1;
  const timers = new Map<number, { at: number; fn: () => void }>();
  const clock: CoalescerClock = {
    now: () => now,
    setTimeout: (fn, ms) => {
      const id = nextId++;
      timers.set(id, { at: now + ms, fn });
      return id;
    },
    clearTimeout: (id) => {
      timers.delete(id);
    },
  };
  const advance = (ms: number) => {
    now += ms;
    for (const [id, t] of [...timers.entries()]) {
      if (t.at <= now) {
        timers.delete(id);
        t.fn();
      }
    }
  };
  return { clock, advance };
}

const item = (seq: number, overrides: Partial<Item> = {}): Item => ({
  id: `i-${seq}`,
  seq,
  role: "assistant",
  kind: "text",
  text: `step ${seq}`,
  ...overrides,
});

describe("item-coalescer", () => {
  it("identifies critical items (user, system, errors, notices)", () => {
    expect(isCriticalItem(item(1))).toBe(false);
    expect(isCriticalItem(item(2, { kind: "tool_use" }))).toBe(false);
    expect(isCriticalItem(item(3, { role: "user" }))).toBe(true);
    expect(isCriticalItem(item(4, { role: "system" }))).toBe(true);
    expect(isCriticalItem(item(5, { is_error: true }))).toBe(true);
    expect(isCriticalItem(item(6, { kind: "notice" }))).toBe(true);
  });

  it("emits leading edge immediately and coalesces rapid burst frames within 48ms", () => {
    const { clock, advance } = fakeClock();
    const batches: Item[][] = [];
    const c = createItemCoalescer((b) => batches.push(b), { clock });

    c.push([item(1)]);
    expect(batches).toHaveLength(1);
    expect(batches[0].map((i) => i.seq)).toEqual([1]);

    advance(10);
    c.push([item(2)]);
    advance(10);
    c.push([item(3)]);
    advance(10);
    c.push([item(4)]);
    // Still inside 48ms window: no extra commits yet
    expect(batches).toHaveLength(1);

    advance(ITEM_COALESCE_MS);
    expect(batches).toHaveLength(2);
    expect(batches[1].map((i) => i.seq)).toEqual([2, 3, 4]);
  });

  it("bypasses coalescing at 0ms for critical items and flushes buffered items in order", () => {
    const { clock, advance } = fakeClock();
    const batches: Item[][] = [];
    const c = createItemCoalescer((b) => batches.push(b), { clock });

    c.push([item(1)]);
    advance(10);
    c.push([item(2)]);
    expect(batches).toHaveLength(1);

    // User message arrives during the 48ms window -> 0ms bypass with buffered item 2 preserved
    advance(5);
    c.push([item(3, { role: "user", text: "stop" })]);
    expect(batches).toHaveLength(2);
    expect(batches[1].map((i) => i.seq)).toEqual([2, 3]);
  });

  it("drops pending items on cancel()", () => {
    const { clock, advance } = fakeClock();
    const batches: Item[][] = [];
    const c = createItemCoalescer((b) => batches.push(b), { clock });

    c.push([item(1)]);
    advance(10);
    c.push([item(2)]);
    c.cancel();
    advance(100);
    expect(batches).toHaveLength(1);
  });

  it("returns buffered items without emitting and cancels timer on drain()", () => {
    const { clock, advance } = fakeClock();
    const batches: Item[][] = [];
    const c = createItemCoalescer((b) => batches.push(b), { clock });

    c.push([item(1)]);
    advance(10);
    c.push([item(2)]);
    c.push([item(3)]);
    const drained = c.drain();
    expect(drained.map((i) => i.seq)).toEqual([2, 3]);
    expect(c.drain()).toEqual([]);
    advance(100);
    expect(batches).toHaveLength(1);

    // Next push after drain emits immediately on a fresh leading edge
    c.push([item(4)]);
    expect(batches).toHaveLength(2);
    expect(batches[1].map((i) => i.seq)).toEqual([4]);
  });
});
