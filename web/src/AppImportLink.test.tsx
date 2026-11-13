// @vitest-environment jsdom
// A provider import link (#5420) leaves the address bar as the page starts, before main.tsx waits for anything (the
// language's strings, App: lib/import-link.ts takes it, provider-link.test.ts checks how); App takes one pasted over a
// page already open, where only the fragment changes and nothing loads.
import { describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  history.replaceState(null, "", "/settings?tab=models");
});
vi.mock("./lib/store", async (load) => ({ ...(await load<typeof import("./lib/store")>()), connect: () => {}, disconnect: () => {} }));
import { dropImportLink, importLinkHeld } from "./lib/import-link";
import main from "./main.tsx?raw";
import "./App";

describe("a page opened with an import link", () => {
  it("is taken by main.tsx before anything it waits for: the language's strings and App", () => {
    const take = main.indexOf("\ntakeImportLinkOnStart();");
    expect(take).toBeGreaterThan(0);
    expect(take).toBeLessThan(main.indexOf("await "));
    expect(take).toBeLessThan(main.indexOf("import(\"./App\")"));
  });

  it("takes one opened on the same page, where only the fragment changes, and its history entry with it", () => {
    dropImportLink();
    const before = history.length;
    // the browser goes to the same address with a new fragment: a new entry, no load
    history.pushState(null, "", "/settings?tab=models#import=eyJr");
    dispatchEvent(new HashChangeEvent("hashchange"));
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models");
    expect(history.length).toBe(before + 1);
    expect(importLinkHeld()).toBe(`${location.origin}/settings?tab=models#import=eyJr`);
    // pasted over another page: the router goes to Model providers, which opens Add on it
    dropImportLink();
    history.pushState(null, "", "/overview#import=eyJs");
    dispatchEvent(new HashChangeEvent("hashchange"));
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models");
    expect(importLinkHeld()).toBe(`${location.origin}/overview#import=eyJs`);
    // the open page is signed in already: the rest of such a fragment goes too
    dropImportLink();
    history.pushState(null, "", "/settings?tab=models#token=jp_x&import=eyJt");
    dispatchEvent(new HashChangeEvent("hashchange"));
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models");
    // a fragment of the page's own is left alone
    dropImportLink();
    history.pushState(null, "", "/settings?tab=models#settings-providers");
    dispatchEvent(new HashChangeEvent("hashchange"));
    expect(location.hash).toBe("#settings-providers");
    expect(importLinkHeld()).toBeNull();
  });
});
