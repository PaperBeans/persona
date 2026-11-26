// The account windows and error details behind a click on the sidebar's usage bars (#10082): these are not needed
// for the cold first screen, so the overlay, its CSS transitions, and its long strings load on intent.
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { createPortal } from "react-dom";
import { t as tr } from "../i18n";
import { sendDraft } from "../lib/drafts";
import { navigate } from "../lib/router";
import { getState } from "../lib/store";
import { balanceEmpty, balanceText } from "../lib/usage-balance";
import { useCloseOnBack } from "../lib/back-close";
import { accountPace } from "../lib/usage-pace";
import { groupByProvider, planNote, providerTitle, remainingText, sortedWindows, TIMELINE_CAP_MS, TIMELINE_TICKS_HOURS, timelinePosition, type ProviderGroup } from "../lib/usage-popover";
import { isWindowPaused, unifiedTimeline, type TimelineLane, type UnifiedTimelineRow } from "../lib/usage-timeline";
import { canonicalUsageProvider, isBalanceAccount, isPinnableUsageAccount, protectReserve, remainingPercent, remainingTone, resetLabel, resetPinnedUsageAccounts, sampleAge, selectSidebarUsageItems, sourceNames, togglePinnedUsageAccount, usageWindowPinId, usePinnedUsageAccounts, windowKey, type UnconnectedProvider, type UsageAccount, type UsageWindow } from "../lib/usage-windows";
import { Collapse, cx } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { reducedMotion } from "../ui/motion";
import { NARROW_QUERY, returnFocus, useMedia } from "../ui/overlay";
import { TONE_BG, TONE_TEXT } from "./UsageBars";
import "./usage-popover.css";

const percent = (p: number) => Math.min(100, Math.max(0, p));
const shown = (p: number) => `${Math.round(p)}%`;

/** Remaining-quota bar for the popover: fills left-to-right to `remaining%`, optional left-side `[0, reserve]`
 *  hatched exclusion zone + vertical line at `x = reserve`, and optional 1px tick at `expectedRemaining` (`100 - expectedUsed`). */
export function Meter({
  remaining,
  expectedRemaining,
  reserve,
  label,
  tall,
  barClass,
  reserveTop = true,
}: {
  remaining: number;
  expectedRemaining?: number;
  reserve?: number;
  label: string;
  tall?: boolean;
  barClass?: string;
  reserveTop?: boolean;
}) {
  const hasReserve = typeof reserve === "number" && Number.isFinite(reserve) && reserve >= 0 && reserve <= 100;
  const r = percent(remaining);
  const R = hasReserve ? percent(reserve) : 0;
  const flagAlign = R <= 18 ? "left-0" : R >= 82 ? "right-0" : "-translate-x-1/2";
  const flagStyle = R <= 18 || R >= 82 ? undefined : { left: `${R}%` };
  return (
    <div
      role="progressbar"
      aria-label={hasReserve ? `${label}, ${tr("usage.protect_label", { percent: Math.round(R) })}` : label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(r)}
      className={cx("relative w-full rounded-full bg-inset", tall ? "h-1.5" : "h-1", hasReserve && reserveTop && "mt-3.5")}
    >
      <div className={cx("h-full rounded-full transition-[width] duration-300", barClass ?? TONE_BG[remainingTone(r, R)])} style={{ width: `${r}%` }} />
      {hasReserve && (
        <>
          <span
            aria-hidden
            data-protect-zone
            className="pointer-events-none absolute inset-y-0 left-0"
            style={{ width: `${R}%`, borderTopLeftRadius: "9999px", borderBottomLeftRadius: "9999px", backgroundImage: "repeating-linear-gradient(135deg, transparent 0 2px, color-mix(in srgb, var(--orange) 45%, transparent) 2px 3px)" }}
          />
          <span aria-hidden data-protect-line className="pointer-events-none absolute -top-1 -bottom-1 w-0.5 -translate-x-1/2 rounded-full bg-orange" style={{ left: `${R}%` }} />
          <span aria-hidden data-protect-flag className={cx("pointer-events-none absolute -top-4 whitespace-nowrap rounded-sm bg-surface px-0.5 text-submeta font-bold leading-none text-orange", flagAlign)} style={flagStyle}>
            {tr("usage.protect_flag", { percent: Math.round(R) })}
          </span>
        </>
      )}
      {expectedRemaining !== undefined && <span aria-hidden data-pace-tick className="pointer-events-none absolute -top-0.5 -bottom-0.5 w-px -translate-x-1/2 rounded-full bg-ink-3" style={{ left: `${percent(expectedRemaining)}%` }} />}
    </div>
  );
}

/** Adaptive multi-column popover geometry constants. */
export const POPOVER_COL_WIDTH = 320;
export const POPOVER_COL_GAP = 12;
export const POPOVER_COL_STEP = POPOVER_COL_WIDTH + POPOVER_COL_GAP; // 332px
export const POPOVER_MAX_COLS = 3;
export const POPOVER_PAD_X = 24;
export const POPOVER_PAD_Y = 24;
export const POPOVER_GAP_PX = 8;
export const POPOVER_EDGE_PX = 16;
export const POPOVER_HYSTERESIS_PX = 24;
const CLOSE_MS = 160;
const REDUCED_CLOSE_MS = 120;

/** The least height a run of cards takes in `cols` CSS columns: cards keep their order (`break-inside: avoid`), so this
 *  is the smallest possible tallest column over every split of the sequence into at most `cols` contiguous runs. */
export function packedColumnHeight(cards: readonly number[], cols: number, gap = POPOVER_COL_GAP): number {
  const hs = cards.filter((h) => h > 0);
  const n = hs.length;
  if (!n) return 0;
  const c = Math.max(1, Math.min(cols, n));
  const prefix = [0];
  for (const h of hs) prefix.push(prefix[prefix.length - 1] + h);
  const run = (i: number, j: number) => prefix[j] - prefix[i] + (j - i - 1) * gap;
  let best = hs.map((_, j) => run(0, j + 1));
  for (let k = 2; k <= c; k++) {
    const next = best.slice();
    for (let j = 0; j < n; j++) {
      for (let i = 0; i < j; i++) next[j] = Math.min(next[j], Math.max(best[i], run(i + 1, j + 1)));
    }
    best = next;
  }
  return best[n - 1];
}

