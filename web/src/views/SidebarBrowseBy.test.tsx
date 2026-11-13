// @vitest-environment jsdom
// The sidebar's paged Agents list drawn by project on a hub with the catalog (#6348): a repository on two computers is
// one section, and each of its rows says which computer it is on.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { loadPages } from "../lib/lazy-page";
import { connect, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

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
const row = (machine: string, id: string, cwd: string, minute: number): Session => ({ machine_id: machine, session_id: id, title: id, cwd, repo_root: cwd, status: "idle", state: "idle",
  updated_at: `2026-01-01T00:${String(minute).padStart(2, "0")}:00Z` }) as Session;
const ROWS = [row("mac", "Widget on the Mac", "/Users/me/code/app", 9), row("pc", "Widget on the PC", "C:\\code\\App", 8), row("mac", "Reading notes", "/Users/me/notes", 7)];
const pair = (machine: string, path: string) => JSON.stringify([machine, path]);
const CATALOG = {
  revision: "r1", total: 3,
  projects: [
    { id: "git:git.example.com/team/app", name: "app", origin: "git.example.com/team/app", total: 2,
      places: [{ machine_id: "mac", path: "/Users/me/code/app", total: 1 }, { machine_id: "pc", path: "C:\\code\\App", total: 1 }] },
    { id: "dir:notes", name: "notes", total: 1, places: [{ machine_id: "mac", path: "/Users/me/notes", total: 1 }] },
  ],
  spaces: [{ machine_id: "mac", id: "", name: "", total: 2 }, { machine_id: "pc", id: "", name: "", total: 1 }],
};
const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
let host: HTMLDivElement, root: Root;
const flush = async () => { for (let i = 0; i < 12; i++) await act(async () => { await Promise.resolve(); }); };

async function render(features: string[]) {
  const fetch = vi.fn((input: string) => {
    const url = new URL(input, "http://hub.example.com");
    const q = url.searchParams;
    if (url.pathname.endsWith("/sessions")) {
      if (q.has("catalog")) return json(CATALOG);
      const places = q.getAll("place");
      const rows = ROWS.filter((r) => (!places.length || places.includes(pair(r.machine_id, r.cwd))) && (!q.get("project") || q.get("project") === r.cwd));
      if (q.has("counts")) {
        const project: Record<string, number> = {};
        for (const r of rows) project[r.cwd] = (project[r.cwd] ?? 0) + 1;
        return json({ total: rows.length, state: { idle: rows.length }, machine: { mac: 2, pc: 1 }, group: {}, project, revision: "r1" });
      }
      return json({ sessions: q.has("since") ? [] : rows, remove: [], has_more: false, revision: "r1", total: rows.length });
    }
    if (url.pathname.endsWith("/pins")) return json({ sessions: [], spaces: [] });
    if (url.pathname.endsWith("/machines")) return json({ machines: [{ id: "mac", name: "MacBook", online: true }, { id: "pc", name: "Windows PC", online: true }] });
    if (url.pathname.endsWith("/todos")) return json({ todos: [] });
    return json({});
  });
  vi.stubGlobal("fetch", fetch);
  act(() => {
    connect();
    const socket = FakeSocket.all.at(-1)!;
    socket.open();
    socket.push({ type: "hello", features });
  });
  await flush();
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
  return fetch;
}
const sections = () => [...host.querySelectorAll<HTMLElement>("[data-session-group]")];
const heading = (section: HTMLElement) => section.querySelector<HTMLButtonElement>("button[aria-expanded]")!.textContent;
const rowTexts = (section: HTMLElement) => [...section.querySelectorAll<HTMLElement>("[data-session-row] a")].map((a) => a.textContent);

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("persona.agentBrowseBy", "project"); // the list as it was last read
  reset();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  history.replaceState(null, "", "/");
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount()); host.remove(); reset(); vi.unstubAllGlobals();
});

beforeAll(() => loadPages(), 60_000);

describe("the sidebar's Agents by project", () => {
  it("draws a repository on two computers as one project, each row naming its computer", async () => {
    const fetch = await render(["sessions_list", "sessions_lookup", "sessions_catalog"]);
    expect(sections().map(heading)).toEqual(["app", "notes"]);
    const [app, notes] = sections();
    expect([...app.querySelectorAll(`[aria-label="${t("paged-agents.places")}"] span.truncate`)].map((el) => el.textContent))
      .toEqual(["git.example.com/team/app", "MacBook · ~/code/app", "Windows PC · C:\\code\\App"]);
    // both computers' sessions, newest first, the computer before each title
    expect(rowTexts(app).map((text) => text!.replace(/\s+/g, " "))).toEqual([expect.stringMatching(/^MacBookWidget on the Mac/), expect.stringMatching(/^Windows PCWidget on the PC/)]);
    // a project on one computer says it once, under its heading
    expect(rowTexts(notes)[0]).toMatch(/^Reading notes/);
    expect(notes.querySelector(`[aria-label="${t("paged-agents.places")}"]`)!.textContent).toBe("MacBook · ~/notes");
    const read = fetch.mock.calls.map(([input]) => new URL(input, "http://hub.example.com").searchParams).find((q) => q.getAll("place").length === 2)!;
    expect(read.getAll("place")).toEqual([pair("mac", "/Users/me/code/app"), pair("pc", "C:\\code\\App")]);
  });

  it("on a hub without the catalog keeps each directory its own project, by its folder's name", async () => {
    const fetch = await render(["sessions_list", "sessions_lookup"]);
    expect(sections().map(heading).sort()).toEqual(["App", "app", "notes"]);
    expect(fetch.mock.calls.some(([input]) => new URL(input, "http://hub.example.com").searchParams.has("catalog"))).toBe(false);
  });
});
