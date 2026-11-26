// Genuine loaded user inputs for the edge navigator. The data-source adapter can replace this projection with
// the independent index without coupling the rail's gestures to transcript loading.

import { NAV_RAIL_METRICS } from "../ui/nav-rail-metrics";
import { lineItemIds } from "./chat-search";
import { splitInlineTrailer } from "./inline-mentions";
import { isLoopTick, isOwnMessage, parseUserText, type ChatLine, type Segment, type UserMessage } from "./turns";
import type { Item } from "./types";
import { hasInputExcerpt, inputEntry, type UserInputIndex, type UserInputPosition } from "./user-input-index";

/** One of your questions in the loaded conversation: what a tick knows. */
export interface NavRailTick {
  /** The line's id (a `seg:…` ChatLine): what a jump looks for in the scroller (`[data-line=…]`). */
  id: string;
  /** Optional loaded row owner and stable sequence from the independent server index. */
  lineId?: string;
  /** Owner in the retained live tail when the corresponding row is outside the reading window. */
  retainedLineId?: string;
  seq?: number;
  /** An unloaded input's ordinal, localized by the component rather than cached in its preview. */
  ordinal?: number;
  /** The question's first line worth showing (quote blocks skipped, trimmed, capped): the preview card and the label. */
  preview: string;
  /** A question with no words of its own (only pictures, files or picked contexts): how many, for a fallback label. */
  attachmentsOnly?: number;
  /** A bounded answer excerpt, or visible follow-up text in this exchange while the answer is not available. */
  answerExcerpt?: string;
  /** A compact position whose excerpt can be requested on demand. */
  loadPreview?: boolean;
}

/** Even a single loaded input is a useful position and selection target. */
export const NAV_RAIL_MIN_TICKS = 1;
export const NAV_RAIL_TICK_PITCH = NAV_RAIL_METRICS.tickPitch;
export const NAV_RAIL_TICK_MIN_PITCH = NAV_RAIL_METRICS.minTickPitch;
export const NAV_RAIL_MIN_HEIGHT = NAV_RAIL_METRICS.minHeight;
/** The card renders at most three excerpt lines. This also bounds the accessible description. */
export const NAV_RAIL_EXCERPT_MAX = 200;
/** The question line's cap for the label; the card truncates to one line, the label carries the whole preview. */
export const NAV_RAIL_PREVIEW_MAX = 120;

/** `text` cut to at most `max` UTF-16 units, never between the two units of a character (a cut text is read from its
 *  head, so this looks at one unit rather than splitting the whole text into characters). */
function cutUnits(text: string, max: number): string {
  if (text.length <= max) return text;
  const last = text.charCodeAt(max - 1);
  return text.slice(0, last >= 0xd800 && last <= 0xdbff ? max - 1 : max);
}

/** The questions of the loaded lines, oldest first: one tick per turn you wrote yourself. Persona's own messages,
 *  other sessions' and other conversations' (`isOwnMessage`), /loop wake-ups (`isLoopTick`) and lines that are not
 *  turns (notices, compactions, recaps, errors) never become ticks; a question with neither words nor attachments has
 *  nothing to show or jump to and is skipped. Only what is loaded counts: the rail never pages history in. */
export function navRailTicks(lines: ChatLine[]): NavRailTick[] {
  const out: NavRailTick[] = [];
  for (const line of lines) {
    if (line.kind !== "segment") continue;
    const { seg } = line;
    let read = ticksOf.get(line);
    if (!read || read.seg !== seg || read.user !== seg.user || read.answer !== seg.answer || read.steps !== seg.steps) {
      read = { seg, user: seg.user, answer: seg.answer, steps: seg.steps, tick: segmentTick(line) };
      ticksOf.set(line, read);
    }
    if (read.tick) out.push(read.tick);
  }
  return out;
}

/** Each segment line's tick (null: none), read once per line object. stabilizeLines hands an unchanged line's object
 *  on to the next push, so a push reads only the lines it changed (re-reading every answer was most of an items
 *  push's script at 200 turns). The parts the tick is read from are kept with it, so a line given another question,
 *  answer or steps is read again. The ticks are shared: read-only. */
const ticksOf = new WeakMap<ChatLine, { seg: Segment; user: Segment["user"]; answer: Segment["answer"]; steps: Segment["steps"]; tick: NavRailTick | null }>();

function segmentTick(line: Extract<ChatLine, { kind: "segment" }>): NavRailTick | null {
  const msg = line.seg.user;
  if (!msg || !isOwnMessage(msg) || isLoopTick(msg)) return null;
  const preview = navRailPreview(wordsOf(msg));
  const attachments = msg.images.length + (msg.files?.length ?? 0) + (msg.photos?.length ?? 0) + msg.contexts.length;
  if (!preview && !attachments) return null;
  const answer = responseExcerpt(line.seg);
  return { id: line.id, preview, ...(preview ? {} : { attachmentsOnly: attachments }), ...(answer ? { answerExcerpt: answer } : {}) };
}

