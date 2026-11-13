// @vitest-environment jsdom
// The live work lines' clocks count themselves in (an older hub's guess at a background workflow yields to them), and
// the running bar shows its own time beside them, as cindy's does (#5312; #3534 left it to the work line).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

import { LiveClockContext } from "./chat-context";
import { RunningStatusView } from "./RunningStatusBar";
import { WorkGroup, type WorkProps } from "./WorkGroup";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const props: WorkProps = { mid: "m", sid: "s", scope: "t:", expandFold: async () => {}, agentStatus: () => "completed" };
let box: HTMLDivElement;
let root: Root;
beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(() => {
  act(() => root.unmount());
  box.remove();
});

describe("the running bar's clock", () => {
  it("stands still while the hub is out of reach", () => {
    vi.useFakeTimers();
    const started = new Date(Date.now() - 5000).toISOString();
    const bar = (offline: boolean) => <RunningStatusView live={null} working startedAt={started} offline={offline} />;
    const shown = () => box.querySelector("[data-running-elapsed]")?.textContent;
    act(() => root.render(bar(true)));
    const stopped = shown();
    act(() => { vi.advanceTimersByTime(3000); });
    expect(shown()).toBe(stopped);
    act(() => root.render(bar(false)));
    act(() => { vi.advanceTimersByTime(3000); });
    expect(shown()).not.toBe(stopped);
    vi.useRealTimers();
  });
});

describe("live clock (#3534)", () => {
  it("a live work line's clock counts itself in and out", () => {
    let n = 0;
    const count = (d: 1 | -1) => (n += d);
    const live = (on: boolean) => (
      <LiveClockContext.Provider value={count}>
        {on && <WorkGroup g={{ kind: "group", id: "work-live", children: [], live: true, startedAt: Date.now() - 5000 }} p={props} />}
      </LiveClockContext.Provider>
    );
    act(() => root.render(live(true)));
    expect(n).toBe(1);
    act(() => root.render(live(false)));
    expect(n).toBe(0);
  });

  it("a done group has no clock to count", () => {
    let n = 0;
    act(() =>
      root.render(
        <LiveClockContext.Provider value={(d) => (n += d)}>
          <WorkGroup g={{ kind: "group", id: "work-done", children: [], live: false, durationMs: 3000, startedAt: Date.now() - 5000 }} p={props} />
        </LiveClockContext.Provider>,
      ),
    );
    expect(n).toBe(0);
  });

  it("the bar keeps its time while a work line ticks, and while it lingers after the turn", () => {
    const live = { verb: "Frosting", elapsed_s: 12 };
    act(() => root.render(<RunningStatusView live={live} working clock={false} />));
    expect(box.querySelector("[data-running-elapsed]")?.textContent).toBe("12s");
    act(() => root.render(<RunningStatusView live={live} working={false} clock />));
    expect(box.querySelector('[data-running-status="linger"]')).not.toBeNull();
    expect(box.querySelector("[data-running-elapsed]")?.textContent).toBe("12s");
  });
});
