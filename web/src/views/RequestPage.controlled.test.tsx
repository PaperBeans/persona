// @vitest-environment jsdom
// Controlled-mode, pure-view, bridge-switch, snapshot-caching, cross-request/scope reset, and default-fallback tests for RequestPage.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { loadPages } from "../lib/lazy-page";
import type { Route } from "../lib/router";
import { getState, reset, type AnsweredCard as AnsweredCardState } from "../lib/store";
import type { Interaction } from "../lib/types";
import { RequestPage, RequestPageView, type RequestPageState } from "./RequestPage";
import { t } from "../i18n";

await loadPages();

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};

const makeInteraction = (id: string, summary: string): Interaction => ({
  id,
  machine_id: "box-1",
  session_id: "",
  kind: "permission",
  tool_name: "personad_ask",
  summary,
});

const makeAnswered = (ix: Interaction): AnsweredCardState => ({
  ix,
  resolution: { kind: "permission", allow: true },
  at: Date.UTC(2026, 10, 26, 12, 0, 0),
});

beforeEach(() => {
  reset();
  history.replaceState({}, "", "/request/req-1");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
  history.replaceState({}, "", "/");
});

describe("RequestPageView (pure presentation)", () => {
  it("renders loading, pending interaction, answered receipt, and ended states without touching global store", async () => {
    const onToggleSidebar = vi.fn();
    const onBack = vi.fn();
    const ix = makeInteraction("req-view", "Ship release?");
    const ans = makeAnswered(ix);

    await act(async () => {
      root.render(<RequestPageView loaded={false} onToggleSidebar={onToggleSidebar} onBack={onBack} />);
    });
    expect(host.querySelector('[role="status"]')!.textContent).toContain(t("common.loading"));

    await act(async () => {
      root.render(
        <RequestPageView
          id={ix.id}
          loaded
          request={ix}
          onToggleSidebar={onToggleSidebar}
          onBack={onBack}
          renderInteraction={(item) => <div data-custom-ix={item.id}>{item.summary}</div>}
        />,
      );
    });
    expect(host.querySelector('[data-custom-ix="req-view"]')!.textContent).toBe("Ship release?");

    await act(async () => {
      root.render(
        <RequestPageView
          id={ix.id}
          loaded
          answered={ans}
          onToggleSidebar={onToggleSidebar}
          onBack={onBack}
          renderAnswered={(card) => <div data-custom-ans={card.ix.id}>Answered</div>}
        />,
      );
    });
    expect(host.querySelector('[data-custom-ans="req-view"]')!.textContent).toBe("Answered");

    await act(async () => {
      root.render(<RequestPageView id={ix.id} loaded onToggleSidebar={onToggleSidebar} onBack={onBack} />);
    });
    expect(host.querySelector('[role="status"]')!.textContent).toBe(t("request-page.ended"));

    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("request-page.sidebar")}"]`)!.click();
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("request-page.back")}"]`)!.click();
    });
    expect(onToggleSidebar).toHaveBeenCalledTimes(1);
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});

