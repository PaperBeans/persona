// Only the Ctrl+R panel needs cross-conversation search; keep it out of the first-screen recall path.
import { readInlineDraft, visibleInlineText, type InlineDocument } from "./inline-mentions";
import type { ContextRef } from "./links";
import { browserStore, composing, HISTORY_PREFIX, plainCtrl, readHistory, type HistoryStore, type KeyLike, type SentEntry } from "./send-history";
import { splitSkillDraft } from "./skill-mentions";

export const SEARCH_MAX = 100;

function formatHistoryLabel(skills: readonly ContextRef[], doc: InlineDocument): string {
  return [...skills.map((s) => `${s.title} · ${s.text.split("\n")[0]}`), visibleInlineText(doc)].filter(Boolean).join("\n");
}

function normalizeRef(ref: ContextRef) {
  const attrs = ref.attrs ? Object.entries(ref.attrs).sort(([a], [b]) => a.localeCompare(b)) : [];
  return [ref.kind, ref.title, ref.url, ref.text, attrs];
}

function dedupKey(text: string, skills: readonly ContextRef[], doc: InlineDocument): string {
  if (!doc.some((part) => typeof part !== "string")) return `r:${text}`;
  const normalizedSkills = skills.map(normalizeRef);
  const normalizedDoc = doc.map((part) => (typeof part === "string" ? part : normalizeRef(part)));
  return `i:${JSON.stringify([normalizedSkills, normalizedDoc])}`;
}

/** The human-readable text shown for a sent history entry (attached skills and decoded inline mentions). */
export function historyLabel(text: string): string {
  const split = splitSkillDraft(text);
  return formatHistoryLabel(split.skills, readInlineDraft(split.text));
}

/** Every conversation's sent messages, newest first: the candidates of the search. */
export function readAllHistory(store: HistoryStore | undefined = browserStore()): (SentEntry & { draftKey: string })[] {
  const all: (SentEntry & { draftKey: string })[] = [];
  try {
    if (store) for (let i = 0; i < store.length; i++) {
      const k = store.key(i);
      if (k?.startsWith(HISTORY_PREFIX)) {
        const draftKey = k.slice(HISTORY_PREFIX.length);
        all.push(...readHistory(draftKey, store).map((entry) => ({ ...entry, draftKey })));
      }
    }
  } catch {
    /* unreadable storage: no candidates */
  }
  return all.sort((a, b) => b.at - a.at);
}

/** Every query word must match the visible label, case-insensitively; newest first, one row per prompt, at most `max`. */
export function searchSent(candidates: SentEntry[], query: string, max: number = SEARCH_MAX): string[] {
  const words = query.toLowerCase().split(" ").filter(Boolean);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const e of candidates) {
    const split = splitSkillDraft(e.text);
    const doc = readInlineDraft(split.text);
    if (words.length) {
      const text = formatHistoryLabel(split.skills, doc).toLowerCase();
      if (!words.every((w) => text.includes(w))) continue;
    }
    const key = dedupKey(e.text, split.skills, doc);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(e.text);
    if (out.length >= max) break;
  }
  return out;
}

export type SearchAction = "up" | "down" | "pick" | "cancel";

/** ↑ ↓ and Ctrl+R/P/S/N move; Enter/Tab picks; Esc/Ctrl+G cancels; typing stays the search box's. */
export function searchAction(e: KeyLike): SearchAction | null {
  if (composing(e)) return null;
  const bare = !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey;
  if (bare && e.key === "ArrowUp") return "up";
  if (bare && e.key === "ArrowDown") return "down";
  if (bare && (e.key === "Enter" || e.key === "Tab")) return "pick";
  if (bare && e.key === "Escape") return "cancel";
  if (plainCtrl(e, "r") || plainCtrl(e, "p")) return "up";
  if (plainCtrl(e, "s") || plainCtrl(e, "n")) return "down";
  if (plainCtrl(e, "g")) return "cancel";
  return null;
}

/** The highlighted row after a move: ↑ goes toward the newest and wraps to the last. */
export function moveActive(active: number, count: number, action: "up" | "down"): number {
  if (count <= 0) return 0;
  return action === "up" ? (active - 1 + count) % count : (active + 1) % count;
}
