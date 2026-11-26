// @vitest-environment jsdom
// Regression tests for #13931 (Web): cards held by a task's supervisor (`delegating: true` on non-question cards,
// `supervisor_held` in hub counts) are excluded from the Agents page filter chip count and sidebar row status dot,
// while user-owned and mixed needs-you sessions stay counted and highlighted as before.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { seedAgentsDisplay } from "../lib/agents-display";
import { loadPages } from "../lib/lazy-page";
import { SESSIONS_AGENTS_ONLY_FEATURE } from "../lib/runtime-list";
import { STATE_LABEL } from "../lib/state-labels";
import { connect, reset } from "../lib/store";
import type { Interaction, Session } from "../lib/types";
import { AgentsList, SessionRow } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor() { FakeSocket.all.push(this); }
  send() {}
  close() { this.readyState = 3; }
  open() { this.readyState = 1; this.onopen?.(); }
  push(msg: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(msg) }); }
}

const at = (minute: number) => `2026-01-01T00:${String(minute).padStart(2, "0")}:00Z`;
const session = (id: string, state: Session["state"], extra: Partial<Session> = {}): Session => ({
  machine_id: "mac",
  session_id: id,
  title: id,
  cwd: "/code/app",
  status: state === "working" ? "working" : "idle",
  state,
  updated_at: at(5),
  pane_id: `w1:${id}`,
  ...extra,
});
const screenCard = (id: string, sid: string, patch: Partial<Interaction> = {}): Interaction => ({
  id,
  machine_id: "mac",
  session_id: sid,
  kind: "screen",
  created_at: at(5),
  dialog: { question: "Do you want to proceed?", detail: "rmdir build", options: [{ key: "1", label: "Yes" }, { key: "2", label: "No" }] },
  ...patch,
});
const permissionCard = (id: string, sid: string, patch: Partial<Interaction> = {}): Interaction => ({
  id,
  machine_id: "mac",
  session_id: sid,
  kind: "permission",
  tool_name: "Bash",
  summary: "Run git status",
  created_at: at(6),
  ...patch,
});
const questionCard = (id: string, sid: string, patch: Partial<Interaction> = {}): Interaction => ({
  id,
  machine_id: "mac",
  session_id: sid,
  kind: "question",
  questions: [{ question: "Which target should we deploy?" }],
  created_at: at(6),
  ...patch,
});

const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 12; i++) await act(async () => { await Promise.resolve(); });
};

const quickChips = () => [...host.querySelectorAll<HTMLButtonElement>(`[role="radiogroup"][aria-label="${t("sidebar.quick_filters")}"] [role="radio"]`)];
const chipLabels = () => quickChips().map((b) => b.getAttribute("aria-label"));
const rowLink = (sid: string) => host.querySelector<HTMLAnchorElement>(`a[href*="/mac/${encodeURIComponent(sid)}"]`);
const rowDotState = (sid: string) => rowLink(sid)?.querySelector<HTMLElement>(".status-mark")?.getAttribute("data-state");
const rowDotShape = (sid: string) => rowLink(sid)?.querySelector<HTMLElement>(".status-mark")?.getAttribute("data-shape");

beforeAll(() => loadPages(), 60_000);

