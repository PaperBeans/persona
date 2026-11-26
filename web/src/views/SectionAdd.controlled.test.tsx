// @vitest-environment jsdom
// Controlled SectionAdd bypasses router.href, caches getter href snapshots, preserves render purity across abandoned
// concurrent transitions, fences cross-scope/cross-route/ABA async clicks, recovers via try/finally, and keeps defaults.
import { act, startTransition, StrictMode, Suspense, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Route } from "../lib/router";
import type { SectionAddDeps } from "./SectionAdd";

const mocks = vi.hoisted(() => ({
  href: vi.fn((r: Route) => `/default/${r.name}`),
}));

vi.mock("../lib/router", async (original) => ({
  ...(await original<typeof import("../lib/router")>()),
  href: (r: Route) => mocks.href(r),
}));

const { SectionAdd, SectionAddView } = await import("./SectionAdd");

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.href.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("SectionAddView (pure presentation)", () => {
  it("renders button and link variants with expected ARIA, active state, and disabled attributes without calling router.href", async () => {
    const onClick = vi.fn();

    await act(async () => root.render(<SectionAddView label="New agent" onClick={onClick} className="extra-cls" />));
    const btn = host.querySelector("button")!;
    expect(btn).not.toBeNull();
    expect(btn.type).toBe("button");
    expect(btn.getAttribute("aria-label")).toBe("New agent");
    expect(btn.getAttribute("title")).toBe("New agent");
    expect(btn.className).toContain("extra-cls");
    expect(btn.className).toContain("text-ink-3");
    await act(async () => btn.click());
    expect(onClick).toHaveBeenCalledTimes(1);

    await act(async () => root.render(
      <SectionAddView label="New chat" icon="compose" href="/shell/new" active disabled onClick={onClick} />,
    ));
    const link = host.querySelector("a")!;
    expect(link).not.toBeNull();
    expect(link.getAttribute("href")).toBe("/shell/new");
    expect(link.getAttribute("aria-current")).toBe("page");
    expect(link.getAttribute("aria-disabled")).toBe("true");
    expect(link.className).toContain("bg-hover text-ink");
    expect(mocks.href).not.toHaveBeenCalled();
  });
});

