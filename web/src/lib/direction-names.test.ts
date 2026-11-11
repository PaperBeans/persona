import { describe, expect, it } from "vitest";
import { directionLabel, directionNames } from "./direction-names";

describe("direction names", () => {
  it("keep the current leaders' names by id, leaving out ended ones and blank names", () => {
    const names = directionNames([
      { id: "a", name: "Product" },
      { id: "b", name: "  " },
      { id: "c", name: "Old", ended: true },
      { id: "", name: "Nobody" },
    ]);
    expect([...names]).toEqual([["a", "Product"]]);
    expect(directionNames(undefined).size).toBe(0);
  });

  it("label a row with its name, else its title", () => {
    const names = new Map([["a", "Product"]]);
    expect(directionLabel(names, "a", "1110 | explore | x")).toBe("Product");
    expect(directionLabel(names, "b", "Trip plan")).toBe("Trip plan");
  });
});
