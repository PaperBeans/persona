// @vitest-environment jsdom
// ExtensionView navigation stack invariants (#15748):
// - Switching top-level tabs persists the active tab's state/title into stack[0] so drill-down + Back returns to that tab.
// - Popping from depth 2 to depth 1 never leaks the popped detail view into onTitle or the root onView cache.
// - Rapid Back clicks while history.back() is pending call history.back() once.
// - Changing firstState with the same ui, or pushing at MAX_STACK with the same ui, triggers a fresh load.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_STACK, type UIView } from "../../lib/extension-ui";

type DrawReq = { ui: string; state?: string; resolve: (view: unknown) => void };
type ActReq = { ui: string; action: string; state?: string; values?: Record<string, string>; resolve: (res: unknown) => void };

const mock = vi.hoisted(() => ({
  draws: [] as DrawReq[],
  acts: [] as ActReq[],
}));

vi.mock("../../lib/extension-ui", async (original) => {
  const real = await original<typeof import("../../lib/extension-ui")>();
  return {
    ...real,
    renderUI: (_ext: string, ui: string, _ctx: unknown, state?: string) =>
      new Promise((resolve) => mock.draws.push({ ui, state, resolve: (v) => resolve(real.readView(v)) })),
    actUI: (_ext: string, ui: string, action: string, opts: { state?: string; values?: Record<string, string> } = {}) =>
      new Promise((resolve) => mock.acts.push({
        ui,
        action,
        state: opts.state,
        values: opts.values,
        resolve: (v) => resolve({ view: real.readView(v) }),
      })),
  };
});

vi.mock("../../lib/store", async (original) => ({
  ...(await original<typeof import("../../lib/store")>()),
  onHubEvent: () => () => {},
}));

vi.mock("../../ui/atoms", async (original) => ({
  ...(await original<typeof import("../../ui/atoms")>()),
  toast: vi.fn(),
}));

import { LegacyExtensionUI } from "./ExtensionView";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

const tabView = (tab: "overview" | "nodes") => ({
  title: tab === "overview" ? "Overview Tab" : "Nodes Tab",
  state: `tab=${tab}`,
  tabs: {
    name: "tab",
    value: tab,
    action: { id: "switch_tab" },
    items: [
      { value: "overview", title: "Overview" },
      { value: "nodes", title: "Nodes" },
    ],
  },
  sections: [{
    items: [
      { type: "text", text: `Body ${tab}` },
      { type: "row", title: "Open Detail", action: { id: "open_detail", push: { ui: "detail", state: `from=${tab}`, title: "Node Detail" } } },
    ],
  }],
});

const detailView = (label = "Detail View") => ({
  title: label,
  state: "detail=1",
  sections: [{ items: [{ type: "text", text: `Detail body: ${label}` }] }],
});

let host: HTMLDivElement;
let root: Root;

const settle = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

const resolveDraw = async (view: unknown) => {
  const req = mock.draws.shift();
  expect(req).toBeTruthy();
  await act(async () => req!.resolve(view));
  await settle();
  return req!;
};

const resolveAct = async (view: unknown) => {
  const req = mock.acts.shift();
  expect(req).toBeTruthy();
  await act(async () => req!.resolve(view));
  await settle();
  return req!;
};

const clickButton = async (label: string) => {
  const btn = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(label));
  expect(btn, `button containing "${label}"`).toBeTruthy();
  await act(async () => btn!.click());
  await settle();
};

