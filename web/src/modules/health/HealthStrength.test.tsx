// @vitest-environment jsdom
// Strength training on the Health home (#14713, #14716; docs/protocol/protocol.md, strength training): on a hub whose
// hello has `health_strength`, strength is one of the items the web draws, its card the hub's own `strength.home` page at
// its place in the list, opening the overview, drawn again on `health_strength`; the editor and the first-time question
// offer it. Without the feature nothing of it is asked for or drawn, and a save keeps it where the list has it. The hub is
// a stand-in that keeps the list at a revision and answers the home page with the card the hub's code drew
// (scripts/mock/fixtures/health-strength-ui/).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({
  features: [] as string[],
  listeners: new Map<string, Set<() => void>>(),
  calls: [] as { method: string; path: string; json?: Record<string, unknown> }[],
  catalog: [] as { id: string; source: string }[],
  items: [] as string[],
  revision: 0,
  saved: false,
  home: null as unknown,
}));
const state = () => ({ catalog: hub.catalog, items: [...hub.items], revision: hub.revision, saved: hub.saved });

vi.mock("../../lib/api", async (orig) => {
  const real = await orig<typeof import("../../lib/api")>();
  return {
    ...real,
    api: async (path: string, init: { method?: string; json?: Record<string, unknown> } = {}) => {
      const method = init.method ?? "GET";
      hub.calls.push({ method, path, json: init.json });
      if (path.startsWith("/assistant?space=health")) return { machine_id: "mac", conversations: [] };
      if (path.startsWith("/assistant/memory")) return { files: [] };
      if (path.endsWith("/health/reports")) return { reports: [] };
      if (path === "/assistant/health/intake/ui?space=health") return structuredClone(hub.home);
      if (path.startsWith("/assistant/health/intake")) {
        return { water: { today: 750, goal: 2000, entries: [] }, protein: { today: 20, goal: 60, entries: [] }, days: [], zone: "UTC" };
      }
      if (!path.startsWith("/assistant/health/items")) return {};
      if (method === "GET") return state();
      const body = init.json as { items: string[]; revision: number };
      if (body.revision !== hub.revision) throw new real.ApiError(409, "stale", state());
      if (new Set(body.items).size !== body.items.length) throw new real.ApiError(400, "Invalid health items", {});
      hub.items = [...body.items];
      hub.revision++;
      hub.saved = true;
      return state();
    },
  };
});
vi.mock("../../lib/store", async (orig) => {
  const real = await orig<typeof import("../../lib/store")>();
  return {
    ...real,
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T => select({ ...real.getState(), features: hub.features } as ReturnType<typeof real.getState>),
    onHubEvent: (type: string, l: () => void) => {
      hub.listeners.set(type, (hub.listeners.get(type) ?? new Set()).add(l));
      return () => void hub.listeners.get(type)?.delete(l);
    },
  };
});
vi.mock("../../views/chat/ChatView", () => ({ ChatView: () => null }));

import { Toasts } from "../../ui/atoms";
import { strengthState } from "../../lib/health-intake-ui";
import { Health } from "./Health";
import { mergeItems, WEB_ITEMS_STRENGTH } from "./items";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const FIXTURES = join(__dirname, "../../../../scripts/mock/fixtures/health-strength-ui");
const HOME = JSON.parse(readFileSync(join(FIXTURES, "health-strength-ui.en.v11.home.response.json"), "utf8")) as { view: { sections: { title?: string; items: { type: string; title?: string }[] }[] } };
// a name the hub's home card draws (its latest session)
const DRAWN = HOME.view.sections[0].items.find((i) => i.title)!.title!;
// the section its chart of the last seven days' working sets is in (#14713)
const CHART = HOME.view.sections.find((s) => s.items.some((i) => i.type === "chart"))!.title!;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const render = async () => {
  act(() => root.render(<><Health fresh onToggleSidebar={() => {}} /><Toasts /></>));
  await flush();
};
const emit = async (type: string) => {
  act(() => hub.listeners.get(type)?.forEach((l) => l()));
  await flush();
};
const owner = (items: string[], saved = false, revision = 0) => {
  hub.items = items;
  hub.saved = saved;
  hub.revision = revision;
};
const press = async (b: HTMLElement | null | undefined) => {
  expect(b, "button").toBeTruthy();
  act(() => b!.click());
  await flush();
};
const byText = (scope: ParentNode, label: string) => [...scope.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const byLabel = (scope: ParentNode, label: string) => scope.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
/** What Today draws, in the page's order. */
const blocks = () => [...host.querySelectorAll<HTMLElement>('[data-testid="health-today-water"], [data-testid="health-today-protein"], [data-testid="health-today-strength"]')]
  .map((e) => e.dataset.testid!.replace("health-today-", ""));
const card = () => host.querySelector<HTMLElement>('[data-testid="health-today-strength"]');
const pageReads = () => hub.calls.filter((c) => c.path === "/assistant/health/intake/ui?space=health");
const puts = () => hub.calls.filter((c) => c.method === "PUT");
const editButton = () => byLabel(host, t("health.items_edit"));
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]');
const rows = () => [...dialog()!.querySelectorAll<HTMLElement>("[data-item]")].map((r) => `${r.dataset.item}:${r.querySelector('[role="switch"]')!.getAttribute("aria-checked")}`);
const sw = (scope: ParentNode, name: string) => scope.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${name}"]`);
const STRENGTH = () => t("health.strength_title");

// the card's renderer is a big chunk: load it once, outside any test's time limit
beforeAll(async () => {
  await import("../../views/settings/ExtensionView");
}, 120_000);

