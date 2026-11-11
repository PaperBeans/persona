import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";
import { t } from "../i18n";
import { ListCounts, ListPage, ListFilters, PAGE_SIZE, SESSIONS_LIST_FEATURE, SessionList, type SessionListOptions } from "./session-list";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

type Row = { id: string; rank: number; title: string; detail?: { value: string } };
const row = (id: string, rank = 0, title = id): Row => ({ id, rank, title });
const rows = (count: number, start = 0): Row[] => Array.from({ length: count }, (_, i) => row(String(start + i), 10_000 - start - i));
const page = (sessions: Row[] = [], patch: Partial<ListPage<Row>> = {}): ListPage<Row> => ({
  sessions, remove: [], has_more: false, revision: "baseline", total: 8_000, ...patch,
});
const counts = (revision = "counts-only"): ListCounts => ({ total: 8_000, machine: { demo: 8_000 }, project: {}, group: {}, state: { idle: 8_000 }, revision });
const params = (path: string) => new URL(path, "https://example.com").searchParams;
const pages = vi.fn<(path: string) => Promise<unknown>>();
const aggregates = vi.fn<(path: string) => Promise<unknown>>();
const make = (onRows?: (rows: Row[]) => void, options?: SessionListOptions<Row>, normalize?: (id: string) => string) => new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank || a.id.localeCompare(b.id), onRows, normalize, options);
const ids = (list: SessionList<Row>) => list.getSnapshot().rows.map((r) => r.id);
const requestParams = () => pages.mock.calls.map(([path]) => Object.fromEntries(params(path)));
const failure = (status: number, code: string) => new ApiError(status, code, { code });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
// Flush promise continuations without real timers or polling.
async function tick() { for (let i = 0; i < 8; i++) await Promise.resolve(); }

beforeEach(() => {
  vi.clearAllMocks();
  pages.mockReset().mockResolvedValue(page(rows(50), { has_more: true, next_cursor: "frozen/one+" }));
  aggregates.mockReset().mockResolvedValue(counts());
  vi.mocked(api).mockImplementation((path) => (params(path).has("counts") ? aggregates(path) : pages(path)) as ReturnType<typeof api>);
});

describe("SessionList baseline, filters and structural sharing", () => {
  it("exports the negotiated feature/page size and a stable empty external-store snapshot", () => {
    expect(SESSIONS_LIST_FEATURE).toBe("sessions_list");
    expect(PAGE_SIZE).toBe(50);
    const list = make();
    const snapshot = list.getSnapshot();
    expect(snapshot).toEqual({ rows: [], loaded: false, loading: false, error: null, total: 0, counts: null, hasMore: false, supported: null });
    expect(list.getSnapshot()).toBe(snapshot);
    expect(api).not.toHaveBeenCalled();
  });

  it("loads only the first 50 summaries, with separate complete counts", async () => {
    const list = make();
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, loading: false, error: null, total: 8_000, hasMore: true, supported: true, counts: counts() });
    expect(list.getSnapshot().rows).toHaveLength(50);
    expect(requestParams()).toEqual([{ summary: "1", limit: "50" }]);
    expect(Object.fromEntries(params(aggregates.mock.calls[0][0]))).toEqual({ counts: "1" });
    expect(list.getSnapshot()).toBe(list.getSnapshot());
  });

  it("publishes rows immediately even when counts have not returned", async () => {
    const later = deferred<ListCounts>();
    aggregates.mockReturnValueOnce(later.promise);
    const onRows = vi.fn();
    const changed = vi.fn();
    const list = make(onRows);
    const unsubscribe = list.subscribe(changed);
    const loading = list.load();
    await tick();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, loading: true, counts: null });
    expect(onRows).toHaveBeenCalledTimes(1);
    const visible = list.getSnapshot().rows;
    later.resolve(counts());
    await loading;
    expect(list.getSnapshot().rows).toBe(visible);
    expect(onRows).toHaveBeenCalledTimes(1);
    expect(changed).toHaveBeenCalled();
    unsubscribe();
    changed.mockClear();
    list.reset();
    expect(changed).not.toHaveBeenCalled();
    expect(onRows).toHaveBeenLastCalledWith([]);
  });

  it("does not use counts revision as the initial or subsequent row checkpoint", async () => {
    const first = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(first.promise);
    const list = make();
    const loading = list.load();
    await tick();
    expect(list.getSnapshot()).toMatchObject({ loaded: false, counts: counts() });
    first.resolve(page(rows(50), { revision: "rows/one+", has_more: true, next_cursor: "frozen" }));
    await loading;
    pages.mockResolvedValueOnce(page([], { revision: "rows/two+" }));
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "rows/three+" }));
    await list.load();
    expect(requestParams().map((p) => p.since)).toEqual([undefined, "rows/one+", "rows/two+"]);
  });

  it("repeats identical encoded filters for counts, frozen pages and deltas", async () => {
    const filters: ListFilters = { state: "working", machine_id: "demo/a", project: "/demo/a + b", group: "g&1", source: "s:1", provider: "p?", q: "hello 日本語 + &" };
    const list = new SessionList<Row>("/agents/sessions?unused=kept&cursor=discard&state=discard", (r) => r.id, (a, b) => b.rank - a.rank);
    await list.setFilters(filters);
    pages.mockResolvedValueOnce(page(rows(50, 50)));
    await list.more();
    pages.mockResolvedValueOnce(page([], { revision: "delta" }));
    await list.load();
    for (const [path] of pages.mock.calls) {
      expect(path.startsWith("/agents/sessions?")).toBe(true);
      expect(Object.fromEntries(params(path))).toMatchObject({ ...filters, unused: "kept", summary: "1", limit: "50" });
    }
    expect(requestParams()[1].cursor).toBe("frozen/one+");
    expect(requestParams()[2].since).toBe("baseline");
    for (const [path] of aggregates.mock.calls) expect(Object.fromEntries(params(path))).toEqual({ ...filters, unused: "kept", counts: "1" });
  });

  it("canonicalizes empty filters and key order without reloading the same selection", async () => {
    const list = make();
    const filters = { q: "search", state: "idle", project: "" };
    await list.setFilters(filters);
    filters.q = "mutated by caller";
    await list.setFilters({ state: "idle", q: "search" });
    expect(pages).toHaveBeenCalledTimes(1);
    expect(requestParams()[0]).toMatchObject({ q: "search", state: "idle" });
    expect(requestParams()[0]).not.toHaveProperty("project");
    await list.setFilters({ q: "different" });
    expect(requestParams()[1]).toEqual({ q: "different", summary: "1", limit: "50" });
  });

  it("retains unchanged row, nested value, array and counts objects across deltas", async () => {
    const original = { ...row("a", 2), detail: { value: "same" } };
    pages.mockResolvedValueOnce(page([original, row("b", 1)]));
    const onRows = vi.fn();
    const list = make(onRows);
    await list.load();
    const before = list.getSnapshot();
    pages.mockResolvedValueOnce(page([{ title: "a", rank: 2, detail: { value: "same" }, id: "a" }], { revision: "delta" }));
    await list.load();
    expect(list.getSnapshot().rows).toBe(before.rows);
    expect(list.getSnapshot().rows[0]).toBe(original);
    expect(list.getSnapshot().counts).toBe(before.counts);
    expect(onRows).toHaveBeenCalledTimes(1);
    pages.mockResolvedValueOnce(page([row("b", 3, "changed")], { revision: "next" }));
    await list.load();
    expect(ids(list)).toEqual(["b", "a"]);
    expect(list.getSnapshot().rows[1]).toBe(original);
    expect(onRows).toHaveBeenCalledTimes(2);
  });

  it("treats composite identities and removals as opaque strings", async () => {
    type Runtime = { machine: string; session: string; rank: number };
    const identity = (r: Runtime) => JSON.stringify([r.machine, r.session]);
    const list = new SessionList<Runtime>("/sessions", identity, (a, b) => b.rank - a.rank);
    const a = { machine: "demo/a", session: "same/one", rank: 2 };
    const b = { machine: "demo/b", session: "same/one", rank: 1 };
    pages.mockResolvedValueOnce({ ...page(), sessions: [a, b] });
    await list.load();
    pages.mockResolvedValueOnce({ ...page([], { revision: "delta", remove: [identity(a)] }), sessions: [{ ...b, rank: 3 }] });
    await list.load();
    expect(list.getSnapshot().rows).toEqual([{ ...b, rank: 3 }]);
  });
});

