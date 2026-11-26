// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  waitForEarlierRetry: vi.fn(),
  chatLineView: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    api: (...args: unknown[]) => mocks.api(...args),
  };
});

vi.mock("./useTranscript", () => ({
  waitForEarlierRetry: (ms: number, signal: AbortSignal) => mocks.waitForEarlierRetry(ms, signal),
}));

vi.mock("./Lines", () => ({
  ChatLineView: (props: { line: { id: string } }) => {
    mocks.chatLineView(props);
    return <div data-default-line={props.line.id} />;
  },
}));

import { t } from "../../i18n";
import { ApiError } from "../../lib/api";
import type { EngineLeg, Item, ItemsPage } from "../../lib/types";
import { ChatActionsContext, useChatActions, type ChatActions } from "./chat-actions";
import EarlierStretches, { type EarlierStretchLineProps, type EarlierStretchesProps } from "./EarlierStretches";
import type { LiveTranscript, StretchesReport } from "./useEarlierStretches";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T09:00:00Z");
const at = (minutes: number) => new Date(T0 + minutes * 60_000).toISOString();

const textItem = (seq: number, minutes: number, role: "user" | "assistant" = "assistant", text = `msg-${seq}`): Item => ({
  id: `item-${seq}#0`,
  seq,
  role,
  kind: "text",
  text,
  ts: at(minutes),
});

const imageItem = (seq: number, minutes: number): Item => ({
  id: `img-${seq}#0`,
  seq,
  role: "assistant",
  kind: "image",
  ts: at(minutes),
});

const foldItem = (seq: number, throughSeq: number, minutes: number): Item => ({
  id: `fold:${seq}#0`,
  seq,
  role: "assistant",
  kind: "fold",
  ts: at(minutes),
  fold: { items: throughSeq - seq + 1, tools: 1, thoughts: 0, through_seq: throughSeq, until: at(minutes) },
});

const liveTranscript = (items: Item[]): LiveTranscript => ({
  items,
  hasMore: false,
  loading: false,
  loadingEarlier: false,
  loadEarlier: async () => undefined,
  expandFold: async () => undefined,
});

function ControlledLineProbe({ props, seenActions }: { props: EarlierStretchLineProps; seenActions: (ChatActions | null)[] }) {
  const actions = useChatActions();
  seenActions.push(actions);
  return <div data-controlled-line={props.line.id} data-stretch-sid={props.sid} />;
}

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.clearAllMocks();
  mocks.waitForEarlierRetry.mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const renderStretches = (props: EarlierStretchesProps, parentActions?: ChatActions) => {
  const element = <EarlierStretches {...props} />;
  act(() => {
    root.render(parentActions ? <ChatActionsContext.Provider value={parentActions}>{element}</ChatActionsContext.Provider> : element);
  });
};

