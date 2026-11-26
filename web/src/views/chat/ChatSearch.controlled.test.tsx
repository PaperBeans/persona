// @vitest-environment jsdom
// Controlled ChatSearch panels can supply precomputed results, custom matchers and earlier-loading state;
// default callers retain the debounced searchChat and internal earlier-loading lifecycle.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { ChatSearchHit, ChatSearchResults } from "../../lib/chat-search";
import type { Item } from "../../lib/types";
import type { ChatSearchProps } from "./ChatSearch";

const mocks = vi.hoisted(() => ({
  searchChat: vi.fn(),
}));

vi.mock("../../lib/chat-search-match", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/chat-search-match")>();
  mocks.searchChat.mockImplementation(actual.searchChat);
  return {
    ...actual,
    searchChat: mocks.searchChat,
  };
});

const { ChatSearch } = await import("./ChatSearch");
const { searchChat: realSearchChat } = await vi.importActual<typeof import("../../lib/chat-search-match")>("../../lib/chat-search-match");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const defaultItems: Item[] = [
  { id: "u#1", seq: 1, role: "user", kind: "text", text: "inspect parser token" },
  { id: "tr#1", seq: 2, role: "user", kind: "tool_result", tool_use_id: "call-1", text: "PASS parser suite" },
];

const controlledHit: ChatSearchHit = {
  itemId: "remote#1",
  category: "text",
  snippet: "remote parser hit",
  ranges: [[7, 13]],
};

let host: HTMLDivElement;
let root: Root;
let onQuery: Mock<(q: string) => void>;
let onPick: Mock<(hit: ChatSearchHit) => void>;
let onClose: Mock<() => void>;

const mount = (props: Partial<ChatSearchProps> = {}) => {
  const full: ChatSearchProps = {
    open: true,
    query: "",
    onQuery,
    rounds: 2,
    hasMore: false,
    onPick,
    onClose,
    ...props,
  };
  act(() => root.render(<ChatSearch {...full} />));
  return full;
};

const searchInput = () => document.querySelector<HTMLInputElement>("[data-chat-search] input")!;
const buttonByText = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === label);

