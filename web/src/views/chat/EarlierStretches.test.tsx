// @vitest-environment jsdom
// The earlier stretches' module (#5054): it splits the live session's items into stretches only when the live session
// ran an earlier one too (A → B → A). While a reply streams into a live session that did not, the pushes split nothing.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineLeg, Item } from "../../lib/types";
import type { LiveTranscript } from "./useEarlierStretches";

const split = vi.hoisted(() => ({ calls: [] as string[] }));
vi.mock("../../lib/engine-stretches", async (importOriginal) => {
  const m = await importOriginal<typeof import("../../lib/engine-stretches")>();
  return { ...m, splitStretches: (...a: Parameters<typeof m.splitStretches>) => (split.calls.push(a[2]), m.splitStretches(...a)) };
});

const { default: EarlierStretches } = await import("./EarlierStretches");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T09:00:00Z");
const at = (m: number) => new Date(T0 + m * 60_000).toISOString();
const item = (seq: number, m: number): Item => ({ id: `i${seq}#0`, seq, role: "assistant", kind: "text", text: `at ${m}`, ts: at(m) });
const noop = async () => {};
const live = (items: Item[]): LiveTranscript => ({ items, hasMore: false, loading: false, loadingEarlier: false, loadEarlier: noop, expandFold: noop });
const report = () => {};

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  split.calls = [];
  host = document.createElement("div");
  root = createRoot(host);
});
afterEach(() => act(() => root.unmount()));

const draw = (sid: string, history: EngineLeg[], items: Item[]) =>
  act(() => root.render(<EarlierStretches mid="m" sid={sid} scope={`m/${sid}`} history={history} live={live(items)} report={report} />));

describe("EarlierStretches", () => {
  it("splits nothing again while a reply streams into a live session that ran one stretch", () => {
    const history = [{ session_id: "a", agent: "claude", until: at(10) }];
    const items = [item(1, 11)];
    draw("b", history, items);
    const before = split.calls.length;
    for (let n = 2; n < 6; n++) draw("b", history, [...items, item(n, 10 + n)]);
    expect(split.calls.length).toBe(before);
    expect(split.calls).not.toContain("b");
  });

  it("splits the live session's items when it ran an earlier stretch too (A → B → A)", () => {
    const history = [{ session_id: "a", agent: "claude", until: at(10) }, { session_id: "b", agent: "codex", until: at(20) }];
    draw("a", history, [item(1, 1), item(2, 21)]);
    const before = split.calls.filter((s) => s === "a").length;
    draw("a", history, [item(1, 1), item(2, 21), item(3, 22)]);
    expect(split.calls.filter((s) => s === "a").length).toBe(before + 1);
  });
});
