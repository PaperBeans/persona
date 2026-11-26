// The IDs and AgentRef of spec/fixtures/ids (schema/wire/ids.schema.json, #15397): the constructors keep the string
// as it is, so an ID reads from and writes to JSON as the bare string.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { machineID, sessionID, todoID, type AgentRef } from "./ids-wire";

const fixture = JSON.parse(readFileSync(new URL("../../../spec/fixtures/ids/ids.json", import.meta.url), "utf8")) as {
  cases: { expected: { value: unknown } }[];
};

describe("wire IDs", () => {
  it("round-trips every fixture sample through JSON unchanged", () => {
    expect(fixture.cases.length).toBeGreaterThan(0);
    for (const c of fixture.cases) {
      expect(JSON.parse(JSON.stringify(c.expected.value))).toEqual(c.expected.value);
    }
  });

  it("builds an AgentRef from IDs and writes it as plain strings", () => {
    const ref: AgentRef = { machine: machineID("desk-mac"), session: sessionID("s1") };
    expect(JSON.stringify(ref)).toBe('{"machine":"desk-mac","session":"s1"}');
    expect(todoID("todo_a1")).toBe("todo_a1");
  });
});