describe("EarlierStretches controlled mode", () => {
  it("loads paginated pages, expands multi-page folds, and renders custom lines without calling default API or ChatLineView", async () => {
    const history: EngineLeg[] = [{ session_id: "leg-a", agent: "claude", until: at(20) }];
    const fold = foldItem(3, 5, 10);
    const img = imageItem(6, 11);
    const loadPage = vi.fn<NonNullable<EarlierStretchesProps["loadPage"]>>()
      .mockResolvedValueOnce({ items: [fold, img], has_more: true })
      .mockResolvedValueOnce({ items: [textItem(1, 1, "user", "hello"), textItem(2, 2, "assistant", "hi")], has_more: false });
    const loadFoldPage = vi.fn<NonNullable<EarlierStretchesProps["loadFoldPage"]>>()
      .mockResolvedValueOnce({ items: [textItem(3, 10, "assistant", "step-1")], more: true })
      .mockResolvedValueOnce({ items: [textItem(4, 10, "assistant", "step-2"), textItem(5, 10, "assistant", "step-3")], more: false });

    const seenActions: (ChatActions | null)[] = [];
    const reports: StretchesReport[] = [];
    const parentActions: ChatActions = {
      draftKey: "draft-1",
      title: "Example chat",
      canCompose: true,
      speaker: "Claude",
      images: () => [],
      forward: vi.fn(),
      fork: vi.fn(),
    };

    renderStretches(
      {
        mid: "example-machine",
        sid: "leg-b",
        scope: "example-machine/leg-b",
        history,
        live: liveTranscript([textItem(10, 25, "user", "live")]),
        report: (_scope, r) => { reports.push(r); },
        loadPage,
        loadFoldPage,
        renderLine: (lineProps) => <ControlledLineProbe props={lineProps} seenActions={seenActions} />,
      },
      parentActions,
    );

    // First page load: no beforeSeq yet
    const page1 = await act(async () => reports.at(-1)!.loadEarlier());
    expect(typeof page1).toBe("symbol");
    expect(loadPage).toHaveBeenNthCalledWith(1, {
      mid: "example-machine",
      sid: "leg-a",
      beforeSeq: undefined,
      signal: expect.any(AbortSignal),
    });
    expect(reports.at(-1)?.hasMore).toBe(true);

    // Second page load: passes beforeSeq of earliest loaded item
    const page2 = await act(async () => reports.at(-1)!.loadEarlier());
    expect(typeof page2).toBe("symbol");
    expect(loadPage).toHaveBeenNthCalledWith(2, {
      mid: "example-machine",
      sid: "leg-a",
      beforeSeq: 3,
      signal: expect.any(AbortSignal),
    });
    expect(reports.at(-1)?.hasMore).toBe(false);
    expect(host.querySelectorAll("[data-controlled-line]").length).toBeGreaterThan(0);
    expect(host.querySelector("[data-default-line]")).toBeNull();

    // Parent chat actions are forced read-only inside earlier stretches and image viewer sees stretch images
    const readOnlyActions = seenActions.at(-1)!;
    expect(readOnlyActions.canCompose).toBe(false);
    expect(readOnlyActions.forward).toBeUndefined();
    expect(readOnlyActions.fork).toBeUndefined();
    expect(readOnlyActions.rewind).toBeUndefined();
    expect(readOnlyActions.images()).toEqual([img]);

    // Fold expansion paginates through loadFoldPage and merges expanded items into the stretch
    await act(async () => {
      await reports.at(-1)!.stretches[0].expandFold(fold);
    });
    expect(loadFoldPage).toHaveBeenNthCalledWith(1, {
      mid: "example-machine",
      sid: "leg-a",
      fromSeq: 3,
      throughSeq: 5,
    });
    expect(loadFoldPage).toHaveBeenNthCalledWith(2, {
      mid: "example-machine",
      sid: "leg-a",
      fromSeq: 4,
      throughSeq: 5,
    });
    expect(reports.at(-1)!.stretches[0].items.map((item) => item.seq)).toEqual([1, 2, 3, 4, 5, 6]);

    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.waitForEarlierRetry).not.toHaveBeenCalled();
    expect(mocks.chatLineView).not.toHaveBeenCalled();
  });

  it("uses controlled retry wait, manual retry, and custom missing-history classification without default API", async () => {
    const history: EngineLeg[] = [
      { session_id: "leg-a", agent: "claude", until: at(10) },
      { session_id: "leg-b", agent: "codex", until: at(20) },
    ];
    class MissingTranscriptError extends Error {
      readonly missing = true;
    }

    const loadPage = vi.fn<NonNullable<EarlierStretchesProps["loadPage"]>>()
      .mockRejectedValueOnce(new MissingTranscriptError("leg-b archived"))
      .mockRejectedValueOnce(new Error("temporary outage"))
      .mockResolvedValueOnce({ items: [textItem(1, 1, "assistant", "recovered")], has_more: false });
    const waitForRetry = vi.fn<NonNullable<EarlierStretchesProps["waitForRetry"]>>().mockResolvedValue(undefined);
    const isHistoryNotFound = vi.fn((error: unknown) => error instanceof MissingTranscriptError);
    const reports: StretchesReport[] = [];

    renderStretches({
      mid: "example-machine",
      sid: "leg-c",
      scope: "example-machine/leg-c",
      history,
      live: liveTranscript([textItem(10, 25)]),
      report: (_scope, r) => { reports.push(r); },
      loadPage,
      waitForRetry,
      isHistoryNotFound,
      renderLine: ({ line }) => <div data-controlled-line={line.id} />,
    });

    // Missing history on leg-b ends leg-b cleanly via custom classifier with no error alert
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    expect(isHistoryNotFound).toHaveBeenCalledTimes(1);
    expect(host.querySelector("[data-engine-stretch-error]")).toBeNull();
    expect(reports.at(-1)!.hasMore).toBe(true);

    // Transient failure on leg-a shows error alert and arms retry backoff
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    const alert = host.querySelector("[data-engine-stretch-error]");
    expect(alert?.textContent).toContain("temporary outage");

    // waitForRetry option delegates delay to the controlled waiter and succeeds on retry
    const page = await act(async () => reports.at(-1)!.loadEarlier({ waitForRetry: true }));
    expect(waitForRetry).toHaveBeenCalledWith(expect.any(Number), expect.any(AbortSignal));
    expect(typeof page).toBe("symbol");
    expect(host.querySelector("[data-engine-stretch-error]")).toBeNull();
    expect(host.querySelector('[data-engine-stretch="leg-a"] [data-controlled-line]')).not.toBeNull();
    expect(reports.at(-1)!.hasMore).toBe(false);

    // Also verify manual Retry button resets backoff and calls loadPage directly
    const retryLoadPage = vi.fn<NonNullable<EarlierStretchesProps["loadPage"]>>()
      .mockRejectedValueOnce(new Error("manual retry failure"))
      .mockResolvedValueOnce({ items: [textItem(2, 2, "assistant", "after button")], has_more: false });
    renderStretches({
      mid: "example-machine",
      sid: "leg-d",
      scope: "example-machine/leg-d",
      history: [{ session_id: "leg-x", agent: "claude", until: at(10) }],
      live: liveTranscript([textItem(10, 25)]),
      report: (_scope, r) => { reports.push(r); },
      loadPage: retryLoadPage,
      renderLine: ({ line }) => <div data-controlled-line={line.id} />,
    });
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    const retryButton = [...host.querySelectorAll<HTMLButtonElement>("[data-engine-stretch-error] button")]
      .find((button) => button.textContent === t("chat-chatview.retry"))!;
    await act(async () => {
      retryButton.click();
      await Promise.resolve();
    });
    expect(retryLoadPage).toHaveBeenCalledTimes(2);
    expect(host.querySelector("[data-engine-stretch-error]")).toBeNull();

    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.waitForEarlierRetry).not.toHaveBeenCalled();
    expect(mocks.chatLineView).not.toHaveBeenCalled();
  });
});