describe("RequestPage controlled mode", () => {
  it("bypasses global store completely when controlled state is provided and uses controlled navigation", async () => {
    // Pollute global store with a conflicting entry under the same request id.
    const globalIx = makeInteraction("req-1", "Global polluted request");
    Object.assign(getState(), {
      loaded: true,
      interactions: [globalIx],
      answered: { "req-1": makeAnswered(globalIx) },
    });

    const controlledIx = makeInteraction("req-1", "Controlled request summary");
    const navCalls: Route[] = [];
    const onBackCalls = vi.fn();

    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: { loaded: true, request: controlledIx, answered: null },
            navigate: (to) => navCalls.push(to),
            renderInteraction: (ix) => <div data-controlled-ix={ix.id}>{ix.summary}</div>,
          }}
        />,
      );
    });

    expect(host.textContent).toContain("Controlled request summary");
    expect(host.textContent).not.toContain("Global polluted request");

    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("request-page.back")}"]`)!.click();
    });
    expect(navCalls).toEqual([{ name: "home" }]);
    expect(location.pathname).toBe("/request/req-1");

    // Explicit onBack takes precedence over navigate.
    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: { loaded: true, request: null, answered: null },
            navigate: (to) => navCalls.push(to),
            onBack: onBackCalls,
          }}
        />,
      );
    });
    expect(host.querySelector('[role="status"]')!.textContent).toBe(t("request-page.ended"));
    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("request-page.back")}"]`)!.click();
    });
    expect(onBackCalls).toHaveBeenCalledTimes(1);
    expect(navCalls).toHaveLength(1);
  });

  it("caches fresh-array and fresh-object getters without infinite re-renders and updates on subscribe", async () => {
    const ix1 = makeInteraction("req-1", "First prompt");
    const ix2 = makeInteraction("req-2", "Second prompt");
    let list: Interaction[] = [ix1, ix2];
    let answeredById: Record<string, AnsweredCardState | undefined> = {};
    const listeners = new Set<() => void>();
    let reads = 0;

    const subscribe = (listener: () => void) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    };

    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            loaded: () => true,
            interactions: () => {
              reads += 1;
              return list.map((item) => ({ ...item }));
            },
            answeredMap: () => ({ ...answeredById }),
            subscribe,
            renderInteraction: (ix) => <div data-live-ix={ix.id}>{ix.summary}</div>,
            renderAnswered: (card) => <div data-live-ans={card.ix.id}>{card.ix.summary}</div>,
          }}
        />,
      );
    });
    await flush();

    expect(host.querySelector('[data-live-ix="req-1"]')!.textContent).toBe("First prompt");
    expect(reads).toBeLessThan(20);

    // Transition pending -> answered through external subscription.
    list = [ix2];
    answeredById = { "req-1": makeAnswered(ix1) };
    await act(async () => listeners.forEach((l) => l()));
    await flush();

    expect(host.querySelector('[data-live-ix="req-1"]')).toBeNull();
    expect(host.querySelector('[data-live-ans="req-1"]')!.textContent).toBe("First prompt");
  });

  it("preserves bridge identity on direct-to-subscribed switch and resets card state across request id and scopeKey switches", async () => {
    const ix1 = makeInteraction("req-1", "Request One");
    const ix2 = makeInteraction("req-2", "Request Two");
    const stateById: Record<string, RequestPageState> = {
      "req-1": { loaded: true, request: ix1, answered: null },
      "req-2": { loaded: true, request: ix2, answered: null },
    };

    function StatefulCard({ ix }: { ix: Interaction }) {
      const [draft, setDraft] = useState("clean");
      return (
        <div data-card-id={ix.id}>
          <span data-draft>{draft}</span>
          <button type="button" data-dirty onClick={() => setDraft("dirty")}>Mark dirty</button>
        </div>
      );
    }

    // Direct mode on req-1.
    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: stateById["req-1"],
            scopeKey: "hub-a",
            renderInteraction: (ix) => <StatefulCard ix={ix} />,
          }}
        />,
      );
    });
    await act(async () => host.querySelector<HTMLButtonElement>("[data-dirty]")!.click());
    expect(host.querySelector("[data-draft]")!.textContent).toBe("dirty");

    // Bridge switch (direct -> getter+subscribe) on the SAME id + scopeKey preserves card identity without unexpected remount.
    const subscribe = () => () => {};
    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: (id) => ({ ...stateById[id]! }),
            subscribe,
            scopeKey: "hub-a",
            renderInteraction: (ix) => <StatefulCard ix={ix} />,
          }}
        />,
      );
    });
    expect(host.querySelector("[data-draft]")!.textContent).toBe("dirty");

    // Switching request id (req-1 -> req-2) resets card state cleanly.
    await act(async () => {
      root.render(
        <RequestPage
          id="req-2"
          onToggleSidebar={() => {}}
          deps={{
            state: (id) => ({ ...stateById[id]! }),
            subscribe,
            scopeKey: "hub-a",
            renderInteraction: (ix) => <StatefulCard ix={ix} />,
          }}
        />,
      );
    });
    expect(host.querySelector("[data-card-id]")!.getAttribute("data-card-id")).toBe("req-2");
    expect(host.querySelector("[data-draft]")!.textContent).toBe("clean");

    // Switching back to req-1 (ABA) starts clean, not carrying old dirty state.
    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: (id) => ({ ...stateById[id]! }),
            subscribe,
            scopeKey: "hub-a",
            renderInteraction: (ix) => <StatefulCard ix={ix} />,
          }}
        />,
      );
    });
    expect(host.querySelector("[data-draft]")!.textContent).toBe("clean");

    // Dirtying again and switching scopeKey on the same id resets card state cleanly.
    await act(async () => host.querySelector<HTMLButtonElement>("[data-dirty]")!.click());
    expect(host.querySelector("[data-draft]")!.textContent).toBe("dirty");
    await act(async () => {
      root.render(
        <RequestPage
          id="req-1"
          onToggleSidebar={() => {}}
          deps={{
            state: (id) => ({ ...stateById[id]! }),
            subscribe,
            scopeKey: "hub-b",
            renderInteraction: (ix) => <StatefulCard ix={ix} />,
          }}
        />,
      );
    });
    expect(host.querySelector("[data-draft]")!.textContent).toBe("clean");
  });

  it("caches fresh-array getters in partial-override StoreRequestPage mode without infinite re-renders", async () => {
    Object.assign(getState(), { loaded: true, interactions: [], answered: {} });
    const ix1 = makeInteraction("req-partial", "Partial fresh interaction");
    let list: Interaction[] = [ix1];
    const listeners = new Set<() => void>();
    let reads = 0;

    await act(async () => {
      root.render(
        <RequestPage
          id="req-partial"
          onToggleSidebar={() => {}}
          deps={{
            interactions: () => {
              reads += 1;
              return list.map((item) => ({ ...item }));
            },
            subscribe: (l) => {
              listeners.add(l);
              return () => listeners.delete(l);
            },
            renderInteraction: (ix) => <div data-partial-ix={ix.id}>{ix.summary}</div>,
          }}
        />,
      );
    });
    await flush();
    expect(host.querySelector('[data-partial-ix="req-partial"]')!.textContent).toBe("Partial fresh interaction");
    expect(reads).toBeLessThan(20);

    list = [];
    await act(async () => listeners.forEach((l) => l()));
    await flush();
    expect(host.querySelector('[role="status"]')!.textContent).toBe(t("request-page.ended"));
  });

  it("keeps 100% backward-compatible default store and router behavior when deps is omitted", async () => {
    const ix = makeInteraction("req-default", "Approve default request?");
    Object.assign(getState(), {
      loaded: true,
      interactions: [ix],
      answered: {},
    });

    await act(async () => {
      root.render(<RequestPage id="req-default" onToggleSidebar={() => {}} />);
    });
    await flush();

    expect(host.textContent).toContain("Approve default request?");
    await act(async () => {
      host.querySelector<HTMLButtonElement>(`button[aria-label="${t("request-page.back")}"]`)!.click();
    });
    expect(location.pathname).toBe("/overview");
  });
});