describe("frozen pagination and bounded atomic deltas", () => {
  it("completes every delta page atomically, repeating the original since checkpoint", async () => {
    const list = make();
    await list.load();
    const before = list.getSnapshot().rows;
    const second = deferred<ListPage<Row>>();
    pages.mockResolvedValueOnce(page([row("0", 20_000, "changed")], { revision: "target", has_more: true, next_cursor: "delta/+" }));
    pages.mockReturnValueOnce(second.promise);
    const pending = list.load();
    await tick();
    expect(list.getSnapshot().rows).toBe(before);
    expect(requestParams().slice(1)).toEqual([
      { summary: "1", limit: "50", since: "baseline" },
      { summary: "1", limit: "50", since: "baseline", cursor: "delta/+" },
    ]);
    second.resolve(page([], { revision: "target", remove: ["1"], total: 7_999 }));
    await pending;
    expect(list.getSnapshot().rows[0].title).toBe("changed");
    expect(ids(list)).not.toContain("1");
    expect(list.getSnapshot().total).toBe(7_999);
    pages.mockResolvedValueOnce(page([], { revision: "next" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("target");
  });

  it("merges later frozen pages with unmaterialized updates/removals and never downgrades total/checkpoint", async () => {
    const list = make();
    await list.load();
    const oldFirst = list.getSnapshot().rows[0];
    pages.mockResolvedValueOnce(page([row("55", 9_945, "updated before paging"), row("new", 20_000)], { revision: "delta-one", remove: ["60"], total: 8_001 }));
    await list.load();
    expect(ids(list)).not.toContain("55");
    expect(list.getSnapshot().rows).toHaveLength(50);
    expect(list.getSnapshot().rows[0].id).toBe("new");
    pages.mockResolvedValueOnce(page(rows(50, 50), { total: 8_000 }));
    await list.more();
    expect(requestParams().at(-1)).toEqual({ summary: "1", limit: "50", cursor: "frozen/one+" });
    expect(ids(list)).not.toContain("60");
    expect(list.getSnapshot().rows.find((r) => r.id === "55")?.title).toBe("updated before paging");
    expect(ids(list).filter((id) => id === "55")).toHaveLength(1);
    expect(list.getSnapshot().rows.find((r) => r.id === "0")).toBe(oldFirst);
    expect(list.getSnapshot().total).toBe(8_001);
    pages.mockResolvedValueOnce(page([], { revision: "delta-two" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("delta-one");
  });

  it("retains tombstones and updates across successive deltas until the frozen view ends", async () => {
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "d1", remove: ["75"] }));
    await list.load();
    pages.mockResolvedValueOnce(page([row("76", 9_924, "new title")], { revision: "d2" }));
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 50), { has_more: true, next_cursor: "frozen-two" }));
    await list.more();
    expect(ids(list)).not.toContain("75");
    expect(list.getSnapshot().rows.find((r) => r.id === "76")?.title).toBe("new title");
    pages.mockResolvedValueOnce(page([row("75", 20_001, "returned")], { revision: "d3" }));
    await list.load();
    expect(list.getSnapshot().rows[0].title).toBe("returned");
  });

  it("keeps the visible prefix bounded even if all 8,000 identities change", async () => {
    const list = make();
    await list.load();
    const changed = rows(8_000).map((r) => ({ ...r, title: `${r.id} changed` }));
    pages.mockImplementation(async (path) => {
      const p = params(path);
      expect(p.get("since")).toBe("baseline");
      const start = Number(p.get("cursor") ?? "0");
      const end = start + 50;
      return page(changed.slice(start, end), { revision: "all-changed", has_more: end < changed.length, next_cursor: end < changed.length ? String(end) : undefined });
    });
    await list.load();
    expect(pages).toHaveBeenCalledTimes(161);
    expect(list.getSnapshot().rows).toHaveLength(50);
    expect(ids(list)).toEqual(rows(50).map((r) => r.id));
    expect(list.getSnapshot().rows.every((r) => r.title.endsWith("changed"))).toBe(true);
  });

  it("admits only newest delta rows into an expanded visible prefix", async () => {
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 50)));
    await list.more();
    const loaded = list.getSnapshot().rows[0];
    pages.mockResolvedValueOnce(page([row("old", -1), row("new", 20_000), { ...loaded, title: "updated" }], { revision: "delta" }));
    await list.load();
    expect(list.getSnapshot().rows).toHaveLength(100);
    expect(ids(list)[0]).toBe("new");
    expect(ids(list)).not.toContain("old");
    expect(list.getSnapshot().rows.find((r) => r.id === loaded.id)?.title).toBe("updated");
    // The completed frozen view has no more cursor, but the retained overlay can still expand lazily.
    expect(list.getSnapshot().hasMore).toBe(true);
    const calls = pages.mock.calls.length;
    await list.more();
    expect(list.getSnapshot().rows).toHaveLength(102);
    expect(list.getSnapshot().hasMore).toBe(false);
    expect(pages).toHaveBeenCalledTimes(calls);
  });

  it("handles zero-row baselines and upsert-after-remove without duplication", async () => {
    pages.mockResolvedValueOnce(page([], { total: 0 }));
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page([row("a")], { revision: "delta", total: 1, remove: ["a"] }));
    await list.load();
    expect(ids(list)).toEqual(["a"]);
  });

  it("deduplicates overlapping frozen identities and uses newest overlay rank", async () => {
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page([row("70", 20_000, "promoted")], { revision: "delta" }));
    await list.load();
    pages.mockResolvedValueOnce(page([row("0", 10_000), ...rows(50, 50)]));
    await list.more();
    expect(ids(list)[0]).toBe("70");
    expect(new Set(ids(list)).size).toBe(ids(list).length);
    expect(ids(list).filter((id) => id === "0")).toHaveLength(1);
  });

  it("resets the first page when the cumulative overlay exceeds 16,384 identities", async () => {
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "one", remove: ["earlier"] }));
    await list.load();
    pages.mockImplementation(async (path) => {
      const p = params(path);
      if (!p.has("since")) return page([row("fresh")], { revision: "fresh-baseline", total: 1 });
      expect(p.get("since")).toBe("one");
      const start = Number(p.get("cursor") ?? "0");
      const end = Math.min(start + 50, 16_384);
      return page([], { revision: "overflow", remove: Array.from({ length: end - start }, (_, i) => `gone-${start + i}`), has_more: end < 16_384, next_cursor: end < 16_384 ? String(end) : undefined });
    });
    await list.load();
    expect(ids(list)).toEqual(["fresh"]);
    expect(list.getSnapshot()).toMatchObject({ loaded: true, total: 1, error: null });
    expect(requestParams().at(-1)).not.toHaveProperty("since");
    pages.mockResolvedValueOnce(page([], { revision: "after-reset" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("fresh-baseline");
  });

  it("allows exactly 16,384 journal identities and repeated identities do not overflow", async () => {
    const list = make();
    await list.load();
    pages.mockImplementation(async (path) => {
      const start = Number(params(path).get("cursor") ?? "0");
      const end = Math.min(start + 50, 16_384);
      return page([], { revision: "large", remove: Array.from({ length: end - start }, (_, i) => `gone-${start + i}`), has_more: end < 16_384, next_cursor: end < 16_384 ? String(end) : undefined });
    });
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "same-identity", remove: ["gone-0"] }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("large");
    expect(list.getSnapshot().rows).toHaveLength(50);
  });
});

