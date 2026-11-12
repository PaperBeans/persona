// @vitest-environment jsdom
// #5707: Got it on a Just finished row files the to-do under Done at once (before the hub answers), the hub keeps the mark
// (PUT …/seen) and the toast's Undo takes it off (DELETE). Opening a Just finished to-do and leaving it counts as seeing
// it, without a word. A hub without `todo_seen` gets neither. Unfinished to-dos start open, above Just finished. The hub
// is faked; the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
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
const { Toasts } = await import("../ui/atoms");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();
const notes: Todo = { id: "notes", title: "Write the release notes", origin: "conv", created_at: iso(7_200_000), updated_at: iso(3_600_000), done_at: iso(3_600_000), agents: [] } as Todo;
const nas: Todo = { id: "nas", title: "Back up the NAS", created_at: iso(7_200_000), updated_at: iso(3_600_000), done_at: null, agents: [] } as Todo;

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };

async function render(features: string[], selectedId?: string) {
  seed({ todos: [notes, nas], todosLoaded: true, sessions: {}, interactions: [], answered: {}, features });
  await act(async () => root.render(<><TodosView selectedId={selectedId} /><Toasts /></>));
  await flush();
}

beforeEach(() => {
  api.mockReset();
  api.mockImplementation(async (path: string, init?: unknown) => {
    const method = (init as { method?: string } | undefined)?.method;
    if (path === "/todos/notes/seen") return { todo: { ...notes, seen_at: method === "PUT" ? new Date().toISOString() : undefined } };
    return {};
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  seed(before);
});

const section = (name: string) => host.querySelector(`[data-testid=todo-section][data-section=${name}]`);
const sections = () => [...host.querySelectorAll<HTMLElement>("[data-testid=todo-section]")].map((s) => s.dataset.section);
const seenCalls = (method: string) => api.mock.calls.filter(([path, init]) => path === "/todos/notes/seen" && (init as { method?: string })?.method === method);

describe("seen to-dos (#5707)", () => {
  it("lists the unfinished to-do open above Just finished", async () => {
    await render(["todo_seen"]);
    expect(sections()).toEqual(["other", "just_done"]);
    expect(section("other")?.querySelector("button[aria-expanded]")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("Got it files the row under Done at once, the hub keeps it, and Undo puts it back", async () => {
    await render(["todo_seen"]);
    const gotIt = section("just_done")!.querySelector<HTMLButtonElement>("[data-testid=todo-seen]")!;
    expect(gotIt.textContent).toBe(t("todos.seen"));
    await act(async () => gotIt.click());
    // moved before the hub's answer is in
    expect(section("just_done")).toBeNull();
    expect(section("done")?.getAttribute("data-todos")).toBe("1");
    await flush();
    expect(seenCalls("PUT")).toHaveLength(1);
    expect(getState().todos.find((x) => x.id === "notes")?.seen_at).toBeTruthy();

    const undo = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("lib-agents.undo"))!;
    expect(document.body.textContent).toContain(t("todos.seen_moved"));
    await act(async () => undo.click());
    await flush();
    expect(seenCalls("DELETE")).toHaveLength(1);
    expect(getState().todos.find((x) => x.id === "notes")?.seen_at ?? null).toBeNull();
    expect(section("just_done")?.querySelector("[data-testid=todo-triage-row][data-id=notes]")).not.toBeNull();
  });

  it("puts the row back and says so when the hub refuses", async () => {
    api.mockImplementation(async (path: string) => {
      if (path === "/todos/notes/seen") throw new Error("hub unavailable");
      return {};
    });
    await render(["todo_seen"]);
    await act(async () => section("just_done")!.querySelector<HTMLButtonElement>("[data-testid=todo-seen]")!.click());
    await flush();
    expect(section("just_done")?.querySelector("[data-id=notes]")).not.toBeNull();
    expect(document.body.textContent).toContain("hub unavailable");
  });

  it("opening a Just finished to-do and leaving it marks it seen, quietly", async () => {
    await render(["todo_seen"], "notes");
    // still where it was while it is open
    expect(section("just_done")?.querySelector("[data-id=notes]")).not.toBeNull();
    expect(seenCalls("PUT")).toHaveLength(0);
    // toasts outlive a test: count what is said from here on
    const said = () => (document.body.textContent ?? "").split(t("todos.seen_moved")).length;
    const saidBefore = said();
    await act(async () => root.render(<><TodosView /><Toasts /></>));
    await flush();
    expect(seenCalls("PUT")).toHaveLength(1);
    expect(said()).toBe(saidBefore);
    // an open one is no Just finished: opening and leaving it marks nothing
    api.mockClear();
    await act(async () => root.render(<><TodosView selectedId="nas" /><Toasts /></>));
    await act(async () => root.render(<><TodosView /><Toasts /></>));
    await flush();
    expect(api.mock.calls.filter(([path]) => path.endsWith("/seen"))).toHaveLength(0);
  });

  it("an Undo stands: leaving a to-do whose Got it was undone marks nothing", async () => {
    await render(["todo_seen"], "notes");
    await act(async () => section("just_done")!.querySelector<HTMLButtonElement>("[data-testid=todo-seen]")!.click());
    await flush();
    const undo = [...document.querySelectorAll<HTMLButtonElement>("button")].filter((b) => b.textContent === t("lib-agents.undo")).pop()!;
    await act(async () => undo.click());
    await flush();
    expect(seenCalls("PUT")).toHaveLength(1);
    expect(seenCalls("DELETE")).toHaveLength(1);
    await act(async () => root.render(<><TodosView /><Toasts /></>));
    await flush();
    expect(seenCalls("PUT")).toHaveLength(1);
  });

  it("offers nothing when the hub keeps no seen marks", async () => {
    await render(["locale"], "notes");
    expect(host.querySelector("[data-testid=todo-seen]")).toBeNull();
    await act(async () => root.render(<><TodosView /><Toasts /></>));
    await flush();
    expect(api.mock.calls.filter(([path]) => path.endsWith("/seen"))).toHaveLength(0);
  });
});
