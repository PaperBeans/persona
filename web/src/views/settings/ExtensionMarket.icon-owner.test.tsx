// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { defaultManager, pinManager, resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { getState, reset } from "../../lib/store";
import { MarketCatalog, MarketItem } from "./ExtensionMarket";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const icon = `/api/v1/extensions/-/catalog/official/notes/icon?sha256=${"a".repeat(64)}`;
const catalog = {
  markets: [{ id: "official", name: "Official", spec: "https://example.com/catalog", official: true, has_token: false, extensions: 1 }],
  extensions: [{ market: "official", market_name: "Official", id: "notes", name: "Example Notes", version: "1.0.0", icon_url: icon }],
};
let host: HTMLDivElement, root: Root;
let calls: { path: string; headers: Headers; init: RequestInit }[];
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); }); };
beforeEach(async () => {
  await initI18n("en");
  reset(); getState().features.push("extensions");
  resetManagerChoice(); setRosterDefault("default-a"); setToken(`synthetic-market-${Math.random()}`);
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:market-icon") });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
  calls = [];
  vi.stubGlobal("fetch", vi.fn(async (input, init: RequestInit = {}) => {
    const path = String(input); calls.push({ path, headers: new Headers(init.headers), init });
    if (path === icon) return new Response("png-fixture", { headers: { "Content-Type": "image/png" } });
    if (path.startsWith("/api/v1/extensions/-/catalog")) return new Response(JSON.stringify(catalog), { headers: { "Content-Type": "application/json" } });
    return new Response(JSON.stringify({ managers: [], extensions: [], operations: [] }), { headers: { "Content-Type": "application/json" } });
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); reset(); resetManagerChoice(); });

it("the real MarketItem catalog and its authenticated catalog-icon path both name the explicit owner", async () => {
  act(() => root.render(<MarketItem market="official" id="notes" manager="owner-b" onBack={() => {}} onInstalled={() => {}} />));
  await flush();
  const json = calls.find(call => call.path.startsWith("/api/v1/extensions/-/catalog?"))!;
  const image = calls.find(call => call.path === icon)!;
  expect(json).toBeDefined(); expect(image).toBeDefined();
  for (const call of [json, image]) {
    expect(call.headers.get("X-Persona-Manager")).toBe("owner-b");
    expect(call.headers.get("X-Persona-Client")).toBe("web/1");
    expect(call.headers.get("Authorization")).toMatch(/^Bearer synthetic-market-/);
    expect(call.init.redirect).toBe("manual");
  }
  expect(image.init.credentials).toBe("same-origin");
  expect(host.querySelector("[data-extension-logo] img")?.getAttribute("src")).toBe("blob:market-icon");
});

it("MarketCatalog artwork stays on the resolved default even when a catalog path is pinned elsewhere", async () => {
  const unpin = pinManager("/extensions/-/catalog", "unrelated-owner");
  try {
    act(() => root.render(<MarketCatalog onBack={() => {}} onOpen={() => {}} onMarkets={() => {}} />));
    await flush();
    const image = calls.find(call => call.path === icon)!;
    expect(image).toBeDefined();
    expect(image.headers.get("X-Persona-Manager")).toBe(defaultManager());
  } finally { unpin(); }
});