beforeEach(() => {
  localStorage.clear();
  reset();
  seedAgentsDisplay({ show_botmux: true, configured: true, show_filter: false });
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  history.replaceState(null, "", "/agents");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

async function mountStore(interactions: Interaction[]) {
  vi.stubGlobal("fetch", vi.fn((input: string) => {
    const url = new URL(input, "http://hub.example.com");
    if (url.pathname.endsWith("/interactions")) return json({ interactions });
    return json({});
  }));
  act(() => {
    connect();
    const sock = FakeSocket.all.at(-1)!;
    sock.open();
    sock.push({ type: "hello", features: [] });
  });
  await flush();
}

describe("sidebar row status dot with supervisor-held cards (#13931)", () => {
  const rowProps = (s: Session) => ({
    s,
    k: `s:${s.machine_id}/${s.session_id}`,
    reg: () => () => {},
    hover: () => {},
    active: false,
    to: { name: "session" as const, mid: s.machine_id, sid: s.session_id },
    go: () => () => {},
  });

  it("shows working dot (not needs_you), hides held row line, and drops held pending count for a held-only row", async () => {
    const heldOnly = session("held-only", "needs_you", { pending_interactions: 1 });
    await mountStore([screenCard("h1", "held-only", { delegating: true })]);
    act(() => root.render(<SessionRow {...rowProps(heldOnly)} />));
    await flush();

    expect(rowDotState("held-only")).toBe("working");
    expect(rowDotShape("held-only")).toBe("arc");
    expect(rowLink("held-only")?.querySelector("[data-row-line]")).toBeNull();
    expect(rowLink("held-only")?.textContent).not.toContain("Do you want to proceed?");
    expect(rowLink("held-only")?.querySelector(".text-attention")).toBeNull();
  });

  it("treats a parent whose folded child only has a supervisor-held card as working", async () => {
    const parent = session("parent-held", "needs_you", { children: { total: 1, needs_you: 1 } });
    await mountStore([screenCard("hc1", "child-1", { delegating: true, folded_under: "parent-held" })]);
    act(() => root.render(<SessionRow {...rowProps(parent)} />));
    await flush();

    expect(rowDotState("parent-held")).toBe("working");
    expect(rowDotShape("parent-held")).toBe("arc");
    expect(rowLink("parent-held")?.querySelector("[data-row-line]")).toBeNull();
    expect(rowLink("parent-held")?.querySelector(".text-attention")).toBeNull();
  });

  it("keeps needs_you dot, shows the user-owned card line, and counts only non-held cards for mixed rows", async () => {
    const mixed = session("mixed", "needs_you", { pending_interactions: 2 });
    const held = screenCard("h1", "mixed", { delegating: true });
    const mine = permissionCard("p1", "mixed");

    // Held card listed first in store.interactions must still pick the user-owned card and keep needs_you dot.
    await mountStore([held, mine]);
    act(() => root.render(<SessionRow {...rowProps(mixed)} />));
    await flush();

    expect(rowDotState("mixed")).toBe("needs_you");
    expect(rowDotShape("mixed")).toBe("alert");
    const lineText = rowLink("mixed")?.querySelector("[data-row-line]")?.textContent ?? "";
    expect(lineText).toContain("Run git status");
    expect(lineText).not.toContain("Do you want to proceed?");
    const badge = rowLink("mixed")?.querySelector(".rounded-full.text-attention");
    expect(badge?.textContent).toBe("1");
  });

  it("preserves needs_you dot and count for zero-held rows (user-owned, delegating question, bare needs_you)", async () => {
    const mine = session("mine", "needs_you", { pending_interactions: 1 });
    const asking = session("asking", "needs_you", { pending_interactions: 1 });
    const bare = session("bare", "needs_you", { attention_reason: "Waiting in terminal" });
    await mountStore([
      screenCard("u1", "mine"),
      questionCard("q1", "asking", { delegating: true }),
    ]);
    act(() => root.render(
      <>
        <SessionRow {...rowProps(mine)} />
        <SessionRow {...rowProps(asking)} />
        <SessionRow {...rowProps(bare)} />
      </>,
    ));
    await flush();

    expect(rowDotState("mine")).toBe("needs_you");
    expect(rowDotShape("mine")).toBe("alert");
    expect(rowLink("mine")?.querySelector(".rounded-full.text-attention")?.textContent).toBe("1");

    // A delegating question is the assistant's own look (lib/delegate.ts), not supervisor-held.
    expect(rowDotState("asking")).toBe("needs_you");
    expect(rowDotShape("asking")).toBe("alert");
    expect(rowLink("asking")?.querySelector(".rounded-full.text-attention")?.textContent).toBe("1");

    expect(rowDotState("bare")).toBe("needs_you");
    expect(rowDotShape("bare")).toBe("alert");
  });
});

async function renderPagedAgents({
  sessions,
  interactions,
  supervisorHeld,
  quickFilters,
}: {
  sessions: Session[];
  interactions: Interaction[];
  supervisorHeld?: number;
  quickFilters: boolean;
}) {
  localStorage.setItem("persona.agentGrouping", "machine");
  const countBy = (key: (s: Session) => string) =>
    sessions.reduce<Record<string, number>>((acc, s) => ({ ...acc, [key(s)]: (acc[key(s)] ?? 0) + 1 }), {});
  const countsBody = {
    total: sessions.length,
    state: countBy((s) => s.state!),
    machine: countBy((s) => s.machine_id),
    group: {},
    project: {},
    todo: { "": sessions.length },
    pane_source: { terminal: sessions.length },
    ...(supervisorHeld !== undefined ? { supervisor_held: supervisorHeld } : {}),
    revision: "r1",
  };
  const fetch = vi.fn((input: string) => {
    const url = new URL(input, "http://hub.example.com");
    const q = url.searchParams;
    if (url.pathname.endsWith("/agents/display-settings")) return json({ show_botmux: true, configured: true });
    if (url.pathname.endsWith("/sessions")) {
      if (q.has("counts")) return json(countsBody);
      return json({ sessions, remove: [], has_more: false, revision: "r1", total: sessions.length, ...(q.get("with_counts") === "1" ? { counts: countsBody } : {}) });
    }
    if (url.pathname.endsWith("/interactions")) return json({ interactions });
    if (url.pathname.endsWith("/pins")) return json({ sessions: [], spaces: [] });
    if (url.pathname.endsWith("/machines")) return json({ machines: [{ id: "mac", name: "MacBook", online: true }] });
    if (url.pathname.endsWith("/todos")) return json({ todos: [] });
    return json({});
  });
  vi.stubGlobal("fetch", fetch);
  const features = ["sessions_list", "sessions_lookup", SESSIONS_AGENTS_ONLY_FEATURE, "sessions_todo", "sessions_page_counts", ...(quickFilters ? ["sessions_quick_filters"] : [])];
  act(() => {
    connect();
    const sock = FakeSocket.all.at(-1)!;
    sock.open();
    sock.push({ type: "hello", features });
  });
  await flush();
  act(() => root.render(<AgentsList route={{ name: "agents" }} onNewAgent={() => {}} />));
  await flush();
  await act(async () => vi.dynamicImportSettled());
  await flush();
}

describe("Agents page filter chip count and rows on a paging hub (#13931)", () => {
  it.each([true, false])("excludes supervisor_held from Needs you chip and shifts it to Working (mixed held + user, quickFilters=%s)", async (quickFilters) => {
    const sessions = [
      session("held-only", "needs_you", { pending_interactions: 1 }),
      session("mixed", "needs_you", { pending_interactions: 2 }),
      session("user-only", "needs_you", { pending_interactions: 1 }),
      session("busy", "working"),
    ];
    const interactions = [
      screenCard("h1", "held-only", { delegating: true }),
      screenCard("h2", "mixed", { delegating: true }),
      permissionCard("p1", "mixed"),
      screenCard("u1", "user-only"),
    ];
    // Hub reports 3 raw needs_you sessions, 1 of which is supervisor_held-only.
    await renderPagedAgents({ sessions, interactions, supervisorHeld: 1, quickFilters });

    const labels = chipLabels();
    expect(labels).toContain(`${STATE_LABEL.needs_you} 2`);
    expect(labels).toContain(`${STATE_LABEL.working} 2`);
    expect(rowDotState("held-only")).toBe("working");
    expect(rowDotState("mixed")).toBe("needs_you");
    expect(rowDotState("user-only")).toBe("needs_you");
    expect(rowDotState("busy")).toBe("working");
  });

  it.each([true, false])("hides the Needs you chip when every needs_you session is supervisor_held (held-only, quickFilters=%s)", async (quickFilters) => {
    const sessions = [
      session("held-1", "needs_you", { pending_interactions: 1 }),
      session("held-2", "needs_you", { pending_interactions: 1 }),
      session("busy", "working"),
    ];
    const interactions = [
      screenCard("h1", "held-1", { delegating: true }),
      permissionCard("h2", "held-2", { delegating: true }),
    ];
    await renderPagedAgents({ sessions, interactions, supervisorHeld: 2, quickFilters });

    const labels = chipLabels();
    expect(labels.some((l) => l?.startsWith(STATE_LABEL.needs_you))).toBe(false);
    expect(labels).toContain(`${STATE_LABEL.working} 3`);
    expect(rowDotState("held-1")).toBe("working");
    expect(rowDotState("held-2")).toBe("working");
  });

  it.each([true, false])("keeps Needs you chip unchanged when supervisor_held is zero (zero-held, quickFilters=%s)", async (quickFilters) => {
    const sessions = [
      session("user-1", "needs_you", { pending_interactions: 1 }),
      session("user-2", "needs_you", { pending_interactions: 1 }),
      session("busy", "working"),
    ];
    const interactions = [screenCard("u1", "user-1"), permissionCard("u2", "user-2")];
    await renderPagedAgents({ sessions, interactions, supervisorHeld: 0, quickFilters });

    const labels = chipLabels();
    expect(labels).toContain(`${STATE_LABEL.needs_you} 2`);
    expect(labels).toContain(`${STATE_LABEL.working} 1`);
    expect(rowDotState("user-1")).toBe("needs_you");
    expect(rowDotState("user-2")).toBe("needs_you");
  });
});

async function renderCompleteAgents(sessions: Session[], interactions: Interaction[]) {
  localStorage.setItem("persona.agentGrouping", "machine");
  const fetch = vi.fn((input: string) => {
    const url = new URL(input, "http://hub.example.com");
    if (url.pathname.endsWith("/agents/display-settings")) return json({ show_botmux: true, configured: true });
    if (url.pathname.endsWith("/sessions")) return json({ sessions });
    if (url.pathname.endsWith("/interactions")) return json({ interactions });
    if (url.pathname.endsWith("/pins")) return json({ sessions: [], spaces: [] });
    if (url.pathname.endsWith("/machines")) return json({ machines: [{ id: "mac", name: "MacBook", online: true }] });
    if (url.pathname.endsWith("/todos")) return json({ todos: [] });
    return json({});
  });
  vi.stubGlobal("fetch", fetch);
  act(() => {
    connect();
    const sock = FakeSocket.all.at(-1)!;
    sock.open();
    sock.push({ type: "hello", features: [] });
    sock.push({ type: "machines", machines: [{ id: "mac", name: "MacBook", online: true }] });
    sock.push({ type: "sessions", machine_id: "mac", sessions });
  });
  await flush();
  act(() => root.render(<AgentsList route={{ name: "agents" }} onNewAgent={() => {}} />));
  await flush();
}

describe("Agents page filter chip count and rows on the complete list (#13931)", () => {
  it("excludes held-only sessions from Needs you chip while keeping mixed and user-owned sessions counted", async () => {
    const sessions = [
      session("held-only", "needs_you", { pending_interactions: 1 }),
      session("mixed", "needs_you", { pending_interactions: 2 }),
      session("user-only", "needs_you", { pending_interactions: 1 }),
    ];
    const interactions = [
      screenCard("h1", "held-only", { delegating: true }),
      screenCard("h2", "mixed", { delegating: true }),
      permissionCard("p1", "mixed"),
      screenCard("u1", "user-only"),
    ];
    await renderCompleteAgents(sessions, interactions);

    const labels = chipLabels();
    expect(labels).toContain(`${STATE_LABEL.needs_you} 2`);
    expect(labels).toContain(`${STATE_LABEL.working} 1`);
    expect(rowDotState("held-only")).toBe("working");
    expect(rowDotState("mixed")).toBe("needs_you");
    expect(rowDotState("user-only")).toBe("needs_you");
  });

  it("hides the Needs you chip when only supervisor-held sessions exist on the complete list", async () => {
    const sessions = [
      session("held-only", "needs_you", { pending_interactions: 1 }),
      session("busy", "working"),
    ];
    const interactions = [screenCard("h1", "held-only", { delegating: true })];
    await renderCompleteAgents(sessions, interactions);

    const labels = chipLabels();
    expect(labels.some((l) => l?.startsWith(STATE_LABEL.needs_you))).toBe(false);
    expect(labels).toContain(`${STATE_LABEL.working} 2`);
    expect(rowDotState("held-only")).toBe("working");
  });

  it("keeps Needs you chip and dots unchanged when there are zero held cards on the complete list", async () => {
    const sessions = [
      session("user-1", "needs_you", { pending_interactions: 1 }),
      session("busy", "working"),
    ];
    const interactions = [screenCard("u1", "user-1")];
    await renderCompleteAgents(sessions, interactions);

    const labels = chipLabels();
    expect(labels).toContain(`${STATE_LABEL.needs_you} 1`);
    expect(labels).toContain(`${STATE_LABEL.working} 1`);
    expect(rowDotState("user-1")).toBe("needs_you");
  });
});