export interface UsagePopoverLayoutInput {
  /** Natural height when laid out in a single 320px column (including vertical padding); used when `cardHeights` is absent. */
  naturalHeight?: number;
  /** Measured height of each provider card (unsplittable, flowed in order into the columns). */
  cardHeights?: readonly number[];
  /** Total height of the full-width blocks (`column-span: all`: pinned bar, unified timeline, unconnected rows) with their gaps. */
  spanHeight?: number;
  viewportWidth: number;
  viewportHeight: number;
  anchorRect?: { left: number; top: number; right?: number; bottom?: number } | null;
  /** Previous settled column count for 24px hysteresis; omit or pass null on initial layout. */
  prevCols?: number | null;
  /** Number of provider cards; caps columns so multi-column never creates empty columns. */
  providerCount?: number;
}

export interface UsagePopoverLayout {
  cols: number;
  colWidth: number;
  width: number;
  left: number;
  bottom: number;
  availH: number;
  availW: number;
}

/** Pure height-first layout calculator: stays single-column (320px) whenever the content fits in availH; otherwise picks
 *  the fewest columns (at most 3 and `floor(availW / 332)`) whose height fits without a scrollbar: the full-width blocks
 *  plus the provider cards packed in order into the columns (`packedColumnHeight`). Without measured cards it falls back
 *  to `ceil(naturalHeight / availH)`. 24px hysteresis on later resizes keeps the column count from flickering. */
export function computeUsagePopoverLayout(input: UsagePopoverLayoutInput): UsagePopoverLayout {
  const vw = Math.max(240, input.viewportWidth);
  const vh = Math.max(160, input.viewportHeight);
  const anchor = input.anchorRect;
  const rawLeft = anchor ? Math.max(8, anchor.left) : POPOVER_EDGE_PX;
  const availW = Math.max(200, vw - POPOVER_EDGE_PX - rawLeft);
  const viewportAvailH = Math.max(120, vh - POPOVER_EDGE_PX * 2);

  let availH = viewportAvailH;
  let bottom = POPOVER_EDGE_PX;
  if (anchor) {
    const spaceAbove = anchor.top - POPOVER_GAP_PX - POPOVER_EDGE_PX;
    if (spaceAbove >= 120) {
      availH = Math.min(viewportAvailH, spaceAbove);
      bottom = Math.max(POPOVER_EDGE_PX, vh - anchor.top + POPOVER_GAP_PX);
    }
  }

  const cards = input.cardHeights?.filter((h) => h > 0);
  const cardCap = cards ? Math.max(1, cards.length) : input.providerCount;
  const maxPossibleCols = cardCap !== undefined ? Math.max(1, Math.min(POPOVER_MAX_COLS, cardCap)) : POPOVER_MAX_COLS;
  const rawMaxByWidth = Math.max(1, Math.min(maxPossibleCols, Math.floor(availW / POPOVER_COL_STEP)));
  const H = Math.max(0, input.naturalHeight ?? 0);
  const spans = Math.max(0, input.spanHeight ?? 0);
  /** Whether `c` columns hold everything within availH, less `slack`. */
  const fits = (c: number, slack = 0) =>
    cards ? POPOVER_PAD_Y + spans + packedColumnHeight(cards, c) <= availH - slack : H <= c * availH - slack;
  let rawNeeded = maxPossibleCols;
  for (let c = 1; c <= maxPossibleCols; c++) {
    if (fits(c)) {
      rawNeeded = c;
      break;
    }
  }
  const rawCols = Math.max(1, Math.min(rawNeeded, rawMaxByWidth));

  let cols = rawCols;
  if (input.prevCols != null) {
    const prev = Math.max(1, Math.min(maxPossibleCols, input.prevCols));
    if (rawCols === prev) {
      cols = prev;
    } else if (rawCols < prev) {
      if (prev > rawMaxByWidth) {
        // Viewport narrowed past the current column count: shrink immediately so we never clip horizontally.
        cols = rawMaxByWidth;
      } else {
        // Height decreased / availH grew: require 24px headroom before collapsing to fewer columns.
        cols = prev;
        for (let c = rawCols; c < prev; c++) {
          if (fits(c, POPOVER_HYSTERESIS_PX)) {
            cols = c;
            break;
          }
        }
      }
    } else {
      // Wants more columns: require 24px width hysteresis past the column step to avoid width-edge bounce.
      cols = prev;
      for (let c = rawCols; c > prev; c--) {
        if (!fits(c - 1) && availW >= c * POPOVER_COL_STEP + POPOVER_HYSTERESIS_PX) {
          cols = c;
          break;
        }
      }
    }
  }

  const idealWidth = cols * POPOVER_COL_WIDTH + (cols - 1) * POPOVER_COL_GAP + POPOVER_PAD_X;
  const width = Math.round(Math.min(availW, idealWidth));
  const innerW = Math.max(160, width - POPOVER_PAD_X);
  const colWidth = cols === 1
    ? Math.min(POPOVER_COL_WIDTH, innerW)
    : Math.max(160, Math.min(POPOVER_COL_WIDTH, Math.floor((innerW - (cols - 1) * POPOVER_COL_GAP) / cols)));
  const left = Math.round(Math.max(8, Math.min(rawLeft, vw - POPOVER_EDGE_PX - width)));

  return { cols, colWidth, width, left, bottom: Math.round(bottom), availH: Math.round(availH), availW: Math.round(availW) };
}

/** Provider brand dot color from the design system's --hue-* palette (no raw hex). */
const PROVIDER_HUE: Record<string, string> = {
  claude: "var(--hue-orange)",
  codex: "var(--hue-green)",
  openai: "var(--hue-green)",
  gemini: "var(--hue-blue)",
  antigravity: "var(--hue-blue)",
  deepseek: "var(--hue-indigo)",
  kimi: "var(--hue-purple)",
  cursor: "var(--hue-teal)",
  copilot: "var(--hue-indigo)",
  trae: "var(--hue-green)",
  grok: "var(--hue-gray)",
  xai: "var(--hue-gray)",
};