/** Prefer the answer, then visible text from this exchange only. Never inspect tool payloads, private reasoning,
 *  injected wake text or another session to manufacture a hover summary. */
function responseExcerpt(seg: Segment): string {
  const answer = navRailExcerpt(seg.answer?.text ?? "");
  if (answer) return answer;
  for (const step of seg.steps) {
    if (step.kind !== "text" && step.kind !== "notice") continue;
    const item = step.item;
    if (item.role !== "assistant" || item.from || item.injected || item.wake || item.supervisionUser) continue;
    const text = navRailExcerpt(item.text ?? "");
    if (text) return text;
  }
  return "";
}

/** A merged bubble (several queued messages in one turn) previews its parts in order; the first line of the first
 *  part that has one is the question's face. */
function wordsOf(msg: UserMessage): string {
  return msg.parts ? msg.parts.join("\n") : msg.text;
}

/** The question's first own line: quote blocks (`> `, drawn as cards by Quote.tsx) are skipped for your words above
 *  them, and a message that is all quote falls back to the quote's first line. */
export function navRailPreview(text: string): string {
  const lines = text.split("\n");
  const own = lines.find((l) => l.trim() && !l.trimStart().startsWith(">"));
  const any = lines.find((l) => l.trim()) ?? "";
  const line = (own ?? any).replace(/^\s*>\s?/, "").trim();
  return line.length > NAV_RAIL_PREVIEW_MAX ? `${cutUnits(line, NAV_RAIL_PREVIEW_MAX - 1)}…` : line;
}

/** The answer's start as one plain line: Markdown marks a preview does not render are stripped (links keep their
 *  text, headings and quotes their prefix, bold and code their marks), whitespace folds, and the result is capped.
 *  A long answer is read from its head when that gives the same result (excerptHead). */
export function navRailExcerpt(raw: string): string {
  const head = excerptHead(raw);
  if (head !== undefined) {
    const out = plainLine(head);
    if (out.length >= NAV_RAIL_EXCERPT_MAX) return cutUnits(out, NAV_RAIL_EXCERPT_MAX);
  }
  return cutUnits(plainLine(raw.replace(COMMENT, "").replace(LINK, "$1")), NAV_RAIL_EXCERPT_MAX);
}

const COMMENT = /<!--[\s\S]*?-->/g;
const LINK = /\[([^\]]*)\]\([^)]*\)/g;
/** How much of a long answer the excerpt reads first: several times what the card shows, so the head of an answer in
 *  prose fills the excerpt; when it does not (a long comment or code block on top), the whole answer is read. */
const EXCERPT_HEAD = 1024;

