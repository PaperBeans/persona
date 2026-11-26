// @vitest-environment jsdom
// Shared machine extension control hook: per-machine request ordering, stale write/error suppression,
// visibility-aware polling, merge reconciliation, toggle/action/recheck flows.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const kept = new Map<string, string>();
  const g = globalThis as unknown as { localStorage: Pick<Storage, "getItem" | "setItem" | "removeItem"> };
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const toasts = vi.hoisted(() => [] as { text: string; tone?: string }[]);
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../ui/atoms")>()),
  toast: (text: string, tone?: string) => void toasts.push({ text, tone }),
}));

import { useMachineExtensionControl, type MachineExtensionControlOptions } from "./useMachineExtensionControl";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface SampleState {
  enabled: boolean;
  step?: string;
  version?: number;
}

type Pending = {
  method: string;
  path: string;
  body?: unknown;
  answer: (v: unknown, status?: number) => void;
};

let pending: Pending[] = [];
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
function hub(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  return new Promise((resolve) => {
    pending.push({
      method: init?.method ?? "GET",
      path: String(input).split("?")[0]!,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      answer: (v, status = 200) => resolve(json(v, status)),
    });
  });
}

let host: HTMLDivElement;
let root: Root;
let latest: ReturnType<typeof useMachineExtensionControl<SampleState>> | null = null;

function Harness(props: MachineExtensionControlOptions<SampleState>) {
  latest = useMachineExtensionControl(props);
  return <div data-step={latest.shown.step ?? ""} data-enabled={String(latest.enabled)} data-checking={String(latest.checking)} data-acting={String(latest.acting)} />;
}

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); await Promise.resolve(); });
};

const render = (props: Partial<MachineExtensionControlOptions<SampleState>> & { mid: string; initial: SampleState }) =>
  act(() => {
    root.render(
      <Harness
        resource="sample"
        online
        pollEvery={() => 0}
        toggleError={(on, error) => `${on ? "on" : "off"}:${error}`}
        {...props}
      />,
    );
  });

beforeEach(() => {
  pending = [];
  toasts.length = 0;
  latest = null;
  vi.stubGlobal("fetch", vi.fn(hub));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("useMachineExtensionControl", () => {
  it("drops stale write responses and stale write errors after switching machines", async () => {
    render({ mid: "m1", initial: { enabled: false, step: "idle" } });
    await flush();

    let togglePromise!: Promise<void>;
    act(() => {
      togglePromise = latest!.toggle(true);
    });
    const put = pending.find((p) => p.method === "PUT" && p.path === "/api/v1/machines/m1/sample")!;
    expect(put).toBeDefined();

    // Switch to another machine before the PUT fails: neither state nor error toast leaks onto m2.
    render({ mid: "m2", initial: { enabled: true, step: "ready" } });
    await flush();
    put.answer({ error: "boom" }, 500);
    await act(async () => { await togglePromise; });
    await flush();

    expect(toasts).toEqual([]);
    expect(latest!.shown).toEqual({ enabled: true, step: "ready" });
  });

  it("applies custom merge reconciliation, formats toggle errors, and tracks recheck and action states", async () => {
    const merge = (fresh: SampleState | null, initial: SampleState) =>
      fresh && (fresh.version ?? 0) >= (initial.version ?? 0) ? fresh : initial;

    render({ mid: "m1", initial: { enabled: true, step: "list-v2", version: 2 }, merge });
    await flush();
    pending[0]!.answer({ enabled: true, step: "stale-v1", version: 1 });
    await flush();
    expect(latest!.shown.step).toBe("list-v2");

    // Recheck toggles checking while GET is in flight and adopts newer data.
    let recheckPromise!: Promise<void>;
    act(() => {
      recheckPromise = latest!.recheck();
    });
    expect(latest!.checking).toBe(true);
    pending.at(-1)!.answer({ enabled: true, step: "fresh-v3", version: 3 });
    await act(async () => { await recheckPromise; });
    expect(latest!.checking).toBe(false);
    expect(latest!.shown.step).toBe("fresh-v3");

    // postAction forwards JSON payload when provided and reports errors via toast.
    let actionPromise!: Promise<void>;
    act(() => {
      actionPromise = latest!.postAction("pair", { device: "d1" });
    });
    expect(latest!.acting).toBe(true);
    const post = pending.at(-1)!;
    expect(post.method).toBe("POST");
    expect(post.path).toBe("/api/v1/machines/m1/sample/pair");
    expect(post.body).toEqual({ device: "d1" });
    post.answer({ error: "pair failed" }, 500);
    await act(async () => { await actionPromise; });
    expect(latest!.acting).toBe(false);
    expect(toasts).toHaveLength(1);
    expect(toasts[0]!.tone).toBe("error");
    expect(toasts[0]!.text).toContain("pair failed");

    // Toggle formats error via toggleError callback.
    let togglePromise!: Promise<void>;
    act(() => {
      togglePromise = latest!.toggle(false);
    });
    pending.at(-1)!.answer({ error: "denied" }, 500);
    await act(async () => { await togglePromise; });
    expect(toasts).toHaveLength(2);
    expect(toasts[1]!.tone).toBe("error");
    expect(toasts[1]!.text).toContain("off:");
    expect(toasts[1]!.text).toContain("denied");
  });
});
