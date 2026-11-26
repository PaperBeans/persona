// Burst coalescer for live transcript `items` frames:
// - Idle leading edge emits at 0ms so single messages incur zero added latency.
// - Rapid incremental bursts within ITEM_COALESCE_MS (48ms) merge into one state update.
// - Critical items (user echoes, errors, system/notice/interaction items, or explicit
//   immediate replays) flush the buffer at 0ms so feedback stays instantaneous.
import { mergeItems } from "./turns";
import type { Item } from "./types";

export const ITEM_COALESCE_MS = 48;

export interface CoalescerClock {
  now: () => number;
  setTimeout: (fn: () => void, ms: number) => number;
  clearTimeout: (id: number) => void;
}

const browserClock: CoalescerClock = {
  now: () => performance.now(),
  setTimeout: (fn, ms) => window.setTimeout(fn, ms),
  clearTimeout: (id) => window.clearTimeout(id),
};

export function isCriticalItem(item: Item): boolean {
  if (item.role === "user" || item.role === "system") return true;
  if (item.is_error) return true;
  return item.kind === "notice";
}

export interface ItemCoalescer {
  push(batch: Item[], immediate?: boolean): void;
  flush(): void;
  drain(): Item[];
  cancel(): void;
}

export function createItemCoalescer(
  emit: (batch: Item[]) => void,
  opts: { windowMs?: number; clock?: CoalescerClock } = {},
): ItemCoalescer {
  const windowMs = opts.windowMs ?? ITEM_COALESCE_MS;
  const clock = opts.clock ?? browserClock;
  let pending: Item[] = [];
  let timer: number | undefined;
  let lastEmit = -Infinity;

  const stop = () => {
    if (timer !== undefined) {
      clock.clearTimeout(timer);
      timer = undefined;
    }
  };

  const commit = () => {
    stop();
    if (!pending.length) return;
    const batch = pending;
    pending = [];
    lastEmit = clock.now();
    emit(batch);
  };

  return {
    push(batch, immediate = false) {
      if (!batch.length) {
        if (immediate) commit();
        return;
      }
      pending = pending.length ? mergeItems(pending, batch) : batch;
      const now = clock.now();
      const elapsed = now - lastEmit;
      if (immediate || batch.some(isCriticalItem) || elapsed >= windowMs) {
        commit();
        return;
      }
      if (timer === undefined) {
        timer = clock.setTimeout(commit, Math.max(1, windowMs - elapsed));
      }
    },
    flush() {
      commit();
    },
    drain() {
      stop();
      const batch = pending;
      pending = [];
      lastEmit = -Infinity;
      return batch;
    },
    cancel() {
      stop();
      pending = [];
      lastEmit = -Infinity;
    },
  };
}
