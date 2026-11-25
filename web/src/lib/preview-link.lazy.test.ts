/// <reference types="node" />
// Chat links, port chips and comment cards are on the first screen and need three small parts of dev server previews.
// Importing them from preview.ts put the whole panel's logic (frames, input, launch options) on every first screen.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import * as preview from "./preview";
import { addressOf, localhostLink, PREVIEW_FEATURE } from "./preview-link";

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
// The names a value import of lib/preview.ts (from any folder) takes; `import type` takes nothing to the bundle.
const fromPreview = (source: string) =>
  [...source.matchAll(/^import\s+(?!type\b)\{([^}]*)\}\s*from\s+["'](?:\.\/|(?:\.\.\/)+lib\/)preview["']/gm)].flatMap((m) =>
    m[1]!.split(",").map((name) => name.trim()).filter((name) => name && !name.startsWith("type ")),
  );
// What preview.ts re-exports from preview-link.ts: Rollup takes such a binding from preview-link.ts, so importing it
// through preview.ts leaves the panel's logic where it is.
const linkNames = ["PREVIEW_FEATURE", "addressOf", "localhostLink"];

describe("preview-link", () => {
  it("keeps preview.ts off the first screen", () => {
    for (const file of ["./links.ts", "../views/chat/ChatView.tsx", "../views/chat/PortChips.tsx", "../views/chat/CommentCard.tsx"]) {
      expect(fromPreview(read(file)).filter((name) => !linkNames.includes(name)), file).toEqual([]);
    }
    expect(read("./preview-link.ts")).not.toMatch(/from\s+["']\.\/preview["']/);
    expect(read("./preview.ts")).toMatch(/^export \{ PREVIEW_FEATURE, addressOf, localhostLink \} from "\.\/preview-link";$/m);
  });

  it("is what preview.ts offers the panel", () => {
    expect(preview.PREVIEW_FEATURE).toBe(PREVIEW_FEATURE);
    expect(preview.addressOf).toBe(addressOf);
    expect(preview.localhostLink).toBe(localhostLink);
    expect(PREVIEW_FEATURE).toBe("preview");
    expect(addressOf("http://localhost:5173/a?b#c", 5173)).toBe("/a?b#c");
    expect(localhostLink("localhost:3000")).toEqual({ port: 3000, path: "/" });
  });
});
