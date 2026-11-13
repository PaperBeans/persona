// "Switch Agent?" (#5054, docs/web/web.md "Model pill and model panel"): what an Agent's model panel asks before a pick
// moves the conversation to the other engine (Claude Code ↔ Codex), in Cindy's confirmation layout and words
// (confirm-dialog.tsx, agentSwitchConfirmation.ts) drawn with Persona's Dialog and Button: the title, why a handoff may
// lose something, "Don't remind me again", then Switch Agent before Keep Current Agent. Keep Current Agent, Escape, the
// backdrop and Back (a phone's sheet) all keep things as they were. Which picks ask is lib/engine-switch-ask.ts.
import { useId, useState } from "react";
import { t } from "../../i18n";
import { Button, Dialog } from "../../ui/atoms";

/** `onAnswer(true, skip)`: switch, and `skip` when "Don't remind me again" was ticked; `onAnswer(false, false)`: keep. */
export function EngineSwitchDialog({ open, onAnswer }: { open: boolean; onAnswer: (go: boolean, skip: boolean) => void }) {
  const titleId = useId();
  const bodyId = useId();
  const [skip, setSkip] = useState(false);
  // each opening starts unticked, as Cindy's does
  const [was, setWas] = useState(open);
  if (was !== open) {
    setWas(open);
    if (open) setSkip(false);
  }
  const keep = () => onAnswer(false, false);
  return (
    <Dialog open={open} onClose={keep} width={400} labelledBy={titleId} describedBy={bodyId}>
      {/* the Dialog's body pads 8 px on top: 16 px all round, as Cindy's p-4 */}
      <div data-engine-switch-dialog className="pt-2">
        <h2 id={titleId} className="text-[18px] leading-[28px] font-medium text-ink select-none">
          {t("chat-modelpanel.engine_dialog_title")}
        </h2>
        <p id={bodyId} className="mt-2 text-[16px] leading-[24px] text-ink-2 select-none">
          {t("chat-modelpanel.engine_dialog_body")}
        </p>
        <label className="mt-4 flex cursor-pointer items-center gap-2 text-[13px] text-ink-2 select-none">
          <input type="checkbox" checked={skip} onChange={(e) => setSkip(e.target.checked)} className="size-3.5 shrink-0 cursor-pointer accent-ink" />
          {t("chat-modelpanel.engine_dialog_skip")}
        </label>
        <div className="mt-6 flex flex-wrap justify-end gap-2.5">
          <Button tone="primary" className="min-w-[96px]" onClick={() => onAnswer(true, skip)}>
            {t("chat-modelpanel.engine_dialog_switch")}
          </Button>
          <Button className="min-w-[96px]" onClick={keep}>
            {t("chat-modelpanel.engine_dialog_keep")}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
