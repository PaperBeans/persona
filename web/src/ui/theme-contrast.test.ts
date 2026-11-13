/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
vi.mock("../styles.css?raw", () => ({ default: "" }));
import { contrast } from "./extension-theme";

// The default palette's text tokens must stay readable as small text (WCAG AA 4.5:1) on every background token
// (#2804); the same backgrounds extension themes are checked against (extension-theme.ts).
const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
const BACKGROUNDS = ["page", "canvas", "surface", "inset", "hover", "hover-2", "field"];
const TEXT = ["ink", "ink-2", "ink-3", "accent-ink", "attention", "green", "orange", "red", "ai", "source-terminal", "source-desktop"];

function tokens(scheme: "light" | "dark"): Record<string, string> {
  const block = (scheme === "light" ? /:root\s*\{([^}]+)\}/ : /\.dark\s*\{([^}]+)\}/).exec(css)?.[1] ?? "";
  return Object.fromEntries([...block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{3,8})\s*;/gi)].map((m) => [m[1], m[2]]));
}

describe("default palette contrast", () => {
  for (const scheme of ["light", "dark"] as const) {
    const t = tokens(scheme);
    it(`${scheme}: text tokens reach 4.5:1 on every background`, () => {
      const short = TEXT.flatMap((fg) => BACKGROUNDS.map((bg) => {
        expect(t[fg], `--${fg}`).toMatch(/^#[0-9a-f]{6}$/i);
        expect(t[bg], `--${bg}`).toMatch(/^#[0-9a-f]{3,6}$/i);
        const ratio = contrast(t[fg], t[bg]);
        return ratio < 4.5 ? `--${fg} on --${bg}: ${ratio.toFixed(2)}` : "";
      })).filter(Boolean);
      expect(short).toEqual([]);
    });
    it(`${scheme}: semantic tints are pale while error and selection fills stay neutral`, () => {
      const expected = scheme === "light"
        ? { green: "#e8f5ed", orange: "#fdf1e5", ai: "#efedfb" }
        : { green: "#3dbb7224", orange: "#f68f3c24", ai: "#9c94ff24" };
      for (const [role, value] of Object.entries(expected)) expect(t[`${role}-tint`]).toBe(value);
      expect(t["red-tint"]).toBe(t.inset);
      expect(t["accent-tint"]).toBe(t["hover-2"]);
      expect(contrast(t["danger-fill"], "#ffffff")).toBeGreaterThanOrEqual(4.5);
      expect(contrast(t["accent-ink"], t.surface)).toBeGreaterThanOrEqual(4.5);
    });
    it(`${scheme}: neutral and user bubbles keep body, quote, attachment and focus contrast at 4.5:1`, () => {
      for (const token of ["neutral-bubble", "user-bubble"]) {
        const bubble = [...css.matchAll(new RegExp(`--${token}:\\s*(#[0-9a-f]{6})`, "gi"))][scheme === "light" ? 0 : 1]?.[1] ?? "";
        expect(bubble).toMatch(/^#[0-9a-f]{6}$/i);
        if (token === "user-bubble") expect(bubble).toBe(scheme === "light" ? "#dceaff" : "#1d3557");
        for (const role of ["ink", "ink-2", "ink-3"]) {
          // The dark blue bubble locally strengthens muted labels without changing page text.
          const fg = token === "user-bubble" && scheme === "dark" && role === "ink-3" ? t["ink-2"] : t[role];
          expect(contrast(fg, bubble), `${role} on ${token}`).toBeGreaterThanOrEqual(4.5);
        }
        if (token === "user-bubble") expect(contrast(t.ink, bubble)).toBeGreaterThanOrEqual(4.5);
      }
      expect(css).toContain(".dark .bg-user-bubble { --ink-3: var(--ink-2); }");
      expect(css).toContain(".bg-user-bubble :is(button, a, summary):focus-visible { outline-color: var(--ink); --tw-ring-color: var(--ink); }");
    });
    it(`${scheme}: --danger-wash (red at 12%) keeps --ink readable on the canvas and the tray's surface (words on it are --ink; --red only colours icons)`, () => {
      expect(css).toContain("--danger-wash: color-mix(in srgb, var(--red) 12%, transparent)");
      const over = (full: string) => {
        const bg = full.length === 4 ? `#${[...full.slice(1)].map((c) => c + c).join("")}` : full;
        const [r, g, b] = [1, 3, 5].map((i) => Math.round(parseInt(t.red.slice(i, i + 2), 16) * 0.12 + parseInt(bg.slice(i, i + 2), 16) * 0.88));
        return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
      };
      for (const bg of ["canvas", "surface"]) expect(contrast(t.ink, over(t[bg])), `ink on the wash over ${bg}`).toBeGreaterThanOrEqual(4.5);
    });
    it(`${scheme}: --ink-3 stays a step quieter than --ink-2`, () => {
      expect(contrast(t["ink-3"], t.surface)).toBeLessThan(contrast(t["ink-2"], t.surface));
    });
  }
});
