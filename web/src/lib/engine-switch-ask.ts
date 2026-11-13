// "Switch Agent?" before a pick that moves an Agent's conversation to the other engine (#5054, docs/web/web.md "Model
// pill and model panel"; after Cindy's agentSwitchConfirmation.ts and model-selector-unified.md 1.6). The question goes
// with a pick of a model whose engine is neither the one the session runs now nor the one a switch already waiting moves
// it to (a second pick inside the engine just accepted only changes the model, as in Cindy); a pick of the engine it runs
// never asks (it keeps what runs, or drops the waiting switch). "Don't remind me again" is a hidden override kept in this
// browser: the key absent means the question shows; ticked and confirmed, it is written; removing it brings the question
// back.
import { agentId } from "./agent-kind";
import { pendingEngine, sessionEngine } from "./session-models";
import type { Session } from "./types";

export const ENGINE_SWITCH_SKIP_KEY = "persona.engineSwitch.skipConfirm";

type Store = Pick<Storage, "getItem" | "setItem">;

/** The browser's localStorage, or none where it can't be reached (a sandboxed frame, a locked-down profile). */
function local(): Store | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

/** Whether "Don't remind me again" was ticked and confirmed in this browser. */
export function engineSwitchSkipped(store: Pick<Storage, "getItem"> | null = local()): boolean {
  try {
    return store?.getItem(ENGINE_SWITCH_SKIP_KEY) === "1";
  } catch {
    return false;
  }
}

/** Remembers "Don't remind me again" (ticked and confirmed). */
export function skipEngineSwitchQuestion(store: Store | null = local()): void {
  try {
    store?.setItem(ENGINE_SWITCH_SKIP_KEY, "1");
  } catch {
    // storage full or blocked: the question simply shows again next time
  }
}

/** Whether a pick of a launcher of `target` (an engine id) on `s` changes the engine it moves to, and so asks: not the
 *  engine it runs, nor the one a waiting switch already moves it to. */
export function engineSwitchAsks(s: Session, target: string): boolean {
  const to = agentId(target);
  return to !== sessionEngine(s) && to !== pendingEngine(s);
}

/** Whether the panel shows "Switch Agent?" for that pick in this browser. */
export function asksEngineSwitch(s: Session, target: string, store: Pick<Storage, "getItem"> | null = local()): boolean {
  return engineSwitchAsks(s, target) && !engineSwitchSkipped(store);
}
