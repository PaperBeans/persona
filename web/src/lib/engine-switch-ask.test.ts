// "Switch Agent?" (#5054, after Cindy's agentSwitchConfirmation.ts and model-selector-unified.md 1.6): which picks ask
// (another engine than the one running and the one a waiting switch already moves to; never the running engine), and
// "Don't remind me again" as a hidden override kept in this browser.
import { describe, expect, it } from "vitest";
import { asksEngineSwitch, ENGINE_SWITCH_SKIP_KEY, engineSwitchAsks, engineSwitchSkipped, skipEngineSwitchQuestion } from "./engine-switch-ask";
import type { Session } from "./types";

const session = (x: Partial<Session> = {}): Session =>
  ({ machine_id: "mac", session_id: "s1", title: "t", cwd: "/code", status: "idle", state: "idle", agent: "claude", launcher: "claude", model: "claude-opus-5-5", ...x }) as Session;

function memory(init: Record<string, string> = {}) {
  const m = new Map(Object.entries(init));
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
}

describe("which picks ask", () => {
  it("the other engine asks; the engine it runs never does", () => {
    expect(engineSwitchAsks(session(), "codex")).toBe(true);
    expect(engineSwitchAsks(session(), "claude")).toBe(false);
    expect(engineSwitchAsks(session({ agent: "codex", launcher: "cx" }), "claude")).toBe(true);
    expect(engineSwitchAsks(session({ agent: "codex", launcher: "cx" }), "codex")).toBe(false);
  });

  it("a second pick inside the engine a waiting switch moves to only changes the model: it doesn't ask again", () => {
    const waiting = session({ pending_switch: { launcher: "cx", agent: "codex" } });
    expect(engineSwitchAsks(waiting, "codex")).toBe(false);
    expect(engineSwitchAsks(waiting, "claude")).toBe(false);
    // a switch to another launcher of its own engine (#5035) is no engine change: the other engine still asks
    expect(engineSwitchAsks(session({ pending_switch: { launcher: "work" } }), "codex")).toBe(true);
    // the switch dropped (picking what runs): the next pick of the other engine asks again
    expect(engineSwitchAsks(session({ pending_switch: undefined }), "codex")).toBe(true);
  });

  it("\"Don't remind me again\": written once confirmed, read back, and the question gone while it is there", () => {
    const store = memory();
    expect(engineSwitchSkipped(store)).toBe(false);
    expect(asksEngineSwitch(session(), "codex", store)).toBe(true);
    skipEngineSwitchQuestion(store);
    expect(store.m.get(ENGINE_SWITCH_SKIP_KEY)).toBe("1");
    expect(ENGINE_SWITCH_SKIP_KEY).toBe("persona.engineSwitch.skipConfirm");
    expect(engineSwitchSkipped(store)).toBe(true);
    expect(asksEngineSwitch(session(), "codex", store)).toBe(false);
    // removing the key brings the question back
    store.m.delete(ENGINE_SWITCH_SKIP_KEY);
    expect(asksEngineSwitch(session(), "codex", store)).toBe(true);
  });

  it("storage that can't be read or written asks every time and throws nothing", () => {
    const broken = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("full"); } };
    expect(engineSwitchSkipped(broken)).toBe(false);
    expect(() => skipEngineSwitchQuestion(broken)).not.toThrow();
    expect(asksEngineSwitch(session(), "codex", null)).toBe(true);
  });
});