export function providerHue(provider: string): string {
  return PROVIDER_HUE[canonicalUsageProvider(provider)] ?? "var(--hue-gray)";
}

const HOUR_MS = 3600_000;
const DAY_HOURS = 24;
const CAP_HOURS = TIMELINE_CAP_MS / HOUR_MS;
/** Minimum center-to-center spacing for secondary ticks and clear pixel gap between adjacent label bounding boxes. */
export const TIMELINE_MIN_TICK_GAP_PX = 20;
const TIMELINE_MIN_LABEL_GAP_PX = 6;
const ALL_TIMELINE_TICKS: readonly number[] = [0, ...TIMELINE_TICKS_HOURS];
/** Interior ticks dropped in priority order when narrow width causes spacing or bounding-box collisions. */
const DROPPABLE_TIMELINE_TICKS = [6, 120, 12, 72, 24] as const;

function tickLabel(hours: number): string {
  if (hours === 0) return tr("usage.timeline_now");
  if (hours < DAY_HOURS) return tr("usage.tick_hours", { h: hours });
  return tr("usage.tick_days", { d: hours / DAY_HOURS });
}

/** Conservative rendered width estimate in px for a text-submeta (11.5px) tick label so collision checks hold across locales. */
function estimateTickWidth(hours: number): number {
  const text = tickLabel(hours);
  let width = 0;
  for (const ch of text) width += ch.charCodeAt(0) > 0x7f ? 12 : 7;
  if (hours === 0) width += 2; // Now is font-semibold
  return Math.max(14, width);
}

function tickBounds(hours: number, axisWidthPx: number, widthPx: number): [number, number] {
  if (hours === 0) return [0, widthPx];
  if (hours === CAP_HOURS) return [axisWidthPx - widthPx, axisWidthPx];
  const cxPos = timelinePosition(hours * HOUR_MS) * axisWidthPx;
  return [cxPos - widthPx / 2, cxPos + widthPx / 2];
}

/** Visible timeline tick hours for an axis width: enforces both >=20px tick spacing and non-overlapping label bounds,
 *  progressively dropping 6h, 5d, 12h, 3d, 1d so narrow widths never overlap or wrap. */
export function visibleTimelineTickHours(axisWidthPx: number | null, labelWidths?: Readonly<Record<number, number>>): number[] {
  const ticks = [...ALL_TIMELINE_TICKS];
  if (axisWidthPx === null || axisWidthPx <= 0) return ticks;
  const widthOf = (h: number) => labelWidths?.[h] ?? estimateTickWidth(h);
  const collides = (a: number, b: number) => {
    const centerGap = (timelinePosition(b * HOUR_MS) - timelinePosition(a * HOUR_MS)) * axisWidthPx;
    const [, aRight] = tickBounds(a, axisWidthPx, widthOf(a));
    const [bLeft] = tickBounds(b, axisWidthPx, widthOf(b));
    return centerGap < TIMELINE_MIN_TICK_GAP_PX || bLeft - aRight < TIMELINE_MIN_LABEL_GAP_PX;
  };
  for (const drop of DROPPABLE_TIMELINE_TICKS) {
    const idx = ticks.indexOf(drop);
    if (idx <= 0 || idx >= ticks.length - 1) continue;
    if (collides(ticks[idx - 1], ticks[idx]) || collides(ticks[idx], ticks[idx + 1])) {
      ticks.splice(idx, 1);
    }
  }
  return ticks;
}

/** Compact duration for narrow (<300px) cards: "45m", "1.5h", "2d 3h", "5d", or "Resets soon". */
export function compactResetText(resetsAt: string | undefined, now: number): string {
  const at = resetsAt ? Date.parse(resetsAt) : NaN;
  if (!Number.isFinite(at)) return "";
  const ms = at - now;
  if (ms <= 0) return tr("usage.due");
  const mins = Math.ceil(ms / 60_000);
  if (mins < 60) return tr("usage.tick_minutes", { m: Math.max(1, mins) });
  const hours = ms / HOUR_MS;
  if (hours < DAY_HOURS) return tr("usage.tick_hours", { h: Math.round(hours * 10) / 10 });
  const d = Math.floor(hours / DAY_HOURS);
  const h = Math.round(hours - d * DAY_HOURS);
  if (h >= DAY_HOURS) return tr("usage.tick_days", { d: d + 1 });
  return h === 0 ? tr("usage.tick_days", { d }) : tr("usage.tick_days_hours", { d, h });
}

function problemText(a: UsageAccount): string {
  switch (a.problem) {
    case "rejected":
      return tr("usage.problem_rejected");
    case "unreachable":
      return tr("usage.problem_unreachable");
    case "failed":
      return tr("usage.problem_failed");
    case "error":
      return tr("usage.problem_error");
    default:
      return "";
  }
}

/** Why a balance could not be read, or why the amount shown is the last one the manager had. */
function balanceProblemText(a: UsageAccount): string {
  if (!isBalanceAccount(a)) return "";
  switch (a.problem) {
    case "rejected":
      return a.balance ? tr("usage.balance_rejected_kept") : tr("usage.balance_rejected");
    case "unreachable":
      return a.balance ? tr("usage.balance_unreachable_kept") : tr("usage.balance_unreachable");
    case "failed":
      return a.balance ? tr("usage.balance_failed_kept") : tr("usage.balance_failed");
    default:
      return "";
  }
}

function connectHint(p: UnconnectedProvider): string {
  if (p.agent === "claude" && p.id === "claude") return tr("usage.connect_hint_claude");
  if (p.agent === "agy" || p.agent === "antigravity" || (p.agent === "codex" && p.id === "codex")) return tr("usage.connect_hint_pending", { name: p.title });
  return tr("usage.connect_hint_other");
}

function connectPrompt(p: UnconnectedProvider): string {
  if (p.agent === "claude" && p.id === "claude") return tr("usage.connect_prompt_claude", { id: p.id });
  if (p.agent === "agy" || p.agent === "antigravity") return tr("usage.connect_prompt_agy", { id: p.id });
  return tr("usage.connect_prompt_other", { name: p.title, id: p.id });
}

