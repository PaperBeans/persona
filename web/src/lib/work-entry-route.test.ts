import { describe, expect, it } from "vitest";
import { tabOfSessionLink } from "./herdr-tree";
import { inWorkView, parse, routePath, withWorkView } from "./router";
import type { Session } from "./types";

const parseURL = (url: string) => {
  const [path, query] = url.split("?");
  return parse(path, query);
};

describe("work browser detail links", () => {
  it("round-trips the browser marker with either content mode and leaves old links unchanged", () => {
    for (const url of ["/s/m/s?view=chat&work=1", "/t/m/project/tab/s?view=term&work=1", "/s/m/s?work=1"]) {
      const r = parseURL(url);
      expect(inWorkView(r)).toBe(true);
      expect(routePath(r)).toBe(url);
    }
    for (const url of ["/s/m/s?view=term", "/t/m/project/tab/s", "/terminal/m/pane%3Aw1%3Ap2", "/terminal/m/s", "/a/s"]) {
      const r = parseURL(url);
      expect(inWorkView(r)).toBe(false);
      expect(routePath(r)).toBe(url);
    }
  });

  it("keeps the browser and content mode when a session URL resolves to its tab", () => {
    const r = parseURL("/s/m/s?view=term&work=1");
    if (r.name !== "session") throw new Error("Expected a session route");
    const s: Session = { machine_id: "m", session_id: "s", space_id: "project", slot_id: "tab", title: "", cwd: "", status: "working" };
    expect(routePath(withWorkView(tabOfSessionLink([s], r)!, inWorkView(r)))).toBe("/t/m/project/tab/s?view=term&work=1");
  });
});
