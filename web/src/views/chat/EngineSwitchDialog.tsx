// "Switch Agent?" (#5054, docs/web/web.md "Model pill and model panel"): what an Agent's model panel asks before a pick
// moves the conversation to the other engine (Claude Code ↔ Codex), in Cindy's confirmation layout and words
// (confirm-dialog.tsx, agentSwitchConfirmation.ts) drawn with Persona's Dialog and Button: the title, why a handoff may
// lose something, "Don't remind me again", then Switch Agent before Keep Current Agent. Keep Current Agent, Escape, the
// backdrop and Back (a phone's sheet) all keep things as they were. Which picks ask is lib/engine-switch-ask.ts.
import { useId, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { Button, Dialog } from "../../ui/atoms";

export interface EngineSwitchDialogContainerProps {
  open: boolean;
  onClose: () => void;
  width: number;
  labelledBy: string;
  describedBy: string;
  children: ReactNode;
}

export interface EngineSwitchDialogProps {
  open: boolean;
  /** Default combined answer callback: `(true, skip)` confirms, `(false, false)` keeps. */
  onAnswer?: (go: boolean, skip: boolean) => void;
  /** Controlled confirm callback; replaces `onAnswer(true, skip)` when provided. */
  onConfirm?: (skip: boolean) => void;
  /** Controlled keep/cancel callback; replaces `onAnswer(false, false)` when provided. */
  onCancel?: () => void;
  /** Controlled "Don't remind me again" checkbox value; preserves caller state across reopenings. */
  skip?: boolean;
  /** Initial value when `skip` is uncontrolled (defaults to `false`). */
  defaultSkip?: boolean;
  /** Called whenever the user toggles "Don't remind me again". */
  onSkipChange?: (skip: boolean) => void;
  /** Whether to show the "Don't remind me again" checkbox (defaults to `true`). */
  showSkip?: boolean;
  /** Disables controls and ignores confirm/cancel actions while a controlled switch is in flight. */
  busy?: boolean;
  /** Custom dialog host lets a controlled panel own portal, Escape, backdrop and history-back behavior. */
  renderDialog?: (props: EngineSwitchDialogContainerProps) => ReactNode;
}

/** `onAnswer(true, skip)`: switch, and `skip` when "Don't remind me again" was ticked; `onAnswer(false, false)`: keep. */
export function EngineSwitchDialog({
  open,
  onAnswer,
  onConfirm,
  onCancel,
  skip: controlledSkip,
  defaultSkip = false,
  onSkipChange,
  showSkip = true,
  busy = false,
  renderDialog,
}: EngineSwitchDialogProps) {
  const titleId = useId();
  const bodyId = useId();
  const isControlledSkip = controlledSkip !== undefined;
  const [internalSkip, setInternalSkip] = useState(defaultSkip);
  // each opening starts at defaultSkip when uncontrolled
  const [was, setWas] = useState(open);
  if (was !== open) {
    setWas(open);
    if (open && !isControlledSkip) setInternalSkip(defaultSkip);
  }
  const skip = showSkip ? (isControlledSkip ? controlledSkip : internalSkip) : false;
  const handleSkipChange = (checked: boolean) => {
    if (busy) return;
    if (!isControlledSkip) setInternalSkip(checked);
    onSkipChange?.(checked);
  };
  const confirm = () => {
    if (busy) return;
    if (onConfirm) onConfirm(skip);
    else onAnswer?.(true, skip);
  };
  const keep = () => {
    if (busy) return;
    if (onCancel) onCancel();
    else onAnswer?.(false, false);
  };
  // the Dialog body pads 8 px on top: 16 px all round
  const content = (
    <div data-engine-switch-dialog className="pt-2">
      <h2 id={titleId} className="text-[18px] leading-[28px] font-medium text-ink select-none">
        {t("chat-modelpanel.engine_dialog_title")}
      </h2>
      <p id={bodyId} className="mt-2 text-[16px] leading-[24px] text-ink-2 select-none">
        {t("chat-modelpanel.engine_dialog_body")}
      </p>
      {showSkip && (
        <label className="mt-4 flex cursor-pointer items-center gap-2 text-[13px] text-ink-2 select-none">
          <input
            type="checkbox"
            checked={skip}
            disabled={busy || undefined}
            onChange={(e) => handleSkipChange(e.target.checked)}
            className="size-3.5 shrink-0 cursor-pointer accent-ink"
          />
          {t("chat-modelpanel.engine_dialog_skip")}
        </label>
      )}
      <div className="mt-6 flex flex-wrap justify-end gap-2.5">
        <Button tone="primary" className="min-w-[96px]" disabled={busy || undefined} onClick={confirm}>
          {t("chat-modelpanel.engine_dialog_switch")}
        </Button>
        <Button className="min-w-[96px]" disabled={busy || undefined} onClick={keep}>
          {t("chat-modelpanel.engine_dialog_keep")}
        </Button>
      </div>
    </div>
  );
  if (renderDialog) {
    return <>{renderDialog({ open, onClose: keep, width: 400, labelledBy: titleId, describedBy: bodyId, children: content })}</>;
  }
  return (
    <Dialog open={open} onClose={keep} width={400} labelledBy={titleId} describedBy={bodyId}>
      {content}
    </Dialog>
  );
}