beforeEach(() => {
  vi.clearAllMocks();
  mocks.searchChat.mockImplementation(realSearchChat);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  onQuery = vi.fn();
  onPick = vi.fn();
  onClose = vi.fn();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

describe("ChatSearch with controlled search and loading props", () => {
  it("renders controlled results without items or searchChat and supports immediate Enter selection", () => {
    const controlledResults: ChatSearchResults = { hits: [controlledHit], truncated: true };
    mount({ query: "parser", results: controlledResults, rounds: 4 });

    expect(mocks.searchChat).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(t("chat-chatsearch.summary", { hits: "1+", rounds: 4 }));
    expect(document.body.textContent).toContain(t("chat-chatsearch.truncated"));
    expect([...document.querySelectorAll("mark")].map((mark) => mark.textContent)).toEqual(["parser"]);

    act(() => {
      searchInput().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    expect(onPick).toHaveBeenCalledWith(controlledHit);
  });

  it("blocks Enter selection while controlled searching is active and updates empty state immediately once done", () => {
    const empty: ChatSearchResults = { hits: [], truncated: false };
    mount({ query: "parser", results: { hits: [controlledHit], truncated: false }, searching: true });

    act(() => {
      searchInput().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    expect(onPick).not.toHaveBeenCalled();

    mount({ query: "missing", results: empty, searching: false });
    expect(document.body.textContent).toContain(t("chat-chatsearch.no_hits"));

    mount({ query: "   ", results: empty, searching: false });
    expect(document.body.textContent).toContain(t("chat-chatsearch.enter_keyword"));
    expect(mocks.searchChat).not.toHaveBeenCalled();
  });

  it("uses a custom searchItems matcher and skips searchChat while closed or open", () => {
    const searchItems = vi.fn<NonNullable<ChatSearchProps["searchItems"]>>(() => ({
      hits: [controlledHit],
      truncated: false,
    }));

    mount({ open: false, query: "parser", items: defaultItems, searchItems, settleMs: 0 });
    expect(searchItems).not.toHaveBeenCalled();
    expect(mocks.searchChat).not.toHaveBeenCalled();

    mount({ open: true, query: "parser", items: defaultItems, searchItems, settleMs: 0 });
    expect(searchItems).toHaveBeenCalledWith(defaultItems, "parser");
    expect(mocks.searchChat).not.toHaveBeenCalled();
    expect(document.querySelectorAll("[data-search-result]")).toHaveLength(1);
  });

  it("honors controlled earlier-loading state, notice, stop callback and missing onLoadEarlier", () => {
    const onStopLoadEarlier = vi.fn();
    mount({
      hasMore: true,
      loadingEarlier: true,
      loadedPages: 3,
      onStopLoadEarlier,
    });

    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 3 }));
    act(() => buttonByText(t("chat-chatsearch.stop_loading"))!.click());
    expect(onStopLoadEarlier).toHaveBeenCalledTimes(1);

    // Closing the dialog while loading notifies the controlled stop callback once.
    mount({
      open: false,
      hasMore: true,
      loadingEarlier: true,
      loadedPages: 3,
      onStopLoadEarlier,
    });
    expect(onStopLoadEarlier).toHaveBeenCalledTimes(2);

    // Without onLoadEarlier, hasMore renders no load button while still showing a controlled notice.
    mount({
      open: true,
      hasMore: true,
      loadingEarlier: false,
      notice: "Custom status notice",
      onStopLoadEarlier,
    });
    expect(document.body.textContent).toContain("Custom status notice");
    expect(buttonByText(t("chat-chatsearch.load_earlier"))).toBeUndefined();
  });

  it("fences overlapping and partially controlled earlier-loading runs against stale page, outcome and failure updates", async () => {
    const runs: Array<{
      onPage: (n: number) => void;
      stopped: () => boolean;
      resolve: (outcome: string | null) => void;
      reject: (error: Error) => void;
    }> = [];
    const onLoadEarlier = vi.fn((onPage: (n: number) => void, stopped: () => boolean) =>
      new Promise<string | null>((resolve, reject) => {
        runs.push({ onPage, stopped, resolve, reject });
      }));

    // Partially controlled: loadingEarlier is held false while pages and notice remain internal,
    // allowing a second run to start before the first settles.
    mount({ hasMore: true, loadingEarlier: false, onLoadEarlier });
    act(() => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    expect(runs).toHaveLength(1);
    act(() => runs[0].onPage(1));
    expect(runs[0].stopped()).toBe(false);

    act(() => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    expect(runs).toHaveLength(2);
    expect(runs[0].stopped()).toBe(true);
    expect(runs[1].stopped()).toBe(false);
    act(() => runs[1].onPage(2));

    // Releasing controlled loadingEarlier exposes internal loading and page count for the active run.
    mount({ hasMore: true, onLoadEarlier });
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 2 }));
    expect(buttonByText(t("chat-chatsearch.stop_loading"))).toBeDefined();

    // Stale page progress and settlement from run 1 cannot overwrite run 2 or end loading early.
    act(() => runs[0].onPage(9));
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 2 }));
    await act(async () => {
      runs[0].resolve("Stale run 1 notice");
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 2 }));
    expect(document.body.textContent).not.toContain("Stale run 1 notice");
    expect(buttonByText(t("chat-chatsearch.stop_loading"))).toBeDefined();

    await act(async () => {
      runs[1].resolve("Run 2 finished");
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("Run 2 finished");
    expect(buttonByText(t("chat-chatsearch.load_earlier"))).toBeDefined();

    // A stale rejection from an overlapped run also cannot clobber the newer run.
    mount({ hasMore: true, loadingEarlier: false, onLoadEarlier });
    act(() => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    act(() => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    expect(runs).toHaveLength(4);
    expect(runs[2].stopped()).toBe(true);
    expect(runs[3].stopped()).toBe(false);
    act(() => runs[3].onPage(4));

    mount({ hasMore: true, onLoadEarlier });
    await act(async () => {
      runs[2].reject(new Error("Stale run 3 failure"));
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 4 }));
    expect(document.body.textContent).not.toContain(t("chat-chatview.search_load_failed"));
    expect(buttonByText(t("chat-chatsearch.stop_loading"))).toBeDefined();

    await act(async () => {
      runs[3].resolve("Run 4 recovered");
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("Run 4 recovered");
  });
});

describe("ChatSearch default compatibility", () => {
  it("debounces query changes and runs default searchChat when controlled search props are absent", () => {
    vi.useFakeTimers();
    mount({ query: "", items: defaultItems, onLoadEarlier: async () => null });
    expect(mocks.searchChat).toHaveBeenCalledWith(defaultItems, "");

    mount({ query: "parser", items: defaultItems, onLoadEarlier: async () => null });
    expect(document.querySelectorAll("[data-search-result]")).toHaveLength(0);

    act(() => vi.advanceTimersByTime(200));
    expect(mocks.searchChat).toHaveBeenCalledWith(defaultItems, "parser");
    expect(document.querySelectorAll("[data-search-result]")).toHaveLength(2);

    act(() => {
      searchInput().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ itemId: "tr#1", category: "tool_output" }));
  });

  it("keeps the default async onLoadEarlier lifecycle, stop flag and failure notice", async () => {
    let finish!: (outcome: string | null) => void;
    let checkStopped!: () => boolean;
    const onLoadEarlier = vi.fn((onPage: (n: number) => void, stopped: () => boolean) => {
      checkStopped = stopped;
      onPage(2);
      return new Promise<string | null>((resolve) => { finish = resolve; });
    });

    mount({ items: defaultItems, hasMore: true, onLoadEarlier });
    act(() => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    expect(document.body.textContent).toContain(t("chat-chatsearch.loading_pages", { count: 2 }));
    expect(checkStopped()).toBe(false);

    act(() => buttonByText(t("chat-chatsearch.stop_loading"))!.click());
    expect(checkStopped()).toBe(true);

    await act(async () => {
      finish("Stopped at page 2");
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("Stopped at page 2");

    const failingLoad = vi.fn(async () => {
      throw new Error("Synthetic failure");
    });
    mount({ items: defaultItems, hasMore: true, onLoadEarlier: failingLoad });
    await act(async () => buttonByText(t("chat-chatsearch.load_earlier"))!.click());
    expect(document.body.textContent).toContain(t("chat-chatview.search_load_failed"));
  });
});
