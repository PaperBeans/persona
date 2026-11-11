// @vitest-environment jsdom
// #3713: a to-do whose agent never started (the hub closes it, with its start error) waits on the owner in Needs you. Its
// row reads open, not done, and says why in red; ticking it closes it for good (`done: true`, the hub clears the start
// error). One whose work finished in a related to-do (the same plan) is history: Just finished, ticked, nothing asked.
// The hub is faked; the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const api = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({}));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

const { getState } = await import("../lib/store");
const { TodosView } = await import("./Todos");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();
const failed = (id: string, title: string, plan?: string): Todo =>
  ({ id, title, plan_id: plan, origin: "conv", created_at: iso(7_200_000), updated_at: iso(3_600_000), done_at: iso(3_600_000), start_error: "machine offline", agents: [] }) as Todo;
const finished: Todo = { id: "done", title: "Verify the panel", plan_id: "p", origin: "conv", created_at: iso(7_200_000), updated_at: iso(1_800_000), done_at: iso(1_800_000), done_by: "review", agents: [] } as Todo;

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };

beforeEach(async () => {
  api.mockReset();
  api.mockImplementation(async () => ({}));
  seed({ todos: [failed("waits", "Rotate the keys"), failed("gone", "Deploy the panel", "p"), finished], todosLoaded: true, sessions: {}, interactions: [], answered: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<TodosView />));
  await flush();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  seed(before);
});

const section = (name: string) => host.querySelector(`[data-testid=todo-section][data-section=${name}]`);
const row = (id: string) => host.querySelector<HTMLElement>(`[data-testid=todo-triage-row][data-id=${id}]`);

describe("a to-do whose agent never started", () => {
  it("waits in Needs you reading open, says why in red, and ticking it closes it", async () => {
    const waits = row("waits")!;
    expect(section("waiting")?.contains(waits)).toBe(true);
    expect(waits.querySelector(".line-through")).toBeNull();
    expect(waits.querySelector("[data-testid=todo-start-failed]")?.textContent).toContain("machine offline");
    const box = waits.querySelector<HTMLButtonElement>("button[aria-label]")!;
    await act(async () => box.click());
    await flush();
    const patch = api.mock.calls.find(([path, init]) => path.includes("/todos/waits") && (init as { method?: string })?.method === "PATCH");
    expect((patch?.[1] as { json?: unknown })?.json).toEqual({ done: true });
  });

  it("goes with the work that finished in its row: Just finished, behind it, done", () => {
    expect(section("waiting")?.querySelector("[data-id=gone]")).toBeNull();
    const r = row("done")!;
    expect(section("just_done")?.contains(r)).toBe(true);
    expect(r.getAttribute("data-members")).toBe("2");
  });
});
