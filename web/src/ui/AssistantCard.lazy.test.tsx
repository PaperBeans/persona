// @vitest-environment jsdom
/// <reference types="node" />
// AssistantCard.tsx stays a thin first-screen shell: its renderer body (AssistantCardBody.tsx) and heavy static
// dependencies load on demand when a message renders a card or a feed mounts a LiveCard, outside the idle tier.
import { readFileSync } from "node:fs";
import { act, useContext } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

const mocks = vi.hoisted(() => ({
  openLink: vi.fn(),
  navigate: vi.fn(),
  openTask: vi.fn(),
  openPanel: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../lib/links", async (orig) => ({ ...(await orig<typeof import("../lib/links")>()), openLink: mocks.openLink }));
vi.mock("../lib/router", async (orig) => ({ ...(await orig<typeof import("../lib/router")>()), navigate: mocks.navigate }));
vi.mock("../lib/handoff", async (orig) => ({ ...(await orig<typeof import("../lib/handoff")>()), openTask: mocks.openTask }));
vi.mock("../lib/panel", async (orig) => ({ ...(await orig<typeof import("../lib/panel")>()), openPanel: mocks.openPanel }));
vi.mock("./atoms", async (orig) => ({ ...(await orig<typeof import("./atoms")>()), toast: mocks.toast }));

import {
  AssistantCard,
  CARD_LANGUAGES,
  CardConversation,
  CardItem,
  HtmlRuntime,
  LiveCard,
  openTargetOf,
  VIEWS_FEATURE,
} from "./AssistantCard";
import { WEB_HTML_RUNTIME_VERIFIED } from "../lib/assistant-html";
import { applyView, resetCards } from "../lib/assistant-views";
import type { AssistantView } from "../lib/extension-wire";
import { getState } from "../lib/store";
import { idleImports } from "../startup-preload";
import { t } from "../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
const staticImports = (code: string) =>
  [...code.matchAll(/^\s*import\s+(?:type\s+)?[^;]*?from\s+["']([^"']+)["']/gm)]
    .filter((m) => !/^\s*import\s+type\s/.test(m[0]))
    .map((m) => m[1]);

describe("AssistantCard thin first-screen shell", () => {
  it("keeps rendering body and heavy static dependencies off the first screen and outside the idle tier", () => {
    const shell = read("./AssistantCard.tsx");
    const imports = staticImports(shell);
    for (const forbidden of [
      "./AssistantCardBody",
      "./HtmlCard",
      "../lib/assistant-view",
      "../lib/assistant-html",
      "../lib/assistant-views",
      "../lib/extension-ui",
      "../lib/extension-owner",
      "../views/extensions/ViewBody",
    ]) {
      expect(imports, forbidden).not.toContain(forbidden);
    }
    expect(shell).toMatch(/^import\s+["']\.\.\/views\/extensions\/view-sections-lazy["'];/m);
    expect(shell).toContain('lazyPage(() => import("./AssistantCardBody").then((m) => m.AssistantCardBody))');
    expect(shell).toContain('lazyPage(() => import("./AssistantCardBody").then((m) => m.LiveCardBody))');
    expect(idleImports(shell)).toEqual([]);
  });

  it("preserves exported constants, contexts, and HtmlRuntime default", () => {
    expect(CARD_LANGUAGES).toEqual(["persona-view", "persona-card"]);
    expect(VIEWS_FEATURE).toBe("assistant_views");
    function Probe() {
      return <>{String(useContext(HtmlRuntime))}</>;
    }
    expect(renderToStaticMarkup(<Probe />)).toBe(String(WEB_HTML_RUNTIME_VERIFIED));
  });
});

describe("openTargetOf", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("routes every supported target kind in-app and reports unsupported targets via toast", () => {
    openTargetOf("https://example.com/docs", "mgr-1");
    expect(mocks.openLink).toHaveBeenCalledWith("https://example.com/docs", undefined, "mgr-1");

    openTargetOf("persona://session/m1/s2");
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "session", mid: "m1", sid: "s2" });

    openTargetOf("persona://task/t-42");
    expect(mocks.openTask).toHaveBeenCalledWith("t-42");

    openTargetOf("persona://kb/notes/a/b");
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "kb", kind: "notes", id: "a/b" });

    openTargetOf("persona://file/m1/%2Ftmp%2Freport.md");
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "file", mid: "m1", path: "/tmp/report.md" });

    openTargetOf("javascript:alert(1)");
    expect(mocks.toast).toHaveBeenCalledWith(t("ui-assistantcard.cannot_open"), "error");
  });
});

describe("AssistantCard and LiveCard on-demand loading", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    resetCards("hub-a");
    Object.assign(getState(), { features: [VIEWS_FEATURE] });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  it("lazy-loads and renders persona-view, legacy persona-card, and LiveCard without eager loadPages", async () => {
    const viewCard = {
      title: "Lazy View Card",
      fallback: "fallback text",
      sections: [{ items: [{ type: "text", text: "Loaded view body" }] }],
    };
    const legacyCard = { type: "stat", title: "Steps", value: 4200 };
    const live: AssistantView = {
      id: "view_lazy_1",
      conversation: "c-lazy",
      version: 1,
      created_at: "",
      updated_at: "",
      view: {
        title: "Lazy Live Card",
        fallback: "live fallback",
        sections: [{ items: [{ type: "text", text: "Loaded live body" }] }],
      } as unknown as AssistantView["view"],
    };
    applyView("c-lazy", live);

    const viewCode = JSON.stringify(viewCard);
    const legacyCode = JSON.stringify(legacyCard);
    await act(async () => {
      root.render(
        <CardConversation.Provider value="c-lazy">
          <CardItem.Provider value={{ id: "item-lazy", text: "```persona-view\n" + viewCode + "\n```\n" }}>
            <AssistantCard lang="persona-view" code={viewCode} fallback={<pre>raw-view</pre>} />
            <AssistantCard lang="persona-card" code={legacyCode} fallback={<pre>raw-legacy</pre>} />
          </CardItem.Provider>
          <LiveCard conversation="c-lazy" id="view_lazy_1" first={live} />
        </CardConversation.Provider>,
      );
    });

    for (let i = 0; i < 60; i++) {
      const text = host.textContent ?? "";
      if (text.includes("Loaded view body") && text.includes("4,200") && text.includes("Loaded live body")) break;
      await act(async () => {
        await new Promise((r) => setTimeout(r, 25));
      });
    }

    expect(host.querySelector('[data-assistant-card="persona-view"]')).not.toBeNull();
    expect(host.querySelector('[data-assistant-card="persona-card"]')).not.toBeNull();
    expect(host.textContent).toContain("Lazy View Card");
    expect(host.textContent).toContain("Loaded view body");
    expect(host.textContent).toContain("4,200");
    expect(host.textContent).toContain("Lazy Live Card");
    expect(host.textContent).toContain("Loaded live body");
  }, 60_000);
});
