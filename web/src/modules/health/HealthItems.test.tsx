// @vitest-environment jsdom
// What the Health home tracks (#14716, docs/protocol/protocol.md "Health items"): read only on a hub whose hello has
// `health_items`, Today draws only the items added and in their order, the editor saves the whole list keeping what only
// phones show where it was, a save that lost to one made elsewhere shows the hub's list without overwriting it, and an
// owner who never chose is asked once. The hub is a stand-in that keeps the list at a revision as the real one does.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({
  features: ["health_intake", "health_items"] as string[],
  listeners: new Map<string, Set<() => void>>(),
  calls: [] as { method: string; path: string; json?: Record<string, unknown> }[],
  catalog: [
    { id: "water", source: "persona" },
    { id: "protein", source: "persona" },
    { id: "activity", source: "apple_health" },
    { id: "trends", source: "apple_health" },
  ],
  items: [] as string[],
  revision: 0,
  saved: false,
  failPut: "",
  /** Holds the next items read: it answers with the list as it was when asked, once this settles. */
  readGate: null as Promise<void> | null,
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
      if (path.startsWith("/assistant/health/intake")) {
        return { water: { today: 750, goal: 2000, entries: [] }, protein: { today: 20, goal: 60, entries: [] }, days: [], zone: "UTC" };
      }
      if (!path.startsWith("/assistant/health/items")) return {};
      if (method === "GET") {
        const gate = hub.readGate;
        hub.readGate = null;
        const snapshot = state();
        if (gate) await gate;
        return snapshot;
      }
      if (hub.failPut) throw new Error(hub.failPut);
      const body = init.json as { items: string[]; revision: number };
      if (body.revision !== hub.revision) throw new real.ApiError(409, "stale", state());
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
import { setRosterDefault } from "../../lib/manager-choice";
import { Health } from "./Health";
import { mergeItems } from "./items";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

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
const rings = () => [...host.querySelectorAll<HTMLElement>('[data-testid="health-today-water"], [data-testid="health-today-protein"]')]
  .map((e) => e.dataset.testid!.replace("health-today-", ""));
const today = () => host.querySelector('[data-testid="health-today"]');
const ask = () => host.querySelector<HTMLElement>('[data-testid="health-items-ask"]');
const itemReads = () => hub.calls.filter((c) => c.method === "GET" && c.path.startsWith("/assistant/health/items"));
const puts = () => hub.calls.filter((c) => c.method === "PUT");
const editButton = () => byLabel(host, t("health.items_edit"));
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]');
const rows = () => [...dialog()!.querySelectorAll<HTMLElement>("[data-item]")].map((r) => `${r.dataset.item}:${r.querySelector('[role="switch"]')!.getAttribute("aria-checked")}`);
const sw = (scope: ParentNode, name: string) => scope.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${name}"]`);

