// @vitest-environment jsdom
/// <reference types="node" />
// File cards, document cards, and inline MediaPlayer (FileCards.tsx, lib/documents.ts, ui/Media.tsx) load on intent
// only when a message attaches files or an answer names file paths, keeping their code and startup namespaces off
// every cold first screen.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import startup from "../../i18n/startup-namespaces.json";
import { startupMatcher } from "../../i18n/startup-keys";
import { loadPages } from "../../lib/lazy-page";
import type { PathStat } from "../../lib/path-presence";
import { LazyFileCards, LazyReplyFiles } from "./LazyFileCards";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const stats: Record<string, PathStat> = {
  "/w/report.docx": { kind: "file", size: 2048 },
};
vi.mock("../../lib/path-presence", async (orig) => ({
  ...(await orig<typeof import("../../lib/path-presence")>()),
  usePathLinks: () => ({ stat: (p: string) => stats[p], key: "k" }),
}));

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
const staticFileCards = /^import\s+(?!type\b)[^;]*from\s+["']\.\/FileCards["']/m;
const staticDeferredDep = /^import\s+(?!type\b)[^;]*from\s+["']\.\.\/\.\.\/(?:lib\/documents|ui\/Media)["']/m;

describe("FileCards stays off the first screen", () => {
  it("keeps static imports of FileCards, documents, and Media off first-screen chat views and their namespaces out of startup", () => {
    for (const file of ["./SegmentView.tsx", "./Messages.tsx", "./SourceMessage.tsx"]) {
      const source = read(file);
      expect(source, file).not.toMatch(staticFileCards);
      expect(source, file).not.toMatch(staticDeferredDep);
      expect(source, file).toMatch(/from\s+["']\.\/LazyFileCards["']/);
    }

    const lazy = read("./LazyFileCards.tsx");
    expect(lazy).not.toMatch(staticFileCards);
    expect(lazy).not.toMatch(staticDeferredDep);
    expect(lazy).toContain('lazyPage(() => import("./FileCards").then((m) => m.FileCards))');
    expect(lazy).toContain('lazyPage(() => import("./FileCards").then((m) => m.ReplyFiles))');
    expect(lazy).not.toMatch(/lazyPage\([^)]*\.\/FileCards[^)]*idle\s*:\s*true/);

    expect(startup).not.toContain("lib-documents");
    expect(startup).not.toContain("ui-media");
    const inStartup = startupMatcher(startup);
    for (const key of ["lib-documents.word", "lib-documents.pdf", "lib-documents.other", "ui-media.video_unplayable", "ui-media.download"]) {
      expect(inStartup(key), key).toBe(false);
    }
  });
});

describe("LazyFileCards and LazyReplyFiles runtime loading", () => {
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

  it("renders nothing when there are no attached files or reply paths", async () => {
    await act(async () => {
      root.render(
        <>
          <LazyFileCards files={[]} mid="m1" />
          <LazyReplyFiles text="Plain reply with no file paths." mid="m1" cwd="/w" />
        </>,
      );
    });
    expect(host.innerHTML).toBe("");
  });

  it("loads FileCards and ReplyFiles on demand and renders them", async () => {
    await act(async () => {
      root.render(
        <>
          <LazyFileCards files={[{ kind: "file", path: "/u/spec.pdf", name: "spec.pdf", size: 128 }]} mid="m1" />
          <LazyReplyFiles text="Saved `/w/report.docx`." mid="m1" cwd="/w" />
        </>,
      );
    });
    await act(async () => {
      await loadPages();
    });
    expect(host.textContent).toContain("spec.pdf");
    expect(host.querySelector('[data-testid="document-card"]')?.textContent).toContain("report.docx");
  });
});
