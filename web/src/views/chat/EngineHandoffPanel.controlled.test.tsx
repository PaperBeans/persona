// @vitest-environment jsdom
// Controlled handoff panels own full-text loading without touching useFullText or lib/api; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineSwitch } from "../../lib/engine-switch";
import type { Item } from "../../lib/types";
import type { EngineHandoffFullItem, EngineHandoffPanelProps } from "./EngineHandoffPanel";

const mocks = vi.hoisted(() => ({
  fullItem: vi.fn(),
  useFullText: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  fullItem: mocks.fullItem,
}));

vi.mock("./FullText", async (importOriginal) => {
  const original = await importOriginal<typeof import("./FullText")>();
  mocks.useFullText.mockImplementation((item: Item | undefined, mid: string | undefined, sid: string | undefined) =>
    original.useFullText(item, mid, sid),
  );
  return {
    ...original,
    useFullText: mocks.useFullText,
  };
});

const { EngineHandoffPanel } = await import("./EngineHandoffPanel");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HANDOFF = "## Conversation so far\nThe user asked for a login page.";
const CUT_MARK = "\n…（已截断，共 9000 字节）"; // i18n-ignore: matches the hub's cut note
const block = (text: string) => `<persona-context kind="engine-switch" title="Handoff" from="claude" to="codex">\n${text}\n</persona-context>\n\n`;
const makeSwitch = (text = HANDOFF): EngineSwitch => ({ from: "claude", to: "codex", model: "gpt-5.5", resumed: false, text });
const makeCutItem = (extra: Partial<Item> = {}): Item => ({
  id: "item-1",
  seq: 1,
  role: "user",
  kind: "text",
  text: `${block(`${HANDOFF}${CUT_MARK}`)}Continue.`,
  full_size: 9000,
  ...extra,
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<EngineHandoffPanelProps> = {}) => {
  await act(async () =>
    root.render(
      <EngineHandoffPanel
        id="handoff-panel-1"
        sw={makeSwitch()}
        {...props}
      />,
    ),
  );
};
const panel = () => host.querySelector<HTMLElement>("[data-engine-handoff]");
const preText = () => panel()?.querySelector("pre")?.textContent;
const fullButton = () => host.querySelector<HTMLButtonElement>("[data-full-text]");
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  mocks.fullItem.mockReset();
  mocks.useFullText.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("EngineHandoffPanel with controlled props", () => {
  it("renders explicit fullText directly without invoking useFullText or fullItem", async () => {
    const complete = `${HANDOFF}\nComplete controlled handoff.`;
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      fullText: complete,
    });

    expect(panel()?.id).toBe("handoff-panel-1");
    expect(panel()?.textContent).toContain(t("chat-engine.handoff_title"));
    expect(preText()).toBe(complete);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("renders a custom footer control or null without touching default full-text hooks", async () => {
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      more: <button type="button" data-custom-more>Custom load</button>,
    });

    expect(preText()).toBe(HANDOFF);
    expect(host.querySelector("[data-custom-more]")?.textContent).toBe("Custom load");
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      more: null,
    });
    expect(preText()).toBe(HANDOFF);
    expect(host.querySelector("[data-custom-more]")).toBeNull();
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
  });

  it("loads a truncated item through loadFullItem, deduplicates clicks and extracts the engine-switch block", async () => {
    let finish!: (value: EngineHandoffFullItem) => void;
    const loadFullItem = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finish = resolve; }));
    const cut = makeCutItem({ source_fingerprint: "fp-1" });
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: cut,
      loadFullItem,
    });

    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(mocks.useFullText).not.toHaveBeenCalled();

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);
    expect(loadFullItem).toHaveBeenCalledWith(cut);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");
    expect(fullButton()?.disabled).toBe(true);
    expect(fullButton()?.getAttribute("aria-busy")).toBe("true");

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);

    const whole = `${HANDOFF}\nAnd the rest of the handoff.`;
    await act(async () => {
      finish({ text: `${block(whole)}Continue.`, source_fingerprint: "fp-1" });
      await Promise.resolve();
    });
    await flush();

    expect(preText()).toBe(whole);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("handles controlled load failures and fingerprint mismatches, then retries with a plain string result", async () => {
    const onLoadError = vi.fn();
    const loadFullItem = vi
      .fn<(item: Item) => Promise<EngineHandoffFullItem>>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ text: `${block("stale")}`, source_fingerprint: "wrong-fp" })
      .mockResolvedValueOnce(`${HANDOFF}\nRecovered plain handoff.`);
    const cut = makeCutItem({ source_fingerprint: "fp-2" });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: cut,
      loadFullItem,
      onLoadError,
    });

    act(() => fullButton()!.click());
    await flush();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("failed");
    expect(preText()).toBe(HANDOFF);
    expect(onLoadError).toHaveBeenCalledTimes(1);

    act(() => fullButton()!.click());
    await flush();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("failed");
    expect(preText()).toBe(HANDOFF);
    expect(onLoadError).toHaveBeenCalledTimes(2);

    act(() => fullButton()!.click());
    await flush();
    expect(preText()).toBe(`${HANDOFF}\nRecovered plain handoff.`);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("ignores stale loadFullItem results when the truncated item changes mid-request", async () => {
    let finishFirst!: (value: EngineHandoffFullItem) => void;
    const loadFullItem = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finishFirst = resolve; }));
    const first = makeCutItem({ id: "item-1" });
    const second = makeCutItem({ id: "item-2" });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: first,
      loadFullItem,
    });
    act(() => fullButton()!.click());
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await mount({
      sw: makeSwitch(`Second preview${CUT_MARK}`),
      item: second,
      loadFullItem,
    });
    expect(preText()).toBe("Second preview");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await act(async () => {
      finishFirst("Stale first full handoff");
      await Promise.resolve();
    });
    await flush();

    expect(preText()).toBe("Second preview");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
  });

  it("isolates cached and pending full text for the same item across mid/sid session switches", async () => {
    const sharedItem = makeCutItem({ id: "item-same", source_fingerprint: "fp-same" });
    const loadS1 = vi.fn(async () => `${HANDOFF}\nSession 1 full handoff.`);
    const loadS2 = vi.fn(async () => `${HANDOFF}\nSession 2 full handoff.`);
    const loadM2 = vi.fn(async () => `${HANDOFF}\nMachine 2 full handoff.`);

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      mid: "machine-1",
      sid: "session-1",
      loadFullItem: loadS1,
    });
    act(() => fullButton()!.click());
    await flush();
    expect(loadS1).toHaveBeenCalledTimes(1);
    expect(preText()).toBe(`${HANDOFF}\nSession 1 full handoff.`);
    expect(fullButton()).toBeNull();

    // Switching sid with an identical item resets cached full text and calls the new loader.
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      mid: "machine-1",
      sid: "session-2",
      loadFullItem: loadS2,
    });
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);

    act(() => fullButton()!.click());
    await flush();
    expect(loadS2).toHaveBeenCalledTimes(1);
    expect(preText()).toBe(`${HANDOFF}\nSession 2 full handoff.`);
    expect(fullButton()).toBeNull();

    // Switching mid with an identical item and sid also isolates the cache.
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      mid: "machine-2",
      sid: "session-2",
      loadFullItem: loadM2,
    });
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    act(() => fullButton()!.click());
    await flush();
    expect(loadM2).toHaveBeenCalledTimes(1);
    expect(preText()).toBe(`${HANDOFF}\nMachine 2 full handoff.`);

    // In-flight load from one session never short-circuits or leaks into another session with the same item.
    let finishPendingA!: (value: EngineHandoffFullItem) => void;
    let finishPendingB!: (value: EngineHandoffFullItem) => void;
    const pendingLoaderA = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finishPendingA = resolve; }));
    const pendingLoaderB = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finishPendingB = resolve; }));

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      mid: "machine-3",
      sid: "session-a",
      loadFullItem: pendingLoaderA,
    });
    act(() => fullButton()!.click());
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      mid: "machine-3",
      sid: "session-b",
      loadFullItem: pendingLoaderB,
    });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    act(() => fullButton()!.click());
    expect(pendingLoaderB).toHaveBeenCalledTimes(1);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await act(async () => {
      finishPendingA("Stale session A text");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await act(async () => {
      finishPendingB("Fresh session B text");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Fresh session B text");
    expect(fullButton()).toBeNull();
  });

  it("isolates same-item cache via scopeKey without mid/sid while keeping inline loader re-renders stable", async () => {
    const sharedItem = makeCutItem({ id: "item-scoped", source_fingerprint: "fp-scoped" });
    const loaderA = vi.fn(async (_item: Item) => `${HANDOFF}\nSource A full handoff.`);
    const loaderAInline = vi.fn(async (_item: Item) => `${HANDOFF}\nShould not be invoked on re-render.`);
    const loaderB = vi.fn(async (_item: Item) => `${HANDOFF}\nSource B full handoff.`);

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      scopeKey: "source-a",
      loadFullItem: (it) => loaderA(it),
    });
    act(() => fullButton()!.click());
    await flush();
    expect(loaderA).toHaveBeenCalledTimes(1);
    expect(preText()).toBe(`${HANDOFF}\nSource A full handoff.`);
    expect(fullButton()).toBeNull();

    // A fresh inline closure with the same scopeKey and item does not reset loaded text.
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      scopeKey: "source-a",
      loadFullItem: (it) => loaderAInline(it),
    });
    expect(preText()).toBe(`${HANDOFF}\nSource A full handoff.`);
    expect(fullButton()).toBeNull();
    expect(loaderAInline).not.toHaveBeenCalled();

    // Switching scopeKey invalidates the cached full text and invokes the new loader.
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: sharedItem,
      scopeKey: "source-b",
      loadFullItem: (it) => loaderB(it),
    });
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);

    act(() => fullButton()!.click());
    await flush();
    expect(loaderB).toHaveBeenCalledTimes(1);
    expect(preText()).toBe(`${HANDOFF}\nSource B full handoff.`);
    expect(fullButton()).toBeNull();
  });

  it("resets loading when a request resolves while switched away so returning to the original key can retry", async () => {
    const resolvers: Array<(value: EngineHandoffFullItem) => void> = [];
    const loadFullItem = vi.fn(
      () => new Promise<EngineHandoffFullItem>((resolve) => { resolvers.push(resolve); }),
    );
    const itemA = makeCutItem({ id: "item-a" });
    const itemB = makeCutItem({ id: "item-b" });

    await mount({
      sw: makeSwitch(`Preview A${CUT_MARK}`),
      item: itemA,
      loadFullItem,
    });
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");
    expect(fullButton()?.disabled).toBe(true);

    await mount({
      sw: makeSwitch(`Preview B${CUT_MARK}`),
      item: itemB,
      loadFullItem,
    });
    expect(preText()).toBe("Preview B");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await act(async () => {
      resolvers[0]("Stale full A while viewing B");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Preview B");

    // Returning to A must not be stuck in loading and must allow a fresh load.
    await mount({
      sw: makeSwitch(`Preview A${CUT_MARK}`),
      item: itemA,
      loadFullItem,
    });
    expect(preText()).toBe("Preview A");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);
    expect(fullButton()?.getAttribute("aria-busy")).toBeNull();

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(2);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await act(async () => {
      resolvers[1]("Retried full A");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Retried full A");
    expect(fullButton()).toBeNull();
  });

  it("resets loading when a request rejects while switched away without leaking errors and stays retryable", async () => {
    const deferred: Array<{ resolve: (v: EngineHandoffFullItem) => void; reject: (e: unknown) => void }> = [];
    const loadFullItem = vi.fn(
      () =>
        new Promise<EngineHandoffFullItem>((resolve, reject) => {
          deferred.push({ resolve, reject });
        }),
    );
    const onLoadError = vi.fn();
    const itemA = makeCutItem({ id: "item-a-rej" });
    const itemB = makeCutItem({ id: "item-b-rej" });

    await mount({
      sw: makeSwitch(`Preview A${CUT_MARK}`),
      item: itemA,
      loadFullItem,
      onLoadError,
    });
    act(() => fullButton()!.click());
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await mount({
      sw: makeSwitch(`Preview B${CUT_MARK}`),
      item: itemB,
      loadFullItem,
      onLoadError,
    });

    await act(async () => {
      deferred[0].reject(new Error("abandoned request"));
      await Promise.resolve();
    });
    await flush();
    expect(onLoadError).not.toHaveBeenCalled();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await mount({
      sw: makeSwitch(`Preview A${CUT_MARK}`),
      item: itemA,
      loadFullItem,
      onLoadError,
    });
    expect(preText()).toBe("Preview A");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);
    expect(onLoadError).not.toHaveBeenCalled();

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(2);
    await act(async () => {
      deferred[1].resolve("Recovered full A");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Recovered full A");
    expect(fullButton()).toBeNull();
  });

  it("fences ABA switches so late old resolve/reject cannot overwrite state or clear the new pending request", async () => {
    type Deferred = { resolve: (v: EngineHandoffFullItem) => void; reject: (e: unknown) => void };
    const calls: Deferred[] = [];
    const loadFullItem = vi.fn(
      () =>
        new Promise<EngineHandoffFullItem>((resolve, reject) => {
          calls.push({ resolve, reject });
        }),
    );
    const onLoadError = vi.fn();
    const sharedItem = makeCutItem({ id: "item-aba" });

    // 1) A -> B -> A while old request is still pending; retry on A, then old request resolves late.
    await mount({ sw: makeSwitch(`Preview A${CUT_MARK}`), item: sharedItem, mid: "m1", sid: "s1", loadFullItem, onLoadError });
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await mount({ sw: makeSwitch(`Preview B${CUT_MARK}`), item: sharedItem, mid: "m1", sid: "s2", loadFullItem, onLoadError });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await mount({ sw: makeSwitch(`Preview A${CUT_MARK}`), item: sharedItem, mid: "m1", sid: "s1", loadFullItem, onLoadError });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(2);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    // Late resolution of old request #1 must not write stale text or clear request #2's pending guard.
    await act(async () => {
      calls[0].resolve("Stale ABA resolve");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Preview A");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");
    expect(fullButton()?.disabled).toBe(true);
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(2);

    await act(async () => {
      calls[1].resolve("Fresh ABA resolve");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Fresh ABA resolve");
    expect(fullButton()).toBeNull();

    // 2) A -> B -> A where old request rejects late while a fresh retry is in flight.
    await mount({ sw: makeSwitch(`Preview A2${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a2", loadFullItem, onLoadError });
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(3);

    await mount({ sw: makeSwitch(`Preview B2${CUT_MARK}`), item: sharedItem, scopeKey: "aba-b2", loadFullItem, onLoadError });
    await mount({ sw: makeSwitch(`Preview A2${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a2", loadFullItem, onLoadError });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(4);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await act(async () => {
      calls[2].reject(new Error("stale aba reject"));
      await Promise.resolve();
    });
    await flush();
    expect(onLoadError).not.toHaveBeenCalled();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");
    expect(fullButton()?.disabled).toBe(true);

    await act(async () => {
      calls[3].resolve("Fresh ABA after stale reject");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Fresh ABA after stale reject");
    expect(fullButton()).toBeNull();

    // 3) A -> B -> A where old request resolves before user clicks retry: stays idle and does not write stale text.
    await mount({ sw: makeSwitch(`Preview A3${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a3", loadFullItem, onLoadError });
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(5);

    await mount({ sw: makeSwitch(`Preview B3${CUT_MARK}`), item: sharedItem, scopeKey: "aba-b3", loadFullItem, onLoadError });
    await mount({ sw: makeSwitch(`Preview A3${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a3", loadFullItem, onLoadError });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await act(async () => {
      calls[4].resolve("Stale before retry");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Preview A3");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(6);
    await act(async () => {
      calls[5].resolve("Explicit retry A3");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Explicit retry A3");
    expect(fullButton()).toBeNull();

    // 4) A -> B -> A where old request rejects before user clicks retry: stays idle without reporting an error.
    await mount({ sw: makeSwitch(`Preview A4${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a4", loadFullItem, onLoadError });
    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(7);

    await mount({ sw: makeSwitch(`Preview B4${CUT_MARK}`), item: sharedItem, scopeKey: "aba-b4", loadFullItem, onLoadError });
    await mount({ sw: makeSwitch(`Preview A4${CUT_MARK}`), item: sharedItem, scopeKey: "aba-a4", loadFullItem, onLoadError });
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await act(async () => {
      calls[6].reject(new Error("stale reject before retry"));
      await Promise.resolve();
    });
    await flush();
    expect(onLoadError).not.toHaveBeenCalled();
    expect(preText()).toBe("Preview A4");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(fullButton()?.disabled).toBe(false);

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(8);
    await act(async () => {
      calls[7].resolve("Explicit retry A4");
      await Promise.resolve();
    });
    await flush();
    expect(preText()).toBe("Explicit retry A4");
    expect(fullButton()).toBeNull();
  });

  it("renders non-truncated handoff verbatim without calling loadFullItem or useFullText", async () => {
    const loadFullItem = vi.fn();
    await mount({ sw: makeSwitch(HANDOFF), loadFullItem });
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()).toBeNull();
    expect(loadFullItem).not.toHaveBeenCalled();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });
});

describe("EngineHandoffPanel default compatibility", () => {
  it("uses useFullText and lib/api fullItem when controlled props are absent", async () => {
    const whole = `${HANDOFF}\nLoaded from default API.`;
    mocks.fullItem.mockResolvedValue({
      id: "item-1",
      seq: 1,
      role: "user",
      kind: "text",
      text: `${block(whole)}Continue.`,
    });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      mid: "example-machine",
      sid: "example-session",
    });

    expect(mocks.useFullText).toHaveBeenCalled();
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    act(() => fullButton()!.click());
    await flush();

    expect(mocks.fullItem).toHaveBeenCalledWith("example-machine", "example-session", "item-1");
    expect(preText()).toBe(whole);
    expect(fullButton()).toBeNull();
  });

  it("keeps plain rendering for non-truncated items without calling fullItem", async () => {
    await mount({
      sw: makeSwitch(HANDOFF),
      mid: "example-machine",
      sid: "example-session",
    });

    expect(mocks.useFullText).toHaveBeenCalled();
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()).toBeNull();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });
});
