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
  ExtensionUI: (props: { onTitle?: (title?: string) => void; onWide?: (wide: boolean) => void }) => {
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
import { ExtensionNavPage, OverviewCards } from "./ExtensionSlots";
import { arrangeOverview, type ArrangedCard } from "../../lib/overview-cards";
import type { UIPlace } from "../../lib/extension-ui";

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

  it("widens the workspace container to 1680px when a view requests wide mode and resets on page change", async () => {
    const container = () => host.querySelector<HTMLElement>("[data-extension-page-container]")!;
    await open("example-net", "topology");
    expect(container().className).toContain("max-w-[880px]");
    expect(container().className).not.toContain("max-w-[1680px]");

    const reportWide = data.props.at(-1)!.onWide as (wide: boolean) => void;
    await act(async () => reportWide(true));
    expect(container().className).toContain("max-w-[1680px]");
    expect(container().className).not.toContain("max-w-[880px]");

    await act(async () => reportWide(false));
    expect(container().className).toContain("max-w-[880px]");

    await act(async () => reportWide(true));
    expect(container().className).toContain("max-w-[1680px]");

    // Switching to another page before it calls onWide immediately resets to the standard column width.
    await open("example-plain", "summary");
    expect(container().className).toContain("max-w-[880px]");
    expect(container().className).not.toContain("max-w-[1680px]");
  });
});

describe("Dashboards phase 2 Overview cards (#13020)", () => {
  const WATCH_CARDS: UIPlace[] = [
    { extension: "-pages", extension_name: "Persona", id: "g_1a2b3c4d5e6f7081", title: "Elm Street rent", placement: "overview_card", size: "compact", opens: "pg_1a2b3c4d5e6f7081" },
    { extension: "-pages", extension_name: "Persona", id: "g_4d5e6f708192a3b4", title: "Morning weight", placement: "overview_card", size: "compact", opens: "pg_4d5e6f708192a3b4" },
  ];

  async function renderCards(prefs: Parameters<typeof arrangeOverview>[1] = []) {
    await initI18n("en");
    const items = arrangeOverview(WATCH_CARDS, prefs);
    const cards = items.filter((i): i is ArrangedCard<UIPlace> => "card" in i);
    await act(async () => root.render(<OverviewCards cards={cards} items={items} />));
    await act(async () => { await Promise.resolve(); });
  }

  it("draws -pages g_ overview cards as ordinary Overview cards and opens /extension/-pages/<pg id> on click", async () => {
    await renderCards();
    const first = host.querySelector<HTMLElement>('[data-extension-card="-pages/g_1a2b3c4d5e6f7081"]')!;
    const second = host.querySelector<HTMLElement>('[data-extension-card="-pages/g_4d5e6f708192a3b4"]')!;
    expect(first).not.toBeNull();
    expect(second).not.toBeNull();
    expect(first.querySelector("a")!.getAttribute("href")).toBe("/extension/-pages/pg_1a2b3c4d5e6f7081");
    expect(first.querySelector("[data-extension-name]")!.textContent).toBe("Persona");
    expect(data.props).toEqual(expect.arrayContaining([
      expect.objectContaining({ extension: "-pages", ui: "g_1a2b3c4d5e6f7081", compact: true, remember: true }),
      expect.objectContaining({ extension: "-pages", ui: "g_4d5e6f708192a3b4", compact: true, remember: true }),
    ]));

    await act(async () => first.click());
    expect(routes).toEqual([{ name: "extension", extension: "-pages", ui: "pg_1a2b3c4d5e6f7081" }]);
  });

  it("arranges, widens and hides -pages g_ cards like any Overview card", async () => {
    await renderCards([
      { extension: "-pages", id: "g_4d5e6f708192a3b4", span: "full" },
      { extension: "-pages", id: "g_1a2b3c4d5e6f7081", hidden: true },
    ]);
    expect(host.querySelector('[data-extension-card="-pages/g_1a2b3c4d5e6f7081"]')).toBeNull();
    const visible = host.querySelector<HTMLElement>('[data-extension-card="-pages/g_4d5e6f708192a3b4"]')!;
    expect(visible.className).toContain("col-span-full");
    expect(data.props.map((p) => p.ui)).toEqual(["g_4d5e6f708192a3b4"]);
  });
});
