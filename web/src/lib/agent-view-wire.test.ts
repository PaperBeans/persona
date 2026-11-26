// The views of spec/fixtures/agent-view (schema/wire/agent-view.schema.json, #15397) read as the generated AgentView:
// a known phase, a reason of a known kind, and an unknown phase from a newer hub kept as it came.
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";

import { AGENT_PHASES, AGENT_REASON_KINDS, AgentViewWireDefects, parseAgentPhase, type AgentView } from "./agent-view-wire";

const fixture = JSON.parse(readFileSync(new URL("../../../spec/fixtures/agent-view/cases.json", import.meta.url), "utf8")) as {
  cases: { name: string; expected: { views: { id: string; view: AgentView }[] } }[];
};

afterEach(() => {
  AgentViewWireDefects.unknownEnum = null;
});

describe("agent views", () => {
  it("reads every fixture view as a known phase and reason", () => {
    const views = fixture.cases.flatMap((c) => c.expected.views);
    expect(views.length).toBeGreaterThan(0);
    for (const { id, view } of views) {
      expect(AGENT_PHASES, id).toContain(view.phase);
      expect(AGENT_REASON_KINDS, id).toContain(view.reason?.kind);
      expect(Number.isInteger(view.rank), id).toBe(true);
      expect(JSON.parse(JSON.stringify(view))).toEqual(view);
    }
  });

  it("keeps a phase from a newer hub as unknown and reports it", () => {
    const seen: string[] = [];
    AgentViewWireDefects.unknownEnum = (name, raw) => seen.push(`${name}=${raw}`);
    expect(parseAgentPhase("paused")).toEqual({ unknown: "paused" });
    expect(parseAgentPhase("error")).toBe("error");
    expect(seen).toEqual(["AgentPhase=paused"]);
  });
});