beforeEach(() => {
  mock.draws = [];
  mock.acts = [];
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("LegacyExtensionUI tab and stack navigation", () => {
  it("preserves switched tab state on stack[0] across drill-down push and Back pop without leaking detail title or cache", async () => {
    const titles: (string | undefined)[] = [];
    const keptViews: UIView[] = [];

    await act(async () => {
      root.render(
        <LegacyExtensionUI
          extension="demo"
          ui="main"
          state="tab=overview"
          history={false}
          onTitle={(t) => titles.push(t)}
          onView={(v) => keptViews.push(v)}
        />,
      );
    });
    await settle();

    const firstDraw = await resolveDraw(tabView("overview"));
    expect(firstDraw.state).toBe("tab=overview");
    expect(host.textContent).toContain("Body overview");

    // Switch top-level tab to Nodes; must NOT trigger a redundant draw.
    await clickButton("Nodes");
    expect(mock.draws).toHaveLength(0);
    const tabAct = await resolveAct(tabView("nodes"));
    expect(tabAct.action).toBe("switch_tab");
    expect(tabAct.values?.tab).toBe("nodes");
    expect(mock.draws).toHaveLength(0);
    expect(host.textContent).toContain("Body nodes");

    // Drill down to detail page.
    await clickButton("Open Detail");
    const detailDraw = await resolveDraw(detailView("Alpha Node Detail"));
    expect(detailDraw.ui).toBe("detail");
    expect(detailDraw.state).toBe("from=nodes");
    // Back button label reflects the updated stack[0] title ("Nodes Tab").
    expect(host.textContent).toContain("Nodes Tab");
    expect(host.textContent).toContain("Detail body: Alpha Node Detail");

    titles.length = 0;
    keptViews.length = 0;

    // Click Back: must NOT emit detail title to onTitle or cache detailView into root onView,
    // and must reload stack[0] with the active tab's state ("tab=nodes").
    await clickButton("Nodes Tab");
    expect(titles).not.toContain("Alpha Node Detail");
    expect(keptViews.some((v) => v.title === "Alpha Node Detail")).toBe(false);

    const backDraw = await resolveDraw(tabView("nodes"));
    expect(backDraw.ui).toBe("main");
    expect(backDraw.state).toBe("tab=nodes");
    expect(host.textContent).toContain("Body nodes");
    expect(titles).toEqual(["Nodes Tab"]);
    expect(keptViews.every((v) => v.title === "Nodes Tab")).toBe(true);
  });

  it("ignores duplicate Back clicks while history.back() is pending", async () => {
    const backSpy = vi.spyOn(history, "back").mockImplementation(() => {});
    const pushSpy = vi.spyOn(history, "pushState").mockImplementation(() => {});

    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="main" history />);
    });
    await settle();
    await resolveDraw(tabView("overview"));

    await clickButton("Open Detail");
    await resolveDraw(detailView("Pending Back"));
    expect(pushSpy).toHaveBeenCalledTimes(1);

    // Click Back twice rapidly before popstate arrives.
    const backBtn = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Overview Tab"))!;
    await act(async () => {
      backBtn.click();
      backBtn.click();
    });
    expect(backSpy).toHaveBeenCalledTimes(1);

    // Completing popstate settles the stack back to depth 1.
    await act(async () => {
      window.dispatchEvent(new PopStateEvent("popstate", { state: { extPage: 1 } }));
    });
    await settle();
    const reload = await resolveDraw(tabView("overview"));
    expect(reload.ui).toBe("main");
  });

  it("reloads when firstState prop changes with the same ui and when pushing at MAX_STACK with the same ui", async () => {
    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="main" state="s1" history={false} />);
    });
    await settle();
    expect((await resolveDraw(tabView("overview"))).state).toBe("s1");

    // Changing firstState with same extension & ui reloads with new state.
    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="main" state="s2" history={false} />);
    });
    await settle();
    expect((await resolveDraw(tabView("nodes"))).state).toBe("s2");

    // Push up to MAX_STACK and push one more page with the same ui ("same").
    const sameUiStep = (idx: number) => ({
      title: `Step ${idx}`,
      state: `step=${idx}`,
      sections: [{
        items: [{ type: "row", title: "Next Step", action: { id: "next", push: { ui: "same", state: `step=${idx + 1}` } } }],
      }],
    });
    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="same" state="step=1" history={false} />);
    });
    await settle();
    await resolveDraw(sameUiStep(1));

    for (let i = 1; i <= MAX_STACK; i++) {
      await clickButton("Next Step");
      const req = await resolveDraw(sameUiStep(i + 1));
      expect(req.ui).toBe("same");
      expect(req.state).toBe(`step=${i + 1}`);
    }
  });

  it("preserves initialState when a rendered view omits state, while allowing a stateless root to clear tab state", async () => {
    // 1. Entry opened with firstState="item=42" whose rendered view omits `state` must preserve "item=42" across push + Back.
    const statelessDetail = {
      title: "Item 42",
      sections: [{
        items: [
          { type: "text", text: "Loaded item 42" },
          { type: "row", title: "Sub Detail", action: { id: "sub", push: { ui: "sub", state: "sub=1", title: "Sub" } } },
        ],
      }],
    };
    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="item" state="item=42" history={false} />);
    });
    await settle();
    expect((await resolveDraw(statelessDetail)).state).toBe("item=42");

    await clickButton("Sub Detail");
    expect((await resolveDraw(detailView("Sub Page"))).state).toBe("sub=1");

    await clickButton("Item 42");
    const restored = await resolveDraw(statelessDetail);
    expect(restored.ui).toBe("item");
    expect(restored.state).toBe("item=42");

    // 2. Stateless root (initialState undefined) switching from stateful tab back to stateless default tab clears state.
    const statelessOverview = {
      title: "Overview Tab",
      tabs: tabView("overview").tabs,
      sections: [{
        items: [
          { type: "text", text: "Stateless overview" },
          { type: "row", title: "Open Detail", action: { id: "open_detail", push: { ui: "detail", title: "Detail" } } },
        ],
      }],
    };
    await act(async () => {
      root.render(<LegacyExtensionUI extension="demo" ui="home" history={false} />);
    });
    await settle();
    expect((await resolveDraw(statelessOverview)).state).toBeUndefined();

    await clickButton("Nodes");
    await resolveAct(tabView("nodes"));
    await clickButton("Overview");
    await resolveAct(statelessOverview);

    await clickButton("Open Detail");
    await resolveDraw(detailView("D"));
    await clickButton("Overview Tab");
    const backHome = await resolveDraw(statelessOverview);
    expect(backHome.ui).toBe("home");
    expect(backHome.state).toBeUndefined();
  });

  it("ignores a stale in-flight renderUI response from a popped page so it never corrupts depth 1", async () => {
    const titles: (string | undefined)[] = [];
    await act(async () => {
      root.render(
        <LegacyExtensionUI
          extension="demo"
          ui="main"
          state="tab=overview"
          history={false}
          onTitle={(t) => titles.push(t)}
        />,
      );
    });
    await settle();
    await resolveDraw(tabView("overview"));

    await clickButton("Nodes");
    await resolveAct(tabView("nodes"));

    // Push detail page, capture its in-flight renderUI request without resolving it yet.
    await clickButton("Open Detail");
    const staleDetailReq = mock.draws.shift()!;
    expect(staleDetailReq.ui).toBe("detail");

    // Pop back to depth 1 while depth 2's renderUI is still in flight.
    titles.length = 0;
    await clickButton("Nodes Tab");
    const depth1Reload = mock.draws.shift()!;
    expect(depth1Reload.ui).toBe("main");
    expect(depth1Reload.state).toBe("tab=nodes");

    // Resolve the stale depth 2 response before depth 1's reload completes: must be completely ignored.
    await act(async () => staleDetailReq.resolve(detailView("Stale Popped Detail")));
    await settle();
    expect(titles).not.toContain("Stale Popped Detail");
    expect(host.textContent).not.toContain("Stale Popped Detail");

    // Now resolve depth 1's reload cleanly.
    await act(async () => depth1Reload.resolve(tabView("nodes")));
    await settle();
    expect(host.textContent).toContain("Body nodes");
    expect(titles).toEqual(["Nodes Tab"]);
  });
});