/** The excerpt's steps after comments and links: line-start marks, bold and code marks, whitespace. */
function plainLine(text: string): string {
  return text
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^>\s?/gm, "")
    .replace(/^[-+*]\s+/gm, "")
    .replace(/[*`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** A longer answer's first EXCERPT_HEAD characters through the end of their line, comments and links already replaced;
 *  undefined when one of those starts in the head and might end after it. Every comment and link in the head is then
 *  decided inside it, and the later steps look only within a line (a heading or list mark's spaces may run on past
 *  the cut, but only past it), so the head's result is a prefix of the whole answer's and its first
 *  NAV_RAIL_EXCERPT_MAX characters are the whole answer's excerpt when it has that many (nav-rail-cache.test.ts
 *  checks this against the whole-text excerpt on random Markdown). */
function excerptHead(raw: string): string | undefined {
  const cut = raw.indexOf("\n", EXCERPT_HEAD) + 1;
  if (cut <= 0 || cut === raw.length) return undefined;
  const head = raw.slice(0, cut);
  // A closer after the head completes anything still open at the cut: a comment or link that reaches into it may
  // close only further on in the whole answer, so the head cannot stand for that answer.
  let open = false;
  const bare = (head + "-->").replace(COMMENT, (m: string, at: number) => {
    if (at + m.length > head.length) open = true;
    return "";
  });
  if (open) return undefined;
  const text = bare.slice(0, -3);
  const linked = (text + "](x)").replace(LINK, (m: string, words: string, at: number) => {
    if (at + m.length > text.length) open = true;
    return words;
  });
  return open ? undefined : linked.slice(0, -4);
}

/** Idle marks stay short, with emphasis on the current input. A preview creates bell-shaped focus shoulders. */
export function navRailMark(index: number, count: number, current: number | null, preview: number | null) {
  const m = NAV_RAIL_METRICS;
  const isCurrent = index === current;
  if (preview === null) {
    return {
      state: isCurrent ? "current" : "idle",
      width: isCurrent ? m.currentWidth : m.tickWidth,
      height: isCurrent ? m.currentHeight : m.tickHeight,
      opacity: isCurrent ? 1 : m.tailOpacity,
    };
  }
  const sigma = Math.min(m.spread, Math.max(m.minSpread, (count - 1) / 2));
  const distance = index - preview;
  const bell = Math.exp(-(distance * distance) / (2 * sigma * sigma));
  const isPreview = index === preview;
  const peak = preview === current ? m.currentHoverWidth : m.hoverWidth;
  const width = m.tickWidth + (peak - m.tickWidth) * bell;
  return {
    state: isCurrent ? (isPreview ? "current-hover" : "current") : isPreview ? "hover" : "idle",
    width,
    height: isCurrent ? (isPreview ? m.currentHoverHeight : m.currentHeight) : isPreview ? m.hoverHeight : m.tickHeight,
    opacity: isCurrent || isPreview ? 1 : m.tailOpacity + m.shoulderOpacity * bell,
  };
}

export interface NavRailPlan {
  start: number;
  pitch: number;
  count: number;
  content: number;
  viewport: number;
}

/** A full-height track with only the marks in and just outside its viewport mounted. */
export function planNavRailTicks(count: number, height: number, scrollTop = 0): NavRailPlan {
  if (count <= 0 || height <= 0) return { start: 0, pitch: NAV_RAIL_TICK_PITCH, count: 0, content: 0, viewport: 0 };
  const pitch = Math.min(NAV_RAIL_TICK_PITCH, Math.max(NAV_RAIL_TICK_MIN_PITCH, Math.floor(height / count)));
  const content = count * pitch;
  const viewport = Math.min(height, content);
  const top = Math.max(0, Math.min(scrollTop, content - viewport));
  const start = Math.max(0, Math.floor(top / pitch) - NAV_RAIL_METRICS.overscan);
  const end = Math.min(count, Math.ceil((top + viewport) / pitch) + NAV_RAIL_METRICS.overscan);
  return { start, pitch, count: end - start, content, viewport };
}

/** Cached monotone offsets make normal scrolling logarithmic, without per-message DOM reads. */
export function activeNavRailOffset(offsets: { index: number; top: number }[], top: number): number | null {
  if (!offsets.length) return null;
  let lo = 0;
  let hi = offsets.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (offsets[mid].top <= top) lo = mid + 1;
    else hi = mid;
  }
  return offsets[Math.max(0, lo - 1)].index;
}

export function withInputPreview(tick: NavRailTick, input?: UserInputPosition): NavRailTick {
  if (!input) return tick;
  const preview = tick.preview || navRailPreview(splitInlineTrailer(input.preview ?? "").text);
  return {
    ...tick, preview,
    attachmentsOnly: preview ? undefined : tick.attachmentsOnly ?? input.attachments,
    answerExcerpt: tick.answerExcerpt || navRailExcerpt(input.answer ?? "") || undefined,
  };
}

export function indexedNavTicks(index: UserInputIndex, lines: ChatLine[], items: Item[], retained?: { lines: ChatLine[]; items: Item[] }): NavRailTick[] {
  const owner = new Map<string, string>();
  for (const line of lines) for (const id of lineItemIds(line)) owner.set(inputEntry(id), line.id);
  const loaded = new Map(navRailTicks(lines).map((tick) => [tick.id, tick]));
  const retainedOwners = new Map<string, string>();
  for (const line of retained?.lines ?? []) for (const id of lineItemIds(line)) retainedOwners.set(inputEntry(id), line.id);
  for (const tick of retained ? navRailTicks(retained.lines) : []) {
    if (!loaded.has(tick.id)) loaded.set(tick.id, tick);
  }
  const words = new Map<string, string>();
  for (const source of retained ? [retained.items, items] : [items]) {
    const byEntry = new Map<string, string>();
    for (const item of source) {
      if (item.role === "user" && item.kind === "text") {
        const key = inputEntry(item.id);
        byEntry.set(key, [byEntry.get(key), parseUserText(item.text ?? "").text].filter(Boolean).join("\n"));
      }
    }
    for (const [id, text] of byEntry) words.set(id, text);
  }
  return index.inputs.map((input, i) => {
    const key = inputEntry(input.id);
    const lineId = owner.get(key);
    const retainedLineId = lineId ? undefined : retainedOwners.get(key);
    const local = lineId ?? retainedLineId;
    const tick = local ? loaded.get(local) : undefined;
    const preview = navRailPreview(words.get(key) ?? "") || tick?.preview || "";
    const loadPreview = !local && !words.has(key) && !hasInputExcerpt(input);
    return withInputPreview({
      id: `seg:${input.id}`, seq: input.seq, lineId, retainedLineId, ordinal: i + 1, preview, loadPreview,
      attachmentsOnly: tick?.attachmentsOnly, answerExcerpt: tick?.answerExcerpt,
    }, hasInputExcerpt(input) ? input : undefined);
  });
}
