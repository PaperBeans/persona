import { describe, expect, it } from "vitest";
import { MAX_TURN_TOOL_VIEWS, turnToolViews } from "./turn-tool-views";
import type { Step } from "./turns";
import type { Item } from "./types";

const MAP_VIEW = {
  title: "Drive Home",
  sections: [{
    items: [{
      type: "map",
      alt: "Route map",
      markers: [
        { id: "o", lat: 39.9, lon: 116.4, label: "Office", kind: "origin" },
        { id: "d", lat: 39.98, lon: 116.32, label: "Home", kind: "destination" },
      ],
      routes: [{
        id: "r1",
        mode: "driving",
        duration_seconds: 1380,
        distance_meters: 12400,
        points: [{ lat: 39.9, lon: 116.4 }, { lat: 39.98, lon: 116.32 }],
      }],
    }],
  }],
};

const foldStep = (id: string, toolViews: NonNullable<NonNullable<Item["fold"]>["tool_views"]>): Step => ({
  kind: "fold",
  id,
  item: {
    id,
    seq: 2,
    role: "assistant",
    kind: "fold",
    fold: { items: 2, tools: 1, thoughts: 0, through_seq: 3, tool_views: toolViews },
  },
});

const toolStep = (id: string, callId: string, toolName: string, toolViews?: unknown[]): Step => {
  const use: Item = { id: `u:${id}`, seq: 4, role: "assistant", kind: "tool_use", tool_use_id: callId, tool_name: toolName };
  const result: Item = { id: `r:${id}`, seq: 5, role: "user", kind: "tool_result", tool_use_id: callId, tool_name: toolName, ...(toolViews ? { tool_views: toolViews } : {}) };
  return { kind: "tool", id, use, result, images: [] };
};

describe("turnToolViews", () => {
  it("extracts fold and expanded tool views in order and deduplicates overlapping ids", () => {
    const steps: Step[] = [
      foldStep("fold:1", [
        { item_id: "r:live-1", tool_use_id: "c1", tool: "mcp__amap__maps_direction_driving", view: MAP_VIEW },
      ]),
      toolStep("live-1", "c1", "mcp__amap__maps_direction_driving", [MAP_VIEW]),
      toolStep("live-2", "c2", "mcp__amap__maps_direction_transit", [{
        title: "Subway",
        sections: [{ items: [{ type: "text", text: "Line 10 -> Line 13" }] }],
      }]),
    ];
    const got = turnToolViews(steps, "m1", "s1");
    expect(got).toHaveLength(2);
    expect(got[0].view.title).toBe("Drive Home");
    expect(got[0].tool).toBe("Amap · Maps direction driving");
    expect(got[0].owner).toContain("chat-tool:m1/s1/");
    expect(got[1].view.title).toBe("Subway");
    expect(got[1].tool).toBe("Amap · Maps direction transit");
  });

  it("skips unreadable or empty views and enforces MAX_TURN_TOOL_VIEWS", () => {
    const badSteps: Step[] = [
      toolStep("bad-1", "b1", "Bash", [
        { sections: "nope" },
        { sections: [{ items: [{ type: "map", alt: "no geometry" }] }] },
        { title: "Empty", sections: [] },
      ]),
    ];
    expect(turnToolViews(badSteps)).toEqual([]);

    const many = Array.from({ length: MAX_TURN_TOOL_VIEWS + 4 }, (_, i) =>
      toolStep(`t${i}`, `c${i}`, "Bash", [{ title: `V${i}`, sections: [{ items: [{ type: "text", text: `Line ${i}` }] }] }]),
    );
    expect(turnToolViews(many)).toHaveLength(MAX_TURN_TOOL_VIEWS);
  });
});