describe("EarlierStretches default compatibility", () => {
  it("uses default api, waitForEarlierRetry, sessionHistoryNotFound, and ChatLineView when controlled props are omitted", async () => {
    const history: EngineLeg[] = [
      { session_id: "leg-a", agent: "claude", until: at(10) },
      { session_id: "leg-b", agent: "codex", until: at(20) },
    ];
    const fold = foldItem(2, 3, 5);
    mocks.api
      // leg-b missing history -> default sessionHistoryNotFound finishes leg-b cleanly
      .mockRejectedValueOnce(new ApiError(404, "missing", { code: "not_found", reason: "no_transcript" }))
      // leg-a first attempt fails transiently -> waitForEarlierRetry -> second attempt returns page with has_more
      .mockRejectedValueOnce(new Error("hub busy"))
      .mockResolvedValueOnce({ items: [fold], has_more: true } satisfies ItemsPage)
      // leg-a older page with before_seq
      .mockResolvedValueOnce({ items: [textItem(1, 1, "user", "oldest")], has_more: false } satisfies ItemsPage)
      // leg-a fold expansion
      .mockResolvedValueOnce({ items: [textItem(2, 5), textItem(3, 5)], more: false } satisfies ItemsPage);

    const reports: StretchesReport[] = [];
    renderStretches({
      mid: "example-machine",
      sid: "leg-c",
      scope: "example-machine/leg-c",
      history,
      live: liveTranscript([textItem(10, 25)]),
      report: (_scope, r) => { reports.push(r); },
    });

    // Missing leg-b
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    expect(mocks.api).toHaveBeenNthCalledWith(
      1,
      "/sessions/example-machine/leg-b/items?turns=2&fold_work=1&lazy_images=1",
      { signal: expect.any(AbortSignal) },
    );
    expect(host.querySelector("[data-engine-stretch-error]")).toBeNull();

    // Transient failure on leg-a followed by waitForRetry
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    expect(host.querySelector("[data-engine-stretch-error]")?.textContent).toContain("hub busy");

    await act(async () => {
      await reports.at(-1)!.loadEarlier({ waitForRetry: true });
    });
    expect(mocks.waitForEarlierRetry).toHaveBeenCalledWith(expect.any(Number), expect.any(AbortSignal));

    // Older page uses before_seq=2
    await act(async () => {
      await reports.at(-1)!.loadEarlier();
    });
    expect(mocks.api).toHaveBeenNthCalledWith(
      4,
      "/sessions/example-machine/leg-a/items?turns=2&fold_work=1&lazy_images=1&before_seq=2",
      { signal: expect.any(AbortSignal) },
    );
    expect(host.querySelector("[data-default-line]")).not.toBeNull();
    expect(mocks.chatLineView).toHaveBeenCalled();

    // Default fold expansion calls default api with from_seq and through_seq
    await act(async () => {
      await reports.at(-1)!.stretches[0].expandFold(fold);
    });
    expect(mocks.api).toHaveBeenNthCalledWith(
      5,
      "/sessions/example-machine/leg-a/items?from_seq=2&through_seq=3&lazy_images=1",
    );
  });
});
