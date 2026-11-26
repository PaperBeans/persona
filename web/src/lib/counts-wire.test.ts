// The counts views of spec/fixtures/counts/view_samples.json (schema/wire/counts.schema.json, #15512): each sample
// reads as the generated CountsView and writes back as the same JSON; a reason this app does not know is kept as it
// came, and a count a manager did not send stays absent rather than 0.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { countsRowKey, parseCountsHeldReason, type CountsView } from "./counts-wire";

const fixture = JSON.parse(
  readFileSync(new URL("../../../spec/fixtures/counts/view_samples.json", import.meta.url), "utf8"),
) as { cases: { name: string; expected: { view: CountsView } }[] };

describe("counts view wire", () => {
  it("writes every sample back unchanged", () => {
    expect(fixture.cases.length).toBeGreaterThan(0);
    for (const c of fixture.cases) {
      const view: CountsView = c.expected.view;
      expect(JSON.parse(JSON.stringify(view)), c.name).toEqual(c.expected.view);
    }
  });

  it("adds each sample's rows up to its number", () => {
    for (const c of fixture.cases) {
      const count = c.expected.view.counts.agents_needs_you;
      if (count === undefined) {
        expect(c.expected.view.totals?.agents_needs_you, c.name).toBeUndefined();
        continue;
      }
      expect((count.keys?.length ?? 0) + (count.more ?? 0), c.name).toBe(count.n);
      if (c.expected.view.totals !== undefined) {
        expect(c.expected.view.totals.agents_needs_you, c.name).toBe(count.n);
      }
    }
  });

  it("keeps a reason it does not know as it came", () => {
    expect(parseCountsHeldReason("muted")).toBe("muted");
    expect(parseCountsHeldReason("quiet")).toEqual({ unknown: "quiet" });
    expect(countsRowKey("card:ix-a")).toBe("card:ix-a");
  });
});
