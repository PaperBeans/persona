import { describe, expect, it } from "vitest";
import { policyInput, referenceDraft, referenceInput, emptyPolicyDraft } from "./draft";

describe("external source drafts", () => {
  it("uses the exact Inbox reference at each selected level", () => {
    const ref = {
      provider: "lark", channel: "2",
      container: { kind: "chat", id: "oc_example" }, item: { kind: "thread", id: "topic/AbC" },
    };
    const draft = referenceDraft(ref);
    expect(referenceInput(draft)).toEqual(ref);
    expect(referenceInput({ ...draft, scope: "container" })).toEqual({
      provider: "lark", channel: "2", container: ref.container,
    });
    expect(referenceInput({ ...draft, scope: "channel" })).toEqual({ provider: "lark", channel: "2" });
  });

  it("keeps mail's account-wide threads and requires an account and valid nodes", () => {
    const ref = { provider: "mail", channel: "mail-1", item: { kind: "thread", id: "Conversation/42" } };
    expect(referenceInput(referenceDraft(ref))).toEqual(ref);
    expect(referenceInput(referenceDraft({ ...ref, channel: "" }))).toBeNull();
    expect(referenceInput(referenceDraft({ ...ref, provider: "lark" }))).toBeNull();
    expect(referenceInput(referenceDraft({ provider: "github", item: { kind: "issue", id: "17" } }))).toBeNull();
  });

  it("omits inherited fields and preserves an explicit empty filter", () => {
    expect(policyInput(emptyPolicyDraft)).toEqual({});
    expect(policyInput({ ...emptyPolicyDraft, filter: "replace" })).toEqual({ pre_filter: {} });
    expect(policyInput({
      ...emptyPolicyDraft, mode: "digest", interval: "30", role: "owner", newItem: "auto_todo",
      filter: "replace", keywords: " release\n \nrelease\nreview ", mentions: "team", senders: "sender",
    })).toEqual({
      mode: "digest", digest_interval_minutes: 30, action_role: "owner", new_item_policy: "auto_todo",
      pre_filter: { keywords: ["release", "review"], mentions: ["team"], senders: ["sender"] },
    });
  });

  it("supports an interval override on inherited mode and excludes it from explicitly incompatible modes", () => {
    expect(policyInput({ ...emptyPolicyDraft, interval: "60" })).toEqual({ digest_interval_minutes: 60 });
    expect(policyInput({ ...emptyPolicyDraft, mode: "realtime", interval: "60" })).toEqual({ mode: "realtime" });
    expect(policyInput({ ...emptyPolicyDraft, mode: "digest", interval: "525601" })).toBeNull();
    expect(policyInput({ ...emptyPolicyDraft, mode: "digest", interval: "3.5" })).toBeNull();
    expect(policyInput({ ...emptyPolicyDraft, filter: "replace", keywords: "経".repeat(86) })).toBeNull();
  });
});
