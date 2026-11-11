// @vitest-environment jsdom
// Recently opened conversations (#2191) against the rules every client shares (spec/fixtures/recents): every case of
// open and shown, then this browser's list.
import { beforeEach, describe, expect, it } from "vitest";
import fixture from "../../../spec/fixtures/recents/recent_conversations.json";
import { loadRecentChats, MAX_RECENT_CHATS, noteChatShown, openChat, PALETTE_RECENT_CHATS, recentChatsKey, resetRecentChatsForTests, shownChats } from "./recent-chats";

type Case = { name: string; operation: string; input: Record<string, unknown>; expected: Record<string, unknown> };
const cases = fixture.cases as Case[];
const of = (operation: string) => cases.filter((c) => c.operation === operation);

describe("recent conversations (spec/fixtures/recents)", () => {
  it("has the fixture's limits and knows its operations", () => {
    expect(MAX_RECENT_CHATS).toBe(fixture.max_entries);
    expect(PALETTE_RECENT_CHATS).toBe(fixture.palette);
    expect(new Set(cases.map((c) => c.operation))).toEqual(new Set(["open", "shown"]));
  });

  it.each(of("open").map((c) => [c.name, c] as const))("open: %s", (_, c) => {
    expect(openChat(c.input.before as string[], c.input.opened as string)).toEqual(c.expected.after);
  });

  it.each(of("shown").map((c) => [c.name, c] as const))("shown: %s", (_, c) => {
    const existing = new Set(c.input.existing as string[]);
    expect(shownChats(c.input.entries as string[], c.input.current as string, (k) => existing.has(k), c.input.limit as number)).toEqual(c.expected.shown);
  });
});

describe("this browser's list", () => {
  beforeEach(() => {
    localStorage.clear();
    resetRecentChatsForTests();
  });

  it("keeps the keys opened, newest first, and which one is on the page", () => {
    noteChatShown("s:m1/a");
    noteChatShown("j:p1");
    noteChatShown("s:m1/a");
    expect(loadRecentChats()).toEqual(["s:m1/a", "j:p1"]);
    expect(JSON.parse(localStorage.getItem(recentChatsKey())!)).toEqual(["s:m1/a", "j:p1"]);
  });

  it("reads only conversation keys back: anything else in storage is dropped", () => {
    localStorage.setItem(recentChatsKey(), JSON.stringify(["s:m1/a", { title: "a secret" }, 42, "x:other", "j:p1"]));
    expect(loadRecentChats()).toEqual(["s:m1/a", "j:p1"]);
    localStorage.setItem(recentChatsKey(), "not json");
    expect(loadRecentChats()).toEqual([]);
  });
});
