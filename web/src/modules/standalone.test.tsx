// @vitest-environment jsdom
// The modules with a page of their own (src/modules/standalone.ts, #14404; docs/web/web.md "Modules"): what main.tsx
// draws in place of the app on such a page's address, before App and before anything is registered, and nothing at all
// on any other address or while the module is switched off.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isValidElement } from "react";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../core/module-switch";
import { registeredModules, resetModulesForTest, type ModuleDescriptor } from "../core/module-host";
import { screenShare } from "./screen-share/module";
import { ScreenView } from "./screen-share/ScreenView";
import { hubServesHere, STANDALONE_MODULES, standalonePage } from "./standalone";

vi.mock("../views/theme", () => ({})); // follows the system theme through matchMedia, which jsdom has none of

const chunk = vi.fn();
const catalog = vi.fn();
function counted(): ModuleDescriptor {
  const locales = Object.fromEntries(Object.entries(screenShare.locales!).map(([lang, load]) => [lang, () => (catalog(lang), load())]));
  return { ...screenShare, locales, load: () => (chunk(), screenShare.load()) };
}

// no hello in time: the hub flag rule's "no hello yet", every module on
const noHello = async () => null;
const helloWith = (features: string[]) => async () => features;

beforeEach(() => {
  localStorage.clear();
  forgetModuleSwitches();
  resetModulesForTest();
  chunk.mockClear();
  catalog.mockClear();
});

afterEach(() => {
  localStorage.clear();
  forgetModuleSwitches();
  resetModulesForTest();
  window.history.replaceState(null, "", "/");
});

describe("the modules' pages of their own", () => {
  it("is what main.tsx draws in place of the app on its addresses, before anything is registered", async () => {
    expect(STANDALONE_MODULES).toContain(screenShare);
    const list = [counted()];
    // another address is the app's: nothing registered, loaded or taken
    window.history.replaceState(null, "", "/kb/notes#token=t1");
    expect(await standalonePage(window.location.href, list, noHello)).toBeNull();
    expect(window.location.hash).toBe("#token=t1");
    expect(chunk).not.toHaveBeenCalled();
    window.history.replaceState(null, "", "/screen/mac#token=t1");
    const page = await standalonePage(window.location.href, list, noHello);
    expect(isValidElement(page) && page.type).toBe(ScreenView);
    expect(window.location.hash).toBe("");
    expect(catalog).toHaveBeenCalledTimes(1);
    expect(chunk).toHaveBeenCalledTimes(1);
    // the host's registry is App's: the page needed none of it
    expect(registeredModules()).toEqual([]);
  });

  it("switched off, leaves its address to the app with the fragment dropped, loading nothing", async () => {
    setModuleSwitchedOff("screen-share", true);
    window.history.replaceState(null, "", "/screen/mac#token=t1&embed=1");
    expect(await standalonePage(window.location.href, [counted()], noHello)).toBeNull();
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/screen/mac");
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
  });

  it("follows the hub's hello: off where it publishes module flags without the module's, loading nothing", async () => {
    window.history.replaceState(null, "", "/screen/mac#token=t1&embed=1");
    expect(await standalonePage(window.location.href, [counted()], helloWith(["modules", "lark"]))).toBeNull();
    expect(window.location.hash).toBe("");
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
    window.history.replaceState(null, "", "/screen/mac#token=t1");
    expect(isValidElement(await standalonePage(window.location.href, [counted()], helloWith(["modules", "module.screen-share"])))).toBe(true);
  });

  it("asks the hub with the page's own token, and takes no hello or an older hub as on", async () => {
    const seen: string[] = [];
    const hello = (features: string[] | null) => async (token: string) => (seen.push(token), features);
    expect(await hubServesHere(screenShare, "t1", hello(null))).toBe(true);
    expect(await hubServesHere(screenShare, "t1", hello(["lark"]))).toBe(true);
    expect(await hubServesHere(screenShare, "t1", hello(["modules"]))).toBe(false);
    expect(await hubServesHere({ ...screenShare, feature: undefined }, "t1", hello(["modules"]))).toBe(true);
    window.history.replaceState(null, "", "/screen/mac#token=from-app");
    await standalonePage(window.location.href, [counted()], hello(null));
    expect(seen.at(-1)).toBe("from-app");
  });
});
