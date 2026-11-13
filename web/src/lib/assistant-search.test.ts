import { describe, expect, it, vi } from "vitest";

const calls = vi.hoisted(() => ({ paths: [] as string[], missing: true }));
vi.mock("./api", async (original) => {
  const real = await original<typeof import("./api")>();
  return {
    ...real,
    api: vi.fn(async (path: string) => {
      calls.paths.push(path);
      if (calls.missing) throw new real.ApiError(404, "not found", {});
      return { results: [], partial: false };
    }),
  };
});
import { markParts, mergeSearch, searchAssistantChats, type AssistantSearchAnswer } from "./assistant-search";

describe("markParts", () => {
  it("cuts a snippet at its matches", () => {
    expect(markParts("我想去青石巷 BlueLane 看看", [[7, 15]])).toEqual([["我想去青石巷 ", false], ["BlueLane", true], [" 看看", false]]);
    expect(markParts("ab ab", [[3, 5], [0, 2]])).toEqual([["ab", true], [" ", false], ["ab", true]]);
  });

  it("keeps ranges inside the snippet and in order", () => {
    expect(markParts("short", [[2, 99], [1, 3]])).toEqual([["s", false], ["ho", true], ["rt", true]]);
    expect(markParts("x", [])).toEqual([["x", false]]);
  });
});

describe("mergeSearch", () => {
  const chat = (session_id: string) => ({ session_id });
  const answer: AssistantSearchAnswer = {
    results: [
      { session_id: "b", machine_id: "m", title: "B", hits: [{ item_id: "i1", seq: 1, role: "user", snippet: "s", ranges: [[0, 1]] }] },
      { session_id: "z", machine_id: "m", title: "Z", archived: true, hits: [] },
    ],
    partial: false,
  };
  it("is the local matches until the manager answers", () => {
    expect(mergeSearch([chat("a")], undefined, [chat("a")], () => chat("?")).map((x) => x.chat.session_id)).toEqual(["a"]);
  });

  it("puts the manager's results first, then the local matches it did not return", () => {
    const made: string[] = [];
    const merged = mergeSearch([chat("a"), chat("b")], answer, [chat("a"), chat("b")], (r) => (made.push(r.session_id), chat(r.session_id)));
    expect(merged.map((x) => [x.chat.session_id, x.hit?.item_id])).toEqual([["b", "i1"], ["z", undefined], ["a", undefined]]);
    expect(made).toEqual(["z"]); // a chat the page does not list is made from the result
  });
});

describe("searchAssistantChats", () => {
  it("remembers a manager without the route and asks it no more", async () => {
    expect(await searchAssistantChats("trip")).toBeNull();
    calls.missing = false;
    expect(await searchAssistantChats("trip")).toBeNull();
    expect(calls.paths).toEqual(["/assistant/search?q=trip&limit=30"]);
  });
});
