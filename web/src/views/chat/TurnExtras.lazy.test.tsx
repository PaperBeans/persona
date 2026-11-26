// @vitest-environment jsdom
/// <reference types="node" />
// TurnImages (+ edge-fade), PortChips (+ port-presence), and TurnChanges load on intent only when a turn actually
// carries inspected images, a finished agent reply with a host:port link, or edited files, keeping their code and
// startup namespaces off every cold first screen without touching IDLE_TIER_BUDGET.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import startup from "../../i18n/startup-namespaces.json";
import { startupMatcher } from "../../i18n/startup-keys";
import { loadPages } from "../../lib/lazy-page";
import { PREVIEW_FEATURE } from "../../lib/preview-link";
import { REVIEW_FEATURE } from "../../lib/types";
import { LazyReplyPorts, LazyTurnChanges, LazyTurnImages } from "./LazyTurnExtras";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../../lib/store", async (orig) => {
  const real = await orig<typeof import("../../lib/store")>();
  return {
    ...real,
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) =>
      select({
        ...real.getState(),
        features: [PREVIEW_FEATURE, REVIEW_FEATURE],
        machines: [{ id: "m1", name: "Workstation", hostname: "workstation.local", online: true } as never],
        sessions: { m1: [{ session_id: "s1", machine_id: "m1", cwd: "/w", assistant: false } as never] },
      }),
  };
});

vi.mock("../../lib/port-presence", async (orig) => ({
  ...(await orig<typeof import("../../lib/port-presence")>()),
  usePortStates: () => () => ({ kind: "live", title: "Preview App" }),
}));

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
const staticTurnExtras = /^import\s+(?!type\b)[^;]*from\s+["']\.\/(?:TurnImages|PortChips|TurnChanges)["']/m;
const staticDeferredDep = /^import\s+(?!type\b)[^;]*from\s+["'](?:\.\/edge-fade|\.\.\/\.\.\/lib\/port-presence)["']/m;

describe("TurnExtras stay off the first screen", () => {
  it("keeps static imports of TurnImages, PortChips, TurnChanges, and exclusive deps off SegmentView and their namespaces out of startup", () => {
    const seg = read("./SegmentView.tsx");
    expect(seg).not.toMatch(staticTurnExtras);
    expect(seg).not.toMatch(staticDeferredDep);
    expect(seg).toMatch(/from\s+["']\.\/LazyTurnExtras["']/);

    const lazy = read("./LazyTurnExtras.tsx");
    expect(lazy).not.toMatch(staticTurnExtras);
    expect(lazy).not.toMatch(staticDeferredDep);
    expect(lazy).toContain('lazyPage(() => import("./TurnImages").then((m) => m.TurnImages))');
    expect(lazy).toContain('lazyPage(() => import("./PortChips").then((m) => m.ReplyPorts))');
    expect(lazy).toContain('lazyPage(() => import("./TurnChanges").then((m) => m.TurnChanges))');
    expect(lazy).not.toMatch(/idle\s*:\s*true/);

    for (const ns of ["chat-turnimages", "chat-portchips", "chat-turnchanges"]) {
      expect(startup, ns).not.toContain(ns);
    }
    const inStartup = startupMatcher(startup);
    for (const key of [
      "chat-turnimages.source_read",
      "chat-turnimages.copy_path",
      "chat-portchips.stopped",
      "chat-portchips.open_hint",
      "chat-turnchanges.title",
      "chat-turnchanges.review",
    ]) {
      expect(inStartup(key), key).toBe(false);
    }
  });
});

describe("LazyTurnExtras runtime loading", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.stubGlobal("localStorage", { getItem: () => "tok" });
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  it("renders nothing when images, host:port links, and edited files are absent", async () => {
    await act(async () => {
      root.render(
        <>
          <LazyTurnImages images={[]} mid="m1" sid="s1" cwd="/w" />
          <LazyReplyPorts text="Plain reply without any localhost port." mid="m1" sid="s1" cwd="/w" />
          <LazyTurnChanges changes={{ files: [], add: 0, del: 0, exact: true }} mid="m1" sid="s1" cwd="/w" />
        </>,
      );
    });
    expect(host.innerHTML).toBe("");
  });

  it("loads TurnImages, PortChips, and TurnChanges on demand and renders them", async () => {
    await act(async () => {
      root.render(
        <>
          <LazyTurnImages
            images={[{ key: "img-1", path: "/w/shot.png", label: "shot.png", source: "read", width: 800, height: 600 }]}
            mid="m1"
            sid="s1"
            cwd="/w"
          />
          <LazyReplyPorts text="Listening on http://localhost:5173/demo" mid="m1" sid="s1" cwd="/w" />
          <LazyTurnChanges
            changes={{
              files: [{ path: "/w/src/app.ts", shown: "src/app.ts", add: 4, del: 1, exact: true }],
              add: 4,
              del: 1,
              exact: true,
            }}
            mid="m1"
            sid="s1"
            cwd="/w"
          />
        </>,
      );
    });
    await act(async () => {
      await loadPages();
    });
    expect(host.querySelector("[data-turn-images]")).not.toBeNull();
    expect(host.querySelector('[data-testid="port-chips"]')?.textContent).toContain(":5173");
    expect(host.querySelector("[data-turn-changes]")?.textContent).toContain("src/app.ts");
  });
});
