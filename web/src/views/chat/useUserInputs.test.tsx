// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useUserInputs } from "./useUserInputs";
import { ApiError } from "../../lib/api";
import type { UserInputIndex } from "../../lib/user-input-index";
import { indexedNavTicks } from "../../lib/nav-rail";
import { resetPullGatesForTests } from "../../lib/pull-gate";
import { useInputPreviews } from "./useInputPreviews";

const pending = vi.hoisted(() => [] as { path: string; resolve: (value: unknown) => void; signal?: AbortSignal }[]);
vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  sessionPath: (mid: string, sid: string) => `/sessions/${mid}/${sid}`,
  api: (path: string, opts: { signal?: AbortSignal }) => new Promise((resolve) => pending.push({ path, resolve, signal: opts.signal })),
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let destroy: (() => void) | undefined;
afterEach(() => { destroy?.(); destroy = undefined; pending.length = 0; resetPullGatesForTests(); vi.useRealTimers(); });
const answer = (history: string, total = 2): UserInputIndex => ({ total, history, inputs: Array.from({ length: total }, (_, i) => ({ id: `input-${i}#0`, seq: i + 1 })) });

describe("independent user-input metadata", () => {
  it("requests and exposes the whole compact index while the transcript is only at its tail", async () => {
    const host = document.createElement("div");
    const root = createRoot(host);
    let got: UserInputIndex | null = null;
    function Probe() { got = useUserInputs("example", "long", "fixture", "input-139#0", true).index; return null; }
    act(() => root.render(<Probe />));
    destroy = () => act(() => root.unmount());
    expect(pending[0].path).toContain("user_inputs=1&meta_only=1");
    await act(async () => pending[0].resolve(answer("fixture", 140)));
    expect(got).toEqual(answer("fixture", 140));
    const legacy = answer("fixture", 141);
    legacy.inputs[0].preview = "Excerpt returned by an older hub";
    expect(indexedNavTicks(legacy, [], [])[0]).toMatchObject({ preview: legacy.inputs[0].preview, loadPreview: false });
  });

  it("fences conversation/history changes and refreshes when a genuine input arrives", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    let got: UserInputIndex | null = null;
    const read = (): UserInputIndex | null => got;
    function Probe({ sid, history, newest }: { sid: string; history: string; newest: string }) { got = useUserInputs("example", sid, history, newest, true).index; return null; }
    const render = (sid: string, history: string, newest = "last") => act(() => root.render(<Probe sid={sid} history={history} newest={newest} />));
    destroy = () => act(() => root.unmount());
    render("a", "old"); render("b", "new");
    expect(pending[0].signal?.aborted).toBe(true);
    await act(async () => pending[0].resolve(answer("old")));
    expect(got).toBeNull();
    await act(async () => pending[1].resolve(answer("new")));
    expect(read()?.total).toBe(2);
    render("b", "new", "new-input");
    await act(async () => pending[2].resolve(answer("new", 3)));
    expect(read()?.total).toBe(3);
    render("b", "rewritten");
    expect(got).toBeNull();
    await act(async () => pending[3].resolve(answer("new", 3)));
    expect(got).toBeNull();
  });
  it("refreshes the transcript when the complete index discovers a different historical identity", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const reload = vi.fn();
    function Probe() { useUserInputs("example", "synthetic", "tail-history", "last", true, reload); return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    await act(async () => pending[0].resolve(answer("complete-history")));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  // #11450: a computer that can't find this conversation's history says so for good; the hub's own 404 (a computer it
  // does not know) is asked again like any failure.
  it("stops asking once its computer can't find the history", async () => {
    vi.useFakeTimers();
    const host = document.createElement("div"); const root = createRoot(host);
    function Probe() { useUserInputs("example", "synthetic", "fixture", "last", true); return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    await act(async () => pending[0].resolve(Promise.reject(new ApiError(404, "Persona can't find its history",
      { code: "not_found", reason: "no_transcript", detail: "transcript for session synthetic not found" }))));
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(pending).toHaveLength(1);
  });

  it("asks again after the hub's own 404", async () => {
    vi.useFakeTimers();
    const host = document.createElement("div"); const root = createRoot(host);
    function Probe() { useUserInputs("example", "synthetic", "fixture", "last", true); return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    await act(async () => pending[0].resolve(Promise.reject(new ApiError(404, "No such computer", { code: "not_found" }))));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(pending).toHaveLength(2);
  });

  it("retries a detail-only history 404 after 3 seconds and recovers", async () => {
    vi.useFakeTimers();
    const host = document.createElement("div"); const root = createRoot(host);
    let got: UserInputIndex | null = null;
    const read = (): UserInputIndex | null => got;
    function Probe() { got = useUserInputs("example", "synthetic", "fixture", "last", true).index; return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    await act(async () => pending[0].resolve(Promise.reject(new ApiError(404, "Persona can't find its history",
      { code: "not_found", detail: "transcript for session synthetic not found" }))));
    await act(async () => { await vi.advanceTimersByTimeAsync(2999); });
    expect(pending).toHaveLength(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(pending).toHaveLength(2);
    await act(async () => pending[1].resolve(answer("fixture", 8)));
    expect(read()?.total).toBe(8);
  });

  it("retries unsupported or incomplete indexes instead of permanently freezing the tail count", async () => {
    vi.useFakeTimers();
    const host = document.createElement("div"); const root = createRoot(host);
    let got: UserInputIndex | null = null;
    const read = (): UserInputIndex | null => got;
    function Probe() { got = useUserInputs("example", "synthetic", "fixture", "last", true).index; return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    await act(async () => pending[0].resolve({ items: [], has_more: false }));
    expect(got).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(pending).toHaveLength(2);
    await act(async () => pending[1].resolve(answer("fixture", 8)));
    expect(read()?.total).toBe(8);
  });

  it("does not read indexes on touch-only or unmounted chats", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    function Probe() { useUserInputs("example", "synthetic", "fixture", "last", false); return null; }
    act(() => root.render(<Probe />)); destroy = () => act(() => root.unmount());
    expect(pending).toHaveLength(0);
  });
});

describe("input previews", () => {
  const index = answer("preview-generation", 220);
  const ticks = indexedNavTicks(index, [], []);
  const setup = (historyChanged = vi.fn()) => {
    const root = createRoot(document.createElement("div"));
    let got!: ReturnType<typeof useInputPreviews>;
    function Probe({ scope }: { scope: string }) {
      got = useInputPreviews("example", "long", scope, index, ticks, historyChanged);
      return null;
    }
    const render = (scope: string) => act(() => root.render(<Probe scope={scope} />));
    destroy = () => act(() => root.unmount());
    render("original");
    return {
      render, historyChanged,
      pick: (at: number) => act(() => { void got.loadPreview(at); }),
      preview: (at: number) => got.previewAt(at),
    };
  };

  it("fetches only a small neighborhood on focus, encodes IDs, and deduplicates pending and cached ticks", async () => {
    const { pick, preview } = setup();
    expect(pending).toHaveLength(0);
    pick(80);
    pick(81);
    expect(pending).toHaveLength(1);
    const url = new URL(pending[0].path, "https://example.com");
    expect(url.searchParams.getAll("preview")).toEqual(index.inputs.slice(76, 85).map(({ seq, id }) => `${seq}:${id}`));
    expect(url.searchParams.get("history")).toBe(index.history);
    expect(url.hash).toBe("");
    expect(url.search).toContain("%23");
    expect(preview(80)?.loading).toBe(true);
    const excerpt = { ...index.inputs[80], preview: "Earlier question", answer: "An answer" };
    await act(async () => pending[0].resolve({ history: index.history, inputs: [excerpt] }));
    expect(preview(80)).toEqual({ loading: false, excerpt });
    expect(preview(81)).toEqual({ loading: false, excerpt: undefined });
    pick(80);
    pick(81);
    expect(pending).toHaveLength(1);
    pick(85);
    expect(new URL(pending[1].path, "https://example.com").searchParams.getAll("preview")).toHaveLength(5);
  });

  it.each(["node_outdated", "unsupported", "ordinary page"])("keeps compact positions when previews return %s", async (outcome) => {
    const { pick, preview, historyChanged } = setup();
    pick(4);
    await act(async () => pending[0].resolve(outcome === "ordinary page" ? { items: [], has_more: true } :
      Promise.reject(new ApiError(409, outcome, { code: outcome }))));
    expect(preview(4)).toEqual({ loading: false, excerpt: undefined });
    pick(100);
    expect(pending).toHaveLength(1);
    expect(ticks[100].ordinal).toBe(101);
    expect(historyChanged).not.toHaveBeenCalled();
  });

  it("refreshes on history_changed and fences an in-flight preview from the old history", async () => {
    const { pick, preview, render, historyChanged } = setup();
    pick(4);
    pick(50);
    await act(async () => pending[0].resolve(Promise.reject(new ApiError(409, "History changed", { code: "history_changed" }))));
    expect(historyChanged).toHaveBeenCalledOnce();
    render("rewritten");
    expect(pending[1].signal?.aborted).toBe(true);
    await act(async () => pending[1].resolve({ history: index.history, inputs: [{ ...index.inputs[50], preview: "Old preview" }] }));
    expect(preview(50)?.excerpt).toBeUndefined();
    pick(50);
    expect(pending).toHaveLength(3);
  });
});