describe("retry, expiry and legacy capability handling", () => {
  it.each(["revision_expired", "cursor_expired"])("recovers %s with a fresh first baseline", async (code) => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(failure(409, code)).mockResolvedValueOnce(page([row("fresh")], { revision: "new-epoch", total: 1 }));
    await list.load();
    expect(ids(list)).toEqual(["fresh"]);
    expect(requestParams().slice(-2)).toEqual([{ summary: "1", limit: "50", since: "baseline" }, { summary: "1", limit: "50" }]);
    pages.mockResolvedValueOnce(page([], { revision: "next" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("new-epoch");
  });

  it("recovers expired frozen cursors, with fresh separate counts", async () => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(failure(409, "cursor_expired")).mockResolvedValueOnce(page([row("fresh")], { total: 1 }));
    await list.more();
    expect(ids(list)).toEqual(["fresh"]);
    expect(requestParams().at(-1)).not.toHaveProperty("cursor");
    expect(aggregates).toHaveBeenCalledTimes(2);
    expect(list.getSnapshot().loading).toBe(false);
  });

  it("swaps in a fresh view at least one page deeper on an expired cursor, never publishing fewer rows (#4410)", async () => {
    const lengths: number[] = [];
    const list = make((r) => lengths.push(r.length));
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 50), { has_more: true, next_cursor: "frozen/two" }));
    await list.more();
    expect(ids(list)).toHaveLength(100);
    pages.mockRejectedValueOnce(failure(409, "cursor_expired"))
      .mockResolvedValueOnce(page(rows(50), { revision: "fresh", total: 400, has_more: true, next_cursor: "fresh/1" }))
      .mockResolvedValueOnce(page(rows(50, 50), { revision: "fresh", total: 400, has_more: true, next_cursor: "fresh/2" }))
      .mockResolvedValueOnce(page(rows(50, 100), { revision: "fresh", total: 400, has_more: true, next_cursor: "fresh/3" }));
    lengths.length = 0;
    await list.more();
    expect(lengths).toEqual([150]);
    expect(ids(list)).toEqual(rows(150).map((r) => r.id));
    expect(list.getSnapshot()).toMatchObject({ total: 400, hasMore: true, error: null, loading: false });
    expect(requestParams().slice(-4).map((p) => p.cursor)).toEqual(["frozen/two", undefined, "fresh/1", "fresh/2"]);
    expect(aggregates).toHaveBeenCalledTimes(2);
    pages.mockResolvedValueOnce(page(rows(50, 150), { revision: "fresh", total: 400 }));
    await list.more();
    expect(requestParams().at(-1)?.cursor).toBe("fresh/3");
    expect(ids(list)).toHaveLength(200);
    pages.mockResolvedValueOnce(page([], { revision: "later" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("fresh");
  });

  it("keeps the rows on screen and stops when the fresh view changes revision mid-read", async () => {
    const list = make();
    await list.load();
    const before = list.getSnapshot().rows;
    pages.mockRejectedValueOnce(failure(409, "cursor_expired"))
      .mockResolvedValueOnce(page(rows(50), { revision: "fresh", has_more: true, next_cursor: "fresh/1" }))
      .mockResolvedValueOnce(page(rows(50, 50), { revision: "moved" }));
    await expect(list.more()).rejects.toThrow(t("paged-agents.inconsistent_page"));
    expect(pages).toHaveBeenCalledTimes(4);
    expect(list.getSnapshot().rows).toBe(before);
    expect(list.getSnapshot()).toMatchObject({ loaded: true, loading: false, hasMore: true });
    expect(list.getSnapshot().error).toBeTruthy();
  });

  it("does not retry expiry forever if the replacement baseline fails", async () => {
    const list = make();
    await list.load();
    const expired = failure(409, "revision_expired");
    pages.mockRejectedValue(expired);
    await expect(list.load()).rejects.toBe(expired);
    expect(pages).toHaveBeenCalledTimes(3);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, loading: false });
    expect(list.getSnapshot().error).toBeTruthy();
  });

  it("preserves loaded rows and cursor when more fails, retrying the same cursor", async () => {
    const list = make();
    await list.load();
    const before = list.getSnapshot().rows;
    const offline = new TypeError("Network unavailable");
    pages.mockRejectedValueOnce(offline);
    await expect(list.more()).rejects.toBe(offline);
    expect(list.getSnapshot().rows).toBe(before);
    expect(list.getSnapshot()).toMatchObject({ loaded: true, loading: false, hasMore: true, supported: true });
    expect(list.getSnapshot().error).toBeTruthy();
    pages.mockResolvedValueOnce(page(rows(50, 50)));
    await list.more();
    expect(requestParams().slice(-2).map((p) => p.cursor)).toEqual(["frozen/one+", "frozen/one+"]);
    expect(list.getSnapshot().error).toBeNull();
  });

  it("discards a partial delta after a later-page failure and retries the original checkpoint", async () => {
    const list = make();
    await list.load();
    const before = list.getSnapshot().rows;
    const error = failure(503, "offline");
    pages.mockResolvedValueOnce(page([row("0", 20_000, "partial")], { revision: "target", has_more: true, next_cursor: "second" })).mockRejectedValueOnce(error);
    await expect(list.load()).rejects.toBe(error);
    expect(list.getSnapshot().rows).toBe(before);
    pages.mockResolvedValueOnce(page([row("0", 20_000, "complete")], { revision: "new-target" }));
    await list.load();
    expect(requestParams().at(-1)).toEqual({ summary: "1", limit: "50", since: "baseline" });
    expect(list.getSnapshot().rows[0].title).toBe("complete");
  });

  it("reports failed counts separately without discarding a successful row baseline", async () => {
    const error = failure(401, "invalid_token");
    aggregates.mockRejectedValueOnce(error);
    const list = make();
    await expect(list.load()).rejects.toBe(error);
    expect(list.getSnapshot()).toMatchObject({ loaded: true, counts: null, supported: true });
    expect(list.getSnapshot().rows).toHaveLength(50);
    pages.mockResolvedValueOnce(page([], { revision: "after-count-retry" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("baseline");
    expect(list.getSnapshot().counts).toEqual(counts());
    expect(list.getSnapshot().error).toBeNull();
  });

  it("does not hide an independent authentication error behind row expiry recovery", async () => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(failure(409, "revision_expired"));
    const auth = failure(401, "invalid_token");
    aggregates.mockRejectedValueOnce(auth);
    const before = list.getSnapshot().rows;
    await expect(list.load()).rejects.toBe(auth);
    expect(list.getSnapshot().rows).toBe(before);
    expect(pages).toHaveBeenCalledTimes(2);
  });

  it("recovers a counts-side epoch expiry without treating counts as a baseline", async () => {
    const list = make();
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "delta" })).mockResolvedValueOnce(page([row("fresh")], { revision: "new-baseline", total: 1 }));
    aggregates.mockRejectedValueOnce(failure(409, "revision_expired")).mockResolvedValueOnce(counts("counts-after-restart"));
    await list.load();
    expect(ids(list)).toEqual(["fresh"]);
    expect(requestParams().at(-1)).not.toHaveProperty("since");
    pages.mockResolvedValueOnce(page([], { revision: "next" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("new-baseline");
  });

  it("does not treat counts success as a completed baseline when rows failed", async () => {
    pages.mockRejectedValueOnce(new TypeError("Network unavailable"));
    const list = make();
    await expect(list.load()).rejects.toThrow("Network unavailable");
    expect(list.getSnapshot()).toMatchObject({ loaded: false, counts: counts(), supported: null });
    await list.load();
    expect(requestParams().at(-1)).not.toHaveProperty("since");
  });

  it("uses all legacy sessions when the hub omits the bounded envelope", async () => {
    const legacy = rows(123);
    pages.mockResolvedValue({ sessions: legacy });
    aggregates.mockResolvedValue({ sessions: [row("counts-are-not-rows")] });
    const list = make();
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, supported: false, total: 123, counts: null, hasMore: false });
    expect(list.getSnapshot().rows).toEqual(legacy);
    const calls = pages.mock.calls.length;
    await list.more();
    expect(pages).toHaveBeenCalledTimes(calls);
    await list.load();
    expect(requestParams().at(-1)).not.toHaveProperty("since");
  });

  it.each([failure(404, "not_found"), failure(400, "unsupported"), failure(501, "unsupported_feature"), new ApiError(400, "Unsupported list API", {})])("marks unsupported API failures without an error", async (error) => {
    pages.mockRejectedValueOnce(error);
    const list = make();
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, loading: false, error: null, supported: false, hasMore: false });
  });

  it.each([failure(401, "unsupported"), failure(403, "forbidden"), failure(500, "internal"), failure(409, "conflict"), failure(400, "invalid_query"), new TypeError("Network unavailable")])("does not swallow genuine network, auth or query failures", async (error) => {
    pages.mockRejectedValueOnce(error);
    const list = make();
    await expect(list.load()).rejects.toBe(error);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, loading: false, supported: null });
    expect(list.getSnapshot().error).toBeTruthy();
    expect(pages).toHaveBeenCalledTimes(1);
  });

  it("keeps a counts-only unsupported result regardless of response order", async () => {
    const later = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(later.promise);
    aggregates.mockRejectedValueOnce(failure(404, "not_found"));
    const list = make();
    const loading = list.load();
    await tick();
    expect(list.getSnapshot().supported).toBe(false);
    later.resolve(page(rows(50)));
    await loading;
    expect(list.getSnapshot().supported).toBe(false);
    expect(list.getSnapshot().rows).toHaveLength(50);
  });

  it("rejects inconsistent/repeating delta cursors without checkpoint or partial-row advancement", async () => {
    const list = make();
    await list.load();
    const before = list.getSnapshot().rows;
    pages.mockResolvedValueOnce(page([row("new", 20_000)], { revision: "target", has_more: true, next_cursor: "repeat" }))
      .mockResolvedValueOnce(page([], { revision: "target", has_more: true, next_cursor: "repeat" }));
    await expect(list.load()).rejects.toThrow(t("paged-agents.repeated_cursor"));
    expect(list.getSnapshot().rows).toBe(before);
    pages.mockResolvedValueOnce(page([], { revision: "target", has_more: true, next_cursor: "second" }))
      .mockResolvedValueOnce(page([], { revision: "different" }));
    await expect(list.load()).rejects.toThrow(t("paged-agents.inconsistent_delta"));
    expect(list.getSnapshot().rows).toBe(before);
    pages.mockResolvedValueOnce(page([], { revision: "next" }));
    await list.load();
    expect(requestParams().at(-1)?.since).toBe("baseline");
  });

  it("rejects malformed bounded pages instead of looping or losing pagination", async () => {
    pages.mockResolvedValueOnce(page([], { has_more: true }));
    const list = make();
    await expect(list.load()).rejects.toThrow(t("paged-agents.invalid_page"));
    expect(list.getSnapshot().loaded).toBe(false);
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 50), { revision: "wrong-frozen-view" }));
    const before = list.getSnapshot().rows;
    await expect(list.more()).rejects.toThrow(t("paged-agents.inconsistent_page"));
    expect(list.getSnapshot().rows).toBe(before);
  });
});

