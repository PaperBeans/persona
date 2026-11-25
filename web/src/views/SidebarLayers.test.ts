import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The usage popover (UsagePopover.tsx) opens as a fixed overlay portaled to document.body at z-50 above both
// the sidebar's isolated rows column and the main workspace pane.
const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("sidebar layers", () => {
  it("keeps the sidebar rows column isolated and renders the usage popover as a fixed high-z overlay", () => {
    const rows = source("./Sidebar.tsx").match(/<div ref=\{nav\} data-sidebar-rows[^\n]*?className="([^"]*)"/);
    expect(rows?.[1].split(/\s+/)).toContain("isolate");
    const popover = source("./UsagePopover.tsx");
    expect(popover).toMatch(/role="dialog"[\s\S]*?className="[^"]*\bfixed\b[^"]*\bz-50\b/);
    expect(popover).toContain("createPortal(dialog, document.body)");
  });
});
