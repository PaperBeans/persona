import { useEffect, type RefObject } from "react";
import { t } from "../../i18n";
import type { Interaction } from "../../lib/types";
import { Button, useNow } from "../../ui/atoms";

export function pendingCommandInput(interactions: readonly Interaction[], mid: string, sid: string, now: number) {
  return interactions.find((request) => {
    if (request.machine_id !== mid || request.session_id !== sid || request.kind !== "permission" || request.tool_name !== "command_input") return false;
    const expires = Date.parse(request.expires_at ?? "");
    return !Number.isFinite(expires) || expires > now;
  });
}

export function focusPendingCommand(dock: HTMLElement, id: string): boolean {
  const entry = Array.from(dock.querySelectorAll<HTMLElement>("[data-card]")).find((row) => row.dataset.card === id);
  const card = entry?.querySelector<HTMLElement>("[data-pending-card]");
  if (!card || card.closest("[hidden],[inert]")) return false;
  card.focus({ preventScroll: true });
  if (document.activeElement !== card) return false;
  const field = card.querySelector("input");
  if (field) field.scrollIntoView({ block: "center" });
  else card.scrollIntoView({ block: "nearest" });
  return true;
}

export function CommandWaitingNotice({ interactions, mid, sid, queueHeld, onAnswer, dock, focusId, keyboard, onFocused }: {
  interactions: readonly Interaction[];
  mid: string;
  sid: string;
  queueHeld: boolean;
  onAnswer: (id: string) => void;
  dock: RefObject<HTMLDivElement | null>;
  focusId?: string;
  keyboard: boolean;
  onFocused: (id: string) => void;
}) {
  const now = useNow(1000, interactions.some((request) => request.tool_name === "command_input"));
  const command = pendingCommandInput(interactions, mid, sid, now);
  useEffect(() => {
    const box = dock.current;
    if (!command || !box || command.id !== focusId || keyboard) return;
    let frame = 0;
    const schedule = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (!focusPendingCommand(box, command.id)) return;
        observer.disconnect();
        onFocused(command.id);
      });
    };
    // The existing card may still be loading or coming out of a fold.
    const observer = new MutationObserver(schedule);
    observer.observe(box, { childList: true, subtree: true, attributes: true, attributeFilter: ["inert", "hidden"] });
    schedule();
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [command, dock, focusId, keyboard, onFocused]);
  if (!command && !queueHeld) return null;
  return (
    <div data-command-waiting className="mb-2 flex flex-wrap items-center gap-x-2 rounded-card bg-inset px-3 py-1 text-meta text-ink-2">
      <div className="min-w-0 flex-1 basis-40 break-words" role="status">
        {command && <div>{t("chat-command-waiting.prompt")}</div>}
        {queueHeld && <div>{t("chat-command-waiting.queue")}</div>}
      </div>
      {command && <Button tone="quiet" className="touch-hit" onClick={() => onAnswer(command.id)}>{t("chat-command-waiting.answer")}</Button>}
    </div>
  );
}