/** The body's blocks as the column choice needs them: each provider card's height (unsplittable) and the full-width
 *  blocks' total with the 12px gap each keeps. `natural`: the single-column height, the fallback without any card. */
function measureBody(bodyEl: HTMLElement | null, scrollEl: HTMLElement | null): { cards: number[]; spans: number; natural: number } {
  const cards: number[] = [];
  let spans = 0;
  if (bodyEl) {
    for (const child of Array.from(bodyEl.children) as HTMLElement[]) {
      const h = child.offsetHeight;
      if (h <= 0) continue;
      if (child.classList.contains("usage-popover-card")) cards.push(h);
      else spans += h + POPOVER_COL_GAP;
    }
  }
  const natural = cards.length || spans ? POPOVER_PAD_Y + spans + packedColumnHeight(cards, 1) : (scrollEl?.scrollHeight ?? 0);
  return { cards, spans, natural };
}

function sameLayout(a: UsagePopoverLayout, b: UsagePopoverLayout): boolean {
  return a.cols === b.cols && a.colWidth === b.colWidth && a.width === b.width && a.left === b.left && a.bottom === b.bottom && a.availH === b.availH && a.availW === b.availW;
}

function readViewport(): { width: number; height: number } {
  if (typeof window === "undefined") return { width: 1280, height: 800 };
  const vv = window.visualViewport;
  return {
    width: vv ? Math.min(window.innerWidth, vv.width) : window.innerWidth,
    height: vv ? Math.min(window.innerHeight, vv.height) : window.innerHeight,
  };
}

