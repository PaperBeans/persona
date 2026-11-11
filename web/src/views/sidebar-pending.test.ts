import { describe, expect, it } from "vitest";
import type { AnsweredCard } from "../lib/store";
import type { Interaction, Session } from "../lib/types";
import { sidebarPending } from "./sidebar-pending";

const session = (machine_id: string, session_id = "session"): Session => ({ machine_id, session_id, title: "Example", cwd: "/work/example", status: "blocked" });
const card = (id: string, machine_id: string, session_id = "session"): Interaction => ({ id, machine_id, session_id, kind: "permission" });

describe("sidebarPending", () => {
  it("counts a card and its blocked session once, keeping multiple cards and the machine boundary", () => {
    const first = card("first", "one");
    const next = { ...card("next", "one"), created_at: "2026-01-01T00:00:00Z" };
    const rows = sidebarPending([session("one"), session("two")], [first, next, first], {});
    expect(rows.map((r) => ({ machine: r.machineId, cards: r.cards.map((ix) => ix.id), to: r.to }))).toEqual([
      { machine: "one", cards: ["next", "first"], to: { name: "session", mid: "one", sid: "session", view: "chat", work: true } },
      { machine: "two", cards: [], to: { name: "session", mid: "two", sid: "session", view: "chat", work: true } },
    ]);
  });

  it("keeps each sessionless request and an interaction whose session is not loaded", () => {
    const rows = sidebarPending([], [card("a", "one", ""), card("b", "one", ""), card("c", "two")], {});
    expect(rows.map((r) => r.to)).toEqual([
      { name: "request", id: "a" },
      { name: "request", id: "b" },
      { name: "session", mid: "two", sid: "session", view: "chat", work: true },
    ]);
  });

  it("removes an answered session before the next push, unless a new card or a hand-back keeps it waiting", () => {
    const s = session("one");
    const old: AnsweredCard = { ix: card("old", "one"), resolution: { allow: true } };
    expect(sidebarPending([s], [], { old })).toEqual([]);
    expect(sidebarPending([s], [card("new", "one")], { old })[0].cards[0].id).toBe("new");
    expect(sidebarPending([s], [], { old: { ...old, handedBack: true } })[0].cards).toEqual([]);
  });

  it("does not treat unread, working, offline or idle sessions as pending", () => {
    const sessions = (["unread", "working", "offline", "idle"] as const).map((state) => ({ ...session("one", state), state }));
    expect(sidebarPending(sessions, [], {})).toEqual([]);
  });
});
