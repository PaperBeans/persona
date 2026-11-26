import { describe, expect, it } from "vitest";
import { conversationRoute, inboxConversationPage, spacePage } from "./conversation-page";
import type { Session } from "./types";

describe("a space conversation's page", () => {
  it("is its space's page, where its plan cards and questions show, not the bare agent chat", () => {
    const row = (space?: string): Record<string, Session[]> => ({ m1: [{ session_id: "c1", machine_id: "m1", assistant: true, space } as Session] });
    expect(conversationRoute("m1", "c1", row("zz-notes"), null)).toEqual({ name: "track", slug: "zz-notes", sid: "c1" });
    expect(conversationRoute("m1", "c1", row("health"), null)).toEqual({ name: "track", slug: "health", sid: "c1" });
    expect(spacePage("zz-notes", "c1")).toEqual({ name: "track", slug: "zz-notes", sid: "c1" });
    // an agent's conversation stays on /s/
    expect(conversationRoute("m1", "a1", { m1: [{ session_id: "a1", machine_id: "m1" } as Session] }, null)).toEqual({ name: "session", mid: "m1", sid: "a1" });
  });
});

describe("a conversation route with a known session row", () => {
  const inbox = { session_id: "c1", machine_id: "m1", assistant: true, archived: true, origin: "inbox" } as Session;
  const assistant = { machine_id: "m1", conversations: [] };

  it("uses an archived assistant summary even when the assistant's list omits it", () => {
    expect(conversationRoute("m1", "c1", {}, assistant, inbox)).toEqual({ name: "persona", sid: "c1" });
    expect(conversationRoute("m1", "c1", {}, assistant, { ...inbox, space: "notes" })).toEqual({ name: "track", slug: "notes", sid: "c1" });
  });

  it("names the computer of an archived Inbox summary on another computer", () => {
    expect(conversationRoute("m2", "c1", {}, assistant, { ...inbox, machine_id: "m2" })).toEqual({ name: "persona", sid: "c1", mid: "m2" });
  });

  it("names a separate handling computer without waiting for a roster or session row", () => {
    expect(inboxConversationPage("c1", "m2", undefined, "m1")).toEqual({ name: "persona", sid: "c1", mid: "m2" });
    expect(inboxConversationPage("c1", "m1", "work", "m1")).toEqual({ name: "persona", sid: "c1", mid: "m1", manager: "work" });
  });
});
