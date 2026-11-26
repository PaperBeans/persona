// @vitest-environment jsdom
// The water and protein page (#13176): the declarative page of the hub's intake route in a full page of its own, for the
// state in `?state=`, drawn again when the hub says a record was added, undone or a goal saved (`health_intake`), and a
// way back to the Health home. Today's rings open it. The strength training pages (#14713) are the same route's: titled
// as the hub's page titles itself, drawn again on `health_strength`, and a draft's page saved or cancelled goes back.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({
  calls: [] as { path: string; body?: Record<string, unknown> }[],
  listeners: new Map<string, Set<(m: Record<string, unknown>) => void>>(),
  // the title the hub's page gives itself, by the state asked for ("" none)
  title: (state: string): string => {
    const s = JSON.parse(state || "{}") as { kind?: string; page?: string };
    return s.page === "strength.edit" ? "Adjust" : s.page ? "Strength training" : s.kind === "protein" ? "Protein" : "Water";
  },
}));
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: async (path: string, init: { json?: Record<string, unknown> } = {}) => {
    hub.calls.push({ path, body: init.json });
    const state = String(init.json?.state ?? "");
    const title = hub.title(state);
    const button = state.includes("strength.edit") ? [{ type: "button", title: "Save", action: { id: "save", submit: true } }] : [];
    const view = { ...(title ? { title } : {}), state, sections: [{ items: [{ type: "text", text: `drawn ${hub.calls.length}` }, ...button] }] };
    // a draft's Save: the hub's answer asks for the page to be taken off
    return init.json?.action === "save" ? { view, pop: true } : { view };
  },
}));
vi.mock("../../lib/store", async (orig) => {
  const real = await orig<typeof import("../../lib/store")>();
  return {
    ...real,
    onHubEvent: (type: string, l: (m: Record<string, unknown>) => void) => {
      if (!hub.listeners.has(type)) hub.listeners.set(type, new Set());
      hub.listeners.get(type)!.add(l);
      return () => void hub.listeners.get(type)?.delete(l);
    },
  };
});

import { healthIntakePath, healthIntakeState, strengthPath, strengthState } from "../../lib/health-intake-ui";
import { HealthIntakePage, isStrength, kindOf } from "./HealthIntakePage";
import { t } from "../../i18n";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

let host: HTMLDivElement;
let root: Root;
const settle = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); });
};
const open = async (path: string) => {
  history.replaceState(null, "", path);
  await act(async () => root.render(<HealthIntakePage onToggleSidebar={() => {}} />));
  // the page's renderer is a chunk of its own: wait for its first draw
  await vi.waitFor(() => expect(host.textContent).toContain("drawn"), { timeout: 20000 });
  await settle();
};
const drawn = () => hub.calls.filter((c) => c.path === "/assistant/health/intake/ui?space=health");

// the page's renderer is a big chunk: load it once, outside any test's time limit (a busy machine takes a while)
beforeAll(async () => {
  await import("../../views/settings/ExtensionView");
}, 120_000);

