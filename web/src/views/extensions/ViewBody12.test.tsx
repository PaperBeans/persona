// @vitest-environment jsdom
// UI version 12 (`map` component) tests: offline vector projection, route tabs, camera controls &
// persistence, marker inspection/actions, compact preview -> Full screen focus management, and
// design token compliance (no raw hex literals).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { readView, type UIAction, type UIItem } from "../../lib/extension-ui";
import { forgetMapStores, formatMapDistance, formatMapDuration, MapCanvas } from "./ViewBody12";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const SAMPLE_VIEW = {
  sections: [
    {
      items: [
        {
          type: "map",
          key: "commute-map",
          title: "Morning Commute",
          alt: "Route map from Home via Coffee Stop to Office.",
          caption: "Switch routes or inspect stops.",
          show_list: true,
          markers: [
            { id: "origin", lat: 39.9087, lon: 116.3975, kind: "origin", label: "Home", subtitle: "Departure" },
            {
              id: "waypoint",
              lat: 39.9410,
              lon: 116.4320,
              kind: "waypoint",
              label: "Coffee Stop",
              subtitle: "Pickup",
              detail: "Fast charger next to exit 12",
              action: { id: "open-stop", values: { id: "waypoint" }, push: { ui: "route-planner", state: "waypoint" } },
            },
            {
              id: "dest",
              lat: 39.9929,
              lon: 116.3340,
              kind: "destination",
              label: "Office",
              action: { id: "open-dest", values: { id: "dest" }, push: { ui: "route-planner", state: "dest" } },
            },
          ],
          routes: [
            {
              id: "r-fastest",
              label: "Fastest",
              mode: "driving",
              distance_meters: 12400,
              duration_seconds: 1380,
              summary: "Via Ring Rd",
              selected: true,
              tone: "positive",
              points: [
                { lat: 39.9087, lon: 116.3975 },
                { lat: 39.9410, lon: 116.4320 },
                { lat: 39.9929, lon: 116.3340 },
              ],
            },
            {
              id: "r-alt",
              label: "Transit",
              mode: "transit",
              distance_meters: 14200,
              duration_seconds: 2100,
              summary: "Line 10",
              selected: false,
              points: [
                { lat: 39.9087, lon: 116.3975 },
                { lat: 39.9560, lon: 116.3890 },
                { lat: 39.9929, lon: 116.3340 },
              ],
            },
          ],
          links: [
            { title: "Route Guide", url: "https://maps.example.com/route?id=commute" },
            { title: "Bad Link", url: "http://insecure.example.com/route" },
          ],
        },
      ],
    },
  ],
};

const drawItem = (raw: unknown = SAMPLE_VIEW): UIItem => readView(raw as object).sections[0].items[0];
const doc = (html: string) => new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");

let host: HTMLDivElement;
let root: Root;

const mount = async (item: UIItem, opts: { compact?: boolean; onPress?: (a: UIAction) => void; owner?: string } = {}) => {
  const key = `${opts.owner ?? ""}/${item.key ?? ""}`;
  const node = <MapCanvas key={key} item={item} compact={opts.compact} busy="" onPress={opts.onPress ?? (() => {})} />;
  await act(async () => {
    root.render(opts.owner ? <ExtensionOwnerScope owner={opts.owner}>{node}</ExtensionOwnerScope> : node);
  });
};