beforeEach(() => {
  hub.features = ["health_intake", "health_items"];
  hub.listeners.clear();
  hub.calls.length = 0;
  hub.catalog = [
    { id: "water", source: "persona" },
    { id: "protein", source: "persona" },
    { id: "activity", source: "apple_health" },
    { id: "trends", source: "apple_health" },
  ];
  owner(["water", "protein", "activity", "trends"]);
  hub.failPut = "";
  hub.readGate = null;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  vi.restoreAllMocks();
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("Health items: a hub without them", () => {
  it("asks nothing and draws the home as before: both rings, no Edit, no question", async () => {
    hub.features = ["health_intake"];
    owner(["activity", "trends"]); // would ask, on a hub that kept the list
    await render();
    expect(itemReads()).toEqual([]);
    expect(rings()).toEqual(["water", "protein"]);
    expect(editButton()).toBeNull();
    expect(ask()).toBeNull();
    await emit("health_items");
    expect(itemReads()).toEqual([]);
  });

  it("draws nothing of them while the hub has no intake either", async () => {
    hub.features = ["health_items"];
    await render();
    expect(itemReads()).toEqual([]);
    expect(today()).toBeNull();
    expect(editButton()).toBeNull();
  });
});

describe("Health items: what Today shows", () => {
  it("reads the list for the default manager's health space and draws an existing owner's rings unchanged", async () => {
    await render();
    expect(itemReads().map((c) => c.path)).toEqual(["/assistant/health/items?space=health"]);
    expect(rings()).toEqual(["water", "protein"]);
    expect(ask()).toBeNull();
    expect(editButton()).not.toBeNull();
  });

  it("draws only the items added, in their order", async () => {
    owner(["protein", "activity", "water", "trends"], true, 3);
    await render();
    expect(rings()).toEqual(["protein", "water"]);
    owner(["protein", "trends"], true, 4);
    await emit("health_items");
    expect(rings()).toEqual(["protein"]);
  });

  it("draws no Today when neither is added, and asks nothing of an owner who already chose", async () => {
    owner(["activity", "trends"], true, 1);
    await render();
    expect(today()).toBeNull();
    expect(ask()).toBeNull();
    expect(editButton()).not.toBeNull();
  });

  it("reads again on the hub's message and on a hello", async () => {
    await render();
    owner(["water", "activity", "trends"], true, 1);
    await emit("health_items");
    expect(rings()).toEqual(["water"]);
    owner(["protein", "water", "activity", "trends"], true, 2);
    await emit("hello");
    expect(rings()).toEqual(["protein", "water"]);
    expect(itemReads()).toHaveLength(3);
  });
});

describe("Health items: the editor", () => {
  it("adds, removes and reorders, and the save keeps Activity rings, trends and items the web does not know where they were", async () => {
    hub.catalog = [...hub.catalog, { id: "sleep", source: "apple_health" }];
    owner(["water", "activity", "protein", "trends", "sleep"], true, 2);
    await render();
    await press(editButton());
    expect(rows()).toEqual(["water:true", "protein:true"]);
    // protein first, then water off: the list keeps its other items in place
    await press(byLabel(dialog()!, t("health.items_move_up", { name: t("health.today_protein") })));
    expect(rows()).toEqual(["protein:true", "water:true"]);
    await press(sw(dialog()!, t("health.today_water")));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["protein", "activity", "trends", "sleep"], revision: 2 }]);
    expect(dialog()).toBeNull();
    expect(rings()).toEqual(["protein"]);
    // water back: after protein, where the web's items were; what it logged shows again
    await press(editButton());
    expect(rows()).toEqual(["protein:true", "water:false"]);
    await press(sw(dialog()!, t("health.today_water")));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["protein", "water", "activity", "trends", "sleep"], revision: 3 });
    expect(rings()).toEqual(["protein", "water"]);
    expect(host.querySelector('[data-testid="health-today-water"]')!.textContent).toContain("750");
  });

  it("shows the hub's list and says so when it was changed elsewhere, without overwriting it", async () => {
    await render();
    await press(editButton());
    await press(sw(dialog()!, t("health.today_protein")));
    owner(["protein", "activity", "trends"], true, 5); // a phone saved meanwhile
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts()).toHaveLength(1);
    expect(hub.items).toEqual(["protein", "activity", "trends"]);
    expect(dialog()).not.toBeNull();
    expect(dialog()!.querySelector('[role="status"]')?.textContent).toBe(t("health.items_stale"));
    expect(rows()).toEqual(["protein:true", "water:false"]);
    expect(rings()).toEqual(["protein"]);
    // saved again, at the hub's revision
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["protein", "activity", "trends"], revision: 5 });
  });

  it("says why a save failed and keeps the edits", async () => {
    await render();
    await press(editButton());
    await press(sw(dialog()!, t("health.today_water")));
    hub.failPut = "disk full";
    await press(byText(dialog()!, t("health.items_save")));
    expect(dialog()!.querySelector('[role="alert"]')?.textContent).toBe(t("health.items_save_failed", { error: "disk full" }));
    expect(rows()).toEqual(["water:false", "protein:true"]);
    expect(rings()).toEqual(["water", "protein"]);
    hub.failPut = "";
    await press(byText(dialog()!, t("health.items_save")));
    expect(dialog()).toBeNull();
    expect(rings()).toEqual(["protein"]);
  });

  it("drops edits that were not saved", async () => {
    await render();
    await press(editButton());
    await press(sw(dialog()!, t("health.today_water")));
    await press(byText(dialog()!, t("health.items_cancel")));
    await press(editButton());
    expect(rows()).toEqual(["water:true", "protein:true"]);
    expect(puts()).toEqual([]);
  });
});