describe("retry preserves the failed operation", () => {
  it("retries a delta even while the frozen list still has another page", async () => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(new Error("offline"));
    await expect(list.load()).rejects.toThrow("offline");
    pages.mockResolvedValueOnce(page([], { revision: "next" }));
    await list.retry();
    expect(requestParams().at(-1)?.since).toBe("baseline");
    expect(requestParams().at(-1)?.cursor).toBeUndefined();
  });
  it("retries a failed continuation with the same frozen cursor", async () => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(new Error("offline"));
    await expect(list.more()).rejects.toThrow("offline");
    await list.retry();
    expect(requestParams().at(-1)?.cursor).toBeDefined();
    expect(requestParams().at(-1)?.since).toBeUndefined();
  });
});

describe("summary projection and completed delta callbacks", () => {
  it("keeps the existing five-argument constructor compatible and defaults to summaries", async () => {
    const onRows = vi.fn();
    const normalize = vi.fn((id: string) => id.replace("wire:", ""));
    const list = new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank, onRows, normalize);
    expect(list.isSummary()).toBe(true);
    await list.load();
    pages.mockResolvedValueOnce(page([], { revision: "delta", remove: ["wire:0"] }));
    await list.load();
    expect(normalize).toHaveBeenCalledWith("wire:0");
    expect(ids(list)).not.toContain("0");
    expect(onRows).toHaveBeenCalled();
    expect(requestParams().every((p) => p.summary === "1")).toBe(true);
  });

  it("binds summary=0 to the first page, frozen cursor and every delta cursor", async () => {
    const list = make(undefined, { summary: false });
    const filters = { machine_id: "demo/a", q: "a + b" };
    expect(list.isSummary()).toBe(false);
    await list.setFilters(filters);
    pages.mockResolvedValueOnce(page(rows(50, 50), { has_more: true, next_cursor: "frozen/two+" }));
    await list.more();
    pages.mockResolvedValueOnce(page([], { revision: "target", has_more: true, next_cursor: "delta/+" }))
      .mockResolvedValueOnce(page([], { revision: "target" }));
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 100)));
    await list.more();
    expect(requestParams()).toEqual([
      { ...filters, summary: "0", limit: "50" },
      { ...filters, summary: "0", limit: "50", cursor: "frozen/one+" },
      { ...filters, summary: "0", limit: "50", since: "baseline" },
      { ...filters, summary: "0", limit: "50", since: "baseline", cursor: "delta/+" },
      { ...filters, summary: "0", limit: "50", cursor: "frozen/two+" },
    ]);
    for (const [path] of aggregates.mock.calls) expect(Object.fromEntries(params(path))).toEqual({ ...filters, counts: "1" });
  });

  it("switches projection by resetting only the baseline, preserving filters and not fetching", async () => {
    const list = make();
    await list.setFilters({ state: "working", project: "/demo/app" });
    const calls = vi.mocked(api).mock.calls.length;
    list.setSummary(false);
    expect(list.isSummary()).toBe(false);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, rows: [], counts: null, loading: false, error: null });
    expect(api).toHaveBeenCalledTimes(calls);
    await list.load();
    expect(requestParams().at(-1)).toEqual({ state: "working", project: "/demo/app", summary: "0", limit: "50" });
    list.reset();
    expect(list.isSummary()).toBe(false);
    await list.load();
    expect(requestParams().at(-1)?.summary).toBe("0");
    list.setSummary(true);
    expect(list.isSummary()).toBe(true);
    await list.load();
    expect(requestParams().at(-1)).toEqual({ state: "working", project: "/demo/app", summary: "1", limit: "50" });
  });

  it("does not reset, refetch or replace snapshots when the selected mode is unchanged", async () => {
    const options = { summary: false };
    const list = make(undefined, options);
    options.summary = true;
    expect(list.isSummary()).toBe(false);
    await list.load();
    const before = list.getSnapshot();
    const calls = vi.mocked(api).mock.calls.length;
    list.setSummary(false);
    expect(list.getSnapshot()).toBe(before);
    expect(api).toHaveBeenCalledTimes(calls);
    pages.mockResolvedValueOnce(page([], { revision: "delta" }));
    await list.load();
    expect(requestParams().at(-1)).toEqual({ summary: "0", limit: "50", since: "baseline" });
  });

  it("never calls onDelta for first pages, continuations or legacy complete lists", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    pages.mockResolvedValueOnce(page(rows(50, 50)));
    await list.more();
    expect(onDelta).not.toHaveBeenCalled();
    list.setSummary(false);
    pages.mockResolvedValueOnce({ sessions: rows(100) });
    await list.load();
    expect(list.getSnapshot().supported).toBe(false);
    expect(onDelta).not.toHaveBeenCalled();
  });

  it("emits only the completed delta's net changes, including unloaded identities and normalized removals", async () => {
    const onDelta = vi.fn();
    const normalize = vi.fn((id: string) => id.replace("wire:", ""));
    const list = make(undefined, { onDelta }, normalize);
    await list.load();
    pages.mockResolvedValueOnce(page([row("older-overlay", -10)], { revision: "previous", remove: ["wire:older-tombstone"] }));
    await list.load();
    onDelta.mockClear();
    const before = list.getSnapshot().rows;
    const last = deferred<ListPage<Row>>();
    pages.mockResolvedValueOnce(page([row("0", 20_000, "partial"), row("unloaded", -1, "partial"), row("will-remove", -2)], {
      revision: "target", has_more: true, next_cursor: "delta-next", remove: ["wire:readded", "wire:gone"],
    })).mockReturnValueOnce(last.promise);
    const pending = list.load();
    await tick();
    expect(onDelta).not.toHaveBeenCalled();
    expect(list.getSnapshot().rows).toBe(before);
    const finalRows = [row("0", 20_001, "final"), row("unloaded", -1, "final"), row("readded", -3)];
    last.resolve(page(finalRows, { revision: "target", remove: ["wire:will-remove", "wire:gone"] }));
    await pending;
    expect(onDelta).toHaveBeenCalledTimes(1);
    const [upsert, remove] = onDelta.mock.calls[0];
    expect(upsert).toEqual(expect.arrayContaining(finalRows));
    expect(upsert).toHaveLength(3);
    expect(remove).toEqual(expect.arrayContaining(["will-remove", "gone"]));
    expect(remove).toHaveLength(2);
    expect(upsert.map((r: Row) => r.id)).not.toContain("older-overlay");
    expect(remove).not.toContain("older-tombstone");
    expect(remove).not.toContain("readded");
    expect(ids(list)).not.toContain("unloaded");
    expect(list.getSnapshot().rows[0].title).toBe("final");
    expect(normalize).toHaveBeenCalledWith("wire:will-remove");
    pages.mockResolvedValueOnce(page([], { revision: "no-changes" }));
    await list.load();
    expect(onDelta).toHaveBeenLastCalledWith([], []);
    expect(requestParams().at(-1)?.since).toBe("target");
  });

  it("calls the consumer after the visible rows commit, without needing any loaded identity to change", async () => {
    const list = make(undefined, { onDelta: (upsert, remove) => {
      expect(list.getSnapshot().total).toBe(7_999);
      expect(list.getSnapshot().rows).toBe(before);
      expect(upsert).toEqual([row("unloaded", -10)]);
      expect(remove).toEqual(["unloaded-deleted"]);
    } });
    await list.load();
    const before = list.getSnapshot().rows;
    pages.mockResolvedValueOnce(page([row("unloaded", -10)], { revision: "delta", total: 7_999, remove: ["unloaded-deleted"] }));
    await list.load();
  });

  it("waits for counts success before committing the delta or notifying its consumer", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const before = list.getSnapshot().rows;
    const later = deferred<ListCounts>();
    aggregates.mockReturnValueOnce(later.promise);
    pages.mockResolvedValueOnce(page([row("0", 20_000, "new")], { revision: "target" }));
    const pending = list.load();
    await tick();
    expect(list.getSnapshot().rows).toBe(before);
    expect(onDelta).not.toHaveBeenCalled();
    later.resolve(counts("latest-counts"));
    await pending;
    expect(onDelta).toHaveBeenCalledExactlyOnceWith([row("0", 20_000, "new")], []);
    expect(list.getSnapshot().rows[0].title).toBe("new");
  });

  it("does not notify or advance checkpoint on counts failure, and retry replays all missed changes", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const before = list.getSnapshot().rows;
    const upsert = [row("0", 20_000, "complete")];
    pages.mockResolvedValueOnce(page(upsert, { revision: "target" }));
    const offline = failure(503, "offline");
    aggregates.mockRejectedValueOnce(offline);
    await expect(list.load()).rejects.toBe(offline);
    expect(onDelta).not.toHaveBeenCalled();
    expect(list.getSnapshot().rows).toBe(before);
    pages.mockResolvedValueOnce(page(upsert, { revision: "target" }));
    await list.retry();
    expect(requestParams().at(-1)?.since).toBe("baseline");
    expect(onDelta).toHaveBeenCalledExactlyOnceWith(upsert, []);
  });

  it("does not emit partial changes after later-page failure or malformed delta revision", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const error = failure(503, "offline");
    pages.mockResolvedValueOnce(page([row("partial", -1)], { revision: "target", has_more: true, next_cursor: "last" }))
      .mockRejectedValueOnce(error);
    await expect(list.load()).rejects.toBe(error);
    expect(onDelta).not.toHaveBeenCalled();
    pages.mockResolvedValueOnce(page([row("partial", -1)], { revision: "target", has_more: true, next_cursor: "last" }))
      .mockResolvedValueOnce(page([], { revision: "wrong" }));
    await expect(list.retry()).rejects.toThrow(t("paged-agents.inconsistent_delta"));
    expect(onDelta).not.toHaveBeenCalled();
    pages.mockResolvedValueOnce(page([row("complete", -1)], { revision: "retry" }));
    await list.retry();
    expect(onDelta).toHaveBeenCalledExactlyOnceWith([row("complete", -1)], []);
    expect(requestParams().at(-1)?.since).toBe("baseline");
  });

  it.each(["cursor_expired", "revision_expired"])("does not emit partial delta changes on %s recovery", async (code) => {
    const onDelta = vi.fn();
    const list = make(undefined, { summary: false, onDelta });
    await list.load();
    pages.mockResolvedValueOnce(page([row("partial", -1)], { revision: "target", has_more: true, next_cursor: "last" }))
      .mockRejectedValueOnce(failure(409, code))
      .mockResolvedValueOnce(page([row("fresh")], { revision: "new-baseline" }));
    await list.load();
    expect(onDelta).not.toHaveBeenCalled();
    expect(ids(list)).toEqual(["fresh"]);
    expect(requestParams().at(-1)).toEqual({ summary: "0", limit: "50" });
    pages.mockResolvedValueOnce(page([row("next", -1)], { revision: "next" }));
    await list.load();
    expect(onDelta).toHaveBeenCalledExactlyOnceWith([row("next", -1)], []);
  });

  it("does not notify about a successful delta discarded by counts-side expiry", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    pages.mockResolvedValueOnce(page([row("discarded", -1)], { revision: "target" }))
      .mockResolvedValueOnce(page([row("fresh")], { revision: "new-baseline" }));
    aggregates.mockRejectedValueOnce(failure(409, "revision_expired"));
    await list.load();
    expect(onDelta).not.toHaveBeenCalled();
    expect(ids(list)).toEqual(["fresh"]);
  });

  it("does not notify for journal overflow and the replacement baseline", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    pages.mockImplementation(async (path) => {
      const p = params(path);
      if (!p.has("since")) return page([row("fresh")], { revision: "fresh" });
      const start = Number(p.get("cursor") ?? "0");
      const end = Math.min(start + 50, 16_385);
      return page([], { revision: "overflow", remove: Array.from({ length: end - start }, (_, i) => `removed-${start + i}`), has_more: end < 16_385, next_cursor: end < 16_385 ? String(end) : undefined });
    });
    await list.load();
    expect(onDelta).not.toHaveBeenCalled();
    expect(ids(list)).toEqual(["fresh"]);
  });

  it.each(["reset", "filter", "summary"])("ignores old delta completion after %s switching", async (switchKind) => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const last = deferred<ListPage<Row>>();
    pages.mockResolvedValueOnce(page([row("partial", -1)], { revision: "target", has_more: true, next_cursor: "last" }))
      .mockReturnValueOnce(last.promise);
    const stale = list.load();
    await tick();
    if (switchKind === "reset") list.reset();
    else if (switchKind === "summary") list.setSummary(false);
    else await list.setFilters({ state: "idle" });
    const current = list.getSnapshot();
    last.resolve(page([row("stale-final", -1)], { revision: "target" }));
    await stale;
    expect(list.getSnapshot()).toBe(current);
    expect(onDelta).not.toHaveBeenCalled();
    if (switchKind !== "filter") await list.load();
    pages.mockResolvedValueOnce(page([row("current", -1)], { revision: "current" }));
    await list.load();
    expect(onDelta).toHaveBeenCalledExactlyOnceWith([row("current", -1)], []);
  });

  it("ignores a fully prepared delta after mode switching while counts are pending", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const later = deferred<ListCounts>();
    aggregates.mockReturnValueOnce(later.promise);
    pages.mockResolvedValueOnce(page([row("stale", -1)], { revision: "target" }));
    const pending = list.load();
    await tick();
    list.setSummary(false);
    const current = list.getSnapshot();
    later.resolve(counts("stale-counts"));
    await pending;
    expect(list.getSnapshot()).toBe(current);
    expect(onDelta).not.toHaveBeenCalled();
  });

  it("ignores failures from the old projection without notifying or polluting the new baseline", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    const staleRows = deferred<ListPage<Row>>();
    const staleCounts = deferred<ListCounts>();
    pages.mockReturnValueOnce(staleRows.promise);
    aggregates.mockReturnValueOnce(staleCounts.promise);
    const old = list.load();
    await tick();
    list.setSummary(false);
    await list.load();
    const current = list.getSnapshot();
    staleRows.reject(failure(409, "revision_expired"));
    staleCounts.reject(failure(401, "invalid_token"));
    await old;
    expect(list.getSnapshot()).toBe(current);
    expect(onDelta).not.toHaveBeenCalled();
    expect(list.getSnapshot().error).toBeNull();
    expect(requestParams().at(-1)).toEqual({ summary: "0", limit: "50" });
  });

  it("invalidates queued reads before switching projection, without automatically fetching the new mode", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    const pending = list.load();
    list.invalidate();
    list.setSummary(false);
    await pending;
    await tick();
    expect(api).not.toHaveBeenCalled();
    expect(onDelta).not.toHaveBeenCalled();
    await list.load();
    expect(requestParams()).toEqual([{ summary: "0", limit: "50" }]);
  });

  it("guards the consumer when onRows synchronously resets during delta publication", async () => {
    const onDelta = vi.fn();
    let shouldReset = false;
    const list = make(() => { if (shouldReset) { shouldReset = false; list.reset(); } }, { onDelta });
    await list.load();
    shouldReset = true;
    pages.mockResolvedValueOnce(page([row("0", 20_000, "new")], { revision: "target" }));
    await list.load();
    expect(list.getSnapshot().loaded).toBe(false);
    expect(onDelta).not.toHaveBeenCalled();
  });

  it("guards the consumer when a subscriber synchronously changes projection during publication", async () => {
    const onDelta = vi.fn();
    const list = make(undefined, { onDelta });
    await list.load();
    let shouldReset = true;
    list.subscribe(() => {
      if (shouldReset && list.getSnapshot().rows[0]?.title === "new") {
        shouldReset = false;
        list.setSummary(false);
      }
    });
    pages.mockResolvedValueOnce(page([row("0", 20_000, "new")], { revision: "target" }));
    await list.load();
    expect(list.isSummary()).toBe(false);
    expect(list.getSnapshot().loaded).toBe(false);
    expect(onDelta).not.toHaveBeenCalled();
  });

  it("permits consumer-driven mode switching without an old callback or trailing read leaking through", async () => {
    const onDelta = vi.fn(() => { list.setSummary(false); });
    const list = make(undefined, { onDelta });
    await list.load();
    pages.mockResolvedValueOnce(page([row("new", -1)], { revision: "target" }));
    await list.load();
    expect(onDelta).toHaveBeenCalledTimes(1);
    expect(list.isSummary()).toBe(false);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, loading: false, rows: [] });
    expect(pages).toHaveBeenCalledTimes(2);
    await list.load();
    expect(requestParams().at(-1)).toEqual({ summary: "0", limit: "50" });
    expect(onDelta).toHaveBeenCalledTimes(1);
  });
});

