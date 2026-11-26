// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../i18n";
import { setToken } from "../lib/api";
import { readAssistantSnapshot } from "../lib/assistant-snapshot";
import { LABS_CATALOG } from "../lib/labs-catalog";
import { loadPages } from "../lib/lazy-page";
import { MANAGER_HEADER, setRosterDefault } from "../lib/manager-choice";
import { connect, getState, refreshAssistant, reset } from "../lib/store";
import type { AssistantInfo } from "../lib/types";
import { Settings } from "./Settings";
import { Sidebar } from "./Sidebar";
import { TAB_INFO } from "./settings/nav";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia = ((media: string) => ({ media, matches: false, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

class Socket {
  static OPEN = 1;
  static client: Socket;
  readyState = 1;
  onmessage?: (ev: { data: string }) => void;
  constructor() { Socket.client = this; }
  send() {}
  close() { this.readyState = 3; }
  push(msg: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(msg) }); }
}

const manager = "assistant";
const happyView: AssistantInfo = {
  manager, machine_id: "example", conversations: [],
  work_guide: true, todos_v2: false, integrations_v2: true, labs_seen: [],
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const noop = () => {};
let view: AssistantInfo;
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;
let put: (init: RequestInit) => Promise<Response>;
let root: Root;
let host: HTMLDivElement;

beforeAll(async () => {
  await initI18n("en");
  await loadPages();
});

beforeEach(() => {
  localStorage.clear();
  setToken("example-browser");
  setRosterDefault(manager);
  history.replaceState(null, "", "/settings");
  view = { ...happyView };
  put = async () => json({ ...view, labs_seen: LABS_CATALOG });
  fetchMock = vi.fn<typeof fetch>(async (input, init = {}) => {
    const url = new URL(input instanceof Request ? input.url : input.toString(), location.origin);
    if (url.pathname === "/api/v1/assistant") return init.method === "PUT" ? put(init) : json(view);
    if (url.pathname === "/api/v1/model-config/purposes/translate") return json({ purpose: {}, providers: [] });
    return json({});
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("isSecureContext", true);
  vi.stubGlobal("WebSocket", Socket);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  setToken("");
  localStorage.clear();
  vi.unstubAllGlobals();
});

const render = () => act(async () => {
  await refreshAssistant();
  root.render(<><Sidebar route={{ name: "home" }} onSearch={noop} onNewAgent={noop} /><Settings onToggleSidebar={noop} /></>);
});
const settings = () => host.querySelector<HTMLAnchorElement>('a[href="/settings"]')!;
const labs = () => host.querySelector<HTMLButtonElement>('[data-settings-nav-group="labs"] button')!;
const general = () => [...host.querySelectorAll<HTMLButtonElement>('nav button')].find((b) => b.textContent === TAB_INFO.general.label)!;
const newCards = () => [...host.querySelectorAll("[data-configuration]")]
  .filter((e) => [...e.querySelectorAll("[data-configuration-field] span")].some((label) => label.textContent === t("settings-labs.new_badge")))
  .map((e) => e.getAttribute("data-configuration"));
const writes = () => fetchMock.mock.calls.filter(([, init]) => init?.method === "PUT");

describe("the Labs read receipt", () => {
  it("clears both indicators before the hub replies and preserves the new card badges until leaving", async () => {
    let commit!: (result: Response) => void;
    put = () => new Promise((resolve) => { commit = resolve; });
    await render();
    expect(settings().getAttribute("aria-label")).toBe("Settings, New Labs experiments");
    expect(labs().getAttribute("aria-label")).toBe(`${TAB_INFO.labs.label}, New Labs experiments`);

    await act(async () => labs().click());
    await vi.waitFor(() => expect(newCards()).toEqual(["assistant-todos-v2", "terminal"]));
    expect(settings().getAttribute("aria-label")).toBeNull();
    expect(labs().getAttribute("aria-label")).toBeNull();
    expect(writes()).toHaveLength(1);
    const init = writes()[0][1]!;
    expect(new Headers(init.headers).get(MANAGER_HEADER)).toBe(manager);
    expect(JSON.parse(String(init.body))).toEqual({ labs_seen: LABS_CATALOG });

    const assistant = getState().assistant;
    await act(async () => {
      view = { ...view, labs_seen: [...LABS_CATALOG] };
      commit(json(view));
    });
    expect(getState().assistant).toBe(assistant);
    expect(readAssistantSnapshot("")?.assistant.labs_seen).toEqual(LABS_CATALOG);
    expect(newCards()).toEqual(["assistant-todos-v2", "terminal"]);
    await act(async () => general().click());
    await act(async () => labs().click());
    await vi.waitFor(() => expect(host.querySelector('[data-configuration="terminal"]')).not.toBeNull());
    expect(newCards()).toEqual([]);
    expect(writes()).toHaveLength(1);
  });

  it("merges WebSocket receipts by manager and keeps a stale GET or replay from bringing back the dot", async () => {
    await render();
    expect(settings().getAttribute("aria-label")).not.toBeNull();
    await act(async () => { connect(); });
    const assistant = getState().assistant;
    await act(async () => {
      Socket.client.push({ type: "assistant_labs_seen", manager: "other", labs_seen: LABS_CATALOG });
      Socket.client.push({ type: "assistant_labs_seen", labs_seen: ["terminal"] });
    });
    expect(getState().assistant).toBe(assistant);
    expect(settings().getAttribute("aria-label")).not.toBeNull();
    await act(async () => {
      Socket.client.push({ type: "assistant_labs_seen", labs_seen: ["todos_v2", "terminal"] });
      Socket.client.push({ type: "assistant_labs_seen", labs_seen: ["terminal"] });
      await refreshAssistant();
    });
    expect(settings().getAttribute("aria-label")).toBeNull();
    expect(labs().getAttribute("aria-label")).toBeNull();
    expect(writes()).toHaveLength(0);
  });

  it("keeps older-hub receipts in browser storage across reload without making a server write", async () => {
    view = { manager, machine_id: "example", conversations: [], work_guide: false };
    await render();
    expect(settings().getAttribute("aria-label")).not.toBeNull();
    await act(async () => labs().click());
    await vi.waitFor(() => expect(newCards()).toEqual(["assistant-work", "terminal"]));
    expect(settings().getAttribute("aria-label")).toBeNull();
    expect(writes()).toHaveLength(0);
    await act(async () => root.unmount());
    reset();
    root = createRoot(host);
    history.replaceState(null, "", "/settings");
    await render();
    expect(labs().getAttribute("aria-label")).toBeNull();
    await act(async () => labs().click());
    await vi.waitFor(() => expect(host.querySelector('[data-configuration="terminal"]')).not.toBeNull());
    expect(newCards()).toEqual([]);
    expect(writes()).toHaveLength(0);
  });

  it("can retry a failed receipt by revisiting Labs", async () => {
    put = async () => json({ error: "Unavailable" }, 503);
    await render();
    await act(async () => labs().click());
    await vi.waitFor(() => expect(newCards()).toContain("terminal"));
    expect(settings().getAttribute("aria-label")).not.toBeNull();
    expect(writes()).toHaveLength(1);
    put = async () => json({ ...view, labs_seen: LABS_CATALOG });
    await act(async () => general().click());
    await act(async () => labs().click());
    await vi.waitFor(() => expect(settings().getAttribute("aria-label")).toBeNull());
    expect(writes()).toHaveLength(2);
  });
});
