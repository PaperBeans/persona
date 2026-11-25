// @vitest-environment jsdom
// Dashboards pages in the extension page's top bar (#15017): an unpinned page is not in the places list, so a deep link
// (/extension/-pages/pg_…) is titled by the page's own view, never by its id; every page but the index says under its
// title, at every width and in the app's words, that Persona keeps it; the index opens a page as a page of its own.
import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { loadPages } from "../../lib/lazy-page";

type Place = { extension: string; id: string; title?: string; extension_name?: string; icon?: string };
const data = vi.hoisted(() => ({ places: [] as Place[], title: undefined as string | undefined, props: [] as Record<string, unknown>[] }));
vi.mock("../../lib/extension-slots", () => ({
  useExtensionPlace: (extension: string, id: string) => data.places.find((p) => p.extension === extension && p.id === id),
  useExtensionSlot: () => [],
}));
vi.mock("../settings/ExtensionView", () => ({
  // the page's view, drawn: it says its title as the hub wrote it
  ExtensionUI: (props: { onTitle?: (title?: string) => void }) => {
    data.props.push(props);
    const { onTitle } = props;
    useEffect(() => {
      if (data.title !== undefined) onTitle?.(data.title);
    }, [onTitle]);
    return null;
  },
}));
const routes = vi.hoisted(() => [] as unknown[]);
vi.mock("../../lib/router", async (original) => ({ ...(await original<typeof import("../../lib/router")>()), navigate: (to: unknown) => routes.push(to) }));
import { ExtensionNavPage } from "./ExtensionSlots";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const RUN = "pg_3f9a1c0d5e7b2a41";
let host: HTMLDivElement;
let root: Root;
beforeAll(() => loadPages(), 60_000);
beforeEach(() => {
  data.places = [];
  data.title = undefined;
  data.props = [];
  routes.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

async function open(extension: string, ui: string, lang = "en") {
  await initI18n(lang);
  await act(async () => root.render(<ExtensionNavPage extension={extension} ui={ui} onToggleSidebar={() => {}} />));
  await act(async () => { await Promise.resolve(); });
}
const heading = () => host.querySelector("header h1")!.textContent;
const line = () => host.querySelector<HTMLElement>("header [data-ownership-line]");

describe("a Dashboards page's top bar (#15017)", () => {
  it("titles an unpinned page opened by a deep link with its view's title, not its id", async () => {
    data.title = "Morning run";
    await open("-pages", RUN);
    expect(heading()).toBe("Morning run");
    expect(host.querySelector("header")!.textContent).not.toContain("pg_");
  });

  it("says the product's name, never the id, before the view has drawn", async () => {
    await open("-pages", RUN);
    expect(heading()).toBe(t("extensions-extensionslots.dashboards"));
    expect(heading()).toBe("Dashboards");
  });

  it("prefers the view's title to the place's, which may be older (a rename on another device)", async () => {
    data.places = [{ extension: "-pages", id: RUN, title: "Morning run", extension_name: "Persona", icon: "heart" }];
    await open("-pages", RUN);
    expect(heading()).toBe("Morning run");
    data.title = "Run club";
    await open("-pages", "pg_8c2e4b6d1a0f9e37");
    await open("-pages", RUN);
    expect(heading()).toBe("Run club");
  });

  it("says Persona keeps the page under its title, at every width, in each language", async () => {
    for (const [lang, words] of [["en", "Kept up to date by Persona"], ["zh-Hans", "由 Persona 维护"], ["ja", "Persona が更新しています"]] as const) {
      data.title = "Morning run";
      await open("-pages", RUN, lang);
      expect(line()!.textContent).toBe(words);
      // never only from a breakpoint up, as the extension's name beside a title is
      const hidden = (el: HTMLElement | null): boolean => !!el && el.tagName !== "HEADER" && (/(^|\s)hidden(\s|$)/.test(el.className) || hidden(el.parentElement));
      expect(hidden(line())).toBe(false);
      // and the extension's name is not written beside it as well
      expect(host.querySelector("header")!.textContent).toBe(`Morning run${words}`);
    }
    await initI18n("en");
  });

  it("has no ownership line on the index, nor on an extension's own page", async () => {
    data.places = [{ extension: "-pages", id: "index", title: "Dashboards", extension_name: "Persona", icon: "chart" }];
    await open("-pages", "index");
    expect(heading()).toBe("Dashboards");
    expect(line()).toBeNull();
    data.places = [{ extension: "example", id: "usage", title: "Usage", extension_name: "Example" }];
    data.title = "Not this";
    await open("example", "usage");
    expect(heading()).toBe("Usage");
    expect(line()).toBeNull();
  });

  it("keeps an extension's own page titled by its place, its id when unlisted", async () => {
    data.title = "From the view";
    await open("example", "usage");
    expect(heading()).toBe("usage");
    expect(data.props.at(-1)!.onTitle).toBeUndefined();
    expect(data.props.at(-1)!.openPage).toBeUndefined();
  });

  it("opens a page the index pushes as a page of its own, and leaves other pushes to the page's stack", async () => {
    await open("-pages", "index");
    const openPage = data.props.at(-1)!.openPage as (p: { ui?: string; state?: string }) => boolean;
    expect(openPage({ ui: RUN })).toBe(true);
    expect(routes).toEqual([{ name: "extension", extension: "-pages", ui: RUN }]);
    expect(openPage({ ui: "index", state: "rename" })).toBe(false);
    expect(openPage({ ui: RUN, state: "rename" })).toBe(false);
    expect(routes).toHaveLength(1);
  });
});
