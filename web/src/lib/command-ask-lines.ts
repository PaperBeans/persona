// What the screen card says of Claude Code's command prompt in plain words (#5266): the warning, Claude Code's own words
// under "Show original", the countdown. Only the lazy views/chat/CommandScreenCard.tsx reads them (chat-commandask, off the
// first screen's catalog).
import { t } from "../i18n";
import type { CommandAsk } from "./command-ask";

/** The warning in plain words; none without a warning. */
export function riskLine(ask: CommandAsk): string | undefined {
  if (!ask.warning) return undefined;
  return t(ask.risk === "delete" ? "chat-commandask.risk_delete" : "chat-commandask.risk_other");
}

/** Claude Code's own warning and countdown sentence, as on screen: under "Show original". */
export function originalWords(ask: CommandAsk): string | undefined {
  return [ask.warning, ask.deadline].filter(Boolean).join("\n") || undefined;
}

/** The countdown's line at `now` ("Denied automatically in 1:44"), or once it ran out what happens; null without one. */
export function countdownLine(ask: CommandAsk, now: number): string | null {
  if (ask.deadlineAt === undefined) return null;
  const left = Math.ceil((ask.deadlineAt - now) / 1000);
  if (left <= 0) return t(ask.deadlineAllows ? "chat-commandask.countdown_over_allow" : "chat-commandask.countdown_over_deny");
  const time = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
  return t(ask.deadlineAllows ? "chat-commandask.countdown_allow" : "chat-commandask.countdown_deny", { time });
}
