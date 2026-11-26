import type { ContextRef } from "./links";

export type InlineDocument = readonly (string | ContextRef)[];
export type InlineSelection = [start: number, end: number];

/** Offsets are UTF-16 code units, with one unit per reference, regardless of its label's length. */
export const INLINE_TOKEN = "\uFFFC";

export function editorText(doc: InlineDocument): string {
  return doc.map((part) => typeof part === "string" ? part : INLINE_TOKEN).join("");
}

export function joinInline(...parts: InlineDocument[]): InlineDocument {
  const result: (string | ContextRef)[] = [];
  for (const part of parts.flat()) {
    if (!part) continue;
    const last = result.at(-1);
    if (typeof part === "string" && typeof last === "string") result[result.length - 1] = last + part;
    else result.push(part);
  }
  return result;
}

export function sliceInline(doc: InlineDocument, start: number, end = Infinity): InlineDocument {
  let offset = 0;
  return doc.flatMap<string | ContextRef>((part) => {
    const from = offset;
    offset += typeof part === "string" ? part.length : 1;
    if (offset <= start || end <= from) return [];
    return typeof part === "string" ? part.slice(Math.max(0, start - from), end - from) : [part];
  });
}

export function replaceInlineRange(doc: InlineDocument, start: number, end: number, insert: InlineDocument): InlineDocument {
  return joinInline(sliceInline(doc, 0, start), insert, sliceInline(doc, end));
}

export function mentionLabel(ref: ContextRef): string {
  return (ref.title || ref.attrs?.path || ref.url).replace(/\s*\r?\n\s*/g, " ").trim();
}

export function mentionedText(ref: ContextRef): string {
  return ref.kind === "file" && ref.attrs?.path ? `@${ref.attrs.path}` : mentionLabel(ref);
}

export function visibleInlineText(doc: InlineDocument): string {
  return doc.map((part) => typeof part === "string" ? part : mentionedText(part)).join("");
}

const DRAFT = "\0persona-inline-v1\n";

export function writeInlineDraft(doc: InlineDocument): string {
  return doc.some((part) => typeof part !== "string") ? DRAFT + JSON.stringify(doc) : doc.join("");
}

export function readInlineDraft(raw: string): InlineDocument {
  if (raw.startsWith(DRAFT)) {
    try {
      const doc = JSON.parse(raw.slice(DRAFT.length));
      if (Array.isArray(doc) && doc.every((p) => typeof p === "string" || (p && typeof p.id === "string"
        && typeof p.kind === "string" && typeof p.title === "string" && typeof p.url === "string" && typeof p.text === "string"
        && (p.attrs === undefined || (p.attrs && Object.values(p.attrs).every((v) => typeof v === "string")))))) return doc;
    } catch { /* A stored plain draft may happen to start with the prefix. */ }
  }
  return raw ? [raw] : [];
}

export function draftText(raw: string): string {
  const start = raw.indexOf(DRAFT);
  return start < 0 ? raw : visibleInlineText(readInlineDraft(raw.slice(start)));
}

export const INLINE_TRAILER_START = "<!-- persona-inline-v1 ";
export const INLINE_TRAILER = /\n\n<!-- persona-inline-v1 (\[[^\r\n]*\]) -->$/;

export function splitInlineTrailer(body: string): { text: string; inlineTrailer?: string } {
  if (!body.includes(INLINE_TRAILER_START)) return { text: body };
  const match = INLINE_TRAILER.exec(body);
  // A cut page or a forward joining transcript blocks may contain a trailer without a usable anchor map.
  const text = (match ? body.slice(0, match.index) : body).replace(/(?:^|\r?\n\r?\n)<!-- persona-inline-v1 \[[^\r\n]*/gm, "");
  return match ? { text, inlineTrailer: match[0] } : { text };
}

let wire: typeof import("./inline-message") | undefined;
export const inlineMessage = () => wire;
export const loadInlineMessage = () => import("./inline-message").then((codec) => (wire = codec));
