// @vitest-environment jsdom
// The lark module (#13404, web half; docs/web/web.md "Modules"): the Messages page, its sidebar entry and the "Show
// messages on home" row in Settings › Connectors › Lark come from the module host. On (the default), the entry shows
// after Inbox exactly when it did (this browser's switch, and a hub that reads Lark), `/messages` opens the page, and the
// row draws in the Lark section; the page's chunk loads only when it opens. Off (switched off, or a hub that publishes
// module flags without `module.lark`): no entry, no row, its addresses are unknown ones, and neither its code nor its
// strings are requested.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// the page as a mark: what matters here is whether its chunk is requested, and what it is given
const page = vi.hoisted(() => ({ loaded: 0 }));
vi.mock("./Messages", () => {
  page.loaded++;
  return { MessagesPage: ({ path }: { path: readonly string[] }) => <div data-testid="messages-page">{path.join("/")}</div> };
});
import { connectModuleHub, MODULES_FEATURE, registerModule, resetModulesForTest, type ModuleDescriptor } from "../../core/module-host";
import { ModulePage } from "../../core/ModulePage";
import { SettingsRows } from "../../core/SettingsRows";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../../core/module-switch";
import { t } from "../../i18n";
import { setSidebarCollapsed } from "../../lib/layout";
import { href, parse } from "../../lib/router";
import { getState, reset, subscribe } from "../../lib/store";
import { Sidebar } from "../../views/Sidebar";
import { setLarkHome } from "./home";
import { lark } from "./module";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const chunk = vi.fn();
const catalog = vi.fn();
function counted(over: Partial<ModuleDescriptor> = {}): ModuleDescriptor {
  const locales = Object.fromEntries(Object.entries(lark.locales!).map(([lang, load]) => [lang, () => (catalog(lang), load())]));
  return { ...lark, locales, load: () => (chunk(), lark.load()), ...over };
}
const setState = (patch: Partial<ReturnType<typeof getState>>) => Object.assign(getState(), patch);

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 12; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const links = () => [...host.querySelectorAll<HTMLAnchorElement>("a")].map((a) => a.textContent?.trim() ?? "");
const sidebar = async () => {
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
};
const MESSAGES = () => t("sidebar.nav_messages");

beforeEach(() => {
  connectModuleHub(getState, subscribe);
  localStorage.clear();
  forgetModuleSwitches();
  resetModulesForTest();
  chunk.mockClear();
  catalog.mockClear();
  page.loaded = 0;
  setSidebarCollapsed(false);
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("{}", { headers: { "Content-Type": "application/json" } }))));
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  resetModulesForTest();
  localStorage.clear();
  forgetModuleSwitches();
  vi.unstubAllGlobals();
});

describe("the lark module", () => {
  it("shows its entry after Inbox only while this browser's switch is on, without loading its code", async () => {
    registerModule(counted());
    await sidebar();
    expect(links()).not.toContain(MESSAGES());
    act(() => setLarkHome(true));
    await flush();
    const pages = links();
    expect(pages).toContain(MESSAGES());
    expect(pages.indexOf(MESSAGES())).toBe(pages.indexOf(t("sidebar.nav_inbox")) + 1);
    expect(host.querySelector('a[href="/messages"]')).not.toBeNull();
    // a hub that does not read Lark (features named, `lark` not among them) takes it away again
    setState({ features: ["starred_messages"], loaded: true });
    await sidebar();
    expect(links()).not.toContain(MESSAGES());
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
  });

  it("routes /messages to its page, loading its strings, then its code, then the page's own chunk", async () => {
    registerModule(counted());
    expect(parse("/messages")).toEqual({ name: "module", module: "lark", base: "messages", path: [] });
    expect(parse("/messages/oc_1")).toEqual({ name: "module", module: "lark", base: "messages", path: ["oc_1"] });
    expect(href({ name: "module", module: "lark", base: "messages", path: ["oc_1"] })).toBe("/messages/oc_1");
    act(() => root.render(<ModulePage route={{ name: "module", module: "lark", base: "messages", path: ["oc_1"] }} />));
    await vi.waitFor(async () => {
      await flush();
      expect(host.querySelector('[data-testid="messages-page"]')?.textContent).toBe("oc_1");
    }, { timeout: 10_000 });
    expect(catalog).toHaveBeenCalledTimes(1);
    expect(chunk).toHaveBeenCalledTimes(1);
    expect(page.loaded).toBe(1);
  });

  it("draws its row in the Lark connector's section without the page's chunk", async () => {
    registerModule(counted());
    act(() => root.render(<SettingsRows place="connectors.lark" />));
    await vi.waitFor(async () => {
      await flush();
      expect(host.textContent).toContain(t("lark-homeswitch.label"));
    }, { timeout: 10_000 });
    expect(chunk).toHaveBeenCalledTimes(1);
    expect(page.loaded).toBe(0);
  });

  it("switched off, is absent: no entry, no row, unknown addresses, nothing loaded", async () => {
    setModuleSwitchedOff("lark", true);
    setLarkHome(true);
    registerModule(counted());
    await sidebar();
    expect(links()).not.toContain(MESSAGES());
    expect(parse("/messages").name).toBe("not-found");
    expect(parse("/messages/oc_1").name).toBe("not-found");
    act(() => root.render(<SettingsRows place="connectors.lark" />));
    await flush();
    expect(host.textContent).toBe("");
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
  });

  it("follows the hub flag module.lark", async () => {
    setLarkHome(true);
    registerModule(counted());
    expect(lark.feature).toBe("module.lark");
    setState({ features: [MODULES_FEATURE, "lark"], loaded: true });
    await sidebar();
    expect(links()).not.toContain(MESSAGES());
    expect(parse("/messages").name).toBe("not-found");
    setState({ features: [MODULES_FEATURE, "lark", "module.lark"], loaded: true });
    await sidebar();
    expect(links()).toContain(MESSAGES());
    expect(parse("/messages").name).toBe("module");
  });
});
