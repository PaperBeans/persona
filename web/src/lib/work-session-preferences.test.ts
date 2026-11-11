import { describe, expect, it } from "vitest";
import { defaultWorkSessionView, readWorkSessionView, writeWorkSessionView } from "./work-session-preferences";

function storage() {
  const map = new Map<string, string>();
  return { getItem: (key: string) => map.get(key) ?? null, setItem: (key: string, value: string) => { map.set(key, value); } };
}

describe("work session view preferences", () => {
  it("uses flat recent order on first use and restores the saved independent settings", () => {
    const store = storage();
    expect(readWorkSessionView(store)).toEqual({ groupBy: [], sort: "recent", direction: "desc" });
    writeWorkSessionView({ groupBy: ["harness", "device", "project"], sort: "title", direction: "asc" }, store);
    expect(readWorkSessionView(store)).toEqual({ groupBy: ["harness", "device", "project"], sort: "title", direction: "asc" });
    writeWorkSessionView({ groupBy: [], sort: "title", direction: "asc" }, store);
    expect(readWorkSessionView(store)).toEqual({ groupBy: [], sort: "title", direction: "asc" });
    writeWorkSessionView(defaultWorkSessionView(), store);
    expect(readWorkSessionView(store)).toEqual({ groupBy: [], sort: "recent", direction: "desc" });
  });

  it("ignores stale dimensions and duplicates without changing the remaining hierarchy", () => {
    const getItem = () => '{"groupBy":["project","obsolete","harness","project","device"],"sort":"obsolete"}';
    expect(readWorkSessionView({ getItem })).toEqual({ groupBy: ["project", "harness", "device"], sort: "recent", direction: "desc" });
  });

  it("defaults safely with malformed or unavailable storage", () => {
    expect(readWorkSessionView({ getItem: () => "{" })).toEqual(defaultWorkSessionView());
    expect(readWorkSessionView({ getItem: () => { throw new Error("disabled"); } })).toEqual(defaultWorkSessionView());
    expect(() => writeWorkSessionView(defaultWorkSessionView(), { setItem: () => { throw new Error("disabled"); } })).not.toThrow();
  });
});
