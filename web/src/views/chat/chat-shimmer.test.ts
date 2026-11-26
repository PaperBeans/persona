import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("keeps currentColor available to paint the running chat labels", () => {
  const css = readFileSync(new URL("../../styles.css", import.meta.url), "utf8");
  const shimmer = /\.chat-shimmer\s*\{([^}]+)\}/.exec(css)?.[1] ?? "";
  expect(shimmer).toMatch(/background-image:\s*linear-gradient\([^;]*currentColor/);
  expect(shimmer).not.toMatch(/(?:^|;)\s*color\s*:\s*transparent\b/);
});
