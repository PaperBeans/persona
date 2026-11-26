// @vitest-environment jsdom
// A manager's own pages (an id starting with "-") are drawn over HTTP only, never on the native UI stream, even on a hub
// whose hello offers the stream (Dashboards' `-pages`, #15017, and the reserved ids before it). A Dashboards page also
// tells the page around it its title, and the index opens a page as a page of its own.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const gate = vi.hoisted(() => ({
  ready: false,
  features: [] as string[],
  listeners: new Set<() => void>(),
  hubEvents: new Map<string, Set<(m: Record<string, unknown>) => void>>(),
  draws: vi.fn(),
  acts: vi.fn(),
  view: { title: "Morning run", sections: [{ items: [{ type: "text", text: "Legacy whole page" }] }] } as Record<string, unknown>,
}));
vi.mock("../../lib/store", async (original) => ({
  ...(await original<typeof import("../../lib/store")>()),
  useStore: (select: (state: { features: string[] }) => unknown) => select({ features: gate.features }),
  appSocketReady: () => gate.ready,
  onAppSocket: (listener: () => void) => { gate.listeners.add(listener); return () => { gate.listeners.delete(listener); }; },
  onHubEvent: (type: string, listener: (m: Record<string, unknown>) => void) => {
    if (!gate.hubEvents.has(type)) gate.hubEvents.set(type, new Set());
    gate.hubEvents.get(type)!.add(listener);
    return () => { gate.hubEvents.get(type)?.delete(listener); };
  },
}));
vi.mock("../../lib/extension-ui", async (original) => ({
  ...(await original<typeof import("../../lib/extension-ui")>()),
  renderUI: (...args: unknown[]) => { gate.draws(...args); return Promise.resolve(gate.view); },
  actUI: (...args: unknown[]) => { gate.acts(...args); return Promise.resolve({}); },
}));
const native = vi.hoisted(() => vi.fn());
vi.mock("./NativeExtensionUI", () => ({ NativeExtensionUI: (props: { extension: string }) => { native(props.extension); return <div data-native-page>Native page</div>; } }));
import { ExtensionUI } from "./ExtensionView";
import { httpOnlyUI } from "../../lib/extension-ui";

let host: HTMLDivElement, root: Root;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
async function streamHello() {
  await act(async () => { gate.features = ["extension_ui_stream_v1"]; gate.ready = true; gate.listeners.forEach((fn) => fn()); });
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  gate.ready = false; gate.features = []; gate.listeners.clear(); gate.hubEvents.clear(); gate.draws.mockClear(); gate.acts.mockClear(); native.mockClear();
  gate.view = { title: "Morning run", sections: [{ items: [{ type: "text", text: "Legacy whole page" }] }] };
  localStorage.setItem("persona.token", "synthetic-token");
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); localStorage.removeItem("persona.token"); });

describe("ids starting with '-' are HTTP only (#15017)", () => {
  it("knows a manager's id by its leading '-'", () => {
    expect(httpOnlyUI("-pages")).toBe(true);
    expect(httpOnlyUI("-subscriptions")).toBe(true);
    expect(httpOnlyUI("-health-intake")).toBe(true);
    expect(httpOnlyUI("example-pages")).toBe(false);
    expect(httpOnlyUI("pages")).toBe(false);
  });

  it.each([["-pages", "pg_3f9a1c0d5e7b2a41"], ["-pages", "g_1a2b3c4d5e6f7081"], ["-pages", "index"], ["-subscriptions", "list"]])("draws %s/%s over HTTP on a hub that offers the stream", async (extension, ui) => {
    await streamHello();
    await act(async () => root.render(<ExtensionUI extension={extension} ui={ui} remember />));
    await flush();
    expect(native).not.toHaveBeenCalled();
    expect(host.querySelector("[data-native-page]")).toBeNull();
    expect(gate.draws).toHaveBeenCalledTimes(1);
    expect(gate.draws.mock.calls[0]!.slice(0, 2)).toEqual([extension, ui]);
    expect(host.textContent).toContain("Legacy whole page");
  });

  it("never waits for the socket's hello to decide either", async () => {
    // no hello yet: an extension's page waits for it (it may open the stream); a manager's page is drawn at once
    await act(async () => { gate.features = ["extension_ui_stream_v1"]; });
    await act(async () => root.render(<ExtensionUI extension="-pages" ui="index" />));
    await flush();
    expect(gate.draws).toHaveBeenCalledTimes(1);
    expect(native).not.toHaveBeenCalled();
  });

  it("still opens the stream for an extension's own page", async () => {
    await streamHello();
    await act(async () => root.render(<ExtensionUI extension="example" ui="main" />));
    await act(async () => { await import("./NativeExtensionUI"); });
    await flush();
    expect(native).toHaveBeenCalledWith("example");
    expect(gate.draws).not.toHaveBeenCalled();
  });
});

