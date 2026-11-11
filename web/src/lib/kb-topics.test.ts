import { describe, expect, it } from "vitest";
import { entryFromDetail, groupEntries, topicPointCount, type KBEntry } from "./kb";
import { groupPoints, pointDay, pointSources, pointWarns, type TopicPoint, pointKindLabel } from "./kb-points";

const entry = (id: string, extra: Partial<KBEntry> = {}): KBEntry => ({ kind: "topics", id, name: id, relevance: 0, ...extra });
const point = (id: string, path?: string[]): TopicPoint => ({ id, path, kind: "fact", text: `Point ${id}` });

describe("groupPoints", () => {
  it("keeps loose points first and makes one group per subtopic, in the order they first appear", () => {
    const g = groupPoints([point("a", ["Storage"]), point("b"), point("c", ["Search", "Cache"]), point("d", ["Storage"]), point("e", [" ", "Search"])]);
    expect(g.points.map((p) => p.id)).toEqual(["b"]);
    expect(g.groups.map((x) => x.path)).toEqual([["Storage"], ["Search"]]);
    expect(g.groups[0].points.map((p) => p.id)).toEqual(["a", "d"]);
    // a blank part is dropped, so "e" is under Search itself
    expect(g.groups[1].points.map((p) => p.id)).toEqual(["e"]);
    expect(g.groups[1].groups[0]).toMatchObject({ path: ["Search", "Cache"] });
  });

  it("skips points without text and takes a missing list", () => {
    expect(groupPoints([{ id: "x", kind: "fact", text: "" }]).points).toEqual([]);
    expect(groupPoints(undefined)).toEqual({ path: [], points: [], groups: [] });
  });
});

describe("knowledge points", () => {
  it("names a point's sources from the detail's links, once each, leaving out unknown ones", () => {
    const links = [{ kind: "projects", id: "alpha", name: "Alpha", relevance: 0 }];
    const p: TopicPoint = { id: "p", kind: "decision", text: "x", sources: [{ kind: "projects", id: "alpha" }, { kind: "projects", id: "alpha", day: "2026-11-02" }, { kind: "chats", id: "gone" }] };
    expect(pointSources(p, links).map((e) => e.name)).toEqual(["Alpha"]);
  });

  it("labels kinds, warns on pitfalls and open questions, and shows days short", () => {
    expect(pointKindLabel("pitfall")).not.toBe(pointKindLabel("something-new"));
    expect([pointWarns("pitfall"), pointWarns("question"), pointWarns("fact")]).toEqual([true, true, false]);
    expect(pointDay("2026-03-04", new Date(2026, 5, 1))).not.toContain("2026");
    expect(pointDay("2025-03-04", new Date(2026, 5, 1))).toContain("2025");
    expect(pointDay("soon")).toBe("soon");
  });

  it("counts a topic's points from its metadata or its list entry", () => {
    expect(topicPointCount(entry("a", { extra: "37" }))).toBe(37);
    expect(topicPointCount(entry("a"), { points: 4 })).toBe(4);
    expect(topicPointCount({ kind: "projects", extra: "short · active" })).toBeNull();
    expect(entryFromDetail(entry("a"), { kind: "topics", id: "a", path: "", meta: { name: "A", note: "About A", points: 9 } })).toMatchObject({ name: "A", one_line: "About A", extra: "9" });
  });

  it("lists topics in one section, importance set by hand first, then the most points", () => {
    const s = groupEntries("topics", [entry("few", { extra: "2" }), entry("many", { extra: "40" }), entry("pinned", { extra: "1", importance: "P1" })]);
    expect(s).toHaveLength(1);
    expect(s[0].items.map((e) => e.id)).toEqual(["pinned", "many", "few"]);
  });
});