/** Every live account, window by window, unified reset timeline, plus active providers without usage data yet. */
export function UsagePopover({
  open = true,
  accounts,
  unconnected = [],
  anchor,
  now: fixed,
  onClose,
}: {
  open?: boolean;
  accounts: UsageAccount[];
  unconnected?: readonly UnconnectedProvider[];
  anchor?: HTMLElement | null;
  now?: number;
  onClose?: () => void;
}) {
  const [phase, setPhase] = useState<"open" | "closing" | "closed">(open ? "open" : "closed");
  const [prevOpen, setPrevOpen] = useState(open);
  const [tick, setTick] = useState(() => Date.now());
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) {
      setPhase("open");
      // Reopened: draw the countdowns from now, not from when it last closed.
      setTick(Date.now());
    } else if (phase === "open") setPhase("closing");
  }
  const dialogRef = useRef<HTMLDivElement>(null);
  const narrow = useMedia(NARROW_QUERY);
  const close = useCallback(() => onClose?.(), [onClose]);
  // On a narrow screen Back closes the popover before the drawer under it (lib/back-close.ts).
  useCloseOnBack(open, close, narrow);
  // Opened: the keyboard focus moves into the popover. Closed (Escape, Back, its trigger): it goes back to the trigger,
  // unless the close moved it elsewhere on purpose (a click on another control).
  useLayoutEffect(() => {
    const el = dialogRef.current;
    if (!el) return;
    if (open) {
      if (!el.contains(document.activeElement)) el.focus({ preventScroll: true });
    } else {
      returnFocus(el, anchor ?? null);
    }
  }, [open, anchor]);

  useEffect(() => {
    if (phase !== "closing") return;
    const ms = reducedMotion() ? REDUCED_CLOSE_MS : CLOSE_MS;
    const timer = setTimeout(() => setPhase("closed"), ms);
    return () => clearTimeout(timer);
  }, [phase]);

  const groups = groupByProvider(accounts);
  const pinnedIds = usePinnedUsageAccounts();
  const pinnedSet = new Set(pinnedIds);
  const { items: selectedPins, custom: hasLivePins } = selectSidebarUsageItems(accounts, pinnedIds);
  const livePinnedCount = hasLivePins ? selectedPins.length : 0;
  useEffect(() => {
    if (fixed !== undefined || phase !== "open") return;
    const t = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(t);
  }, [fixed, phase]);
  const now = fixed ?? tick;

  const scrollRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const prevColsRef = useRef<number | null>(null);
  const [ready, setReady] = useState(false);
  const [reflow, setReflow] = useState<"none" | "a" | "b">("none");
  const [scrollState, setScrollState] = useState({ scrollable: false, fadeTop: false, fadeBottom: false });
  const [layout, setLayout] = useState<UsagePopoverLayout>(() => {
    const vp = readViewport();
    const rect = anchor?.getBoundingClientRect();
    return computeUsagePopoverLayout({
      naturalHeight: 0,
      viewportWidth: vp.width,
      viewportHeight: vp.height,
      anchorRect: rect,
      prevCols: null,
      providerCount: groups.length || 1,
    });
  });

  const updateScrollEdges = useCallback((availH: number) => {
    const el = scrollRef.current;
    if (!el) return;
    const scrollable = el.scrollHeight > availH + 1;
    const fadeTop = scrollable && el.scrollTop > 1;
    const fadeBottom = scrollable && el.scrollTop + el.clientHeight < el.scrollHeight - 1;
    setScrollState((s) => (s.scrollable === scrollable && s.fadeTop === fadeTop && s.fadeBottom === fadeBottom ? s : { scrollable, fadeTop, fadeBottom }));
  }, []);

  const recompute = useCallback(() => {
    const vp = readViewport();
    const rect = anchor?.getBoundingClientRect();
    const measured = measureBody(bodyRef.current, scrollRef.current);
    const next = computeUsagePopoverLayout({
      naturalHeight: measured.natural,
      ...(measured.cards.length ? { cardHeights: measured.cards, spanHeight: measured.spans } : {}),
      viewportWidth: vp.width,
      viewportHeight: vp.height,
      anchorRect: rect,
      prevCols: prevColsRef.current,
      providerCount: groups.length || 1,
    });
    if (prevColsRef.current !== null && next.cols !== prevColsRef.current) {
      setReflow((r) => (r === "a" ? "b" : "a"));
    }
    prevColsRef.current = next.cols;
    setLayout((cur) => (sameLayout(cur, next) ? cur : next));
    updateScrollEdges(next.availH);
  }, [anchor, groups.length, updateScrollEdges]);

  useLayoutEffect(() => {
    if (phase !== "open") {
      prevColsRef.current = null;
      setReady(false);
      return;
    }
    recompute();
    const frame = typeof requestAnimationFrame === "function" ? requestAnimationFrame(() => setReady(true)) : 0;
    const onResize = () => recompute();
    window.addEventListener("resize", onResize);
    const vv = window.visualViewport;
    vv?.addEventListener("resize", onResize);
    let ro: ResizeObserver | undefined;
    if (typeof ResizeObserver !== "undefined" && bodyRef.current) {
      ro = new ResizeObserver(() => recompute());
      ro.observe(bodyRef.current);
    }
    return () => {
      if (frame && typeof cancelAnimationFrame === "function") cancelAnimationFrame(frame);
      window.removeEventListener("resize", onResize);
      vv?.removeEventListener("resize", onResize);
      ro?.disconnect();
    };
  }, [phase, recompute, accounts.length, unconnected.length, pinnedIds.length]);

  useLayoutEffect(() => {
    if (phase === "open") updateScrollEdges(layout.availH);
  }, [phase, layout, updateScrollEdges]);

  if (phase === "closed") return null;

  const askAssistant = (p: UnconnectedProvider) => {
    sendDraft("persona-new", { type: "replace", text: connectPrompt(p) });
    onClose?.();
    const onStarter = typeof location !== "undefined" && (location.pathname === "/persona/new" || (location.pathname === "/persona" && !getState().assistant?.main));
    if (!onStarter) navigate("/persona/new");
  };
  const bodyStyle = {
    "--usage-cols": String(layout.cols),
    "--usage-col-w": `${layout.colWidth}px`,
  } as CSSProperties;

  const dialog = (
    <div
      ref={dialogRef}
      role="dialog"
      aria-label={tr("usage.title")}
      tabIndex={-1}
      aria-hidden={phase === "closing" || undefined}
      inert={phase === "closing" || undefined}
      data-usage-popover={phase}
      data-state={phase}
      data-ready={ready ? "true" : undefined}
      data-cols={layout.cols}
      className="usage-popover fixed z-50 rounded-card bg-surface shadow-overlay outline-none"
      style={{ left: `${layout.left}px`, bottom: `${layout.bottom}px`, width: `${layout.width}px` }}
      onAnimationEnd={(e) => {
        if (e.target === e.currentTarget && phase === "closing") setPhase("closed");
      }}
    >
      <div
        ref={scrollRef}
        data-usage-scroll
        data-scrollable={scrollState.scrollable ? "true" : "false"}
        data-fade-top={scrollState.fadeTop ? "true" : "false"}
        data-fade-bottom={scrollState.fadeBottom ? "true" : "false"}
        className="usage-popover-scroll p-3"
        style={{ maxHeight: `${layout.availH}px` }}
        onScroll={() => updateScrollEdges(layout.availH)}
      >
        <div
          className="usage-popover-content"
          data-reflow={reflow !== "none" ? reflow : undefined}
          onAnimationEnd={(e) => {
            if (e.target === e.currentTarget && reflow !== "none") setReflow("none");
          }}
        >
          <div ref={bodyRef} className="usage-popover-body" data-cols={layout.cols} style={bodyStyle}>
            {pinnedIds.length > 0 && (accounts.length > 0 || unconnected.length > 0) && (
              <div data-usage-pinned-bar className="usage-popover-span usage-popover-pinned flex items-center justify-between gap-2 border-b border-line pb-2 text-submeta">
                <span className="min-w-0 truncate text-ink-3">
                  {livePinnedCount > 0 ? tr("usage.pinned_summary", { count: livePinnedCount }) : tr("usage.pinned_fallback")}
                </span>
                <button
                  type="button"
                  data-usage-reset-pins
                  onClick={() => resetPinnedUsageAccounts()}
                  className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline"
                >
                  {tr("usage.reset_pinned")}
                </button>
              </div>
            )}
            {groups.map((g) => {
              const hasWindows = g.accounts.some((a) => a.windows.length > 0);
              return (
                <section key={g.provider} data-usage-provider={g.provider} aria-label={providerTitle(g.provider)} className="usage-popover-card @container flex flex-col gap-3 rounded-control border border-line p-2.5">
                  <header className="flex flex-col gap-0.5">
                    <div className="flex items-center gap-2">
                      <span className="flex min-w-0 flex-1 items-center gap-1.5">
                        <span aria-hidden data-provider-dot={g.provider} className="size-2 shrink-0 rounded-full" style={{ background: providerHue(g.provider) }} />
                        <h3 className="truncate text-meta font-semibold text-ink">{providerTitle(g.provider)}</h3>
                        <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{tr("usage.provider_accounts", { count: g.accounts.length })}</span>
                      </span>
                      {g.sampledAt && <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{sampleAge(g.sampledAt, now)}</span>}
                    </div>
                    {hasWindows && (
                      <p data-usage-sort-subtitle className="text-submeta text-ink-3">
                        {tr("usage.card_sort_subtitle")}
                      </p>
                    )}
                  </header>
                  {g.accounts.map((a, i) => (
                    <div key={a.id} className={cx("flex flex-col", i > 0 && "border-t border-line pt-2.5")}>
                      <AccountSection a={a} now={now} pinnedSet={pinnedSet} />
                    </div>
                  ))}
                </section>
              );
            })}
            <UnifiedTimeline groups={groups} now={now} />
            {unconnected.map((p, i) => (
              <section key={`provider:${p.id}`} data-usage-unconnected-card={p.id} aria-label={p.title} className={cx("usage-popover-span flex flex-col gap-1 text-xs", (accounts.length > 0 || i > 0) && "usage-popover-unconnected border-t border-line pt-2.5")}>
                <div className="flex items-baseline gap-2">
                  <h3 className="min-w-0 flex-1 truncate font-medium text-ink">{p.title}</h3>
                  <button
                    type="button"
                    data-usage-connect={p.id}
                    onClick={() => askAssistant(p)}
                    className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline"
                  >
                    {tr("usage.connect_action")}
                  </button>
                </div>
                <div className="leading-snug text-ink-3">{connectHint(p)}</div>
              </section>
            ))}
          </div>
        </div>
      </div>
    </div>
  );

  return anchor && typeof document !== "undefined" ? createPortal(dialog, document.body) : dialog;
}

