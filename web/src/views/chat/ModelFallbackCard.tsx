// Above the composer while a session runs on the model Claude Code fell back to after an upstream refusal (#2528): the
// model the refused request asked for and the one Claude Code moved to (history, as the record says), the error since
// in Claude Code's own words, and the ways back. In a Persona conversation (headless) "Back to the configured model"
// closes the idle process so the next start uses it (sends nothing) and, in the main conversation, "New main chat"
// stays offered; a terminal session gets the manual step only. While a restore waits for a newer reply the card says
// only that the next start will use the configured model: whether it runs is for that reply to show.
import { useState } from "react";
import { fallbackView, restoreErrorText, restoreModel } from "../../lib/model-fallback";
import type { Session } from "../../lib/types";
import { Button } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

export function ModelFallbackCard({ session, headless, onNewMain }: { session: Session; headless: boolean; onNewMain?: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const v = fallbackView(session);
  if (!v) return null;
  const working = session.state === "working";
  const restore = async () => {
    setBusy(true);
    setError("");
    try {
      await restoreModel(session.machine_id, session.session_id);
      // the session's next push carries model_restore; nothing is claimed before it
    } catch (e) {
      setError(restoreErrorText(e));
    } finally {
      setBusy(false);
    }
  };
  const canRestore = headless && !v.pending;
  return (
    <div role="status" data-testid="model-fallback" className="flex flex-col gap-2 rounded-[12px] bg-orange-tint px-3 py-2 text-[12.5px] text-ink-2">
      <div className="flex items-start gap-2">
        <Icon name="warning" size={13} stroke={2.2} className="mt-[2px] shrink-0 text-orange" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="font-semibold text-ink">{v.pending ? t("chat-modelfallback.pending_title") : t("chat-modelfallback.title")}</span>
          {v.original && <span className="[overflow-wrap:anywhere]">{t("chat-modelfallback.original", { model: v.original })}</span>}
          <span className="[overflow-wrap:anywhere]">{t("chat-modelfallback.fallback", { model: v.fallback })}</span>
          {v.pending && v.target && <span className="text-ink [overflow-wrap:anywhere]">{t("chat-modelfallback.pending_target", { model: v.target })}</span>}
          {v.error && <span className="break-words whitespace-pre-wrap">{t("chat-modelfallback.error_after", { text: v.error })}</span>}
          {v.pending && <span>{t("chat-modelfallback.unverified")}</span>}
          {headless ? <span className="text-ink-3">{t("chat-modelfallback.no_promise")}</span> : <span className="text-ink-3">{t("chat-modelfallback.terminal_hint")}</span>}
          {error && (
            <span role="alert" className="text-red">
              {error}
            </span>
          )}
        </div>
      </div>
      {(canRestore || onNewMain) && (
        <div className="flex flex-wrap gap-2 pl-5">
          {canRestore && (
            <Button tone="primary" busy={busy} disabled={working || busy} onClick={() => void restore()}>
              {t("chat-modelfallback.restore")}
            </Button>
          )}
          {onNewMain && <Button onClick={onNewMain}>{t("chat-modelfallback.new_main")}</Button>}
        </div>
      )}
    </div>
  );
}