describe("generation isolation and inflight coalescing", () => {
  it("coalesces parallel loads and more calls into one inflight request", async () => {
    const first = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(first.promise);
    const list = make();
    const a = list.load();
    expect(list.load()).toBe(a);
    await tick();
    expect(list.load()).toBe(a);
    first.resolve(page(rows(50), { has_more: true, next_cursor: "frozen" }));
    await a;
    expect(pages).toHaveBeenCalledTimes(1);
    const next = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(next.promise);
    const b = list.more();
    expect(list.more()).toBe(b);
    await tick();
    expect(list.more()).toBe(b);
    next.resolve(page(rows(50, 50)));
    await b;
    expect(pages).toHaveBeenCalledTimes(2);
  });

  it("coalesces invalidation bursts and schedules one trailing delta during an inflight read", async () => {
    const list = make();
    await list.load();
    const pending = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(page([], { revision: "latest" }));
    list.invalidate(); list.invalidate(); list.invalidate();
    await tick();
    expect(pages).toHaveBeenCalledTimes(2);
    list.invalidate(); list.invalidate();
    await tick();
    expect(pages).toHaveBeenCalledTimes(2);
    pending.resolve(page([], { revision: "intermediate" }));
    await list.load();
    expect(pages).toHaveBeenCalledTimes(3);
    expect(requestParams().at(-1)?.since).toBe("intermediate");
  });

  it("records fire-and-forget invalidation failures without unhandled rejection", async () => {
    const list = make();
    await list.load();
    pages.mockRejectedValueOnce(new TypeError("Network unavailable"));
    list.invalidate();
    await tick();
    expect(list.getSnapshot().error).toBeTruthy();
    expect(list.getSnapshot().loading).toBe(false);
    expect(list.getSnapshot().rows).toHaveLength(50);
  });

  it("serializes more and refresh, keeping each request's cursor/checkpoint semantics", async () => {
    const list = make();
    await list.load();
    const next = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(next.promise).mockResolvedValueOnce(page([], { revision: "delta" }));
    const paging = list.more();
    await tick();
    list.invalidate();
    await tick();
    expect(pages).toHaveBeenCalledTimes(2);
    next.resolve(page(rows(50, 50)));
    await paging;
    expect(requestParams().slice(-2)).toEqual([
      { summary: "1", limit: "50", cursor: "frozen/one+" },
      { summary: "1", limit: "50", since: "baseline" },
    ]);
    expect(list.getSnapshot().rows).toHaveLength(100);
  });

  it("queues more behind a delta and then merges the page with its completed overlay", async () => {
    const list = make();
    await list.load();
    const delta = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(delta.promise).mockResolvedValueOnce(page(rows(50, 50)));
    const loading = list.load();
    await tick();
    expect(list.more()).toBe(loading);
    delta.resolve(page([], { revision: "target", remove: ["75"] }));
    await loading;
    expect(ids(list)).not.toContain("75");
    expect(requestParams().at(-1)?.cursor).toBe("frozen/one+");
  });

  it("ignores old rows/counts after filter switching and starts new filters without waiting", async () => {
    const oldRows = deferred<ListPage<Row>>();
    const oldCounts = deferred<ListCounts>();
    pages.mockReturnValueOnce(oldRows.promise).mockResolvedValueOnce(page([row("new-filter")], { revision: "new", total: 1 }));
    aggregates.mockReturnValueOnce(oldCounts.promise).mockResolvedValueOnce(counts("new-counts"));
    const list = make();
    const old = list.setFilters({ state: "working" });
    await tick();
    await list.setFilters({ state: "idle" });
    const current = list.getSnapshot();
    oldRows.resolve(page([row("stale")], { revision: "stale" }));
    oldCounts.resolve(counts("stale-counts"));
    await old;
    expect(list.getSnapshot()).toBe(current);
    expect(ids(list)).toEqual(["new-filter"]);
    expect(list.getSnapshot().counts?.revision).toBe("new-counts");
    pages.mockResolvedValueOnce(page([], { revision: "newer" }));
    await list.load();
    expect(requestParams().at(-1)).toMatchObject({ state: "idle", since: "new" });
  });

  it("ignores errors and later delta pages from a reset generation", async () => {
    const list = make();
    await list.load();
    const second = deferred<ListPage<Row>>();
    const count = deferred<ListCounts>();
    pages.mockResolvedValueOnce(page([row("stale")], { revision: "old-target", has_more: true, next_cursor: "second" })).mockReturnValueOnce(second.promise);
    aggregates.mockReturnValueOnce(count.promise);
    const old = list.load();
    await tick();
    list.reset();
    const empty = list.getSnapshot();
    second.reject(failure(401, "invalid_token"));
    count.reject(new TypeError("Network unavailable"));
    await old;
    expect(list.getSnapshot()).toBe(empty);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, loading: false, error: null, counts: null, supported: null });
    await list.load();
    expect(requestParams().at(-1)).not.toHaveProperty("since");
  });

  it("cancels queued starts and invalidations on reset before any request is made", async () => {
    const list = make();
    const queued = list.load();
    list.invalidate();
    list.reset();
    const empty = list.getSnapshot();
    await queued;
    await tick();
    expect(api).not.toHaveBeenCalled();
    expect(list.getSnapshot()).toBe(empty);
    await list.load();
    expect(pages).toHaveBeenCalledTimes(1);
  });

  it("does not let stale completion stop loading or clear the new generation's promise", async () => {
    const first = deferred<ListPage<Row>>();
    const second = deferred<ListPage<Row>>();
    pages.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const list = make();
    const old = list.load();
    await tick();
    list.reset();
    const current = list.load();
    await tick();
    first.resolve(page([row("stale")]));
    await old;
    expect(list.getSnapshot().loading).toBe(true);
    expect(list.load()).toBe(current);
    second.resolve(page([row("current")]));
    await current;
    expect(ids(list)).toEqual(["current"]);
    expect(list.getSnapshot().loading).toBe(false);
  });
});