function PinToggle({
  pinned,
  label,
  onClick,
  pinAttr,
  windowAttr,
  iconSize = 12,
  className,
}: {
  pinned: boolean;
  label: string;
  onClick: () => void;
  pinAttr?: string;
  windowAttr?: string;
  iconSize?: number;
  className?: string;
}) {
  return (
    <button
      type="button"
      data-usage-pin={pinAttr}
      data-usage-pin-window={windowAttr}
      aria-pressed={pinned}
      aria-label={label}
      title={pinned ? tr("usage.unpin_title") : tr("usage.pin_title")}
      onClick={onClick}
      className={cx(
        "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-control transition-colors",
        pinned ? "bg-hover-2 text-accent-ink" : "text-ink-3 hover:bg-hover hover:text-ink",
        className,
      )}
    >
      <Icon name="pin" size={iconSize} />
    </button>
  );
}

function AccountSection({ a, now, pinnedSet }: { a: UsageAccount; now: number; pinnedSet: ReadonlySet<string> }) {
  const from = sourceNames(a);
  const plan = planNote(a);
  const pinned = pinnedSet.has(a.id);
  const showPin = isPinnableUsageAccount(a) || pinned;
  // A balance's failure is about the provider's balance route, not a usage gateway's stored windows (#11825).
  const problem = isBalanceAccount(a) ? balanceProblemText(a) : problemText(a);
  return (
    <section aria-label={a.name} data-usage-stale={a.stale ? "true" : undefined} className="flex flex-col gap-2">
      <header className="flex flex-col gap-0.5">
        <div className="flex items-center gap-1.5">
          <h3 className={cx("min-w-0 flex-1 truncate text-label font-medium", a.stale ? "text-ink-2" : "text-ink")}>{a.name}</h3>
          <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{a.stale ? tr("usage.stale_age", { age: sampleAge(a.sampled_at, now) }) : sampleAge(a.sampled_at, now)}</span>
          {showPin && (
            <PinToggle
              pinned={pinned}
              pinAttr={a.id}
              label={pinned ? tr("usage.unpin_account", { name: a.name }) : tr("usage.pin_account", { name: a.name })}
              onClick={() => togglePinnedUsageAccount(a.id)}
            />
          )}
        </div>
        {(from.length > 0 || plan) && (
          <div data-usage-source className="flex min-w-0 items-baseline gap-1.5 text-submeta text-ink-3">
            {plan && <span className="shrink-0 whitespace-nowrap">{plan}</span>}
            {plan && from.length > 0 && <span aria-hidden>·</span>}
            <span className="min-w-0 truncate" title={tr("usage.sources_title", { sources: from.join(" · ") })}>{from.join(" · ")}</span>
          </div>
        )}
        {problem && <div className="text-submeta leading-snug text-orange">{problem}</div>}
        {a.headline?.trim() && <div data-usage-headline className="text-submeta leading-snug text-ink-2">{a.headline.trim()}</div>}
        {a.notice?.trim() && <div data-usage-notice className="text-submeta leading-snug text-ink-3">{a.notice.trim()}</div>}
      </header>
      {a.balance && (
        <div data-usage-balance className="flex items-baseline justify-between gap-2 rounded-control bg-inset px-2.5 py-2">
          <span className="text-xs text-ink-3">{tr("usage.balance_label")}</span>
          <span className={cx("text-sm font-medium tabular-nums", a.stale ? "text-ink-2" : balanceEmpty(a.balance) ? "text-orange" : "text-ink")}>{balanceText(a.balance)}</span>
        </div>
      )}
      {!a.balance && isBalanceAccount(a) && <div data-usage-balance-unavailable className="text-xs text-orange">{tr("usage.balance_unavailable")}</div>}
      {sortedWindows(a).map((w) => {
        const pinId = usageWindowPinId(a.id, w);
        return <WindowRow key={windowKey(w)} name={a.name} w={w} stale={Boolean(a.stale)} now={now} pinId={pinId} pinned={pinnedSet.has(pinId)} />;
      })}
    </section>
  );
}

const TIMELINE_NAME_W = "calc(var(--spacing) * 22)";
const LANE_COLOR = { short: "var(--hue-teal)", long: "var(--hue-blue)" } as const;

/** Callback-ref width observer so re-mounting the timeline axis while the popover stays open rebinds ResizeObserver. */
function useAxisWidth(): [(el: HTMLDivElement | null) => void, number | null] {
  const [node, setNode] = useState<HTMLDivElement | null>(null);
  const [width, setWidth] = useState<number | null>(null);
  useEffect(() => {
    if (!node) {
      setWidth(null);
      return;
    }
    const measure = (w: number) => setWidth(w > 0 ? Math.round(w) : null);
    measure(node.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => {
      if (entry) measure(entry.contentRect.width);
    });
    ro.observe(node);
    return () => ro.disconnect();
  }, [node]);
  return [setNode, width];
}

function uniqueLaneLabels(rows: readonly UnifiedTimelineRow[], long: boolean): string[] {
  const out: string[] = [];
  for (const r of rows) {
    for (const l of r.lanes) {
      if (l.long === long && !out.includes(l.label)) out.push(l.label);
    }
  }
  return out;
}

function formatLaneSummary(l: TimelineLane, now: number): string {
  const paused = l.paused ? ` · ${tr("usage.paused")}` : "";
  if (l.models.length > 0) {
    const entries: string[] = [];
    if (l.planResetsAt) entries.push(`${l.label} · ${resetLabel(l.planResetsAt, now)}`);
    for (const m of l.models) entries.push(`${m.model} · ${resetLabel(m.resetsAt, now)}`);
    return `${l.label}${paused} (${entries.join(", ")})`;
  }
  return `${l.label}${paused} · ${resetLabel(l.resetsAt, now)}`;
}

