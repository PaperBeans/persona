// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBDetailResponse, KBList } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { KnowledgeTopicsSwitch } from "../settings/KnowledgeTopics";
import { EntryDetail } from "./KBDetail";
import { KnowledgeMap } from "./KnowledgeMap";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const reply = async (request: Request, value: unknown, status = 200) => act(async () => {
  request.resolve(new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } }));
});
const pending = (path: string) => requests.find((r) => r.url.endsWith(path) && !(r as { done?: boolean }).done)!;
const answer = async (path: string, value: unknown) => {
  const r = pending(path);
  (r as { done?: boolean }).done = true;
  await reply(r, value);
};

const topic: KBDetailResponse = {
  detail: {
    kind: "topics", id: "search", path: "", meta: { name: "Search", note: "How search works", points: 3 },
    links: [{ kind: "projects", id: "alpha", name: "Project Alpha", relevance: 0 }],
    points: [
      { id: "p1", kind: "decision", text: "Results are grouped by entry", why: "Lines alone could not be found again", sources: [{ kind: "projects", id: "alpha" }], when: "2026-11-02" },
      { id: "p2", path: ["Cache"], kind: "pitfall", text: "Files changed within 2 seconds are not cached", why: "Same-timestamp rewrites", sources: [{ kind: "chats", id: "unknown-chat" }] },
      { id: "p3", path: ["Cache", "Limits"], kind: "fact", text: "The cache holds at most 64 MB" },
    ],
  },
};

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  // jsdom lays nothing out and has no element scrolling; the map's first fit only scrolls.
  Element.prototype.scrollTo = vi.fn() as unknown as Element["scrollTo"];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("a topic's page", () => {
  it("shows its points grouped by subtopic, with kind, reason, sources by name and day", async () => {
    act(() => root.render(<EntryDetail kind="topics" id="search" reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={() => {}} />));
    await reply(requests.at(-1)!, topic);
    const text = host.textContent ?? "";
    expect(text).toContain("How search works");
    expect(text).toContain("3 points");
    const sections = [...host.querySelectorAll("section")].map((s) => s.textContent ?? "");
    const loose = sections.find((s) => s.startsWith("Knowledge points"))!;
    expect(loose).toContain("Decision");
    expect(loose).toContain("Results are grouped by entry");
    expect(loose).toContain("Why: Lines alone could not be found again");
    expect(loose).toContain("Project Alpha");
    const cache = sections.find((s) => s.startsWith("Cache"))!;
    expect(cache).toContain("Pitfall");
    expect(cache).toContain("Cause: Same-timestamp rewrites");
    expect(cache).toContain("Limits");
    expect(cache).toContain("The cache holds at most 64 MB");
    // a source the detail does not name is left out rather than shown as an id
    expect(text).not.toContain("unknown-chat");
    // the summary rendered from the points is not shown a second time
    expect(sections.some((s) => s.startsWith("Profile"))).toBe(false);
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent === "Project Alpha")!;
    expect(link.getAttribute("href")).toContain("/kb/projects/alpha");
  });
});

describe("the knowledge map", () => {
  const empty: KBList = { items: [] };

  it("says curation fills it in when there are no topics yet", async () => {
    act(() => root.render(<KnowledgeMap onToggleSidebar={() => {}} />));
    await answer("/api/v1/kb/topics", empty);
    await answer("/api/v1/assistant", { kb_topics: true });
    expect(host.textContent).toContain("No topics yet");
    expect(host.textContent).not.toContain("Open Settings");
  });

  it("says where to turn curation on when it is off", async () => {
    act(() => root.render(<KnowledgeMap onToggleSidebar={() => {}} />));
    await answer("/api/v1/kb/topics", empty);
    await answer("/api/v1/assistant", { kb_topics: false });
    expect(host.textContent).toContain("Knowledge points are off");
    expect(host.textContent).toContain("Open Settings");
  });

  it("draws the topics and opens a point in the panel", async () => {
    act(() => root.render(<KnowledgeMap onToggleSidebar={() => {}} />));
    await answer("/api/v1/kb/topics", { items: [{ kind: "topics", id: "search", name: "Search", relevance: 0, extra: "3" }] });
    await answer("/api/v1/assistant", {});
    await answer("/api/v1/kb/topics/search", topic);
    const svg = host.querySelector("svg[data-map]")!;
    expect(svg.getAttribute("aria-label")).toBe("Mind map: Knowledge");
    const nodes = [...svg.querySelectorAll<SVGGElement>("g[role=button][tabindex]")];
    const decision = nodes.find((g) => g.getAttribute("aria-label") === "Decision: Results are grouped by entry")!;
    act(() => decision.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    const panel = host.querySelector("aside")!.textContent ?? "";
    expect(panel).toContain("Results are grouped by entry");
    expect(panel).toContain("Lines alone could not be found again");
    expect(panel).toContain("Project Alpha");
    // the subtopic opens with its toggle, which says what it does
    const toggle = [...svg.querySelectorAll("g[role=button]")].find((g) => g.getAttribute("aria-label") === "Expand Cache")!;
    act(() => toggle.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect([...svg.querySelectorAll("g[role=button]")].some((g) => g.getAttribute("aria-label") === "Collapse Cache")).toBe(true);
  });

  it("switches to the projects view", async () => {
    act(() => root.render(<KnowledgeMap onToggleSidebar={() => {}} />));
    await answer("/api/v1/kb/topics", empty);
    await answer("/api/v1/assistant", {});
    const tab = [...host.querySelectorAll<HTMLButtonElement>("[role=tab]")].find((b) => b.textContent === "Projects")!;
    act(() => tab.click());
    await answer("/api/v1/kb/projects", { items: [{ kind: "projects", id: "alpha", name: "Project Alpha", relevance: 0, importance: "P1", extra: "short · active" }] });
    await answer("/api/v1/kb/projects/alpha", { detail: { kind: "projects", id: "alpha", path: "", meta: { name: "Project Alpha", goal: "Ship it" } } });
    expect(host.querySelector("svg[data-map]")?.getAttribute("aria-label")).toBe("Mind map: Projects");
    expect(tab.getAttribute("aria-selected")).toBe("true");
  });
});

describe("the knowledge points switch", () => {
  it("reads the setting as on when unset, and writes the change", async () => {
    act(() => root.render(<KnowledgeTopicsSwitch manager="assistant" />));
    await answer("/api/v1/assistant", {});
    const sw = host.querySelector<HTMLButtonElement>("[role=switch]")!;
    expect(sw.getAttribute("aria-checked")).toBe("true");
    act(() => sw.click());
    const put = requests.find((r) => r.init.method === "PUT")!;
    expect(JSON.parse(put.init.body as string)).toEqual({ kb_topics: false });
    expect(new Headers(put.init.headers).get("X-Persona-Manager")).toBe("assistant");
    await reply(put, { kb_topics: false });
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("puts the switch back when the write fails", async () => {
    act(() => root.render(<KnowledgeTopicsSwitch manager="assistant" />));
    await answer("/api/v1/assistant", { kb_topics: false });
    const sw = host.querySelector<HTMLButtonElement>("[role=switch]")!;
    act(() => sw.click());
    await reply(requests.find((r) => r.init.method === "PUT")!, { error: "nope" }, 500);
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });
});