describe("a Dashboards page tells the page around it what it is", () => {
  it("reports its first page's view title", async () => {
    const titles: (string | undefined)[] = [];
    await act(async () => root.render(<ExtensionUI extension="-pages" ui="pg_3f9a1c0d5e7b2a41" bare onTitle={(title) => titles.push(title)} />));
    await flush();
    expect(titles.at(-1)).toBe("Morning run");
  });

  it("hands a push to another page to the page around it, and keeps its own stack otherwise", async () => {
    gate.view = {
      title: "Dashboards",
      sections: [{ items: [
        { type: "row", title: "Reading list", action: { id: "open", push: { ui: "pg_8c2e4b6d1a0f9e37" } } },
        { type: "button", title: "Rename", action: { id: "persona.rename", push: { ui: "index", state: "rename", title: "Rename" } } },
      ] }],
    };
    const opened: unknown[] = [];
    const openPage = (p: { ui?: string; state?: string }) => {
      opened.push(p);
      return Boolean(p.ui && p.ui !== "index" && !p.state);
    };
    await act(async () => root.render(<ExtensionUI extension="-pages" ui="index" bare openPage={openPage} />));
    await flush();
    const row = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Reading list"))!;
    await act(async () => row.click());
    await flush();
    expect(opened).toEqual([{ ui: "pg_8c2e4b6d1a0f9e37" }]);
    expect(gate.draws).toHaveBeenCalledTimes(1); // the page around it opened it: nothing pushed here
    const rename = [...host.querySelectorAll("button")].find((b) => b.textContent === "Rename")!;
    await act(async () => rename.click());
    await flush();
    expect(opened).toHaveLength(2);
    expect(gate.draws).toHaveBeenCalledTimes(2); // its own stack: the rename form drawn here
    expect(gate.draws.mock.calls[1]!.slice(0, 2)).toEqual(["-pages", "index"]);
    expect(gate.acts).not.toHaveBeenCalled();
  });

  it("reloads a -pages g_ Overview card on extension_ui invalidations for its g_ id only (#13020)", async () => {
    const emit = (type: string, msg: Record<string, unknown>) => gate.hubEvents.get(type)?.forEach((l) => l({ type, ...msg }));
    gate.view = { title: "Elm Street rent", sections: [{ key: "persona.watch", items: [{ type: "metric", key: "persona.reading", label: "Rent", value: "2,150 USD", detail: "Checked today 08:02" }] }] };
    await act(async () => root.render(<ExtensionUI extension="-pages" ui="g_1a2b3c4d5e6f7081" context={{ size: "compact" }} compact remember />));
    await flush();
    expect(gate.draws).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("2,150 USD");

    // an invalidation for another page id leaves this Overview card alone
    gate.view = { title: "Elm Street rent", sections: [{ key: "persona.watch", items: [{ type: "metric", key: "persona.reading", label: "Rent", value: "1,985.5 USD", detail: "Below 2,000 USD · Checked today 08:02", tone: "caution", icon: "warning" }] }] };
    await act(async () => emit("extension_ui", { extension: "-pages", id: "pg_1a2b3c4d5e6f7081", invalidate: true }));
    await flush();
    expect(gate.draws).toHaveBeenCalledTimes(1);

    // an invalidation for the card's own g_ id reloads it in place
    await act(async () => emit("extension_ui", { extension: "-pages", id: "g_1a2b3c4d5e6f7081", invalidate: true }));
    await flush();
    expect(gate.draws).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("Below 2,000 USD · Checked today 08:02");
    expect(host.querySelector(".text-orange")).not.toBeNull();
  });
});