/** Single aggregated reset timeline at the bottom of the popover (spanning all columns in multi-column layouts).
 *  Hidden completely when no account has any resettable window. */
export function UnifiedTimeline({ groups, now }: { groups: readonly ProviderGroup[]; now: number }) {
  const rows = unifiedTimeline(groups, now);
  const [axisRef, axisWidth] = useAxisWidth();
  if (!rows.length) return null;
  const ticks = visibleTimelineTickHours(axisWidth);
  const shortLabels = uniqueLaneLabels(rows, false);
  const longLabels = uniqueLaneLabels(rows, true);
  return (
    <div data-usage-timeline className="usage-popover-span border-t border-line pt-2.5" role="group" aria-label={tr("usage.timeline_title")}>
      <div className="mb-1.5 text-submeta text-ink-3">{tr("usage.timeline_title")}</div>
      <div ref={axisRef} data-timeline-axis className="relative h-4 whitespace-nowrap text-submeta leading-none text-ink-3" style={{ marginLeft: TIMELINE_NAME_W }} aria-hidden>
        {ticks.map((h) => {
          const isNow = h === 0;
          const isEnd = h === CAP_HOURS;
          return (
            <span
              key={h}
              data-timeline-tick={h}
              className={cx("absolute top-0 whitespace-nowrap", isNow ? "left-0 font-semibold text-ink" : isEnd ? "right-0" : "-translate-x-1/2")}
              style={isNow || isEnd ? undefined : { left: `${timelinePosition(h * HOUR_MS) * 100}%` }}
            >
              {tickLabel(h)}
            </span>
          );
        })}
      </div>
      <div className="relative" style={{ marginLeft: TIMELINE_NAME_W }}>
        {TIMELINE_TICKS_HOURS.map((h) => (
          <span key={h} aria-hidden className={cx("absolute inset-y-0 z-[1] w-px bg-line-strong", h !== DAY_HOURS && "opacity-60")} style={{ left: `${timelinePosition(h * HOUR_MS) * 100}%` }} />
        ))}
        <span aria-hidden data-timeline-now className="absolute inset-y-0 z-[3] w-0.5 rounded-full bg-ink" />
        {rows.map((r, i) => {
          const providerBreak = i > 0 && rows[i - 1].provider !== r.provider;
          const parts = r.lanes.map((l) => formatLaneSummary(l, now));
          const plan = planNote(r.account).split(" ")[0];
          const shaded = i % 2 === 0;
          return (
            <div
              key={r.account.id}
              data-timeline-row={r.account.id}
              data-timeline-provider={r.provider}
              data-timeline-paused={r.paused ? "true" : undefined}
              role="img"
              aria-label={`${r.account.name}: ${parts.join("; ")}`}
              title={parts.join("\n")}
              className="relative flex min-h-8 flex-col justify-center gap-0.5 py-1"
            >
              {providerBreak && (
                <span aria-hidden data-timeline-provider-divider className="absolute top-0 right-0 z-[2] border-t border-line" style={{ left: `calc(-1 * ${TIMELINE_NAME_W})` }} />
              )}
              {shaded && <span aria-hidden className="absolute inset-y-0 right-0 rounded-md bg-inset" style={{ left: `calc(-1 * ${TIMELINE_NAME_W})` }} />}
              <span className="absolute inset-y-0 right-full z-[1] flex flex-col justify-center pr-2 pl-1 text-submeta leading-tight text-ink-2" style={{ width: TIMELINE_NAME_W }}>
                <span className="flex min-w-0 items-center gap-1.5" title={r.account.name}>
                  <span aria-hidden data-timeline-provider-dot={r.provider} className="size-1.5 shrink-0 rounded-full" style={{ background: providerHue(r.provider) }} />
                  <span data-timeline-account-name className="min-w-0 truncate">{r.displayName}</span>
                </span>
                {plan && <small className="truncate pl-3 text-ink-3">{plan}</small>}
              </span>
              {r.lanes.map((l) => {
                const color = l.long ? LANE_COLOR.long : LANE_COLOR.short;
                const end = `${l.end * 100}%`;
                return (
                  <span key={l.key} data-timeline-lane={l.label} aria-hidden className="relative z-[2] block">
                    <span className="mx-1 grid" style={{ gridTemplateColumns: `${l.end}fr auto ${1 - l.end}fr` }}>
                      <span data-timeline-remaining className={cx("col-start-2 min-w-0 text-submeta font-semibold leading-none text-ink", shaded ? "bg-inset" : "bg-surface")}>
                        {remainingText(l.remainingMs)}
                      </span>
                    </span>
                    <span className={cx("relative block h-2.5", r.account.stale && "opacity-60")}>
                      <span className={cx("absolute left-0 top-1/2 -translate-y-1/2 rounded", l.long ? "h-2" : "h-1.5")} style={{ width: end, backgroundImage: `linear-gradient(90deg, transparent, ${color})` }} />
                      <span
                        data-timeline-dot={l.paused ? "paused" : "active"}
                        className="absolute top-0 size-2.5 -translate-x-1/2 rounded-full border-2 bg-surface"
                        style={l.paused ? { left: end, borderColor: "var(--orange)", background: "var(--orange)" } : { left: end, borderColor: color }}
                      />
                    </span>
                  </span>
                );
              })}
            </div>
          );
        })}
      </div>
      <div data-timeline-legend aria-hidden className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-submeta text-ink-3">
        {shortLabels.map((windowLabel) => (
          <span key={`short:${windowLabel}`} data-legend-item="short" className={cx("inline-flex max-w-full items-center gap-1.5", windowLabel.length <= 12 && "whitespace-nowrap")}>
            <span className="inline-block h-1 w-3 shrink-0 rounded-full" style={{ background: LANE_COLOR.short }} />
            <span>{tr("usage.legend_window", { window: windowLabel })}</span>
          </span>
        ))}
        {longLabels.map((windowLabel) => (
          <span key={`long:${windowLabel}`} data-legend-item="long" className={cx("inline-flex max-w-full items-center gap-1.5", windowLabel.length <= 12 && "whitespace-nowrap")}>
            <span className="inline-block h-1 w-3 shrink-0 rounded-full" style={{ background: LANE_COLOR.long }} />
            <span>{tr("usage.legend_window", { window: windowLabel })}</span>
          </span>
        ))}
        <span data-legend-item="now" className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <span className="inline-block h-1 w-3 shrink-0 rounded-full bg-ink" />
          <span>{tr("usage.timeline_now")}</span>
        </span>
        <span data-legend-item="protect" className="inline-flex items-center gap-1.5 whitespace-nowrap" title={tr("usage.legend_protect_title")}>
          <span className="inline-block h-1 w-3 shrink-0 rounded-full bg-orange" />
          <span>{tr("usage.legend_protect")}</span>
        </span>
      </div>
    </div>
  );
}