describe("counts read with the page (sessions_page_counts, #4858)", () => {
  const withCounts = (sessions: Row[], patch: Partial<ListPage<Row>> = {}, revision = "page") => page(sessions, { counts: counts(revision), ...patch });

  it("reads a first page and each delta in one request, the counts riding on the page", async () => {
    pages.mockResolvedValueOnce(withCounts(rows(50), { has_more: true, next_cursor: "frozen/one+" }, "first"));
    const list = make(undefined, { pageCounts: () => true });
    expect(list.usesPageCounts()).toBe(true);
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, total: 8_000, counts: counts("first") });
    pages.mockResolvedValueOnce(withCounts([row("new", 20_000)], { revision: "next" }, "next"));
    await list.load();
    expect(ids(list)[0]).toBe("new");
    expect(list.getSnapshot().counts).toEqual(counts("next"));
    expect(requestParams()).toEqual([
      { summary: "1", limit: "50", with_counts: "1" },
      { summary: "1", limit: "50", since: "baseline", with_counts: "1" },
    ]);
    expect(aggregates).not.toHaveBeenCalled();
    expect(api).toHaveBeenCalledTimes(2);
  });

  it("takes two requests per refresh without the feature, as before", async () => {
    const list = make();
    expect(list.usesPageCounts()).toBe(false);
    await list.load();
    pages.mockResolvedValueOnce(page([row("new", 20_000)], { revision: "next" }));
    await list.load();
    expect(api).toHaveBeenCalledTimes(4);
    expect(requestParams().every((query) => !("with_counts" in query))).toBe(true);
    expect(aggregates).toHaveBeenCalledTimes(2);
  });

  it("asks for counts on a delta's first page only, with the filters it reads", async () => {
    const list = make(undefined, { pageCounts: () => true });
    pages.mockResolvedValueOnce(withCounts(rows(50)));
    await list.setFilters({ state: "working" });
    pages
      .mockResolvedValueOnce(withCounts([row("a", 20_000)], { revision: "next", has_more: true, next_cursor: "delta/two" }, "next"))
      .mockResolvedValueOnce(page([row("b", 19_999)], { revision: "next" }));
    await list.load();
    expect(ids(list).slice(0, 2)).toEqual(["a", "b"]);
    expect(requestParams().slice(1)).toEqual([
      { state: "working", summary: "1", limit: "50", since: "baseline", with_counts: "1" },
      { state: "working", summary: "1", limit: "50", since: "baseline", cursor: "delta/two" },
    ]);
    expect(list.getSnapshot().counts).toEqual(counts("next"));
    expect(aggregates).not.toHaveBeenCalled();
  });

  it("falls back to the separate counts read when a page comes back without valid counts", async () => {
    const list = make(undefined, { pageCounts: () => true });
    pages.mockResolvedValueOnce(page(rows(10)));
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ loaded: true, counts: counts() });
    pages.mockResolvedValueOnce(page([], { revision: "next", counts: { total: 1 } as ListCounts }));
    aggregates.mockResolvedValueOnce(counts("separate"));
    await list.load();
    expect(list.getSnapshot().counts).toEqual(counts("separate"));
    expect(aggregates.mock.calls.map(([path]) => Object.fromEntries(params(path)))).toEqual([{ counts: "1" }, { counts: "1" }]);
  });

  it("keeps a failed page's checkpoint so the retry sees the same changes", async () => {
    const list = make(undefined, { pageCounts: () => true });
    pages.mockResolvedValueOnce(withCounts(rows(10)));
    await list.load();
    pages.mockRejectedValueOnce(new Error("offline"));
    await expect(list.load()).rejects.toThrow("offline");
    expect(list.getSnapshot()).toMatchObject({ error: "offline", counts: counts("page") });
    pages.mockResolvedValueOnce(withCounts([row("new", 20_000)], { revision: "next" }, "next"));
    await list.retry();
    expect(requestParams().slice(1).map((query) => query.since)).toEqual(["baseline", "baseline"]);
    expect(list.getSnapshot()).toMatchObject({ error: null, counts: counts("next") });
    expect(aggregates).not.toHaveBeenCalled();
  });
});
