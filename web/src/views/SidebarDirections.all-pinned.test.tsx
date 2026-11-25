// @vitest-environment jsdom
// Regression coverage for showing every unarchived pinned direction (>= 9) in the sidebar without truncation,
// merging live assistant sessions with the assistant snapshot, updating leaders on live direction pushes, and
// rendering SidebarDirectionsSection in controlled mode without touching global stores or fetching.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { resetDirectionNames } from "../lib/direction-names";
import { resetDirectionTree, type DirectionTree } from "../lib/direction-tree";
import { resetManagerChoice } from "../lib/manager-choice";
import { connect, flushPushes, getState, refresh, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";
import { resolveSidebarDirections, sidebarConversations, SidebarDirectionsSection } from "./SidebarDirections";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string, updated_at: string, extra: Partial<Session> = {}): Session => ({
  machine_id: "hub",
  session_id,
  title,
  cwd: "/persona",
  status: "idle",
  state: "idle",
  assistant: true,
  origin: "user",
  updated_at,
  ...extra,
});

const DIR_IDS = Array.from({ length: 9 }, (_, i) => `dir-${i + 1}`);
const pad2 = (n: number) => String(n).padStart(2, "0");

class FakeSocket {
  static OPEN = 1;
  static last: FakeSocket | null = null;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor() {
    FakeSocket.last = this;
  }
  send() {}
  close() {
    this.readyState = 3;
  }
}

const pushWs = (msg: Record<string, unknown>) => {
  act(() => FakeSocket.last!.onmessage?.({ data: JSON.stringify(msg) }));
  act(() => flushPushes());
};

