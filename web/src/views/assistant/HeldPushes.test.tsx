// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { HeldPushesDialog, HeldPushesEntry } from "./HeldPushes";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const NOW = new Date("2026-11-14T04:00:00Z");
const at = "2026-11-14T04:00:00Z";
const yesterdayAt = "2026-11-13T04:00:00Z";
let host: HTMLDivElement;
let root: Root;
let requests: string[];
let mode = true;

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

beforeEach(async () => {
  await initI18n("en");
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  requests = [];
  mode = true;
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const path = String(url);
    requests.push(path);
    if (path.includes("/ln-mode")) return json({ enabled: mode });
    if (path.includes("/intercepts")) return json({ intercepts: [
      { at, level: "P1", reason: "ln_p1_held", title: "Review the release", also: ["Check the rollout"] },
      { at, level: "P2", reason: "ln_p2_silent", body: "Quiet update" },
      { at: yesterdayAt, level: "P1", reason: "ln_p1_held", title: "Yesterday" },
    ] });
    return json({});
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

async function renderEntry() {
  await act(async () => {
    root.render(<StrictMode><HeldPushesEntry className="entry" /></StrictMode>);
  });
}

describe("held-push entry", () => {
  it("is hidden while ln mode is off and does not request intercepts", async () => {
    mode = false;
    await renderEntry();
    expect(host.querySelector("[data-held-pushes-entry]")).toBeNull();
    expect(requests.filter((path) => path.includes("/intercepts"))).toHaveLength(0);
  });

  it("shows P1 first, keeps P2 folded, and retains merged summaries", async () => {
    await renderEntry();
    const entry = host.querySelector<HTMLButtonElement>("[data-held-pushes-entry]")!;
    expect(entry).not.toBeNull();
    expect(entry.textContent).toContain("2");
    expect(requests.some((path) => path.includes("/api/v1/api/v1/"))).toBe(false);
    expect(requests.some((path) => path.endsWith("/api/v1/push/ln-mode"))).toBe(true);
    expect(requests.some((path) => path.endsWith("/api/v1/push/intercepts"))).toBe(true);
    await act(async () => entry.click());
    expect(document.body.textContent).toContain("Review the release");
    expect(document.body.textContent).toContain("Check the rollout");
    expect(document.body.textContent).not.toContain("Quiet update");
    expect(document.body.textContent).not.toContain("Yesterday");
    const p2 = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.getAttribute("aria-expanded") === "false" && button.textContent?.includes("P2"))!;
    await act(async () => p2.click());
    expect(document.body.textContent).toContain("Quiet update");
  });

  it("shows an empty state when ln mode is on but nothing was held", async () => {
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const path = String(url);
      if (path.includes("/ln-mode")) return json({ enabled: true });
      if (path.includes("/intercepts")) return json({ intercepts: [] });
      return json({});
    }));
    await renderEntry();
    await act(async () => host.querySelector<HTMLButtonElement>("[data-held-pushes-entry]")!.click());
    expect(document.body.textContent).toContain("No pushes were held today.");
  });

  it("loads intercepts on demand when HeldPushesDialog opens and stays idle while closed", async () => {
    await act(async () => {
      root.render(<StrictMode><HeldPushesDialog open={false} onClose={() => {}} /></StrictMode>);
    });
    expect(requests).toEqual([]);
    await act(async () => {
      root.render(<StrictMode><HeldPushesDialog open onClose={() => {}} /></StrictMode>);
    });
    expect(requests.some((path) => path.endsWith("/api/v1/push/intercepts"))).toBe(true);
    expect(requests.some((path) => path.includes("/ln-mode"))).toBe(false);
    expect(document.body.textContent).toContain("Review the release");
  });

  it("isolates HeldPushesDialog across credential switches and ignores stale in-flight responses", async () => {
    setToken("cred-a");
    let resolveA!: (res: Response) => void;
    let rejectB!: (err: unknown) => void;
    let call = 0;
    vi.stubGlobal("fetch", vi.fn(() => {
      call += 1;
      if (call === 1) return new Promise<Response>((resolve) => { resolveA = resolve; });
      return new Promise<Response>((_resolve, reject) => { rejectB = reject; });
    }));
    await act(async () => {
      root.render(<HeldPushesDialog open onClose={() => {}} />);
    });
    // Switch credential A -> B while A's request is still in flight.
    await act(async () => {
      setToken("cred-b");
    });
    // A resolves late: must never appear under B.
    await act(async () => {
      resolveA(json({ intercepts: [{ at, level: "P1", reason: "ln_p1_held", title: "Stale Cred A Push" }] }));
    });
    expect(document.body.textContent).not.toContain("Stale Cred A Push");
    // B fails: dialog shows failure alert and zero records from A.
    await act(async () => {
      rejectB(new Error("offline"));
    });
    expect(document.body.querySelector('[role="alert"]')).not.toBeNull();
    expect(document.body.textContent).not.toContain("Stale Cred A Push");
  });
});
