// @vitest-environment jsdom
// An earlier stretch whose page failed (#5054): the chat says so with Retry, and tries again by itself once the wait is
// over (3 s, doubling up to 30 s) while its top is in view: no scroll is needed for it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  (g.CSS as { escape?: (s: string) => string } | undefined) ?? (g.CSS = {});
  (g.CSS as { escape?: (s: string) => string }).escape ??= (s: string) => s;
});

const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "", loadingEarlier: false, loadEarlier: async () => {}, expandFold: async () => {}, reload: () => {} }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));

const { ChatView } = await import("./ChatView");
const { getState, reset, upsertSession } = await import("../../lib/store");
// the stretches' own module, in before the clock is faked (the chat loads it lazily)
await import("./EarlierStretches");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T09:00:00Z");
const at = (m: number) => new Date(T0 + m * 60_000).toISOString();
const user = (sid: string, seq: number, m: number, text: string): Item => ({ id: `${sid}u${seq}#0`, seq, role: "user", kind: "text", text, ts: at(m) });
const handoff = '<persona-context kind="engine-switch" title="Handoff" from="claude" to="codex">\nWhat happened so far.\n</persona-context>\n\n';

let host: HTMLDivElement;
let root: Root;
let asks = 0;
const tick = async (ms: number) => {
  for (let i = 0; i < 10; i++) await act(async () => { await vi.advanceTimersByTimeAsync(ms / 10); });
};

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
  localStorage.clear();
  asks = 0;
  Object.assign(getState(), { loaded: true });
  upsertSession({ machine_id: "m", session_id: "b", title: "Login page", cwd: "/w", status: "idle", state: "idle", agent: "codex", engine_history: [{ session_id: "a", agent: "claude", until: at(10) }] });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    if (url.includes("/sessions/m/a/items?")) {
      // the first ask fails, the next one gets A's stretch
      return ++asks === 1 ? json({ error: "the node did not answer" }, 502) : json({ items: [user("a", 1, 1, "Build a login page.")], has_more: false });
    }
    return json({ items: [] });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("a failed earlier page", () => {
  it("is asked for again once its wait is over, without a scroll", async () => {
    Object.assign(transcript, { items: [], loading: true, hasMore: false });
    act(() => root.render(<ChatView mid="m" sid="b" onToggleSidebar={() => {}} />));
    Object.assign(transcript, { loading: false, items: [user("b", 1, 11, `${handoff}Now add the tests.`)] });
    act(() => root.render(<ChatView mid="m" sid="b" onToggleSidebar={() => {}} />));
    await tick(200);
    expect(asks).toBe(1);
    expect(host.querySelector("[data-engine-stretch-error]")).not.toBeNull();
    // still waiting: nothing asked again
    await tick(2_500);
    expect(asks).toBe(1);
    // the wait is over: asked again, and A's stretch is in
    await tick(500);
    expect(asks).toBe(2);
    expect(host.querySelector("[data-engine-stretch='a']")?.textContent).toContain("Build a login page.");
    expect(host.querySelector("[data-engine-stretch-error]")).toBeNull();
  });
});