function WindowRow({ name, w, stale, now, pinId, pinned }: { name: string; w: UsageWindow; stale: boolean; now: number; pinId: string; pinned: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const p = accountPace({ stale }, w, now);
  const remaining = remainingPercent(w.used_percent);
  const reserve = protectReserve(w.protect_percent);
  const hasProtect = reserve !== null;
  const tone = remainingTone(remaining, reserve ?? 0);
  const paused = isWindowPaused(w);
  const expectedRemaining = p ? remainingPercent(p.expected) : undefined;
  const paceVerdict = p?.state === "over" ? tr("usage.pace_over") : "";
  const fullReset = resetLabel(w.resets_at, now);
  const shortReset = compactResetText(w.resets_at, now);
  const pausedLabel = paused ? tr("usage.paused") : "";
  const fullResetWithPaused = paused ? (fullReset ? `${pausedLabel} · ${fullReset}` : pausedLabel) : fullReset;
  const hasDetail = Boolean(paceVerdict || fullResetWithPaused);
  const meterBaseLabel = w.model
    ? tr("usage.window_model_remaining_label", { name, window: w.label, model: w.model, percent: shown(remaining) })
    : tr("usage.window_remaining_label", { name, window: w.label, percent: shown(remaining) });
  const meterLabel = paceVerdict ? `${meterBaseLabel}, ${paceVerdict}` : meterBaseLabel;
  const rowTitle = [w.label, shown(remaining), paceVerdict, fullResetWithPaused].filter(Boolean).join(" · ");
  const pinLabel = w.model
    ? (pinned ? tr("usage.unpin_window_model", { name, window: w.label, model: w.model }) : tr("usage.pin_window_model", { name, window: w.label, model: w.model }))
    : (pinned ? tr("usage.unpin_window", { name, window: w.label }) : tr("usage.pin_window", { name, window: w.label }));
  return (
    <div className="flex flex-col">
      {w.model && (
        <span data-usage-model className="mb-1 truncate text-submeta text-ink-3" title={w.model}>
          {w.model}
        </span>
      )}
      <div className="flex items-center gap-1">
        <button
          type="button"
          data-window-row={w.label}
          data-window-paused={paused ? "true" : undefined}
          aria-expanded={hasDetail ? expanded : undefined}
          aria-label={rowTitle}
          title={rowTitle}
          disabled={!hasDetail}
          onClick={() => hasDetail && setExpanded((v) => !v)}
          className={cx(
            "grid min-w-0 flex-1 grid-cols-[1.75rem_minmax(2.5rem,1fr)_auto_minmax(0,auto)_auto] items-center gap-x-1.5 rounded-sm text-left text-submeta [@media(pointer:coarse)]:min-h-11",
            hasProtect && "mt-3.5",
            hasDetail && "hover:bg-hover/60",
          )}
        >
          <span className="w-7 truncate text-ink-2">{w.label}</span>
          <span className={cx("block min-w-0", stale && "opacity-70")}>
            <Meter
              remaining={remaining}
              expectedRemaining={expectedRemaining}
              reserve={reserve ?? undefined}
              barClass={TONE_BG[tone]}
              reserveTop={false}
              label={meterLabel}
              tall
            />
          </span>
          <span data-usage-percent className={cx("shrink-0 whitespace-nowrap text-right font-medium tabular-nums", stale ? "text-ink-2" : TONE_TEXT[tone])}>
            {shown(remaining)}
          </span>
          {fullResetWithPaused ? (
            <span data-usage-reset data-usage-paused={paused ? "true" : undefined} className="min-w-0 truncate text-right text-ink-3" title={fullResetWithPaused}>
              {paused && <span className="font-medium text-red">{pausedLabel}</span>}
              {paused && fullReset && <span aria-hidden> · </span>}
              {fullReset && (
                <>
                  <span data-reset-full className="hidden @[300px]:inline">{fullReset}</span>
                  <span data-reset-short className="@[300px]:hidden">{shortReset}</span>
                </>
              )}
            </span>
          ) : (
            <span aria-hidden />
          )}
          {hasDetail ? (
            <span aria-hidden data-window-chevron className={cx("inline-flex shrink-0 items-center text-ink-3 transition-transform", expanded ? "rotate-180" : "-rotate-90")}>
              <Icon name="chevron" size={10} />
            </span>
          ) : (
            <span aria-hidden />
          )}
        </button>
        <PinToggle
          pinned={pinned}
          windowAttr={pinId}
          label={pinLabel}
          onClick={() => togglePinnedUsageAccount(pinId)}
          iconSize={11}
          className={hasProtect ? "mt-3.5" : undefined}
        />
      </div>
      {hasDetail && (
        <Collapse open={expanded} lazy>
          <div data-usage-pace-detail className="flex flex-wrap items-baseline justify-between gap-x-2 gap-y-0.5 pl-7 pt-1 text-submeta">
            {paceVerdict && <span className="whitespace-nowrap text-orange">{paceVerdict}</span>}
            {fullResetWithPaused && <span className={cx(paused && !fullReset ? "font-medium text-red" : "text-ink-3")}>{fullResetWithPaused}</span>}
          </div>
        </Collapse>
      )}
    </div>
  );
}
