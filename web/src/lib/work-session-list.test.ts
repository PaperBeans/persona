import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";
import { PAGE_SIZE } from "./session-list";
import { WorkSessionList, workSessionCountsPath, workSessionQuery } from "./work-session-list";
import type { WorkSession } from "./work-session-data";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

const query = workSessionQuery({ device: ["two", "one", "one"], project: ["git:example"], harness: ["codex"] }, "  find me  ", "offline",
  { groupBy: ["project", "harness", "device"], sort: "created", direction: "asc" });
const facets = { device: [{ id: "one", name: "One", count: 52 }], project: [], harness: [] };
const groups = [{ path: ["git:example"], count: 52 }];
const row = (id: number): WorkSession => ({
  machine_id: "one", session_id: String(id), title: String(id), status: "offline", cwd: "/demo",
  updated_at: new Date(Date.UTC(2026, 0, id + 1)).toISOString(),
});
const rows = (n: number, start = 0) => Array.from({ length: n }, (_, i) => row(start + i));
const page = (sessions: WorkSession[], revision: string, next_cursor?: string) => ({
  sessions, revision, next_cursor, has_more: !!next_cursor, total: 52, remove: [], facets, groups,
});
const requestParams = () => vi.mocked(api).mock.calls.map(([url]) => new URL(url, "https://example.com").searchParams);
const more = "frozen/cursor+";
const mock = vi.mocked(api);
beforeEach(() => mock.mockReset());

describe("WorkSessionList", () => {
  it("keeps the Hub's ordered pages and full group counts while replaying the same query on each cursor", async () => {
    const first = rows(PAGE_SIZE), last = rows(2, PAGE_SIZE);
    mock.mockResolvedValueOnce(page(first, "r", more)).mockResolvedValueOnce(page(last, "r"));
    const list = new WorkSessionList(query);
    await list.load();
    expect(list.getSnapshot()).toMatchObject({ rows: first, total: 52, hasMore: true, dimensions: { facets, groups } });
    await list.more();
    expect(list.getSnapshot()).toMatchObject({ rows: [...first, ...last], total: 52, hasMore: false });
    const [a, b] = requestParams();
    expect(b.get("cursor")).toBe(more);
    b.delete("cursor");
    expect(a.toString()).toBe(b.toString());
    expect(a.has("agents_only")).toBe(false);
    expect(a.getAll("machine_id")).toEqual(["one", "two"]);
    expect(Object.fromEntries(a)).toMatchObject({
      facets: "1", scope: "work", state: "offline", q: "find me", project_id: "git:example",
      harness: "codex", group_by: "project,harness,device", sort: "created", direction: "asc",
    });
  });

  it("replaces a multi-page prefix atomically after a Hub hint without issuing a delta", async () => {
    mock.mockResolvedValueOnce(page(rows(PAGE_SIZE), "old", more)).mockResolvedValueOnce(page(rows(2, PAGE_SIZE), "old"));
    const list = new WorkSessionList(query);
    await list.load();
    await list.more();
    const before = list.getSnapshot().rows;
    let resolveSecond!: (value: unknown) => void;
    const second = new Promise((resolve) => { resolveSecond = resolve; });
    mock.mockResolvedValueOnce(page(rows(PAGE_SIZE, 100), "new", "fresh")).mockReturnValueOnce(second as ReturnType<typeof api>);
    list.invalidate();
    await vi.waitFor(() => expect(mock).toHaveBeenCalledTimes(4));
    expect(list.getSnapshot().rows).toBe(before);
    resolveSecond(page(rows(2, 150), "new"));
    await list.load();
    expect(list.getSnapshot().rows).toEqual(rows(52, 100));
    expect(requestParams().slice(2).map((p) => [p.get("since"), p.get("cursor")])).toEqual([[null, null], [null, "fresh"]]);
  });

  it("recovers an expired next cursor with a new frozen prefix and rejects missing metadata", async () => {
    mock.mockResolvedValueOnce(page(rows(PAGE_SIZE), "old", more))
      .mockRejectedValueOnce(new ApiError(409, "expired", { code: "cursor_expired" }))
      .mockResolvedValueOnce(page(rows(PAGE_SIZE, 10), "new", "fresh"))
      .mockResolvedValueOnce(page(rows(2, 60), "new"));
    const list = new WorkSessionList(query);
    await list.load();
    await list.more();
    expect(list.getSnapshot().rows).toEqual(rows(52, 10));
    mock.mockResolvedValueOnce({ ...page(rows(1), "legacy"), facets: undefined });
    await expect(new WorkSessionList(query).load()).rejects.toThrow();
  });

  it("omits only the enumerated filter and strips presentation choices from full counts", () => {
    const search = (except: "project" | "state") => new URL(workSessionCountsPath(query, except), "https://example.com").searchParams;
    const project = search("project"), state = search("state");
    expect(project.getAll("machine_id")).toEqual(["one", "two"]);
    expect(project.get("state")).toBe("offline");
    expect(project.has("project_id")).toBe(false);
    expect(state.get("project_id")).toBe("git:example");
    expect(state.has("state")).toBe(false);
    expect(state.get("scope")).toBe("work");
    expect(state.has("agents_only")).toBe(false);
    expect(state.has("sort")).toBe(false);
    expect(state.has("direction")).toBe(false);
    expect(state.has("group_by")).toBe(false);
  });
});
