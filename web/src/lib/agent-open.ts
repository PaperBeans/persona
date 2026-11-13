// Settings › Agent preferences › "How Agents open" (#5424): whether an Agent or a task opened from a conversation (its
// hand-off card, the dock, Needs you, In progress, the board, a link…) opens in the side panel beside the chat, the
// default, or as a page of its own (#5096, with "Came from … · click to return"). lib/handoff.ts asks it on every open,
// so a change applies to the next click; the Settings row (views/settings/AgentOpenSetting.tsx) shows it.
//
// Kept by this browser, not the account: the choice depends on the window it is made in (a wide desktop window has room
// for the panel, a phone-sized one covers the chat with it either way), as the other switches of that group are. Nothing
// is written until "Full page" is chosen, so a new browser opens the panel.

export type AgentOpen = "panel" | "page";

export const AGENT_OPEN_KEY = "agent.open";

/** How this browser opens an Agent or a task from a conversation. */
export function agentOpenMode(): AgentOpen {
  try {
    return localStorage.getItem(AGENT_OPEN_KEY) === "page" ? "page" : "panel";
  } catch {
    return "panel";
  }
}

export function setAgentOpenMode(mode: AgentOpen): void {
  try {
    if (mode === "page") localStorage.setItem(AGENT_OPEN_KEY, "page");
    else localStorage.removeItem(AGENT_OPEN_KEY);
  } catch {
    // storage unavailable: nothing is kept, the default (the side panel) stays
  }
}
