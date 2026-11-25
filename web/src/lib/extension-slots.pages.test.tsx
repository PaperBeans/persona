// @vitest-environment jsdom
// Dashboards (#15017): the default manager lists its `-pages` places after the extensions' (the index while a page is
// kept, then each pinned page). Keeping, pinning, unpinning, renaming, removing or restoring a page, or turning the
// switch, sends assistant_pages: the places list is asked for again, so the sidebar shows the change without a reload.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({ listeners: new Map<string, ((m: Record<string, unknown>) => void)[]>() }));
const STATE = vi.hoisted(() => ({ features: [] as string[], conn: "connecting", version: "1" }));
vi.mock("./store", async (original) => {
  const real = await original<typeof import("./store")>();
  return {
    ...real,
    getState: () => ({ ...real.getState(), ...STATE }),
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T => select({ ...real.getState(), ...STATE } as ReturnType<typeof real.getState>),
    onHubEvent: (type: string, l: (m: Record<string, unknown>) => void) => {
      hub.listeners.set(type, [...(hub.listeners.get(type) ?? []), l]);
      return () => {};
    },
  };
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Place = { extension: string; extension_name?: string; id: string; placement: string; title: string; icon?: string };
const today: Place = { extension: "example-today", id: "today", placement: "nav_page", title: "Today", icon: "calendar" };
const index: Place = { extension: "-pages", extension_name: "Persona", id: "index", placement: "nav_page", title: "Dashboards", icon: "chart" };
const run: Place = { extension: "-pages", extension_name: "Persona", id: "pg_3f9a1c0d5e7b2a41", placement: "nav_page", title: "Morning run", icon: "heart" };
const emit = (type: string, msg: Record<string, unknown> = {}) => hub.listeners.get(type)?.forEach((l) => l({ type, ...msg }));
let answer: Place[] = [];
let asked = 0;
let host: HTMLDivElement;
let root: Root;
let slots: typeof import("./extension-slots");
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};

function Sidebar() {
  const entries = slots.useExtensionSlot("nav_page");
  return <p data-entries>{entries.map((u) => `${u.extension}/${u.id}:${u.title}`).join(",")}</p>;
}
const entries = () => host.querySelector("[data-entries]")!.textContent;

beforeEach(async () => {
  localStorage.clear();
  localStorage.setItem("persona.token", "jd_example_browser_credential");
  answer = [today];
  asked = 0;
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (url.includes("/-/ui")) asked++;
    return { ok: true, status: 200, text: async () => JSON.stringify(url.includes("/-/ui") ? { ui: answer } : {}), headers: new Headers() };
  }));
  host = document.createElement("div");
  document.body.append(host);
  hub.listeners.clear();
  STATE.features = [];
  STATE.conn = "connecting";
  vi.resetModules();
  slots = await import("./extension-slots");
  root = createRoot(host);
  act(() => root.render(<Sidebar />));
  STATE.features = ["extension_ui_slots", "extensions", "assistant_pages"];
  STATE.conn = "online";
  act(() => emit("hello", { server_version: "1" }));
  await flush();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("assistant_pages refreshes the places (#15017)", () => {
  it("asks for the list again and draws what changed: the first Keep, a pin, the switch turned off", async () => {
    expect(asked).toBe(1);
    expect(entries()).toBe("example-today/today:Today");

    answer = [today, index];
    act(() => emit("assistant_pages"));
    await flush();
    expect(asked).toBe(2);
    expect(entries()).toBe("example-today/today:Today,-pages/index:Dashboards");

    answer = [today, index, run];
    act(() => emit("assistant_pages"));
    await flush();
    expect(asked).toBe(3);
    expect(entries()).toBe("example-today/today:Today,-pages/index:Dashboards,-pages/pg_3f9a1c0d5e7b2a41:Morning run");

    answer = [today];
    act(() => emit("assistant_pages"));
    await flush();
    expect(entries()).toBe("example-today/today:Today");
  });

  it("does not ask on a page's own content change (extension_ui invalidate), which redraws only that page", async () => {
    answer = [today, index];
    act(() => emit("extension_ui", { extension: "-pages", id: "index", invalidate: true }));
    await flush();
    expect(asked).toBe(1);
    expect(entries()).toBe("example-today/today:Today");
  });

  it("asks nothing on a hub without UI slots", async () => {
    STATE.features = ["assistant_pages"];
    act(() => emit("assistant_pages"));
    await flush();
    expect(asked).toBe(1);
    expect(entries()).toBe("");
  });
});