describe("Sidebar pinned directions completeness (>= 9 unarchived directions)", () => {
  let host: HTMLDivElement;
  let root: Root;
  let leaders: string[];
  let conversations: Session[];
  let treeDirections: DirectionTree["directions"];

  const flush = async () => {
    for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
  };
  const pinnedIds = () => [...host.querySelectorAll<HTMLElement>("[data-direction]")].map((el) => el.dataset.direction);
  const personaLinkIds = () =>
    [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/persona/"]')]
      .map((a) => a.getAttribute("href")!.slice("/persona/".length))
      .filter((id) => id !== "new");

  beforeEach(() => {
    localStorage.clear();
    leaders = [...DIR_IDS];
    conversations = [
      conversation("main", "Persona", "2026-11-25T10:00:00Z"),
      // First 6 active recently, last 3 low-activity and much older than all ordinary chats.
      ...DIR_IDS.map((id, i) =>
        conversation(
          id,
          `Direction ${i + 1}`,
          i < 6 ? `2026-11-${pad2(20 + i)}T09:00:00Z` : `2026-09-${pad2(1 + i)}T09:00:00Z`,
        )),
    ];
    treeDirections = DIR_IDS.map((id, i) => ({
      id,
      name: `Direction ${i + 1}`,
      counts: { working: 0, needs_you: 0, stuck: 0, review: 0 },
      running: false,
      tasks: [],
    }));
    vi.stubGlobal("WebSocket", FakeSocket);
    vi.stubGlobal("fetch", vi.fn((url: string) => {
      const path = new URL(url, "http://x").pathname;
      const body = path === "/api/v1/assistant"
        ? { machine_id: "hub", main: "main", leaders, conversations }
        : path === "/api/v1/assistant/directions/tree"
          ? { directions: [...treeDirections, { id: "main", name: "", secretary: true, counts: { working: 0, needs_you: 0, stuck: 0, review: 0 }, tasks: [] }] }
          : path === "/api/v1/assistant/leaders"
            ? { leaders: treeDirections.map((d) => ({ id: d.id, name: d.name })) }
            : path === "/api/v1/machines"
              ? { machines: [{ id: "hub", name: "Hub", online: true }] }
              : {};
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    }));
    history.replaceState(null, "", "/overview");
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    reset();
    resetDirectionNames();
    resetDirectionTree();
    resetManagerChoice();
    vi.unstubAllGlobals();
  });

  async function renderSidebar() {
    await act(() => refresh());
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
  }

  it("renders all 9 unarchived pinned directions in hub order even when low-activity directions are older than recent chats", async () => {
    const ordinary = Array.from({ length: 10 }, (_, i) =>
      conversation(`recent-${pad2(i + 1)}`, `Ordinary chat ${i + 1}`, `2026-11-${pad2(10 + i)}T12:00:00Z`),
    );
    leaders = [...DIR_IDS, "dir-archived"];
    conversations = [
      ...conversations,
      conversation("dir-archived", "Archived direction", "2026-11-24T00:00:00Z", { archived: true }),
      ...ordinary,
    ];
    treeDirections = [
      ...treeDirections,
      { id: "dir-archived", name: "Archived direction", counts: { working: 0, needs_you: 0, stuck: 0, review: 0 }, running: false, tasks: [] },
    ];

    await renderSidebar();

    expect(pinnedIds()).toEqual(DIR_IDS);
    expect(pinnedIds()).not.toContain("dir-archived");
    // All 9 pinned directions followed by the default 5 newest ordinary chats.
    expect(personaLinkIds()).toEqual([...DIR_IDS, "recent-10", "recent-09", "recent-08", "recent-07", "recent-06"]);
  });

  it("recovers unarchived pinned directions present in live assistant sessions when the assistant snapshot listed only 6", async () => {
    // Simulate GET /api/v1/assistant returning all 9 leader ids but only 6 conversations while live sessions carry dir-7..dir-9.
    const missingFromSnapshot = conversations.filter((c) => ["dir-7", "dir-8", "dir-9"].includes(c.session_id))
      .map((c) => ({ ...c, cwd: "", status: "offline" as const, state: "offline" as const }));
    conversations = conversations.filter((c) => !["dir-7", "dir-8", "dir-9"].includes(c.session_id));

    await renderSidebar();
    expect(pinnedIds()).toEqual(DIR_IDS.slice(0, 6));

    act(() => {
      connect();
      FakeSocket.last!.readyState = 1;
      FakeSocket.last!.onopen?.();
    });
    pushWs({ type: "hello", features: ["sessions_delta"] });
    pushWs({
      type: "sessions",
      machine_id: "hub",
      sessions: [
        ...conversations,
        ...missingFromSnapshot,
        conversation("dir-other-space", "Health space chat", "2026-11-25T09:00:00Z", { space: "health" }),
        conversation("dir-other-manager", "Other manager chat", "2026-11-25T09:00:00Z", { manager: "other-manager" }),
      ],
    });
    await flush();

    expect(pinnedIds()).toEqual(DIR_IDS);

    // Archiving one live direction removes it from Pinned immediately without losing the remaining 8.
    pushWs({
      type: "sessions_delta",
      machine_id: "hub",
      upsert: [{ ...missingFromSnapshot[2], archived: true }],
      remove: [],
    });
    await flush();
    expect(pinnedIds()).toEqual(DIR_IDS.slice(0, 8));
  });

  it("updates assistant.leaders on a directions push even when another leader id is unlisted or archived", async () => {
    leaders = DIR_IDS.slice(0, 6);
    treeDirections = treeDirections.slice(0, 6);
    await renderSidebar();
    expect(pinnedIds()).toEqual(DIR_IDS.slice(0, 6));

    act(() => {
      connect();
      FakeSocket.last!.readyState = 1;
      FakeSocket.last!.onopen?.();
    });
    pushWs({ type: "hello", features: [] });
    await flush();

    // Directions tree now advertises all 9 unarchived directions plus 1 archived id not in conversations.
    leaders = [...DIR_IDS, "dir-gone"];
    treeDirections = [
      ...DIR_IDS.map((id, i) => ({
        id,
        name: `Direction ${i + 1}`,
        counts: { working: 0, needs_you: 0, stuck: 0, review: 0 },
        running: false,
        tasks: [],
      })),
      { id: "dir-gone", name: "Ended direction", counts: { working: 0, needs_you: 0, stuck: 0, review: 0 }, running: false, tasks: [] },
    ];
    pushWs({ type: "directions" });
    await flush();

    expect(getState().assistant?.leaders).toEqual([...DIR_IDS, "dir-gone"]);
    expect(pinnedIds()).toEqual(DIR_IDS);
  });
});

describe("Controlled SidebarDirectionsSection and pure sidebar direction helpers", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    localStorage.clear();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    reset();
    resetDirectionNames();
    resetDirectionTree();
    resetManagerChoice();
    vi.unstubAllGlobals();
  });

  it("keeps listed object references stable while merging missing unarchived live rows and hiding live-archived rows", () => {
    const c1 = conversation("dir-1", "Direction 1", "2026-11-25T09:00:00Z");
    const c2 = conversation("dir-2", "Direction 2", "2026-11-24T09:00:00Z");
    const live = [
      { ...c1 },
      { ...c2, archived: true },
      conversation("dir-3", "Direction 3", "2026-11-23T09:00:00Z", { cwd: "", status: "offline", state: "offline" }),
      conversation("dir-space", "Space chat", "2026-11-23T09:00:00Z", { space: "health" }),
    ];

    const merged = sidebarConversations([c1, c2], live, "assistant");
    expect(merged.map((c) => c.session_id)).toEqual(["dir-1", "dir-3"]);
    expect(merged[0]).toBe(c1);
  });

  it("renders all 9 controlled directions in SidebarDirectionsSection with zero network fetches", async () => {
    const fetcher = vi.fn(() => Promise.reject(new Error("controlled mode must not fetch")));
    vi.stubGlobal("fetch", fetcher);

    const convs = DIR_IDS.slice(0, 8).map((id, i) => conversation(id, `Direction ${i + 1}`, `2026-11-${pad2(10 + i)}T00:00:00Z`));
    const entries = resolveSidebarDirections({
      leaders: [...DIR_IDS, "dir-archived"],
      main: "main",
      conversations: [...convs, conversation("dir-archived", "Archived", "2026-11-01T00:00:00Z", { archived: true })],
      externalOf: (id) => (id === "dir-9" ? { external: true, machine_id: "mac", session_id: "ext-9" } : undefined),
    });
    expect(entries.map((e) => e.id)).toEqual(DIR_IDS);

    const controlledTree: DirectionTree = {
      directions: DIR_IDS.map((id, i) => ({
        id,
        name: `Direction ${i + 1}`,
        counts: { working: i === 0 ? 1 : 0, needs_you: 0, stuck: 0, review: 0 },
        running: i === 0,
        tasks: i === 0
          ? [{ id: "todo-1", title: "Synthetic task", state: "working", agents: [{ machine_id: "mac", session_id: "agent-1", title: "Worker 1", state: "working" }] }]
          : [],
      })),
    };
    const controlledAgents: Session[] = [
      { machine_id: "mac", session_id: "agent-1", title: "Worker 1", cwd: "/code/repo", status: "working", state: "working" },
    ];

    act(() => {
      root.render(
        <SidebarDirectionsSection
          directions={entries}
          label="Pinned"
          nameOf={(entry) => entry.conversation?.title ?? `External ${entry.id}`}
          renderRow={(entry) => <div data-row-id={entry.id}>{entry.conversation?.title ?? entry.id}</div>}
          reg={() => () => {}}
          hover={() => {}}
          go={() => () => {}}
          directionTree={controlledTree}
          agentSessions={controlledAgents}
        />,
      );
    });
    await act(async () => { await Promise.resolve(); });

    const renderedIds = [...host.querySelectorAll<HTMLElement>("[data-direction]")].map((el) => el.dataset.direction);
    expect(renderedIds).toEqual(DIR_IDS);
    expect(fetcher).not.toHaveBeenCalled();

    // Expanding a direction reveals its controlled task and agent without calling global stores or fetch.
    const toggle = host.querySelector<HTMLButtonElement>('[data-direction="dir-1"] button[aria-expanded]')!;
    act(() => toggle.click());
    await act(async () => { await Promise.resolve(); });
    expect(host.querySelector('a[href="/todos?id=todo-1"]')?.textContent).toContain("Synthetic task");
    expect(fetcher).not.toHaveBeenCalled();
  });
});
