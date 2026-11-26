import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { NoOtherLooks } from "./PersonaPicker";

// The avatar picker with no looks beyond the built-in ones (#3597): it says where to get one in words a user can follow, with
// Settings › Extensions as a link, never a file in the repository nor a path on the hub machine (docs/assistant/onboarding.md 4.5).
describe("the picker with no other looks", () => {
  const html = renderToStaticMarkup(<NoOtherLooks />);

  it("links to 设置 › 扩展", () => {
    expect(html).toContain('href="/settings?tab=extensions"');
    expect(html).toMatch(/<a [^>]*>设置 › 扩展<\/a>/);
    expect(html).not.toContain("~/.");
  });

  it("names no repository file", () => {
    expect(html).not.toMatch(/docs\/|\.md\b/);
  });
});
