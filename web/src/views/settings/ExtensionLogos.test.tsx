// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CatalogItem, Market } from "../../lib/extension-market";
import type { HubExtensionDetail } from "../../lib/extensions";

const hub = vi.hoisted(() => ({ items: [] as CatalogItem[], installed: [] as HubExtensionDetail[] }));
vi.mock("../../lib/extension-market", async (original) => ({
  ...(await original<typeof import("../../lib/extension-market")>()),
  getCatalog: () => Promise.resolve({ markets: [{ id: "official", name: "Official", official: true }], extensions: hub.items }),
}));
vi.mock("../../lib/extensions", async (original) => ({
  ...(await original<typeof import("../../lib/extensions")>()),
  listExtensionsInfo: () => Promise.resolve({ extensions: hub.installed, markets: true }),
  extensionDetail: () => Promise.resolve(hub.installed[0]),
}));
import { catalogOf, marketView } from "../../lib/extension-market";
import { detailOf } from "../../lib/extensions";
import { initI18n } from "../../i18n";
import { Extensions } from "./Extensions";
import { MarketCatalog, MarketItem } from "./ExtensionMarket";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement, root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};
const extra = { icon_url: "https://example.com/logo.png", downloads: 0, downloads_updated_at: "2026-11-11T00:00:00Z" };
const item = (metadata: Partial<CatalogItem> = {}): CatalogItem => ({ market: "official", market_name: "Official", id: "example-ext", name: "Example", version: "1.0.0", ...metadata });
const installed = (metadata: Partial<HubExtensionDetail> = {}): HubExtensionDetail => ({ id: "example-ext", name: "Example", kind: "process", status: "running", source: "installed", enabled: true, config: {}, ...metadata });
const views = {
  "catalog list": () => <MarketCatalog onBack={() => {}} onOpen={() => {}} onMarkets={() => {}} />,
  "market detail": () => <MarketItem market="official" id="example-ext" onBack={() => {}} onInstalled={() => {}} />,
  "installed list": () => <Extensions open={null} onOpen={() => {}} />,
  "installed detail": () => <Extensions open="example-ext" onOpen={() => {}} />,
};
const reload = async () => {
  const button = host.querySelector<HTMLButtonElement>('button[aria-label*="刷新"], button[aria-label*="重新读取"], button[aria-label*="Reload"], button[aria-label*="Refresh"]');
  expect(button).toBeTruthy();
  act(() => button!.click()); await flush();
};
beforeEach(async () => {
  await initI18n("en");
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  hub.items = [item(extra)]; hub.installed = [installed(extra)];
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

for (const [name, view] of Object.entries(views)) describe(name, () => {
  it("loads public artwork anonymously and retains explicit zero downloads", async () => {
    act(() => root.render(view())); await flush();
    const image = host.querySelector<HTMLImageElement>("[data-extension-logo] img")!;
    expect(image.src).toBe(extra.icon_url);
    expect(image.className).toContain("opacity-0");
    expect(image.className).not.toContain("extension-logo-backing");
    expect(image.parentElement?.querySelector("svg")).not.toBeNull();
    act(() => image.dispatchEvent(new Event("load")));
    expect(image.parentElement?.querySelector("svg")).toBeNull();
    expect(image.className).toContain("bg-[var(--extension-logo-backing)]");
    expect(image.parentElement?.className).not.toContain("extension-logo-backing");
    expect(image.getAttribute("loading")).toBe("lazy"); expect(image.getAttribute("decoding")).toBe("async");
    expect(image.getAttribute("referrerpolicy")).toBe("no-referrer"); expect(image.crossOrigin).toBe("anonymous");
    expect(image.alt).toBe(""); expect(image.parentElement?.getAttribute("aria-hidden")).toBe("true");
    expect(host.querySelector("[data-extension-downloads]")?.textContent).toMatch(/0/);
  });
  it("preserves old API geometry and fallback when fields are absent", async () => {
    hub.items = [item()]; hub.installed = [installed()];
    act(() => root.render(view())); await flush();
    const tile = host.querySelector("[data-extension-logo]")!;
    expect(tile.querySelector("img")).toBeNull(); expect(tile.querySelector("svg")).not.toBeNull();
    expect(tile.className).toContain(name.includes("detail") ? "size-11" : name === "catalog list" ? "size-9" : "size-8");
    expect(host.querySelector("[data-extension-downloads]")).toBeNull();
  });
  it.each(["http://example.com/logo.png", "https://user:secret@example.com/logo.png", "https://user@example.com/logo.png", "//example.com/logo.png", "/logo.png", "data:image/png;base64,AAA", "javascript:alert(1)", "invalid"])("refuses unsafe artwork %s", async (icon_url) => {
    hub.items = [item({ icon_url })]; hub.installed = [installed({ icon_url })];
    act(() => root.render(view())); await flush();
    expect(host.querySelector("[data-extension-logo] img")).toBeNull();
    expect(host.querySelector("[data-extension-logo] svg")).not.toBeNull();
  });
  it("falls back after failure, retries changed URLs, and clears omitted metadata on refresh", async () => {
    act(() => root.render(view())); await flush();
    const tile = host.querySelector("[data-extension-logo]")!, classes = tile.className;
    act(() => tile.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(tile.querySelector("svg")).not.toBeNull(); expect(tile.className).toBe(classes);
    const update = { ...extra, icon_url: "https://example.com/new-logo.png", downloads: 1200 };
    hub.items = [item(update)]; hub.installed = [installed(update)]; await reload();
    expect(tile.querySelector<HTMLImageElement>("img")?.src).toBe(update.icon_url);
    expect(host.querySelector("[data-extension-downloads]")?.textContent).toContain("1,200");
    hub.items = [item()]; hub.installed = [installed()]; await reload();
    expect(tile.querySelector("img")).toBeNull(); expect(tile.querySelector("svg")).not.toBeNull();
    expect(host.querySelector("[data-extension-downloads]")).toBeNull();
  });
});

it("keeps API order and optional metadata through old/new response adapters", () => {
  const first = item({ ...extra, name: "Zebra", downloads: 1200 });
  const last = item({ name: "Alpha" });
  const market: Market = { id: "official", name: "Official", official: true, spec: "https://example.com", has_token: false, extensions: 2, downloads_updated_at: extra.downloads_updated_at };
  expect(catalogOf({ markets: [market], extensions: [first, last] }).extensions.map((x) => x.name)).toEqual(["Zebra", "Alpha"]);
  expect(catalogOf({ markets: [market], plugins: [item(extra)] } as Parameters<typeof catalogOf>[0]).extensions[0]).toMatchObject(extra);
  expect(marketView(market).downloads_updated_at).toBe(extra.downloads_updated_at);
  expect(detailOf(installed(extra))).toMatchObject(extra);
});

it.each(["en", "zh-Hans", "ja"])("defines package downloads and keeps zero visible in %s", async (language) => {
  await initI18n(language); act(() => root.render(views["market detail"]())); await flush();
  expect(host.querySelector("[data-extension-downloads]")?.textContent).toMatch(/0/);
  expect(host.textContent).not.toContain("settings-extensions.package_downloads");
});

it("uses the English singular for one package download", async () => {
  hub.items = [item({ downloads: 1 })];
  act(() => root.render(views["catalog list"]())); await flush();
  expect(host.querySelector("[data-extension-downloads]")?.textContent).toBe("1 package download");
});

it.each([null, -1, 1.5, NaN, Infinity])("keeps malformed installed counts from adding an empty line %s", async (downloads) => {
  hub.installed = [installed({ downloads } as Partial<HubExtensionDetail>)];
  act(() => root.render(views["installed list"]())); await flush();
  const row = host.querySelector('[data-extension-row="example-ext"]')!;
  expect(row.querySelector("[data-extension-downloads]")).toBeNull();
  expect(row.querySelector("[data-extension-logo]")?.nextElementSibling?.childElementCount).toBe(1);
});

it.each([-1, 1.5, NaN, Infinity])("does not fabricate malformed counts %s", async (downloads) => {
  hub.items = [item({ downloads })]; act(() => root.render(views["catalog list"]())); await flush();
  expect(host.querySelector("[data-extension-downloads]")).toBeNull();
});
