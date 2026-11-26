// A question's option, as the question card and "Change answer" (Delegated.tsx) draw it.
import type { ReactNode } from "react";
import { optionDisplay } from "../../lib/tool-detail";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { RecommendedTag } from "../../ui/RecommendedTag";

export interface DigitHintProps {
  n: number;
  className?: string;
}

/** A digit the card's own keys pick (lib/card-keys.ts), drawn on an option — but only while the card holds keyboard
 *  focus, so a card at rest looks as it always did. Same look as a terminal dialog's key badge. */
export function DigitHint({ n, className }: DigitHintProps) {
  return <span aria-hidden data-key-hint className={cx("mt-[2px] flex h-4.5 w-4.5 shrink-0 items-center justify-center rounded-[5px] bg-field font-mono text-[10.5px] text-ink-2 shadow-hairline", className)}>{n}</span>;
}

export interface ChoiceLabelDisplay {
  text: string;
  recommended?: boolean;
}

export interface ChoiceProps {
  on: boolean;
  multi?: boolean;
  label: string;
  desc?: string;
  /** The assistant recommends it (its `delegate.recommend`, #544). */
  pick?: boolean;
  /** Explicit recommendation state overriding label-derived detection. */
  recommended?: boolean;
  /** Custom label parser replacing default `optionDisplay` (`lib/tool-detail`). */
  parseLabel?: (label: string) => ChoiceLabelDisplay;
  /** Custom recommendation badge renderer replacing default `RecommendedTag`. */
  renderRecommendedTag?: (kind: "option" | "assistant") => ReactNode;
  /** The digit that picks this option, while the card has keyboard focus. */
  hint?: number;
  /** Custom keyboard hint renderer replacing default `DigitHint`. */
  renderHint?: (hint: number) => ReactNode;
  disabled?: boolean;
  /** The option is one of the card's guarded answers (lib/tap-guard.ts, #8161). */
  marker?: boolean;
  className?: string;
  onClick?: () => void;
  /** Controlled selection toggle callback invoked with `!on` on activation. */
  onChange?: (next: boolean) => void;
}

export function Choice({
  on,
  multi,
  label,
  desc,
  pick,
  recommended,
  parseLabel,
  renderRecommendedTag,
  hint,
  renderHint,
  disabled = false,
  marker = false,
  className,
  onClick,
  onChange,
}: ChoiceProps) {
  const shown = parseLabel ? parseLabel(label) : optionDisplay(label);
  const isRecommended = recommended ?? Boolean(shown.recommended);
  const handleClick = () => {
    if (disabled) return;
    onClick?.();
    onChange?.(!on);
  };
  return (
    <button type="button" aria-pressed={on} disabled={disabled} onClick={handleClick} {...(marker ? { "data-tap-answer": "" } : {})} className={cx("touch-hit -mx-1.5 flex items-start gap-2 rounded-control px-1.5 py-1.5 text-left transition-colors duration-100 enabled:hover:bg-hover disabled:opacity-40", className)}>
      {hint !== undefined && (renderHint ? renderHint(hint) : <DigitHint n={hint} />)}
      <span
        className={cx(
          "mt-[2px] flex size-4 shrink-0 items-center justify-center transition-colors duration-200",
          multi ? "rounded-[5px]" : "rounded-full",
          on ? "bg-ink text-canvas" : "text-transparent shadow-[inset_0_0_0_1.5px_var(--line-strong)]",
        )}
      >
        {multi ? <Icon name="check" size={11} stroke={3} /> : <span className="size-1.5 rounded-full bg-canvas transition-transform duration-200" style={{ transform: on ? "scale(1)" : "scale(0)" }} />}
      </span>
      <span className="min-w-0">
        <span className={cx("block text-[13px] transition-colors", on ? "text-ink" : "text-ink-2")}>
          {shown.text}
          {isRecommended && (renderRecommendedTag ? renderRecommendedTag("option") : <RecommendedTag />)}
          {pick && (renderRecommendedTag ? renderRecommendedTag("assistant") : <RecommendedTag assistant />)}
        </span>
        {desc && <span className="block text-[12px] leading-snug text-ink-3">{desc}</span>}
      </span>
    </button>
  );
}
