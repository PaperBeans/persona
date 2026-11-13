// @vitest-environment jsdom
// While the main Persona conversation works, its state is said once: the live work line in the message stream
// ("Working…", spinner and clock) stays, and the dock's running status bar does not repeat it (#6230). Agent chats keep
// the full dock bar.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const captured = vi.hoisted(() => ({
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle", state: "idle" } as Session,
}));
const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => select({ ...store.getState(), sessions: { hub: [captured.session] } }),
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/router")>(), useRoute: () => ({ name: "session", mid: "hub", id: "c1" }) }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));

const { ChatView } = await import("./ChatView");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};

beforeEach(() => {
  localStorage.clear();
  transcript.items = [{ id: "u1#0", seq: 1, role: "user", kind: "text", text: "What should I do today?", ts: "2026-11-13T01:00:00Z" }];
  transcript.loading = false;
  transcript.error = "";
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("the running status is said once in the Persona conversation (#6230)", () => {
  it("keeps the in-message live work line and leaves the dock bar out", async () => {
    captured.session = { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "working", state: "working", state_since: "2026-11-13T01:00:01Z" } as Session;
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant />));
    await flush();
    // the message stream still says the turn is at work…
    expect(host.querySelector('[data-work-group="live"]')).not.toBeNull();
    // …and the dock does not say it a second time
    expect(host.querySelector("[data-running-status]")).toBeNull();
  });

  it("still draws the dock bar in an Agent chat", async () => {
    captured.session = { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "working", state: "working", state_since: "2026-11-13T01:00:01Z" } as Session;
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelector('[data-running-status="on"]')).not.toBeNull();
  });
});