// From the independent review of #14944: the list an edit saves at, and which of crossing answers is taken.
describe("Health items: crossing changes", () => {
  it("saves an edit at the list it started from, so a change that reached the page meanwhile is a 409, not overwritten", async () => {
    owner(["water", "protein", "activity", "trends"], true, 2);
    await render();
    await press(editButton());
    await press(sw(dialog()!, t("health.today_water")));
    // a phone removes protein; its message reaches the page, which reads the list again for the home
    owner(["water", "activity", "trends"], true, 3);
    await emit("health_items");
    expect(rings()).toEqual(["water"]);
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["protein", "activity", "trends"], revision: 2 }]);
    expect(hub.items).toEqual(["water", "activity", "trends"]);
    expect(dialog()!.querySelector('[role="status"]')?.textContent).toBe(t("health.items_stale"));
    expect(rows()).toEqual(["water:true", "protein:false"]);
    // decided again from the hub's list: saved at its revision
    await press(sw(dialog()!, t("health.today_water")));
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["activity", "trends"], revision: 3 });
  });

  it("takes a lower revision read after a reconnect: another default manager's list, saved at its own revision", async () => {
    owner(["water", "activity", "trends"], true, 10);
    await render();
    owner(["protein", "activity", "trends"], true, 1);
    await emit("hello");
    expect(rings()).toEqual(["protein"]);
    await press(editButton());
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["protein", "activity", "trends"], revision: 1 }]);
    expect(dialog()).toBeNull();
  });

  it("drops the list and reads the new one when the default manager moves", async () => {
    act(() => setRosterDefault("health-items-a"));
    owner(["water", "activity", "trends"], true, 10);
    await render();
    await press(editButton());
    owner(["protein", "activity", "trends"], true, 1);
    act(() => setRosterDefault("health-items-b"));
    await flush();
    expect(dialog()).toBeNull();
    expect(rings()).toEqual(["protein"]);
    expect(itemReads()).toHaveLength(2);
    await press(editButton());
    expect(rows()).toEqual(["protein:true", "water:false"]);
    await press(byText(dialog()!, t("health.items_save")));
    expect(puts().at(-1)!.json).toEqual({ items: ["protein", "activity", "trends"], revision: 1 });
  });

  it("does not let a read that answers late undo a newer one", async () => {
    owner(["water", "activity", "trends"], true, 2);
    await render();
    let release!: () => void;
    hub.readGate = new Promise((resolve) => { release = resolve; });
    await emit("health_items"); // answers with revision 2, late
    owner(["protein", "activity", "trends"], true, 3);
    await emit("health_items");
    expect(rings()).toEqual(["protein"]);
    await act(async () => { release(); });
    await flush();
    expect(rings()).toEqual(["protein"]);
  });
});

describe("Health items: asked once", () => {
  it("asks a fresh owner what to track; Add saves the ones picked and the question does not come back", async () => {
    owner(["activity", "trends"]);
    await render();
    expect(today()).toBeNull();
    const card = ask()!;
    expect(card).not.toBeNull();
    expect(byText(card, t("health.ask_add"))!.disabled).toBe(true);
    await press(sw(card, t("health.today_protein")));
    await press(byText(card, t("health.ask_add")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["protein", "activity", "trends"], revision: 0 }]);
    expect(ask()).toBeNull();
    expect(rings()).toEqual(["protein"]);
    await emit("hello");
    expect(ask()).toBeNull();
  });

  it("Not now saves the list as it is, and the question does not come back", async () => {
    owner(["activity", "trends"]);
    await render();
    await press(byText(ask()!, t("health.ask_later")));
    expect(puts().map((c) => c.json)).toEqual([{ items: ["activity", "trends"], revision: 0 }]);
    expect(ask()).toBeNull();
    expect(today()).toBeNull();
    await emit("health_items");
    expect(ask()).toBeNull();
  });

  it("goes when the owner chose elsewhere meanwhile, keeping that choice", async () => {
    owner(["activity", "trends"]);
    await render();
    owner(["water", "activity", "trends"], true, 1);
    await press(byText(ask()!, t("health.ask_later")));
    expect(hub.items).toEqual(["water", "activity", "trends"]);
    expect(ask()).toBeNull();
    expect(rings()).toEqual(["water"]);
  });
});

describe("mergeItems", () => {
  it("fills the web's slots in order and keeps the rest where it was", () => {
    expect(mergeItems(["water", "activity", "protein", "trends"], ["protein", "water"])).toEqual(["protein", "activity", "water", "trends"]);
    expect(mergeItems(["water", "activity", "protein", "trends"], ["protein"])).toEqual(["protein", "activity", "trends"]);
    expect(mergeItems(["activity", "water", "trends", "later"], ["water", "protein"])).toEqual(["activity", "water", "protein", "trends", "later"]);
    expect(mergeItems(["activity", "trends"], ["protein", "water"])).toEqual(["protein", "water", "activity", "trends"]);
    expect(mergeItems(["activity", "trends"], [])).toEqual(["activity", "trends"]);
    expect(mergeItems([], [])).toEqual([]);
  });
});
