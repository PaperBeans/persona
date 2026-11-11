// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { WORK_SESSION_DIMENSIONS_FEATURE, type WorkSession } from "../../lib/work-session-data";
import { WorkSessionBrowser } from "./WorkSessionBrowser";

vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), api: vi.fn() }));
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count, getItemKey }: { count: number; getItemKey: (index: number) => string }) => ({
    getTotalSize: () => 0,
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({ index, start: 0, key: getItemKey(index) })),
    measureElement: () => {},
  }),
}));
const env = vi.hoisted(() => ({
  features: ["sessions_dimensions"], conn: "online", machines: [],
}));
vi.mock("../../lib/store", () => ({
  useStore: (read: (store: typeof env) => unknown) => read(env),
  onHubEvent: () => () => {},
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const read = (key: string) => t(`work-session-browser.${key}`);
const currentPage = () => vi.mocked(api).mock.calls
  .map(([url]) => new URL(url, "https://example.com").searchParams)
  .filter((p) => p.has("limit")).at(-1)!;

function session(session_id: string, machine_id: string, project_id: string, title: string): WorkSession {
  return { session_id, machine_id, project_id, title, project_name: "Example project", harness_id: "codex",
    harness_name: "Codex", cwd: "/example", status: "offline", state: "offline", updated_at: "2026-01-01T00:00:00Z" };
}
const rows = [
  session("first", "one", "git:one", "Read-only history"),
  { ...session("second", "two", "git:one", "No terminal"), assistant: true, source_kind: "runtime" },
  { ...session("third", "one", "name:project", "Legacy independent API conversation"), assistant: true },
];
const facets = {
  device: [{ id: "one", name: "Device one", count: 74 }, { id: "two", name: "Device two", count: 41 }],
  project: [{ id: "git:one", name: "Example project", count: 101 }, { id: "name:project", name: "Example project", count: 14 }],
  harness: [{ id: "codex", name: "Codex", count: 115 }],
};
const groups = [
  { path: ["git:one"], count: 101 }, { path: ["git:one", "codex"], count: 101 },
  { path: ["name:project"], count: 14 }, { path: ["name:project", "codex"], count: 14 },
];

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  env.features = [WORK_SESSION_DIMENSIONS_FEATURE];
  vi.mocked(api).mockReset().mockImplementation(async (url) => {
    const p = new URL(url, "https://example.com").searchParams;
    if (p.has("counts")) return { state: { offline: 115 }, facets } as never;
    const found = p.get("q") !== "impossible";
    return { sessions: found ? rows : [], total: found ? 115 : 0, has_more: false, revision: "test", remove: [],
      facets, groups: p.get("group_by") === "project,harness" ? groups : [] } as never;
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

async function change(element: HTMLSelectElement, value: string) {
  await act(async () => {
    element.value = value;
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}
async function click(el: HTMLElement | null | undefined) {
  expect(el).toBeTruthy();
  await act(async () => { el!.click(); });
}
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent === label);
const choose = (field: string, index = 0) =>
  [...[...host.querySelectorAll("fieldset")].find((f) => f.querySelector("legend")?.textContent === read(field))!.querySelectorAll("input")][index];

describe("WorkSessionBrowser", () => {
  it("defaults to flat activity order and passes an offline no-pane row to the host without losing query state", async () => {
    const onSelect = vi.fn();
    await act(async () => root.render(<WorkSessionBrowser onSelect={onSelect} />));
    expect(currentPage().get("group_by")).toBeNull();
    expect(currentPage().get("sort")).toBe("recent");
    expect(currentPage().get("direction")).toBe("desc");
    expect(currentPage().get("state")).toBeNull();
    expect(currentPage().get("scope")).toBe("work");
    expect(currentPage().has("agents_only")).toBe(false);
    expect(host.textContent).toContain("Legacy independent API conversation");
    const link = [...host.querySelectorAll("a")].find((el) => el.textContent?.includes("No terminal"));
    expect(link?.textContent).toContain("Device two");
    expect(link?.textContent).toContain("Example project");
    expect(link?.textContent).toContain("Codex");
    await click(link);
    expect(onSelect).toHaveBeenCalledWith(rows[1]);
    const calls = vi.mocked(api).mock.calls.length;
    await act(async () => root.render(<WorkSessionBrowser onSelect={onSelect} activeSession={{ machineId: "two", sessionId: "second" }} />));
    expect(link?.getAttribute("aria-current")).toBe("true");
    expect(vi.mocked(api).mock.calls).toHaveLength(calls);
  });

  it("keeps authorized legacy rows with an explicit limited-capability notice", async () => {
    env.features = [];
    vi.mocked(api).mockResolvedValue({ sessions: rows });
    await act(async () => root.render(<WorkSessionBrowser />));
    expect(host.textContent).toContain(read("legacy"));
    expect(host.textContent).toContain("No terminal");
    expect(host.querySelector("[data-work-session-browser]")).toBeNull();
    expect(currentPage().has("facets")).toBe(false);
    expect(currentPage().has("scope")).toBe(false);
  });

  it("keeps filter ORs and ANDs in the Hub query, shows no matches and can reset", async () => {
    await act(async () => root.render(<WorkSessionBrowser />));
    const details = host.querySelector("details")!;
    await act(async () => { details.open = true; details.dispatchEvent(new Event("toggle")); });
    await click(choose("device"));
    await click(choose("device", 1));
    await click(choose("project"));
    await click(choose("harness"));
    await click([...host.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent?.startsWith(t("lib-state-labels.offline"))));
    const input = host.querySelector<HTMLInputElement>("input:not([type=checkbox])")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "impossible");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const request = currentPage();
    expect(request.getAll("machine_id")).toEqual(["one", "two"]);
    expect(request.getAll("project_id")).toEqual(["git:one"]);
    expect(request.getAll("harness")).toEqual(["codex"]);
    expect(request.get("state")).toBe("offline");
    expect(request.get("q")).toBe("impossible");
    expect(host.textContent).toContain(t("sidebar.no_match"));
    await click(button(read("reset_filters")));
    expect(currentPage().has("machine_id")).toBe(false);
    expect(currentPage().has("state")).toBe(false);
    expect(currentPage().has("q")).toBe(false);
    expect(input.value).toBe("");
  });

  it("builds distinct ordered groups from server IDs and restores the view after remounting", async () => {
    await act(async () => root.render(<WorkSessionBrowser />));
    const selects = () => [...host.querySelectorAll("select")];
    await change(selects()[0], "project");
    expect(selects()[1].querySelector<HTMLOptionElement>("option[value=project]")?.disabled).toBe(true);
    await change(selects()[1], "harness");
    await change(selects().at(-2)!, "created");
    await change(selects().at(-1)!, "asc");
    const headings = [...host.querySelectorAll("[role=heading][aria-level='2']")];
    expect(headings.map((el) => el.textContent)).toEqual(["Example project101", `Example project (${read("by_name")})14`]);
    await act(async () => root.render(null));
    await act(async () => root.render(<WorkSessionBrowser />));
    expect(currentPage().get("group_by")).toBe("project,harness");
    expect(currentPage().get("sort")).toBe("created");
    expect(currentPage().get("direction")).toBe("asc");
    await change(selects()[2], "device");
    await click(host.querySelector<HTMLElement>(`[aria-label="${t("work-session-browser.move_up", { name: read("device") })}"]`));
    expect(currentPage().get("group_by")).toBe("project,device,harness");
    await change(selects()[1], "");
    expect(currentPage().get("group_by")).toBe("project");
    await change(selects()[0], "");
    expect(currentPage().has("group_by")).toBe(false);
    expect(currentPage().get("sort")).toBe("created");
    expect(currentPage().get("direction")).toBe("asc");
    await click(button(read("reset_view")));
    expect(currentPage().has("group_by")).toBe(false);
    expect(currentPage().get("sort")).toBe("recent");
  });
});
