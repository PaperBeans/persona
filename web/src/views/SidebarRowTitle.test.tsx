// An agent row behind a prefix keeps room for its title: the prefix gives way first, and a tab with several panes
// (endRoom) leaves its terminal mark to its pane rows.
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Session } from "../lib/types";

// the sidebar's modules read the colour scheme and this browser's settings when they load
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const { SessionRow } = await import("./Sidebar");

// a herdr pane: its source is "Terminal"
const s: Session = { machine_id: "mac", session_id: "s1", title: "整理发布说明", cwd: "/code", status: "idle", state: "idle", pane_id: "p1" };
const props = { s, k: "s:mac/s1", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {} };
const titleOf = (html: string) => html.match(/<span class="([^"]*)">整理发布说明<\/span>/)?.[1] ?? "";

describe("a pinned direction row's title beside long metadata", () => {
  it("keeps a nonzero minimum while launcher, counts, and time metadata truncate first", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} hint="Antigravity · Needs you 12 · 123 minutes ago" hintFull showState={false} keepTrailing onMenu={() => {}} />);
    expect(titleOf(html)).toContain("min-w-(--row-title-min)");
    const metadata = html.match(/<span class="([^"]*)">Antigravity · Needs you 12 · 123 minutes ago<\/span>/)?.[1] ?? "";
    expect(metadata).toContain("min-w-0");
    expect(metadata).toContain("shrink-[20]");
    expect(metadata).toContain("truncate");
    expect(metadata).not.toContain("shrink-0");
  });
  it("keeps the ordinary title basis when there is no trailing metadata", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} hintFull showState={false} keepTrailing onMenu={() => {}} />);
    expect(titleOf(html)).toContain("min-w-0");
    expect(titleOf(html)).not.toContain("--row-title-min");
  });
  it("keeps the same minimum on the title column when a draft adds a second line", () => {
    localStorage.setItem("draft:mac/s1", "unfinished");
    try {
      const html = renderToStaticMarkup(<SessionRow {...props} hint="Antigravity · Needs you 12 · 123 minutes ago" hintFull showState={false} keepTrailing onMenu={() => {}} />);
      expect(html).toContain('data-draft=""');
      const column = html.match(/<span class="([^"]*)"><span class="truncate text-\[13px\][^"]*">整理发布说明<\/span><span data-row-line="true" data-draft=""/)?.[1] ?? "";
      expect(column).toContain("min-w-(--row-title-min)");
      expect(column).not.toContain("min-w-0");
    } finally {
      localStorage.removeItem("draft:mac/s1");
    }
  });
});

describe("an agent row's title behind a prefix", () => {
  it("keeps its named minimum width, and the prefix may shrink", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} prefix="release" prefixClass="min-w-0 shrink truncate" />);
    expect(titleOf(html)).toContain("min-w-(--row-title-min)");
    expect(titleOf(html)).not.toContain("min-w-0");
  });

  it("without a prefix it is what it was", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} />);
    expect(titleOf(html)).toContain("min-w-0");
    expect(html).not.toContain("--row-title-min");
  });

  it("a tab with several panes leaves its 终端 mark to the rows of its panes", () => {
    expect(renderToStaticMarkup(<SessionRow {...props} prefix="release" />)).toContain('data-source="terminal"');
    expect(renderToStaticMarkup(<SessionRow {...props} prefix="release" endRoom />)).not.toContain("data-source=");
  });
});
