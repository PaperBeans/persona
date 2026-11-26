// @vitest-environment jsdom
// @vitest-environment-options {"url":"https://hub.example.com/"}
// A strength training draft's card in a Health chat (#14713, docs/protocol/protocol.md, strength training): its Adjust is
// an https link to the hub's own page (`<hub>/extension/-health-intake/pages?state={"page":"strength.edit",…}`), which the
// web opens in the app, in place of the chat, never in a new tab. The card is the one the hub's code drew
// (scripts/mock/fixtures/health-strength-ui/), with the hub's public address the one this page is on.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { AssistantCard, CardConversation, CardItem, VIEWS_FEATURE } from "../../ui/AssistantCard";
import { resetCards } from "../../lib/assistant-views";
import { loadPages } from "../../lib/lazy-page";
import { parse } from "../../lib/router";
import { getState } from "../../lib/store";

beforeAll(() => loadPages(), 60_000);
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FIXTURE = join(__dirname, "../../../../scripts/mock/fixtures/health-strength-ui/health-strength-ui.zh-Hans.v11.card.view.json");
type Item = { type: string; title?: string; action?: { id: string; open?: string } };
const exported = JSON.parse(readFileSync(FIXTURE, "utf8")) as { view: { sections: { items: Item[] }[] } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  resetCards("hub-a");
  Object.assign(getState(), { features: [VIEWS_FEATURE] });
  history.replaceState(null, "", "/health/chat-1");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("a strength training card's Adjust", () => {
  it("opens the draft's edit page in the app, in place of the chat, not in a new tab", async () => {
    const view = exported.view;
    const adjust = view.sections[0].items.find((i) => i.action?.id === "adjust")!;
    expect(new URL(adjust.action!.open!).origin).toBe(location.origin);
    const opened = vi.spyOn(window, "open").mockImplementation(() => null);
    const code = JSON.stringify(view);
    act(() =>
      root.render(
        <CardConversation.Provider value="chat-1">
          <CardItem.Provider value={{ id: "item-1", text: "```persona-view\n" + code + "\n```\n" }}>
            <AssistantCard lang="persona-view" code={code} fallback={<pre />} />
          </CardItem.Provider>
        </CardConversation.Provider>,
      ),
    );
    await flush();
    const button = [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === adjust.title);
    expect(button, "Adjust").toBeTruthy();
    await act(async () => button!.click());
    await flush();
    expect(opened).not.toHaveBeenCalled();
    expect(location.pathname).toBe("/extension/-health-intake/pages");
    const route = parse(location.pathname, location.search.slice(1));
    expect(route).toMatchObject({ name: "extension", extension: "-health-intake", ui: "pages" });
    expect(JSON.parse(new URLSearchParams(location.search).get("state")!)).toMatchObject({ page: "strength.edit", rev: 1 });
  });
});
