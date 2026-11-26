import { describe, expect, it } from "vitest";
import { LABS_CATALOG, unseenLabs, visibleLabs } from "./labs-catalog";

describe("the Labs catalog", () => {
  it("only offers supported experiments and keeps the browser renderer available on older hubs", () => {
    expect(visibleLabs({})).toEqual(["terminal"]);
    expect(visibleLabs({ work_guide: false, todos_v2: true, integrations_v2: false })).toEqual(LABS_CATALOG);
    expect(unseenLabs({ todos_v2: false }, false, [])).toEqual(["todos_v2", "terminal"]);
  });

  it("excludes enabled, seen and pending experiments", () => {
    const settings = { work_guide: true, todos_v2: false, integrations_v2: false };
    expect(unseenLabs(settings, true, ["todos_v2", "retired_trial"])).toEqual(["integrations_v2"]);
    expect(unseenLabs(settings, false, ["terminal"], ["todos_v2", "integrations_v2"])).toEqual([]);
  });
});