beforeEach(() => {
  hub.calls.length = 0;
  hub.listeners.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe("the page", () => {
  it("opens the page for the state a ring's link carries, over the hub's intake route", async () => {
    await open(healthIntakePath("protein"));
    expect(drawn()[0].body).toMatchObject({ state: healthIntakeState("protein") });
    // the header says the title the hub's page gives itself
    expect(host.querySelector("h1")?.textContent).toBe("Protein");
    expect(host.textContent).toContain("drawn 1");
  });

  it("and the water link's state opens the water page", async () => {
    await open(healthIntakePath("water"));
    expect(drawn()[0].body).toMatchObject({ state: healthIntakeState("water") });
    expect(host.querySelector("h1")?.textContent).toBe("Water");
  });

  it("opens water when there is no state, as the hub does, or one it can't read", async () => {
    await open("/extension/-health-intake/pages");
    expect(drawn()[0].body).toMatchObject({ state: healthIntakeState("water") });
    expect(kindOf(undefined)).toBe("water");
    expect(kindOf("not json")).toBe("water");
    expect(kindOf('{"kind":"other"}')).toBe("water");
    expect(kindOf('{"kind":"protein","days":30}')).toBe("protein");
  });

  it("is drawn again when the hub says a record changed, and only for that message", async () => {
    await open(healthIntakePath("water"));
    expect(drawn()).toHaveLength(1);
    vi.useFakeTimers();
    act(() => void hub.listeners.get("subscription")?.forEach((l) => l({ type: "subscription" })));
    act(() => void hub.listeners.get("health_intake")?.forEach((l) => l({ type: "health_intake" })));
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    vi.useRealTimers();
    await settle();
    expect(drawn()).toHaveLength(2);
    expect(host.textContent).toContain("drawn 2");
  });

  it("has a way back to the Health home, and another page of the app does not listen for health_intake", async () => {
    await open(healthIntakePath("water"));
    act(() => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("health.title")}"]`)!.click());
    expect(location.pathname).toBe("/health/new");
    act(() => root.unmount());
    root = createRoot(host);
    hub.listeners.clear();
    const { LegacyExtensionUI } = await import("../../views/settings/ExtensionView");
    act(() => root.render(<LegacyExtensionUI extension="demo" ui="home" />));
    await settle();
    expect(hub.listeners.get("health_intake")?.size ?? 0).toBe(0);
  });
});

describe("the strength training pages", () => {
  const realTitle = hub.title;
  afterEach(() => {
    hub.title = realTitle;
  });

  it("opens the page its state names, titled as the hub's page titles itself, not as water", async () => {
    await open(strengthPath("strength.overview"));
    expect(drawn()[0].body).toMatchObject({ state: strengthState("strength.overview") });
    expect(host.querySelector("h1")?.textContent).toBe("Strength training");
    expect(host.querySelector("h1")?.textContent).not.toBe(t("health.today_water"));
    expect(isStrength(strengthState("strength.edit"))).toBe(true);
    expect(isStrength(healthIntakeState("water"))).toBe(false);
    expect(isStrength("not json")).toBe(false);
  });

  it("says strength training until the hub's page names itself", async () => {
    hub.title = () => "";
    await open(strengthPath("strength.home"));
    expect(host.querySelector("h1")?.textContent).toBe(t("health.strength_title"));
  });

  it("is drawn again when the hub says strength training changed", async () => {
    await open(strengthPath("strength.overview"));
    expect(drawn()).toHaveLength(1);
    vi.useFakeTimers();
    act(() => void hub.listeners.get("health_strength")?.forEach((l) => l({ type: "health_strength" })));
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    vi.useRealTimers();
    await settle();
    expect(drawn()).toHaveLength(2);
    expect(drawn()[1].body).toMatchObject({ state: strengthState("strength.overview") });
  });

  it("goes back to where a draft's page was opened from once it is saved, and home when opened on its own", async () => {
    const draft = JSON.stringify({ page: "strength.edit", draft: "00000000-0000-4000-8000-000000000001", rev: 1 });
    const path = `/extension/-health-intake/pages?state=${encodeURIComponent(draft)}`;
    // opened from a chat: the chat's entry is under it
    history.replaceState(null, "", "/persona/chat-1");
    history.pushState(null, "", path);
    const back = vi.spyOn(history, "back").mockImplementation(() => {});
    await open(path);
    expect(host.querySelector("h1")?.textContent).toBe("Adjust");
    await act(async () => host.querySelector<HTMLButtonElement>("button:not([aria-label])")!.click());
    await settle();
    expect(drawn().at(-1)?.body).toMatchObject({ action: "save" });
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
    // a page of its own (nothing under it in this tab): the Health home
    const length = vi.spyOn(history, "length", "get").mockReturnValue(1);
    await open(path);
    await act(async () => host.querySelector<HTMLButtonElement>("button:not([aria-label])")!.click());
    await settle();
    expect(location.pathname).toBe("/health/new");
    length.mockRestore();
  });
});
