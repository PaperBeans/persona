// Wire reconstruction is needed for inline sends and recalled selections.
import { canonicalAppURL } from "./compatibility";
import { joinInline, mentionedText, INLINE_TRAILER, splitInlineTrailer, type InlineDocument } from "./inline-mentions";
import type { ContextRef } from "./links";
import type { Context } from "./turns";

/** Use wire identity, which survives draft IDs, reordered blocks and hub enrichment of context descriptions. */
function referenceKey(ref: Context): string | undefined {
  const a = ref.attrs;
  const id = ref.kind === "skill" ? a?.ref
    : ref.kind === "file" && a?.path ? [a.path, a.machine, a.cwd, a.launcher, a.runtime]
    : ref.kind === "workspace" && a?.cwd ? [a.machine, a.cwd]
    : ref.kind === "repository" ? a?.repo_id
    : ref.kind === "context" && ref.url ? canonicalAppURL(ref.url).match(/^persona:\/\/session\/[^?]+$/)?.[0] : undefined;
  return id === undefined ? undefined : JSON.stringify([ref.kind, id]);
}

export function inlineReferences(doc: InlineDocument): ContextRef[] {
  const refs = new Map<string | undefined, ContextRef>();
  for (const part of doc) if (typeof part !== "string") refs.set(referenceKey(part) ?? part.id, part);
  return [...refs.values()];
}

type Anchor = [fromEnd: number, length: number, key: string];
/** The body stays ordinary text, including native @paths. The trailer points back from the end of the prose so quotes
 * and other prefixes do not move anchors. Actual selection metadata still travels in the existing context tags. */
export function inlineBody(doc: InlineDocument, trim = true): string {
  const anchors: Anchor[] = [];
  let text = "";
  for (const part of doc) {
    if (typeof part === "string") {
      text += part;
    } else {
      const label = mentionedText(part);
      const key = referenceKey(part);
      if (key && label) anchors.push([text.length, label.length, key]);
      text += label;
    }
  }
  if (!anchors.length) return text;
  if (trim) {
    const [start, length] = anchors[anchors.length - 1];
    text = text.slice(0, Math.max(start + length, text.trimEnd().length));
  }
  for (const anchor of anchors) anchor[0] = text.length - anchor[0];
  const encoded = JSON.stringify(anchors).replaceAll("-", "\\u002d").replaceAll("<", "\\u003c");
  return `${trim ? text.trimStart() : text}\n\n<!-- persona-inline-v1 ${encoded} -->`;
}

export function readInlineBody<T extends Context>(body: string, refs: readonly T[]) {
  const used = new Set<T>();
  const trailer = INLINE_TRAILER.exec(body);
  if (!trailer) {
    const { text } = splitInlineTrailer(body);
    return { text, document: text ? [text] : [], used };
  }
  const text = body.slice(0, trailer.index);
  const document: (string | ContextRef)[] = [];
  const byKey = new Map(refs.map((ref) => [referenceKey(ref), ref]));
  let cursor = 0;
  try {
    const anchors: unknown = JSON.parse(trailer[1]);
    if (Array.isArray(anchors)) for (const anchor of anchors) {
      if (!Array.isArray(anchor)) continue;
      const [fromEnd, length, key] = anchor;
      if (!Number.isSafeInteger(fromEnd) || !Number.isSafeInteger(length) || length < 1 || typeof key !== "string") continue;
      const start = text.length - fromEnd;
      const ref = byKey.get(key);
      if (!ref || start < cursor || start + length > text.length) continue;
      const picked = { ...ref, id: "id" in ref ? String(ref.id) : key, kind: ref.kind as ContextRef["kind"], title: ref.title ?? "", url: ref.url ?? "" };
      if (text.slice(start, start + length) !== mentionedText(picked)) continue;
      document.push(text.slice(cursor, start), picked);
      used.add(ref);
      cursor = start + length;
    }
  } catch { /* A malformed anchor map leaves the message's prose and existing contexts. */ }
  document.push(text.slice(cursor));
  return { text, document: joinInline(document), used };
}
