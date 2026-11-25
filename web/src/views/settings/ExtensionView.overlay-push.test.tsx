// @vitest-environment jsdom
// Regression test (#14998): pushing a page from inside a MapCanvas Full screen overlay must replace the overlay's
// history entry (without copying personaOverlay* markers) so closing Full screen does not trigger history.back().
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";

const hub = vi.hoisted(() => ({
  draws: [] as Array<string | undefined>,
}));

vi.mock("../../lib/extension-ui", async (original) => {
  const real = await original<typeof import("../../lib/extension-ui")>();
  return {
    ...real,
    renderUI: async (_ext: string, _ui: string, _ctx: unknown, state?: string) => {
      hub.draws.push(state);
      if (state === "dest") {
        return real.readView({
          title: "Office",
          sections: [{ items: [{ type: "text", text: "Stop details for Office" }] }],
        });
      }
      return real.readView({
        title: "Commute Map",
        sections: [
          {
            items: [
              {
                type: "map",
                key: "commute-map",
                alt: "Commute route map",
                markers: [
                  { id: "origin", lat: 39.9087, lon: 116.3975, kind: "origin", label: "Home" },
                  {
                    id: "dest",
                    lat: 39.9929,
                    lon: 116.3340,
                    kind: "destination",
                    label: "Office",
                    action: { id: "open-stop", values: { id: "dest" }, push: { ui: "route-planner", state: "dest" } },
                  },
                ],
                routes: [
                  {
                    id: "r-fastest",
                    label: "Fastest",
                    mode: "driving",
                    points: [
                      { lat: 39.9087, lon: 116.3975 },
                      { lat: 39.9929, lon: 116.3340 },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      });
    },
  };
});
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));

import { LegacyExtensionUI } from "./ExtensionView";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

let host: HTMLDivElement;
let root: Root;

const settle = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  hub.draws.length = 0;
  history.replaceState(null, "", "/extension/example-maps/route-planner");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.querySelectorAll("[role=dialog]").forEach((d) => d.remove());
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("ExtensionView push from MapCanvas Full screen overlay", () => {
  it("keeps the pushed stop detail page after Full screen closes and returns cleanly on Back", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const backSpy = vi.spyOn(history, "back");

    await act(async () => {
      root.render(<LegacyExtensionUI extension="example-maps" ui="route-planner" />);
      await import("../extensions/ViewBody12");
    });
    await settle();
    expect(hub.draws).toEqual([undefined]);

    const fullBtn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_full_screen")}"]`)!;
    expect(fullBtn).toBeTruthy();
    await act(async () => fullBtn.click());
    await settle();

    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog).toBeTruthy();
    const lenAtOverlay = history.length;
    expect((history.state as { personaOverlay?: unknown })?.personaOverlay).toBeTypeOf("string");

    // Clicking a stop with action.push inside Full screen closes Full screen and pushes the detail page.
    const stopBtn = dialog.querySelector<HTMLButtonElement>('[data-map-stop="dest"]')!;
    await act(async () => stopBtn.click());
    await settle();
    await act(async () => { await vi.runAllTimersAsync(); });
    await settle();

    expect(backSpy).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(history.length).toBe(lenAtOverlay);
    expect(history.state).toEqual({ extPage: 2 });
    expect(host.textContent).toContain("Stop details for Office");
    expect(hub.draws).toEqual([undefined, "dest"]);

    // Browser Back returns cleanly to the root map page without reopening Full screen.
    await act(async () => {
      history.replaceState(null, "", location.href);
      window.dispatchEvent(new PopStateEvent("popstate", { state: null }));
    });
    await settle();
    expect(host.querySelector("[data-map-stage]")).toBeTruthy();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
});
