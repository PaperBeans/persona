// @vitest-environment jsdom
// Controlled ConnectionPill and ConnectionDetails bypass store singletons while preserving popover dismissal and 900 ms reconnect throttling; default fallbacks stay pinned too.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NO_LINK, type ConnLink } from "../lib/connection";
import type { Conn } from "../lib/store";
import type { ConnectionPillDeps, ConnectionState } from "./ConnectionPill";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(),
  reconnectNow: vi.fn(),
}));

vi.mock("../lib/store", () => ({
  useStore: mocks.useStore,
  reconnectNow: mocks.reconnectNow,
}));

const {
  ConnectionDetails,
  ConnectionDetailsView,
  ConnectionPill,
  ConnectionPillView,
} = await import("./ConnectionPill");
const { initI18n, t } = await import("../i18n");

const NOW = 1_000_000;
let host: HTMLDivElement;
let root: Root;

const pill = () => host.querySelector<HTMLButtonElement>("[data-connection-pill]")!;
const details = () => host.querySelector<HTMLElement>("[data-connection-details]");
const retryBtn = () => details()?.querySelector<HTMLButtonElement>("button") ?? null;

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.reconnectNow).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  mocks.useStore.mockReset();
  mocks.reconnectNow.mockReset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe("ConnectionPill controlled deps, view separation, popover close, and reconnect throttle", () => {
  it("runs fully controlled via subscribed connection reader without touching default store singletons", async () => {
    let current: ConnectionState = {
      conn: "offline",
      link: { ...NO_LINK, nextTry: NOW + 4_200, lastOnline: NOW - 12_000 },
    };
    const listeners = new Set<() => void>();
    const emit = (next: ConnectionState) => {
      current = next;
      listeners.forEach((l) => l());
    };
    const reconnect = vi.fn();
    const deps: ConnectionPillDeps = {
      // Return a fresh outer object AND a fresh nested ConnLink object on every read to verify value-based snapshot caching.
      connection: () => ({ conn: current.conn, link: { ...current.link } }),
      subscribe: (l) => {
        listeners.add(l);
        return () => { listeners.delete(l); };
      },
      reconnectNow: reconnect,
      now: NOW,
    };

    await act(async () => root.render(<StrictMode><ConnectionPill deps={deps} /></StrictMode>));
    expect(pill().textContent).toBe(t("app.reconnecting"));
    expect(pill().getAttribute("aria-expanded")).toBe("false");
    expect(details()).toBeNull();

    // Open details and verify facts + live phase updates through subscribe, including lastError-only link changes.
    await act(async () => pill().click());
    expect(pill().getAttribute("aria-expanded")).toBe("true");
    expect(details()?.textContent).toContain(t("lib-connection.retry_in", { count: 5 }));
    expect(retryBtn()).not.toBeNull();

    await act(async () => emit({ conn: "offline", link: { ...current.link, lastError: "HTTP 502" } }));
    expect(details()?.textContent).toContain("HTTP 502");

    await act(async () => emit({ conn: "offline", link: { ...NO_LINK, restarting: true } }));
    expect(pill().textContent).toBe(t("lib-connection.restarting"));
    expect(details()).not.toBeNull();

    await act(async () => emit({ conn: "offline", link: { ...NO_LINK, networkUp: false } }));
    expect(pill().textContent).toBe(t("lib-connection.no_network"));

    await act(async () => emit({ conn: "online", link: { ...NO_LINK } }));
    expect(pill().textContent).toBe(t("lib-connection.online"));
    expect(retryBtn()).toBeNull();
    expect(details()?.querySelector("ul")).toBeNull();

    // Emitting again with identical values in fresh nested objects stays stable (no infinite loop).
    await act(async () => emit({ conn: "online", link: { ...NO_LINK } }));
    expect(pill().textContent).toBe(t("lib-connection.online"));

    expectNoDefaultSingletons();
  });

  it("caches nested fresh NO_LINK snapshots without hitting Maximum update depth exceeded", async () => {
    const listeners = new Set<() => void>();
    const subscribe = (l: () => void) => {
      listeners.add(l);
      return () => { listeners.delete(l); };
    };
    const deps: ConnectionPillDeps = {
      connection: () => ({ conn: "offline", link: { ...NO_LINK } }),
      subscribe,
      reconnectNow: vi.fn(),
      now: NOW,
    };

    await act(async () => root.render(<StrictMode><ConnectionPill deps={deps} /></StrictMode>));
    expect(pill().textContent).toBe(t("app.reconnecting"));
    await act(async () => { listeners.forEach((l) => l()); });
    expect(pill().textContent).toBe(t("app.reconnecting"));
    expectNoDefaultSingletons();
  });

  it("preserves popover open/close semantics (toggle, inside pointerdown, outside pointerdown, Escape, non-Escape, unmount cleanup)", async () => {
    const deps: ConnectionPillDeps = {
      conn: "connecting",
      link: { ...NO_LINK, nextTry: NOW + 3_000 },
      reconnectNow: vi.fn(),
      now: NOW,
    };

    await act(async () => root.render(<ConnectionPill deps={deps} />));
    await act(async () => pill().click());
    expect(details()).not.toBeNull();

    // Pointerdown inside details or on pill itself must not close prematurely.
    await act(async () => { details()!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); });
    expect(details()).not.toBeNull();
    await act(async () => { pill().dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); });
    expect(details()).not.toBeNull();

    // Non-Escape keys must not close details.
    await act(async () => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" })); });
    await act(async () => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab" })); });
    expect(details()).not.toBeNull();

    // Outside pointerdown closes details.
    await act(async () => { document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); });
    expect(details()).toBeNull();
    expect(pill().getAttribute("aria-expanded")).toBe("false");

    // Reopen, Escape closes details.
    await act(async () => pill().click());
    expect(details()).not.toBeNull();
    await act(async () => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); });
    expect(details()).toBeNull();

    // Reopen and toggle off via pill click.
    await act(async () => pill().click());
    expect(details()).not.toBeNull();
    await act(async () => pill().click());
    expect(details()).toBeNull();

    // Unmount while open removes global listeners cleanly.
    const removeSpy = vi.spyOn(window, "removeEventListener");
    await act(async () => pill().click());
    await act(async () => root.render(null));
    expect(removeSpy).toHaveBeenCalledWith("pointerdown", expect.any(Function), true);
    expect(removeSpy).toHaveBeenCalledWith("keydown", expect.any(Function));
    removeSpy.mockRestore();
    expectNoDefaultSingletons();
  });

  it("throttles reconnectNow for 900 ms across rapid double-click and re-enables cleanly after timeout or close/reopen", async () => {
    vi.useFakeTimers();
    const reconnect = vi.fn();
    const deps: ConnectionPillDeps = {
      connection: { conn: "offline", link: { ...NO_LINK, nextTry: NOW + 4_000, lastOnline: NOW - 15_000 } },
      reconnectNow: reconnect,
      now: NOW,
    };

    await act(async () => root.render(<ConnectionPill deps={deps} />));
    await act(async () => pill().click());

    // Rapid synchronous double-click admits only one reconnect call.
    await act(async () => {
      retryBtn()!.click();
      retryBtn()!.click();
    });
    expect(reconnect).toHaveBeenCalledTimes(1);
    expect(retryBtn()!.disabled).toBe(true);
    expect(retryBtn()!.getAttribute("aria-busy")).toBe("true");

    // Still throttled before 900 ms.
    await act(async () => { vi.advanceTimersByTime(899); });
    expect(retryBtn()!.disabled).toBe(true);
    await act(async () => retryBtn()!.click());
    expect(reconnect).toHaveBeenCalledTimes(1);

    // At 900 ms the throttle clears and a new click invokes reconnectNow again.
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(retryBtn()!.disabled).toBe(false);
    expect(retryBtn()!.getAttribute("aria-busy")).toBeNull();

    await act(async () => retryBtn()!.click());
    expect(reconnect).toHaveBeenCalledTimes(2);
    expect(retryBtn()!.disabled).toBe(true);

    // Closing popover during the 900 ms window clears the timer; reopening starts ready.
    await act(async () => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); });
    expect(details()).toBeNull();
    await act(async () => { vi.advanceTimersByTime(1_000); });
    await act(async () => pill().click());
    expect(retryBtn()!.disabled).toBe(false);
    await act(async () => retryBtn()!.click());
    expect(reconnect).toHaveBeenCalledTimes(3);
    expectNoDefaultSingletons();
  });

  it("renders pure ConnectionPillView and ConnectionDetailsView without default singletons", async () => {
    const onToggle = vi.fn();
    const onRetry = vi.fn();
    const link: ConnLink = { ...NO_LINK, nextTry: NOW + 5_000, lastOnline: NOW - 30_000 };

    await act(async () => root.render(
      <ConnectionPillView conn="offline" link={link} open onToggle={onToggle} details={<div data-custom-details>Custom</div>} />,
    ));
    expect(host.querySelector("[data-custom-details]")?.textContent).toBe("Custom");
    await act(async () => pill().click());
    expect(onToggle).toHaveBeenCalledTimes(1);

    await act(async () => root.render(
      <ConnectionDetailsView conn="offline" link={link} now={NOW} trying={false} onRetry={onRetry} />,
    ));
    await act(async () => retryBtn()!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("preserves default store and reconnectNow fallbacks when deps are omitted or partial", async () => {
    const storeState: { conn: Conn; link: ConnLink } = {
      conn: "offline",
      link: { ...NO_LINK, nextTry: NOW + 3_000, lastOnline: NOW - 20_000 },
    };
    mocks.useStore.mockImplementation((sel: (s: typeof storeState) => unknown) => sel(storeState));

    await act(async () => root.render(<ConnectionPill />));
    expect(mocks.useStore).toHaveBeenCalled();
    await act(async () => pill().click());
    await act(async () => retryBtn()!.click());
    expect(mocks.reconnectNow).toHaveBeenCalledTimes(1);

    // Partial override: custom reconnectNow still reads default store state.
    const customReconnect = vi.fn();
    await act(async () => root.render(<ConnectionDetails conn="offline" link={storeState.link} now={NOW} deps={{ reconnectNow: customReconnect }} />));
    await act(async () => retryBtn()!.click());
    expect(customReconnect).toHaveBeenCalledTimes(1);
    expect(mocks.reconnectNow).toHaveBeenCalledTimes(1);
  });
});
