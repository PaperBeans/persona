// A refusal fallback (#2289, #2528): the upstream refused a request and Claude Code answered it with another model on
// its own, keeping the session there for later turns (system/model_refusal_fallback, scope "session"). What the app
// shows of it, and the one action it offers: "back to the configured model", which closes the session's idle headless
// process so its next start uses the configured model (server/internal/node/model_restore.go). It sends no message and
// replays nothing; whether the configured model then runs is only known from a newer reply, so nothing here says it was
// restored, and the same request may be refused again.

import { api, ApiError, errorText, sessionPath } from "./api";
import { t } from "../i18n";
import { publicRefusal } from "./refusal-public";
import type { ModelRefusal, Session } from "./types";

/** The turn_error_code of a turn that failed on the missing fallback model: the user's to act on, never "Continue". */
export const FALLBACK_UNAVAILABLE = "model_fallback_unavailable";

export interface FallbackView {
  /** The model the refused request asked for: history, not the session's live configuration. */
  original: string;
  /** The model Claude Code moved to after the refusal. */
  fallback: string;
  /** The turn's error after the fallback, Claude Code's own words; "" none. */
  error: string;
  /** "Back to the configured model" was done: the next start asks for `target`. Not proof that it runs. */
  pending: boolean;
  /** The configured model the node read when it closed the process; "" none pending. */
  target: string;
}

/** What the card shows for a session, or null when it has no active refusal fallback. */
export function fallbackView(s: Pick<Session, "model_fallback" | "model_restore" | "turn_error">): FallbackView | null {
  const f = s.model_fallback;
  if (!f || f.subtype !== "model_refusal_fallback" || !f.fallback_model) return null;
  const pending = s.model_restore?.state === "next_start";
  return {
    original: f.original_model ?? "",
    fallback: f.fallback_model,
    error: s.turn_error ?? "",
    pending,
    target: pending ? (s.model_restore?.to ?? "") : "",
  };
}

/** What session.restore_model answers: what it did and the configured model it read, nothing about which model runs. */
export interface RestoreModelResult {
  restore: string;
  restore_to?: string;
  /** An idle process was closed (false: none was running; the next start uses the configured model all the same). */
  closed: boolean;
}

/** POST …/restore-model, no body: whatever the page could say is not sent. The card waits for the session's next push. */
export function restoreModel(mid: string, sid: string): Promise<RestoreModelResult> {
  return api<RestoreModelResult>(`${sessionPath(mid, sid)}/restore-model`, { method: "POST" });
}

/** A refusal of the restore in the page's language, by its code; the node's own words for anything else. */
export function restoreErrorText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "restore_busy":
        return t("chat-modelfallback.err_busy");
      case "restore_unsupported":
        return t("chat-modelfallback.err_unsupported");
      case "restore_nothing":
        return t("chat-modelfallback.err_nothing");
      case "restore_unknown":
        return t("chat-modelfallback.err_unknown");
      case "node_outdated":
        return t("chat-modelfallback.err_outdated");
    }
  }
  return errorText(e);
}

/** A refusal notice's facts in the order they are shown: the public record only (lib/refusal-public.ts), the refusal's
 *  words last and untouched. Whatever else an older hub sent (its "raw" line) is never read. */
export function refusalFacts(r: ModelRefusal): { original?: string; fallback?: string; category?: string; scope?: string; explanation?: string; text?: string } {
  const p = publicRefusal(r);
  return {
    original: p.original_model,
    fallback: p.fallback_model,
    category: p.category,
    scope: p.scope,
    explanation: p.explanation,
    text: p.text?.trim() ? p.text : undefined,
  };
}
