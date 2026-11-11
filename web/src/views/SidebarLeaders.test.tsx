// @vitest-environment jsdom
// The leader conversations (#3623): the hub's `assistant.leaders` come first among the sidebar's Persona chats, in the
// hub's order, on any browser (none of this browser's pins needed), as the "Directions" group under each direction's name
// (GET /api/v1/assistant/leaders); then this browser's pins under "Pinned", then the rest under "Other chats".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { resetDirectionNames } from "../lib/direction-names";
import { refresh, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string, updated_at: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at,
});
const conversations = [
  conversation("main", "Persona", "2026-11-10T09:00:00Z"),
  conversation("newest", "Trip plan", "2026-11-10T08:00:00Z"),
  conversation("mine", "Release notes", "2026-11-01T00:00:00Z"),
  conversation("lead-b", "Payments", "2026-11-02T00:00:00Z"),
  conversation("lead-a", "Growth", "2026-11-03T00:00:00Z"),
];
let leaders: string[] | undefined;
let named: unknown;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const rowIds = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/persona/"]')].map((a) => a.getAttribute("href")!.slice("/persona/".length)).filter((id) => id !== "new");
const headings = () => [...host.querySelectorAll('[role="heading"]')].map((h) => h.textContent);
const newChat = () => host.querySelector<HTMLAnchorElement>(`a[href="/persona/new"][aria-label="${t("sidebar.new_chat")}"]`);
const row = (sid: string) => host.querySelector<HTMLAnchorElement>(`a[href="/persona/${sid}"]`)!;

beforeEach(() => {
  localStorage.clear();
  leaders = ["lead-a", "lead-b"];
  named = { leaders: [
    { id: "lead-a", title: "1110 | explore | Growth", name: "Product" },
    { id: "lead-b", title: "Payments", name: "Infrastructure" },
    { id: "old", title: "Old", name: "Ended one", ended: true },
  ] };
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://x").pathname;
    if (path === "/api/v1/assistant/leaders" && named === undefined) return Promise.resolve(new Response("{}", { status: 404 }));
    const body = path === "/api/v1/assistant" ? { machine_id: "hub", main: "main", leaders, conversations } : path === "/api/v1/assistant/leaders" ? named : path === "/api/v1/machines" ? { machines: [{ id: "hub", name: "Hub", online: true }] } : {};
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
  vi.unstubAllGlobals();
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
}

describe("the leader conversations in the sidebar", () => {
  it("are the Directions group, first in the hub's order, under their direction's names, with no badge and no pin of this browser's", async () => {
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b", "newest", "mine"]);
    expect(headings()).toEqual([t("sidebar.directions"), t("sidebar.other_chats")]);
    expect(row("lead-a").textContent).toContain("Product");
    expect(row("lead-a").textContent).not.toContain("Growth");
    expect(row("lead-b").textContent).toContain("Infrastructure");
    // the group says what they are: no pin icon, and no "Pinned" heading only because of them
    const plain = row("newest").querySelector("svg")!.innerHTML;
    for (const id of ["lead-a", "lead-b"]) expect(row(id).querySelector("svg")!.innerHTML).toBe(plain);
    expect(headings()).not.toContain(t("sidebar.pinned_section"));
    expect(row("newest").textContent).toContain("Trip plan");
    // nothing pinned in this browser: no Unpin all
    expect(host.querySelector(`button[aria-label="${t("conversation-menu.unpin_all")}"]`)).toBeNull();
  });

  it("put this browser's pins under Pinned after them, and the rest under Other chats", async () => {
    localStorage.setItem("assistant.pinned", '["mine", "lead-b"]');
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b", "mine", "newest"]);
    expect(headings()).toEqual([t("sidebar.directions"), t("sidebar.pinned_section"), t("sidebar.other_chats")]);
    expect(row("mine").querySelector("svg")!.innerHTML).not.toBe(row("newest").querySelector("svg")!.innerHTML);
    expect(row("lead-b").textContent).toContain("Infrastructure");
    expect(host.querySelector(`button[aria-label="${t("conversation-menu.unpin_all")}"]`)).not.toBeNull();
  });

  it("show their titles on a hub without the leaders list", async () => {
    named = undefined;
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b", "newest", "mine"]);
    expect(row("lead-a").textContent).toContain("Growth");
    expect(headings()[0]).toBe(t("sidebar.directions"));
  });

  it("leave the list as before when the hub has none: no group titles", async () => {
    leaders = undefined;
    localStorage.setItem("assistant.pinned", '["mine"]');
    await renderSidebar();
    expect(rowIds()).toEqual(["mine", "newest", "lead-a", "lead-b"]);
    expect(headings()).toEqual([t("sidebar.pinned_section")]);
    expect(host.textContent).not.toContain(t("sidebar.directions"));
    expect(host.textContent).not.toContain(t("sidebar.other_chats"));
  });

  it("have New chat as the compose icon on the section's title, not a row of its own", async () => {
    await renderSidebar();
    const link = newChat();
    expect(link).not.toBeNull();
    expect(link!.textContent).toBe("");
    expect(link!.closest("[data-sidebar-rows] .group\\/sec")).not.toBeNull();
    expect(link!.parentElement!.textContent).toContain(t("sidebar.persona_chats"));
    expect([...host.querySelectorAll('a[href="/persona/new"]')].filter((a) => a.textContent?.includes(t("sidebar.new_chat")))).toHaveLength(0);
  });

  it("have no Pin / Unpin in their menu, while a local pin keeps its Unpin", async () => {
    localStorage.setItem("assistant.pinned", '["mine"]');
    await renderSidebar();
    const menuOf = async (sid: string) => {
      act(() => row(sid).dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, clientX: 10, clientY: 10 })));
      await flush();
      const items = [...document.querySelectorAll('[role="menu"] [role="menuitem"]')].map((el) => el.textContent ?? "");
      act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
      await flush();
      return items;
    };
    const lead = await menuOf("lead-a");
    expect(lead.some((x) => x.includes(t("conversation-menu.rename")))).toBe(true);
    expect(lead.some((x) => x.includes(t("conversation-menu.unpin")) || x.includes(t("conversation-menu.pin")))).toBe(false);
    const mine = await menuOf("mine");
    expect(mine.some((x) => x.includes(t("conversation-menu.unpin")))).toBe(true);
  });
});