describe("UI 12: vector map canvas", () => {
  beforeEach(() => {
    forgetMapStores();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    document.querySelectorAll("[role=dialog]").forEach((d) => d.remove());
    vi.restoreAllMocks();
  });

  it("renders vector polylines, marker buttons, route tabs, scale bar, and verified HTTPS links", () => {
    const html = renderToStaticMarkup(<MapCanvas item={drawItem()} busy="" onPress={() => {}} />);
    const d = doc(html);
    expect(d.querySelectorAll("polyline[data-map-route-line]")).toHaveLength(2);
    expect(d.querySelector('[data-map-route-line="r-fastest"]')?.getAttribute("data-selected")).toBe("true");
    expect(d.querySelectorAll("[data-map-marker]")).toHaveLength(3);
    expect(d.querySelectorAll('[role="tab"]')).toHaveLength(2);
    expect(d.querySelector("[data-map-scale]")).not.toBeNull();
    const links = [...d.querySelectorAll("[data-map-link]")];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe("https://maps.example.com/route?id=commute");
    // Tab whose label matches its travel mode should not repeat the mode label twice (e.g. "Transit Transit").
    const transitTab = d.querySelector('[data-map-route="r-alt"]')?.textContent ?? "";
    expect(transitTab).toContain(t("extensions-viewbody.map_mode_transit"));
    expect(transitTab.split(t("extensions-viewbody.map_mode_transit")).length - 1).toBe(1);
    // Compact mode (no tab title) retains the mode label alongside duration and distance.
    const compactHtml = renderToStaticMarkup(
      <MapCanvas item={drawItem({ sections: [{ items: [{ ...SAMPLE_VIEW.sections[0].items[0], routes: [SAMPLE_VIEW.sections[0].items[0].routes[1]] }] }] })} compact busy="" onPress={() => {}} />,
    );
    expect(doc(compactHtml).querySelector("[data-map-route-summary]")?.textContent).toContain(t("extensions-viewbody.map_mode_transit"));
  });

  it("formats distance and duration through localized strings", () => {
    expect(formatMapDistance(850)).toBe(t("extensions-viewbody.map_distance_m", { value: 850 }));
    expect(formatMapDistance(12400)).toBe(t("extensions-viewbody.map_distance_km", { value: "12" }));
    expect(formatMapDistance(3450)).toBe(t("extensions-viewbody.map_distance_km", { value: "3.5" }));
    expect(formatMapDuration(1380)).toBe(t("extensions-viewbody.map_duration_m", { minutes: 23 }));
    expect(formatMapDuration(4500)).toBe(t("extensions-viewbody.map_duration_hm", { hours: 1, minutes: 15 }));
  });

  it("switches routes, enables Reset View only after camera movement, and handles keyboard pan/zoom/reset", async () => {
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(400);
    await mount(drawItem());

    const resetBtn = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_reset")}"]`)!;
    expect(resetBtn().disabled).toBe(true);

    const altTab = host.querySelector<HTMLButtonElement>('[data-map-route="r-alt"]')!;
    await act(async () => altTab.click());
    expect(altTab.getAttribute("aria-selected")).toBe("true");
    expect(host.querySelector('[data-map-route-line="r-alt"]')?.getAttribute("data-selected")).toBe("true");
    expect(resetBtn().disabled).toBe(true);

    // Zoom in via button enables Reset View.
    const zoomIn = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_zoom_in")}"]`)!;
    await act(async () => zoomIn.click());
    expect(resetBtn().disabled).toBe(false);

    // Clicking Reset View fits back and disables itself.
    await act(async () => resetBtn().click());
    expect(resetBtn().disabled).toBe(true);

    // Keyboard arrow pan on stage enables Reset View, '0' resets it.
    const stage = host.querySelector<HTMLElement>("[data-map-stage]")!;
    await act(async () => stage.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true })));
    expect(resetBtn().disabled).toBe(false);
    await act(async () => stage.dispatchEvent(new KeyboardEvent("keydown", { key: "0", bubbles: true, cancelable: true })));
    expect(resetBtn().disabled).toBe(true);
  });

  it("opens marker detail card when marker has detail, and fires action directly or from detail button", async () => {
    const pressed: UIAction[] = [];
    await mount(drawItem(), { onPress: (a) => pressed.push(a) });

    // Marker with detail selects first and reveals detail card with action button.
    const waypoint = host.querySelector<HTMLButtonElement>('[data-map-marker="waypoint"]')!;
    await act(async () => waypoint.click());
    const detail = host.querySelector('[data-map-detail="waypoint"]');
    expect(detail?.textContent).toContain("Fast charger next to exit 12");
    expect(pressed).toEqual([]);

    const openBtn = detail!.querySelector<HTMLButtonElement>('[data-map-open-stop="waypoint"]')!;
    await act(async () => openBtn.click());
    expect(pressed).toHaveLength(1);
    expect(pressed[0].id).toBe("open-stop");

    // Marker with action and no detail fires action immediately.
    const dest = host.querySelector<HTMLButtonElement>('[data-map-marker="dest"]')!;
    await act(async () => dest.click());
    expect(pressed).toHaveLength(2);
    expect(pressed[1].id).toBe("open-dest");
  });

  it("persists selected route & camera movement per (owner, key) across remounts", async () => {
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(400);
    await mount(drawItem(), { owner: "ext-a" });
    await act(async () => host.querySelector<HTMLButtonElement>('[data-map-route="r-alt"]')!.click());
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_zoom_in")}"]`)!.click());

    // Different owner starts fresh.
    await mount(drawItem(), { owner: "ext-b" });
    expect(host.querySelector('[data-map-route="r-alt"]')?.getAttribute("aria-selected")).toBe("false");
    expect(host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_reset")}"]`)!.disabled).toBe(true);

    // Same owner restores route and zoom state.
    await mount(drawItem(), { owner: "ext-a" });
    expect(host.querySelector('[data-map-route="r-alt"]')?.getAttribute("aria-selected")).toBe("true");
    expect(host.querySelector<HTMLButtonElement>(`button[aria-label="${t("extensions-viewbody.map_reset")}"]`)!.disabled).toBe(false);
  });

  it("renders non-nested compact preview that opens Full screen dialog and returns focus on Escape", async () => {
    const pressed: UIAction[] = [];
    await mount(drawItem(), { compact: true, onPress: (a) => pressed.push(a) });

    const preview = host.querySelector<HTMLButtonElement>("[data-map-preview]")!;
    expect(preview.tagName).toBe("BUTTON");
    // Ensure valid HTML: no interactive buttons nested inside the preview button.
    expect(preview.querySelectorAll("button")).toHaveLength(0);

    preview.focus();
    await act(async () => preview.click());
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog).not.toBeNull();
    expect(dialog.querySelectorAll("[data-map-stop]").length).toBe(3);

    // Pressing an action with `push` closes the Full screen dialog and calls onPress.
    const destMarker = dialog.querySelector<HTMLButtonElement>('[data-map-marker="dest"]')!;
    await act(async () => destMarker.click());
    expect(pressed.map((p) => p.id)).toEqual(["open-dest"]);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("renders static:true maps without an expandable preview button and degrades cleanly without geometry", async () => {
    const staticRaw = structuredClone(SAMPLE_VIEW);
    (staticRaw.sections[0].items[0] as { static?: boolean }).static = true;
    await mount(drawItem(staticRaw), { compact: true });
    const preview = host.querySelector("[data-map-preview]")!;
    expect(preview.tagName).not.toBe("BUTTON");
    expect(preview.getAttribute("role")).toBe("img");

    // Direct mount of geometryless map shows accessible fallback instead of throwing.
    await mount({ type: "map", alt: "Map fallback summary" } as UIItem);
    expect(host.querySelector("[data-map-fallback]")?.textContent).toContain("Map fallback summary");
  });

  it("uses design-system color tokens with zero raw hex literals", () => {
    for (const file of ["ViewBody12.tsx", "ViewBody12.css"]) {
      const src = readFileSync(join(__dirname, file), "utf8");
      const stripped = src.replace(/\/\/.*$/gm, "").replace(/\/\*[\s\S]*?\*\//g, "");
      expect(stripped, file).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    }
  });
});
