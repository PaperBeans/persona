// @vitest-environment jsdom
// A monitoring chunk failure is local to its computer card, and a later sample asks for the chunk again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resetManagerChoice } from "../lib/manager-choice";
import { resetRoster } from "../lib/managers";
import type { Machine, MachineMetrics } from "../lib/types";
import { OverviewMachineCards } from "./OverviewMachineCards";

const state = vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
  return { attempts: 0, fail: true };
});

vi.mock("../lib/lazy-page", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/lazy-page")>();
  return {
    ...actual,
    lazyPage: () => function Summary() {
      state.attempts++;
      if (state.fail) throw new Error("stale monitoring chunk");
      return <div data-testid="recovered-summary">Recovered monitoring</div>;
    },
  };
});

const now = Math.floor(Date.now() / 1000);
let sampleTime = now;
let tokenOnly = false;
let host: HTMLDivElement;
let root: Root;
const machine = { id: "mac", name: "Mac", online: true } as Machine;
const response = (): { machines: MachineMetrics[] } => tokenOnly
  ? { machines: [{ machine_id: "mac", online: true, points: [], usage: { today: { in: 123 }, per_minute: 0, recent: [] } }] }
  : { machines: [{ machine_id: "mac", online: true, latest: { t: sampleTime, cpu: 42 }, points: [] }] };
const flush = async () => {
  for (let i = 0; i < 20; i++) await act(async () => { await vi.advanceTimersByTimeAsync(0); });
};

beforeEach(() => {
  state.attempts = 0;
  state.fail = true;
  sampleTime = now;
  tokenOnly = false;
  resetRoster();
  resetManagerChoice();
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(JSON.stringify(response()), { status: 200, headers: { "Content-Type": "application/json" } }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it("keeps Overview available after a summary chunk failure and retries on the next sample", async () => {
  act(() => root.render(<><div data-testid="other-overview-section">Agents</div><OverviewMachineCards machines={[machine]} offlineText={() => "Offline"} /></>));
  await flush();
  const firstAttempts = state.attempts;

  expect(firstAttempts).toBeGreaterThan(0);
  expect(host.querySelector("[data-testid='other-overview-section']")?.textContent).toBe("Agents");
  expect(host.querySelector("a[href='/machines/mac']")?.textContent).toContain("Mac");
  expect(host.querySelector(".grid")?.querySelector(".grid")).toBeNull();
  expect(host.querySelector("[data-testid='recovered-summary']")).toBeNull();

  state.fail = false;
  sampleTime += 10;
  await act(async () => { vi.advanceTimersByTime(10_000); });
  await flush();
  expect(state.attempts).toBeGreaterThan(firstAttempts);
  expect(host.querySelector("[data-testid='recovered-summary']")?.textContent).toBe("Recovered monitoring");
});

it("retries a token-only failure when an identical later poll arrives", async () => {
  tokenOnly = true;
  act(() => root.render(<><div data-testid="other-overview-section">Agents</div><OverviewMachineCards machines={[machine]} offlineText={() => "Offline"} /></>));
  await flush();
  const firstAttempts = state.attempts;
  expect(firstAttempts).toBeGreaterThan(0);
  expect(host.querySelector("[data-testid='other-overview-section']")?.textContent).toBe("Agents");
  expect(host.querySelector("[data-testid='recovered-summary']")).toBeNull();

  // No latest.t and no usage.today change: only another successful load can signal a retry.
  state.fail = false;
  await act(async () => { vi.advanceTimersByTime(10_000); });
  await flush();
  expect(state.attempts).toBeGreaterThan(firstAttempts);
  expect(host.querySelector("[data-testid='recovered-summary']")?.textContent).toBe("Recovered monitoring");
});
