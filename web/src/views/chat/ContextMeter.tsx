// A session's context & usage meter (#2430, moved from the header to the usage row under the composer in #5056,
// enabled for Assistant chats with lazy Session Insights popover in #12541): shows context window share when known
// (or the latest context size, then cumulative tokens from loaded items), and opens the lazy Session Insights popover
// on click. "Show details in Agent rows" does not gate it. Nothing renders while both window and token usage are unknown.
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { t } from "../../i18n";
import { contextHint } from "../../lib/context-notes";
import { contextLabel, contextTier, contextTooltip, contextUsage, currentContextTokens, type ContextTier, type ContextUsage } from "../../lib/context-usage";
import { bareModel } from "../../lib/model-change";
import { compactTokens } from "../../lib/tokens";
import { cx } from "../../ui/atoms";
import { ContextUsageBar } from "../../ui/ContextUsageBar";
import type { Item, Session } from "../../lib/types";

const SessionInsightsPopover = lazy(() => import("./SessionInsightsPopover").then((m) => ({ default: m.SessionInsightsPopover })));

const TIER_TEXT: Record<ContextTier, string> = { ok: "", warn: "font-medium text-orange", danger: "font-medium text-orange" };

const positive = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0);

/** Lightweight cumulative token total from loaded items, matching aggregateSessionTokens' per-field maxima for
 *  deduplicated `(id, bareModel, fast)` subagent reports so the L1 pill and lazy popover never disagree. */
function sessionTokenTotal(items?: readonly Item[]): number {
  if (!items?.length) return 0;
  let sum = 0;
  const reports = new Map<string, { inp: number; out: number; read: number; write: number }>();
  for (const it of items) {
    for (const list of [it.usage, it.fold?.usage]) {
      if (!list?.length) continue;
      for (const u of list) {
        const inp = positive(u.in);
        const out = positive(u.out);
        const read = positive(u.cache_read);
        const write = positive(u.cache_write);
        if (inp + out + read + write === 0) continue;
        if (u.id) {
          const model = u.model ? bareModel(u.model) : "";
          const key = `${u.id}\n${model}\n${u.fast ? 1 : 0}`;
          const prev = reports.get(key);
          if (prev) {
            prev.inp = Math.max(prev.inp, inp);
            prev.out = Math.max(prev.out, out);
            prev.read = Math.max(prev.read, read);
            prev.write = Math.max(prev.write, write);
          } else {
            reports.set(key, { inp, out, read, write });
          }
        } else {
          sum += inp + out + read + write;
        }
      }
    }
  }
  for (const r of reports.values()) sum += r.inp + r.out + r.read + r.write;
  return sum;
}

/** True when any loaded item carries positive token usage. */
export function hasSessionTokenUsage(items?: readonly Item[]): boolean {
  if (!items?.length) return false;
  for (const it of items) {
    for (const list of [it.usage, it.fold?.usage]) {
      if (!list?.length) continue;
      for (const u of list) {
        if (positive(u.in) + positive(u.out) + positive(u.cache_read) + positive(u.cache_write) > 0) return true;
      }
    }
  }
  return false;
}

export interface ContextMeterPopoverProps {
  anchor: RefObject<HTMLElement | null>;
  session: Session;
  items?: readonly Item[];
  assistant?: boolean;
  onCompact?: () => boolean | void | Promise<boolean | void | unknown>;
  compactBlocked?: boolean;
  onClose: () => void;
}

export interface ContextMeterProps {
  session: Session;
  compact?: boolean;
  items?: readonly Item[];
  assistant?: boolean;
  onCompact?: () => boolean | void | Promise<boolean | void | unknown>;
  compactBlocked?: boolean;
  /** Controlled popover open state; omitted uses internal toggle state. */
  open?: boolean;
  /** Called when the trigger toggles, the popover closes, or usage becomes unknown while open. */
  onOpenChange?: (open: boolean) => void;
  /** Custom popover renderer bypassing the default lazy `SessionInsightsPopover`. */
  renderPopover?: (props: ContextMeterPopoverProps) => ReactNode;
  /** Controlled context-window usage (`null` forces the token-only fallback). */
  usage?: ContextUsage | null;
  /** Controlled token total used when window usage is absent; skips scanning `items`. */
  tokens?: number;
}

export function ContextMeter({
  session,
  compact = false,
  items,
  assistant,
  onCompact,
  compactBlocked,
  open: controlledOpen,
  onOpenChange,
  renderPopover,
  usage: controlledUsage,
  tokens: controlledTokens,
}: ContextMeterProps) {
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [internalOpen, setInternalOpen] = useState(false);
  const isOpen = controlledOpen ?? internalOpen;
  const openRef = useRef(isOpen);
  openRef.current = isOpen;
  const onOpenChangeRef = useRef(onOpenChange);
  onOpenChangeRef.current = onOpenChange;
  const setOpenState = useCallback((next: boolean) => {
    if (controlledOpen === undefined) setInternalOpen(next);
    if (next !== openRef.current) onOpenChangeRef.current?.(next);
  }, [controlledOpen]);
  const close = useCallback(() => setOpenState(false), [setOpenState]);
  const usage = controlledUsage !== undefined ? controlledUsage : contextUsage(session);
  const current = currentContextTokens(session);
  const tokens = useMemo(
    () => (controlledTokens !== undefined ? positive(controlledTokens) : current || sessionTokenTotal(items)),
    [controlledTokens, current, items],
  );
  const known = usage !== null || tokens > 0;
  // close if the window and usage go unknown (e.g. a /clear) while open
  useEffect(() => {
    if (!known && isOpen) setOpenState(false);
  }, [known, isOpen, setOpenState]);
  if (!known) return null;

  const tier = usage ? contextTier(usage) : "ok";
  const tip = usage ? contextTooltip(usage) : t(controlledTokens === undefined && current ? "context.current_tokens" : "chat-runningstatusbar.token_count", { tokens: compactTokens(tokens) });
  const description = usage ? contextHint(usage) : undefined;
  const title = description ? [tip, description].join("\n") : tip;

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpenState(!isOpen)}
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        aria-label={tip}
        aria-description={description}
        title={isOpen ? undefined : title}
        className={cx("touch-hit -mx-1.5 flex shrink-0 items-center gap-1.5 rounded-chip px-1.5 text-submeta whitespace-nowrap text-ink-3 transition-colors", isOpen && "bg-hover")}
      >
        {usage ? (
          <>
            {!compact && <ContextUsageBar usage={usage} className="h-1 w-10" />}
            <span className={cx("tabular-nums", TIER_TEXT[tier])}>{contextLabel(usage)}</span>
          </>
        ) : (
          <span className="tabular-nums" data-context-tokens>{tip}</span>
        )}
      </button>
      {isOpen && (renderPopover ? (
        renderPopover({
          anchor: buttonRef,
          session,
          items,
          assistant,
          onCompact,
          compactBlocked,
          onClose: close,
        })
      ) : (
        <Suspense fallback={null}>
          <SessionInsightsPopover
            anchor={buttonRef}
            session={session}
            items={items}
            assistant={assistant}
            onCompact={onCompact}
            compactBlocked={compactBlocked}
            onClose={close}
          />
        </Suspense>
      ))}
    </>
  );
}
