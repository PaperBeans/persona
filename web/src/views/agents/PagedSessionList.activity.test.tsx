// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { SessionList, type ListCounts } from "../../lib/session-list";
import { PagedSessionList } from "./PagedSessionList";

vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), api: vi.fn() }));
vi.mock("../../lib/store", () => ({ onHubEvent: () => () => {} }));
// jsdom lays nothing out, so the virtualizer would draw no row: draw every item, in order
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count, getItemKey }: { count: number; getItemKey: (i: number) => string }) => ({
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({ index, key: getItemKey(index), start: index * 32 })),
    getTotalSize: () => count * 32,
    measureElement: () => {},
  }),
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Row = { id: string; updated_at: string; state: string };
const NOW = Date.UTC(2026, 10, 11, 12);
const HOUR = 3_600_000;
const row = (id: string, hoursAgo: number, state = "idle"): Row => ({ id, updated_at: new Date(NOW - hoursAgo * HOUR).toISOString(), state });
// newest first: three within a day, two older ones loaded, more pages behind them
const rows = [row("a", 1), row("b", 3, "needs_you"), row("c", 20), row("d", 30, "needs_you"), row("e", 200)];
const identity = (r: Row) => r.id;
const compare = (a: Row, b: Row) => Date.parse(b.updated_at) - Date.parse(a.updated_at);
const activeAt = (r: Row) => Date.parse(r.updated_at);
const needsYou = (r: Row) => r.state === "needs_you";
const counts: ListCounts = { total: 40, state: { needs_you: 4, idle: 36 }, machine: {}, project: {}, group: {}, revision: "r" };

let host: HTMLDivElement;
let root: Root;
let list: SessionList<Row>;

beforeEach(() => {
  vi.useFakeTimers({ now: NOW });
  localStorage.clear();
  vi.mocked(api).mockImplementation((path) => {
    const params = new URL(path, "https://example.com").searchParams;
    return Promise.resolve(params.has("counts") ? counts
      : { sessions: rows, remove: [], has_more: true, next_cursor: "next", total: 40, revision: "r" }) as ReturnType<typeof api>;
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  list = new SessionList<Row>("/sessions", identity, compare);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

async function show(open?: string) {
  await list.load();
  await act(async () => root.render(<PagedSessionList root={list} path="/sessions" identity={identity} compare={compare} event="sessions_list"
    activeAt={activeAt} needsYou={needsYou} isOpen={open ? (r) => r.id === open : undefined} renderRow={(r) => <span>{r.id}</span>} />));
}
const shown = () => [...host.querySelectorAll("[data-session-row]")].map((el) => el.textContent);
const header = () => host.querySelector<HTMLElement>("[data-earlier]");
const loadMore = () => [...host.querySelectorAll("button")].find((b) => b.textContent === t("paged-agents.more"));
const windowButton = () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"][title]')!;
async function choose(label: string) {
  await act(async () => windowButton().click());
  const item = [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')].find((el) => el.textContent?.includes(label))!;
  await act(async () => item.click());
}

describe("Active within", () => {
  it("folds nothing by default: every loaded row shows, with the list's own Load more", async () => {
    await show();
    expect(windowButton().getAttribute("aria-label")).toBe(t("paged-agents.active_within_value", { window: t("paged-agents.window_any") }));
    expect(shown()).toEqual(["a", "b", "c", "d", "e"]);
    expect(header()).toBeNull();
    expect(loadMore()).toBeDefined();
  });

  it("folds the sessions before the window into Earlier, counting all of them and the ones that need you", async () => {
    await show();
    await choose(t("paged-agents.window_1d"));
    expect(localStorage.getItem("persona.agentActiveWindow")).toBe("1d");
    expect(shown()).toEqual(["a", "b", "c"]);
    // 40 in the list, 3 recent; 4 need you, 1 of them recent
    expect(header()?.querySelector("button")?.getAttribute("aria-expanded")).toBe("false");
    const toggle = header()!.querySelector("button")!;
    expect(toggle.textContent).toBe(`${t("paged-agents.earlier")}37`);
    expect(header()!.querySelector("button + span")?.textContent).toBe(`3 ${t("sidebar.needs_you_suffix", { count: 3 })}`);
    // folded, the list reads no further pages
    expect(loadMore()).toBeUndefined();

    await act(async () => header()!.querySelector("button")!.click());
    expect(shown()).toEqual(["a", "b", "c", "d", "e"]);
    expect(loadMore()).toBeDefined();
  });

  it("goes back to the whole list with Any time, and forgets the window", async () => {
    localStorage.setItem("persona.agentActiveWindow", "7d");
    await show();
    expect(shown()).toEqual(["a", "b", "c", "d"]);
    await choose(t("paged-agents.window_any"));
    expect(localStorage.getItem("persona.agentActiveWindow")).toBeNull();
    expect(header()).toBeNull();
    expect(shown()).toEqual(["a", "b", "c", "d", "e"]);
  });

  it("opens Earlier by itself while one of its sessions is open", async () => {
    localStorage.setItem("persona.agentActiveWindow", "1d");
    await show("e");
    expect(header()?.querySelector("button")?.getAttribute("aria-expanded")).toBe("true");
    expect(shown()).toEqual(["a", "b", "c", "d", "e"]);
  });

  it("offers no window to a list that gives no activity time", async () => {
    await list.load();
    await act(async () => root.render(<PagedSessionList root={list} path="/sessions" identity={identity} compare={compare} event="sessions_list" renderRow={(r) => <span>{r.id}</span>} />));
    expect(host.querySelector('button[aria-haspopup="menu"][title]')).toBeNull();
    expect(shown()).toEqual(["a", "b", "c", "d", "e"]);
  });
});
