import { describe, expect, it } from "vitest";
import { lookupTab } from "./tab-lookup";
import type { Session } from "./types";

const row = (over: Partial<Session>) => ({ machine_id: "mac-2", session_id: "629d8f15", space_id: "w3", slot_id: "w3:t3", state: "idle", ...over }) as Session;

describe("lookupTab", () => {
  it("finds an idle tab the page's rows do not have", async () => {
    const asked: (string | undefined)[] = [];
    const read = async (_r: unknown, cursor?: string) => {
      asked.push(cursor);
      return cursor ? { sessions: [row({})] } : { sessions: [row({ session_id: "other", slot_id: "w3:t1" })], next_cursor: "c2" };
    };
    expect(await lookupTab({ mid: "mac-2", space: "w3", slot: "w3:t3" }, read)).toEqual({ state: "found", mid: "mac-2", sid: "629d8f15" });
    expect(asked).toEqual([undefined, "c2"]);
  });
  it("says missing only when the hub has no such tab", async () => {
    const read = async () => ({ sessions: [row({ slot_id: "w3:t1" })] });
    expect(await lookupTab({ mid: "mac-2", space: "w3", slot: "w3:t3" }, read)).toEqual({ state: "missing" });
    expect(await lookupTab({ mid: "mac-2", space: "w3", slot: "w3:t3" }, async () => ({ sessions: null }))).toEqual({ state: "missing" });
  });
  it("ignores rows of another machine", async () => {
    const read = async () => ({ sessions: [row({ machine_id: "local" })] });
    expect(await lookupTab({ mid: "mac-2", space: "w3", slot: "w3:t3" }, read)).toEqual({ state: "missing" });
  });
});
