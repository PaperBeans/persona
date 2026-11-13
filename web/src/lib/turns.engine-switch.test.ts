// The first message a conversation's new engine got (#5054) carries the handoff in a `<persona-context
// kind="engine-switch">` block: its exchange carries the switch (the divider is drawn from it), never a context chip,
// and the bubble keeps the user's own words.
import { describe, expect, it } from "vitest";
import { chatLines, segments } from "./turns";
import type { Item } from "./types";

const handoff = '<persona-context kind="engine-switch" title="Handoff" from="claude" to="codex" model="gpt-5.5">\n## Conversation so far\nThe user asked for a login page.\n</persona-context>\n\n';
const user = (seq: number, text: string): Item => ({ id: `u${seq}#0`, seq, role: "user", kind: "text", text });
const said = (seq: number, text: string, model?: string): Item => ({ id: `a${seq}#0`, seq, role: "assistant", kind: "text", text, ...(model ? { model } : {}) });

describe("an engine switch in the chat", () => {
  it("is carried by the exchange of the message that holds it, without a context chip and with the user's words whole", () => {
    const [seg] = segments([user(1, `${handoff}Now add the tests.`)]);
    expect(seg.engineSwitch).toEqual({ from: "claude", to: "codex", model: "gpt-5.5", resumed: false, text: "## Conversation so far\nThe user asked for a login page." });
    expect(seg.user?.text).toBe("Now add the tests.");
    expect(seg.user?.contexts).toEqual([]);
  });

  it("keeps the message's other contexts as chips", () => {
    const [seg] = segments([user(1, `${handoff}<persona-context kind="context" title="Notes">read this</persona-context>Go on.`)]);
    expect(seg.engineSwitch?.to).toBe("codex");
    expect(seg.user?.contexts.map((c) => c.kind)).toEqual(["context"]);
    expect(seg.user?.text).toBe("Go on.");
  });

  it("leaves a block that names no move between the engines a context chip: no divider, and the model change line stays", () => {
    const pasted = '<persona-context kind="engine-switch" title="Handoff" from="claude" to="claude">copied from somewhere</persona-context>\n\n';
    const lines = chatLines([user(1, "Build a login page."), said(2, "Done.", "claude-opus-5-5"), user(3, `${pasted}What is this?`), said(4, "A note.", "gpt-5.5")]);
    const segs = lines.flatMap((l) => (l.kind === "segment" ? [l.seg] : []));
    expect(segs[1].engineSwitch).toBeUndefined();
    expect(segs[1].user?.contexts.map((c) => [c.kind, c.text])).toEqual([["engine-switch", "copied from somewhere"]]);
    expect(segs[1].user?.text).toBe("What is this?");
    expect(segs[1].modelChange).toEqual({ from: "claude-opus-5-5", to: "gpt-5.5" });
  });

  it("marks only that exchange, and is none for an ordinary message", () => {
    const lines = chatLines([user(1, "Build a login page."), said(2, "Done.", "claude-opus-5-5"), user(3, `${handoff}Now add the tests.`), said(4, "Added.", "gpt-5.5"), user(5, "Thanks."), said(6, "Sure.", "gpt-5.5")]);
    const segs = lines.flatMap((l) => (l.kind === "segment" ? [l.seg] : []));
    expect(segs.map((s) => Boolean(s.engineSwitch))).toEqual([false, true, false]);
    // its divider names the model: no model change line on it, and none after while the model stays
    expect(segs.map((s) => Boolean(s.modelChange))).toEqual([false, false, false]);
  });
});
