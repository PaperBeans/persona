import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { SessionList, type ListCounts, type ListPage } from "./session-list";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

type Row = { id: string; rank: number };
const makePage = (ids: string[], revision = "r1"): ListPage<Row> => ({
  sessions: ids.map((id, i) => ({ id, rank: ids.length - i })),
  remove: [],
  has_more: false,
  revision,
  total: ids.length,
});
const makeCounts = (total: number): ListCounts => ({
  total,
  machine: {},
  project: {},
  group: {},
  state: {},
  revision: "r1",
});

function tick() {
  return new Promise((r) => setTimeout(r, 0));
}

describe("SessionList request cancellation and scheduling", () => {
  beforeEach(() => {
    vi.mocked(api).mockReset();
  });

  it("aborts in-flight page and counts requests on reset without recording an error", async () => {
    const signals: AbortSignal[] = [];
    vi.mocked(api).mockImplementation((_path, init) => {
      const signal = init?.signal;
      if (signal) signals.push(signal);
      return new Promise((_resolve, reject) => {
        signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      }) as ReturnType<typeof api>;
    });

    const list = new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank);
    const pending = list.load();
    await tick();

    expect(signals).toHaveLength(2);
    expect(signals.every((s) => !s.aborted)).toBe(true);

    list.reset();
    await pending;

    expect(signals.every((s) => s.aborted)).toBe(true);
    expect(list.getSnapshot()).toMatchObject({ loaded: false, loading: false, error: null, rows: [] });
  });

  it("aborts the previous filter read when setFilters switches scope", async () => {
    const calls: { path: string; signal?: AbortSignal | null; resolve: (v: unknown) => void }[] = [];
    vi.mocked(api).mockImplementation((path, init) => new Promise((resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      calls.push({ path: String(path), signal: init?.signal, resolve });
    }) as ReturnType<typeof api>);

    const list = new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank, undefined, undefined, { counts: false });
    const first = list.setFilters({ state: "working" });
    await tick();
    expect(calls).toHaveLength(1);
    expect(calls[0]!.signal?.aborted).toBe(false);

    const second = list.setFilters({ state: "unread" });
    await tick();
    expect(calls[0]!.signal?.aborted).toBe(true);
    expect(calls).toHaveLength(2);
    expect(calls[1]!.signal?.aborted).toBe(false);

    calls[1]!.resolve(makePage(["u1"]));
    await Promise.all([first, second]);
    expect(list.getSnapshot().rows.map((r) => r.id)).toEqual(["u1"]);
    expect(list.getSnapshot().error).toBeNull();
  });

  it("cancels a scheduled refresh cleanly if reset happens before the gate runs it", async () => {
    let releaseGate!: () => void;
    let scheduledSignal: AbortSignal | undefined;
    const schedule = vi.fn((run: () => Promise<void>, signal: AbortSignal) => {
      scheduledSignal = signal;
      return new Promise<void>((resolve, reject) => {
        const onAbort = () => resolve();
        signal.addEventListener("abort", onAbort, { once: true });
        releaseGate = () => {
          signal.removeEventListener("abort", onAbort);
          if (signal.aborted) {
            resolve();
            return;
          }
          void run().then(resolve, reject);
        };
      });
    });

    vi.mocked(api).mockImplementation((path) => Promise.resolve(
      String(path).includes("counts=1") ? makeCounts(1) : makePage(["a1"]),
    ) as ReturnType<typeof api>);

    const list = new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank, undefined, undefined, { schedule, counts: false });
    const pending = list.load();
    await tick();

    expect(schedule).toHaveBeenCalledTimes(1);
    expect(api).not.toHaveBeenCalled();

    list.reset();
    expect(scheduledSignal?.aborted).toBe(true);
    releaseGate();
    await pending;

    expect(api).not.toHaveBeenCalled();
    expect(list.getSnapshot().error).toBeNull();
  });
});
