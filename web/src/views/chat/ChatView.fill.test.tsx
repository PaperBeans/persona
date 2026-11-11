// @vitest-environment jsdom
// Older pages load when the reader reaches the top. A first page shorter than the view never scrolls, so that top was
// never reached and the chat stayed at its newest page: the main conversation's newest rounds are often two folded
// background rounds, a couple of lines. Older pages now load until the view is full.
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
vi.mock("./Composer", async (importOriginal) => ({ ...await importOriginal<typeof import("./Composer")>(), Composer: () => null }));
const transcript = vi.hoisted(() => ({
  items: [] as Item[],
  loading: false,
  error: "",
  hasMore: true,
  loadingEarlier: false,
  streaming: "",
  loadEarlier: (() => Promise.resolve()) as () => Promise<void>,
}));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));

import { applyCompleteSessions, reset } from "../../lib/store";
const { ChatView } = await import("./ChatView");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let size = { scrollHeight: 0, clientHeight: 0 };
beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("{}", { status: 200 }))));
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => size.scrollHeight);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => size.clientHeight);
  // as a browser clamps it: a view with nothing to scroll stays at 0
  let top = 0;
  vi.spyOn(HTMLElement.prototype, "scrollTop", "get").mockImplementation(() => top);
  vi.spyOn(HTMLElement.prototype, "scrollTop", "set").mockImplementation((v: number) => { top = Math.max(0, Math.min(v, size.scrollHeight - size.clientHeight)); });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.restoreAllMocks();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const row = { machine_id: "mac", session_id: "s1", title: "s1", cwd: "/code", status: "idle", state: "idle" } as Session;
const background: Item[] = [
  { id: "u1#0", seq: 10, ts: "2026-11-11T08:00:00Z", role: "user", kind: "text", text: "a report from another conversation" },
  { id: "a1#0", seq: 11, ts: "2026-11-11T08:00:30Z", role: "assistant", kind: "text", text: "noted" },
];

async function open(view: { scrollHeight: number; clientHeight: number }, hasMore = true) {
  size = view;
  const loadEarlier = vi.fn(() => Promise.resolve());
  Object.assign(transcript, { items: [], hasMore: false, loading: true, loadEarlier });
  act(() => applyCompleteSessions([row]));
  act(() => root.render(<ChatView mid="mac" sid="s1" inPage />));
  // the hub's first page comes in
  Object.assign(transcript, { items: background, hasMore, loading: false });
  act(() => root.render(<ChatView mid="mac" sid="s1" inPage />));
  for (let i = 0; i < 3; i++) await act(async () => { await vi.advanceTimersByTimeAsync(100); });
  return loadEarlier;
}

describe("ChatView older pages", () => {
  it("loads an older page when the newest one does not fill the view", async () => {
    const loadEarlier = await open({ scrollHeight: 400, clientHeight: 800 });
    expect(loadEarlier).toHaveBeenCalled();
  });

  it("waits for the reader to scroll up when the page fills the view", async () => {
    const loadEarlier = await open({ scrollHeight: 6000, clientHeight: 800 });
    expect(loadEarlier).not.toHaveBeenCalled();
  });

  it("asks for nothing once the history is complete", async () => {
    const loadEarlier = await open({ scrollHeight: 400, clientHeight: 800 }, false);
    expect(loadEarlier).not.toHaveBeenCalled();
  });
});