beforeEach(() => {
  hub.features = ["health_intake", "health_items", "health_strength"];
  hub.listeners.clear();
  hub.calls.length = 0;
  hub.catalog = [
    { id: "water", source: "persona" },
    { id: "protein", source: "persona" },
    { id: "strength", source: "persona" },
    { id: "activity", source: "apple_health" },
    { id: "trends", source: "apple_health" },
  ];
  owner(["water", "strength", "protein", "activity", "trends"], true, 2);
  hub.home = HOME;
  history.replaceState(null, "", "/health/new");
  // the card's chart draws once it knows its width
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private cb: ResizeObserverCallback) {}
      observe() {
        this.cb([{ contentRect: { width: 320 } } as ResizeObserverEntry], this as unknown as ResizeObserver);
      }
      unobserve() {}
      disconnect() {}
    },
  );
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("Strength training: a hub without it", () => {
  it("asks for none of its pages and draws the home as before, with strength training in the list", async () => {
    hub.features = ["health_intake", "health_items"];
    await render();
    expect(blocks()).toEqual(["water", "protein"]);
    expect(card()).toBeNull();
    await emit("health_strength");
    expect(pageReads()).toEqual([]);
  });

  it("offers only water and protein, and a save keeps strength training where the list has it", async () => {
    hub.features = ["health_intake", "health_items"];
    await render();
    await press(editButton());
    expect(rows()).toEqual(["water:true", "protein:true"]);
    await press(sw(dialog()!, t("health.today_protein")));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["water", "strength", "activity", "trends"], revision: 2 }]);
  });
});

describe("Strength training: the home card", () => {
  it("draws the hub's own home card where the list has it, between the rings", async () => {
    await render();
    expect(blocks()).toEqual(["water", "strength", "protein"]);
    await vi.waitFor(() => expect(card()!.textContent).toContain(DRAWN), { timeout: 20000 });
    expect(pageReads()[0].json).toMatchObject({ state: strengthState("strength.home") });
    expect(card()!.textContent).toContain(STRENGTH());
    expect(card()!.textContent).toContain(CHART);
    // first in the list, first on the page; alone, the only thing Today draws
    owner(["strength", "water", "activity"], true, 3);
    await emit("health_items");
    expect(blocks()).toEqual(["strength", "water"]);
    owner(["strength", "activity"], true, 4);
    await emit("health_items");
    expect(blocks()).toEqual(["strength"]);
  });

  it("draws no card when strength training is not added", async () => {
    owner(["water", "protein", "activity"], true, 2);
    await render();
    expect(blocks()).toEqual(["water", "protein"]);
    expect(pageReads()).toEqual([]);
  });

  it("opens the overview from its heading", async () => {
    await render();
    await vi.waitFor(() => expect(card()!.textContent).toContain(DRAWN), { timeout: 20000 });
    await press(host.querySelector<HTMLAnchorElement>('[data-testid="health-today-strength-open"]'));
    expect(location.pathname).toBe("/extension/-health-intake/pages");
    expect(new URLSearchParams(location.search).get("state")).toBe(strengthState("strength.overview"));
  });

  it("is drawn again when the hub says strength training changed", async () => {
    await render();
    await vi.waitFor(() => expect(card()!.textContent).toContain(DRAWN), { timeout: 20000 });
    const before = pageReads().length;
    vi.useFakeTimers();
    await emit("health_strength");
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    vi.useRealTimers();
    await flush();
    expect(pageReads()).toHaveLength(before + 1);
    // with the state the hub's last answer gave it
    expect(JSON.parse(String(pageReads().at(-1)!.json!.state))).toMatchObject({ page: "strength.home" });
  });
});

describe("Strength training: choosing it", () => {
  it("is one of the editor's items: removed, added back and moved, the rest kept in place", async () => {
    await render();
    await press(editButton());
    expect(rows()).toEqual(["water:true", "strength:true", "protein:true"]);
    await press(sw(dialog()!, STRENGTH()));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["water", "protein", "activity", "trends"], revision: 2 });
    expect(blocks()).toEqual(["water", "protein"]);
    // back, and first
    await press(editButton());
    expect(rows()).toEqual(["water:true", "protein:true", "strength:false"]);
    await press(sw(dialog()!, STRENGTH()));
    await press(byLabel(dialog()!, t("health.items_move_up", { name: STRENGTH() })));
    await press(byLabel(dialog()!, t("health.items_move_up", { name: STRENGTH() })));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["strength", "water", "protein", "activity", "trends"], revision: 3 });
    expect(blocks()).toEqual(["strength", "water", "protein"]);
  });

  it("is one of the first-time question's choices", async () => {
    owner(["activity", "trends"]);
    await render();
    const ask = host.querySelector<HTMLElement>('[data-testid="health-items-ask"]')!;
    expect(ask.textContent).toContain(t("health.ask_body_strength"));
    await press(sw(ask, STRENGTH()));
    await press(byText(ask, t("health.ask_add")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["strength", "activity", "trends"], revision: 0 }]);
    expect(host.querySelector('[data-testid="health-items-ask"]')).toBeNull();
    expect(blocks()).toEqual(["strength"]);
  });

  it("is a web item only with the feature: merged in place with it, kept as it is without", () => {
    expect(mergeItems(["water", "strength", "protein", "activity"], ["protein", "strength"], WEB_ITEMS_STRENGTH)).toEqual(["protein", "strength", "activity"]);
    expect(mergeItems(["water", "strength", "protein", "activity"], ["protein"])).toEqual(["protein", "strength", "activity"]);
  });
});
