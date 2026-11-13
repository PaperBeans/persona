// @vitest-environment jsdom
// A conclusion card drawn before its todo is in the store (the todo list not read back yet, a reconnect's re-read), then
// again once it arrives or goes away: every render calls the same hooks, so React does not throw "Rendered more hooks
// than during the previous render" (#310) and blank the page (#5422, #5569).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { refresh, refreshTodos, reset } from "../../lib/store";
import type { FeedItem } from "../../lib/feed";
import { FeedLineView } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const now = new Date().toISOString();
const RECEIPT = (state: string): FeedItem => ({ id: "r1", conversation: "c1", at: now, kind: "receipt", ref: "t1", body: { state, title: "My task", summary: "It broke" } });
const TODO = { id: "t1", title: "My task", origin: "c1", created_at: now, updated_at: now, agents: [{ machine_id: "m1", session_id: "s1", cwd: "/code/app" }] };
const SESSIONS = [{ machine_id: "m1", session_id: "s1", state: "working", status: "working", title: "a", last_text: "running the tests", updated_at: now }];
const MACHINES = [{ id: "m1", name: "MacBook", online: true }];

let todos: (typeof TODO)[] = [];
let host: HTMLDivElement;
let root: Root;
let errors: unknown[];

beforeEach(async () => {
  todos = [];
  errors = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      const u = String(url);
      const body = u.includes("/todos") ? { todos } : u.includes("/sessions") ? { sessions: SESSIONS } : u.includes("/machines") ? { machines: MACHINES } : {};
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    }),
  );
  await refresh();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host, { onUncaughtError: (e) => errors.push(e), onCaughtError: (e) => errors.push(e) });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

const render = (state: string) => act(() => root.render(<FeedLineView item={RECEIPT(state)} simplified />));

describe("a conclusion card drawn before its todo arrives", () => {
  for (const state of ["done", "failed"]) {
    it(`draws the ${state} card with and without its todo, and again, without throwing`, async () => {
      render(state);
      expect(host.textContent).toContain("My task");
      todos = [TODO];
      await act(async () => {
        await refreshTodos();
      });
      expect(errors).toEqual([]);
      expect(host.textContent).toContain("My task");
      todos = [];
      await act(async () => {
        await refreshTodos();
      });
      expect(errors).toEqual([]);
      expect(host.textContent).toContain("My task");
    });
  }
});
