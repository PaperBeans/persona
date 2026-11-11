// Where text picked in the link panel goes ("Add as context" / "AI edit"): the composer the user is writing in — the
// conversation on screen, or the one last typed in. With none on the page ("Overview", "Machines", "Settings"…), or one that can't send
// (a read-only source), it goes to Persona's main conversation (a new one when there is none): the page moves there and
// the panel stays open, as on the Mac.
import { addContext, editMessage, usableComposer, whenComposer, type ContextRef } from "../../lib/links";
import { conversationPage } from "../../lib/conversation-page";
import { shownManager } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { getState } from "../../lib/store";
import { toast } from "../../ui/atoms";
import { t } from "../../i18n";

/** Persona's main conversation (its composer's draft key and route), else a new conversation. */
function personaTarget(): { key: string; go: () => void } {
  const a = getState().assistant;
  if (a?.main) return { key: `${a.machine_id}/${a.main}`, go: () => navigate(conversationPage(a.main!, a.machine_id, shownManager())) };
  return { key: "persona-new", go: () => navigate({ name: "persona" }) };
}

/** Who gets it: "the current chat" when a composer is on the page, else Persona. */
export function handoffLabel(): string {
  return usableComposer() ? t("link-handoff.current_chat") : "Persona";
}

export function giveContext(ref: ContextRef) {
  const c = usableComposer();
  if (c) {
    addContext(c.key, ref);
    toast(t("link-handoff.added"), "ok");
    return;
  }
  const target = personaTarget();
  addContext(target.key, ref);
  target.go();
  toast(t("link-handoff.added_persona"), "ok");
}

/** "AI edit": sent at once. If it can't go (the conversation can't take messages now), it waits as a context instead. */
export function giveEdit(ref: ContextRef, ask: string) {
  const text = editMessage(ref, ask);
  const keep = (key: string) => {
    addContext(key, { ...ref, kind: "edit" });
    toast(t("link-handoff.send_failed"), "error");
  };
  const send = (c: { key: string; send: (text: string) => Promise<boolean> }) =>
    void c.send(text).then((ok) => {
      if (!ok) keep(c.key);
    });
  const c = usableComposer();
  if (c) return send(c);
  const target = personaTarget();
  target.go();
  whenComposer(target.key, send, () => keep(target.key));
}