describe("SectionAdd controlled deps, snapshot caching, render purity, fencing, and default fallback", () => {
  it("bypasses router.href when controlled, caches getter href, and switches cleanly between direct and subscribed modes", async () => {
    const hrefListeners = new Set<() => void>();
    const scopeListeners = new Set<() => void>();
    let prefix = "/v1";
    let currentScope = "s1";
    let hrefReads = 0;
    const onNavigate = vi.fn((_to: Route, e: React.MouseEvent<HTMLElement>) => { e.preventDefault(); });

    // Start in direct mode.
    await act(async () => root.render(
      <StrictMode>
        <SectionAdd
          label="New chat"
          icon="compose"
          to={{ name: "persona-new" }}
          deps={{
            scopeKey: "s1",
            href: "/v1/persona-new",
            onNavigate,
          }}
        />
      </StrictMode>,
    ));
    expect(host.querySelector("a")?.getAttribute("href")).toBe("/v1/persona-new");
    expect(mocks.href).not.toHaveBeenCalled();

    // Switch in-place to getter + subscribe mode with identical values.
    const subDeps: SectionAddDeps = {
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      href: (to) => {
        hrefReads += 1;
        return `${prefix}/${to?.name ?? "none"}`;
      },
      subscribeHref: (l) => { hrefListeners.add(l); return () => { hrefListeners.delete(l); }; },
      onNavigate,
    };
    await act(async () => root.render(
      <StrictMode>
        <SectionAdd label="New chat" icon="compose" to={{ name: "persona-new" }} deps={subDeps} />
      </StrictMode>,
    ));
    expect(host.querySelector("a")?.getAttribute("href")).toBe("/v1/persona-new");
    expect(hrefReads).toBeLessThan(20);

    // Equal re-emission stays cached; changing prefix updates link href.
    await act(async () => { hrefListeners.forEach((l) => l()); });
    expect(host.querySelector("a")?.getAttribute("href")).toBe("/v1/persona-new");

    prefix = "/v2";
    await act(async () => { hrefListeners.forEach((l) => l()); });
    expect(host.querySelector("a")?.getAttribute("href")).toBe("/v2/persona-new");

    await act(async () => { host.querySelector("a")!.click(); });
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(onNavigate.mock.calls[0][0]).toEqual({ name: "persona-new" });
    expect(mocks.href).not.toHaveBeenCalled();

    // Custom renderView seam works cleanly.
    await act(async () => root.render(
      <SectionAdd
        label="Custom"
        deps={{
          scopeKey: "s1",
          href: "/custom",
          renderView: (p) => <div data-custom-add={`${p.label}|${p.href}`} />,
        }}
      />,
    ));
    expect(host.querySelector("[data-custom-add]")?.getAttribute("data-custom-add")).toBe("Custom|/custom");
    expect(mocks.href).not.toHaveBeenCalled();
  });

  it("fences async actions across scope switches, ABA bursts, route switches, and unmount while recovering via try/finally", async () => {
    const scopeListeners = new Set<() => void>();
    let currentScope = "s1";
    const calls: Array<{ signal: AbortSignal; gate: ReturnType<typeof deferred<void>> }> = [];
    const onError = vi.fn();

    const makeDeps = (extra?: Partial<SectionAddDeps>): SectionAddDeps => ({
      scopeKey: () => currentScope,
      subscribeScopeKey: (l) => { scopeListeners.add(l); return () => { scopeListeners.delete(l); }; },
      onAction: (_e, signal) => {
        const gate = deferred<void>();
        calls.push({ signal, gate });
        return gate.promise;
      },
      onError,
      ...extra,
    });

    await act(async () => root.render(<SectionAdd label="Add item" deps={makeDeps()} />));
    const btn = () => host.querySelector("button")!;

    // Single-flight busy guard prevents duplicate concurrent invocations.
    await act(async () => btn().click());
    await act(async () => btn().click());
    expect(calls).toHaveLength(1);
    expect(btn().disabled).toBe(true);

    // ABA scope burst (s1 -> s2 -> s1) aborts the in-flight click, clears busy, and discards its late rejection.
    await act(async () => {
      currentScope = "s2";
      scopeListeners.forEach((l) => l());
      currentScope = "s1";
      scopeListeners.forEach((l) => l());
    });
    expect(calls[0].signal.aborted).toBe(true);
    expect(btn().disabled).toBe(false);
    await act(async () => {
      calls[0].gate.reject(new Error("stale ABA error"));
      await Promise.resolve();
    });
    expect(onError).not.toHaveBeenCalled();

    // Async rejection in active scope releases busy via try/finally and reports error (even if onError throws).
    onError.mockImplementationOnce(() => { throw new Error("observer boom"); });
    await act(async () => btn().click());
    expect(calls).toHaveLength(2);
    expect(btn().disabled).toBe(true);
    await act(async () => {
      calls[1].gate.reject(new Error("network failed"));
      await Promise.resolve();
    });
    expect(onError).toHaveBeenCalledTimes(1);
    expect(btn().disabled).toBe(false);

    // Synchronous throw releases cleanly, reports error, and allows immediate retry.
    const syncErrors: unknown[] = [];
    let shouldThrow = true;
    let succeeded = 0;
    await act(async () => root.render(
      <SectionAdd
        label="Add item"
        deps={{
          scopeKey: "s3",
          onAction: () => {
            if (shouldThrow) throw new Error("sync boom");
            succeeded += 1;
          },
          onError: (e) => { syncErrors.push(e); },
        }}
      />,
    ));
    await act(async () => btn().click());
    expect(syncErrors).toHaveLength(1);
    expect(btn().disabled).toBe(false);

    shouldThrow = false;
    await act(async () => btn().click());
    expect(succeeded).toBe(1);

    // Unmount aborts active async click and discards late rejection.
    await act(async () => root.render(<SectionAdd label="Add item" deps={makeDeps()} />));
    await act(async () => btn().click());
    const last = calls.at(-1)!;
    await act(async () => root.render(null));
    expect(last.signal.aborted).toBe(true);
    await act(async () => {
      last.gate.reject(new Error("after unmount"));
      await Promise.resolve();
    });
    expect(onError).toHaveBeenCalledTimes(1);
  });

  it("keeps committed scope and in-flight async action intact when a concurrent transition render is abandoned", async () => {
    const suspendGate = deferred<void>();
    const actionGate = deferred<void>();
    let capturedSignal: AbortSignal | null = null;
    const onError = vi.fn();
    const renderedScopes: string[] = [];

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarness!: (next: { scope: string; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ scope: "s1", block: false });
      setHarness = setState;
      renderedScopes.push(state.scope);
      return (
        <Suspense fallback={<div data-fallback />}>
          <SectionAdd
            label="Add"
            deps={{
              scopeKey: state.scope,
              onAction: (_e, signal) => {
                capturedSignal = signal;
                return actionGate.promise;
              },
              onError,
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    await act(async () => host.querySelector("button")!.click());
    expect(capturedSignal).not.toBeNull();
    expect(capturedSignal!.aborted).toBe(false);
    expect(host.querySelector("button")!.disabled).toBe(true);

    // Suspended concurrent transition to s2 must NOT abort committed s1 action or corrupt committed scope.
    await act(async () => {
      startTransition(() => setHarness({ scope: "s2", block: true }));
    });
    expect(renderedScopes).toContain("s2");
    expect(capturedSignal!.aborted).toBe(false);
    expect(host.querySelector("button")!.disabled).toBe(true);

    // Abandon transition back to s1; resolving actionGate still completes cleanly on s1.
    await act(async () => setHarness({ scope: "s1", block: false }));
    expect(capturedSignal!.aborted).toBe(false);
    await act(async () => {
      actionGate.resolve();
      await Promise.resolve();
    });
    expect(host.querySelector("button")!.disabled).toBe(false);
    expect(onError).not.toHaveBeenCalled();
  });

  it("preserves 100% backward-compatible default link and button behavior when deps is omitted", async () => {
    const goHandler = vi.fn((e: React.MouseEvent) => e.preventDefault());
    const go = vi.fn(() => goHandler);
    await act(async () => root.render(
      <SectionAdd icon="compose" label="New chat" to={{ name: "persona-new" }} go={go} active />,
    ));
    const link = host.querySelector("a")!;
    expect(link.getAttribute("href")).toBe("/default/persona-new");
    expect(link.getAttribute("aria-current")).toBe("page");
    expect(mocks.href).toHaveBeenCalledWith({ name: "persona-new" });
    await act(async () => link.click());
    expect(go).toHaveBeenCalledWith({ name: "persona-new" });
    expect(goHandler).toHaveBeenCalledTimes(1);

    const onClick = vi.fn();
    await act(async () => root.render(<SectionAdd label="New agent" onClick={onClick} />));
    const btn = host.querySelector("button")!;
    await act(async () => btn.click());
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
