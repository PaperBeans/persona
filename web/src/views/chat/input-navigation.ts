import { api, ApiError, sessionPath } from "../../lib/api";
import type { Item, ItemsPage } from "../../lib/types";
import { containsInput, validUserInputPreviews, type UserInputPosition } from "../../lib/user-input-index";

const RADIUS = 40;

export interface InputWindow {
  items: Item[];
  history: string;
  before?: number;
  after?: number;
}

function conflict(error: unknown): "stale" | "unsupported" | undefined {
  if (!(error instanceof ApiError) || error.status !== 409) return;
  if (error.code === "history_changed") return "stale";
  if (error.code === "node_outdated" || error.code === "unsupported") return "unsupported";
}

export async function readInputPreviews(mid: string, sid: string, positions: UserInputPosition[], history: string, signal: AbortSignal) {
  const query = new URLSearchParams();
  for (const { seq, id } of positions) query.append("preview", `${seq}:${id}`);
  if (history) query.set("history", history);
  try {
    const result = await api<unknown>(`${sessionPath(mid, sid)}/items?${query}`, { signal });
    if (!validUserInputPreviews(result)) return "unsupported"; // an old hub can ignore the selector
    if (history && result.history !== history) return "stale";
    const requested = new Map(positions.map(({ id, seq }) => [id, seq]));
    return result.inputs.filter(({ id, seq }) => requested.get(id) === seq);
  } catch (error) {
    const outcome = conflict(error);
    if (outcome) return outcome;
    throw error;
  }
}

export async function readInputWindow(mid: string, sid: string, target: UserInputPosition, history: string, signal: AbortSignal): Promise<InputWindow | "stale" | "unsupported"> {
  const query = new URLSearchParams({ around_seq: String(target.seq), radius: String(RADIUS) });
  if (history) query.set("history", history);
  try {
    const page = await api<ItemsPage>(`${sessionPath(mid, sid)}/items?${query}`, { signal });
    if (!Array.isArray(page?.items) || typeof page.has_more !== "boolean") throw new Error("Invalid transcript page");
    const { from_seq: from, to_seq: to, more_after: later } = page;
    // Ordinary pages can carry coverage, but they do not have the target window's two explicit flags.
    if (from === undefined || to === undefined || later === undefined) return "unsupported";
    if (history && page.history !== history) return "stale";
    if (typeof page.history !== "string" || typeof later !== "boolean" ||
        !Number.isSafeInteger(from) || !Number.isSafeInteger(to) ||
        from < Math.max(0, target.seq - RADIUS - 1) || from >= target.seq || to > target.seq + RADIUS || to < target.seq ||
        page.items.some((item) => !item || typeof item.id !== "string" || !Number.isSafeInteger(item.seq) || item.seq <= from || item.seq > to) ||
        !containsInput(page.items, target)) throw new Error("Invalid target window");
    return { items: page.items, history: page.history, before: page.has_more ? from + 1 : undefined, after: later ? to : undefined };
  } catch (error) {
    const outcome = conflict(error);
    if (outcome) return outcome;
    throw error;
  }
}

export async function readInputWindowPage(mid: string, sid: string, history: string, direction: "before" | "after", cursor: number, signal: AbortSignal) {
  const paging = direction === "before" ? `turns=2&before_seq=${cursor}` : `after_seq=${cursor}`;
  try {
    const page = await api<ItemsPage>(`${sessionPath(mid, sid)}/items?${paging}&lazy_images=1`, { signal });
    if (history && page.history && page.history !== history) return "stale";
    if (!Array.isArray(page.items)) throw new Error("Invalid transcript page");
    if (direction === "before") {
      const items = page.items.filter((item) => item.seq < cursor);
      const first = items[0]?.seq;
      if (page.has_more && first === undefined) throw new Error("Transcript page did not move back");
      return { items, cursor: page.has_more ? first : undefined };
    }
    if (page.from_seq !== undefined && page.from_seq > cursor) throw new Error("Transcript page skipped lines");
    const items = page.items.filter((item) => item.seq > cursor);
    const last = page.to_seq ?? items.at(-1)?.seq ?? cursor;
    if (page.more_after && last <= cursor) throw new Error("Transcript page did not move forward");
    return { items, cursor: page.more_after ? last : undefined };
  } catch (error) {
    if (conflict(error) === "stale") return "stale";
    throw error;
  }
}
