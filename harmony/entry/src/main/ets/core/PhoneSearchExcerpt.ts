export interface SearchTextPart {
  text: string;
  matched: boolean;
}

export function searchHitRanges(raw: unknown): number[][] {
  if (!Array.isArray(raw)) return [];
  return raw.filter((r): r is number[] =>
    Array.isArray(r) && r.length === 2 && r.every(n => Number.isSafeInteger(n)));
}

interface GraphemeSegment { index: number; segment: string }
interface GraphemeSegmenter { segment(text: string): Iterable<GraphemeSegment> }

// Manager ranges use UTF-16 offsets. The lead in the sidebar counts displayed
// characters, including a combining sequence or a joined emoji as one character.
function graphemeOffsets(text: string): number[] {
  const ctor = (Intl as unknown as { Segmenter?: new (locale: string, options: { granularity: string }) => GraphemeSegmenter }).Segmenter;
  if (ctor) return [...new ctor('en', { granularity: 'grapheme' }).segment(text)].map(s => s.index).concat(text.length);
  const clusters = text.match(/(?:\r\n|\p{Regional_Indicator}{2}|[^\r\n](?:\p{Mark}|\p{Emoji_Modifier}|\uFE0F)*(?:\u200D[^\r\n](?:\p{Mark}|\p{Emoji_Modifier}|\uFE0F)*)*|[\r\n])/gu) || [];
  const offsets = [0];
  for (const cluster of clusters) offsets.push(offsets[offsets.length-1]+cluster.length);
  return offsets;
}

export function phoneSearchExcerpt(text: string, ranges: number[][], scale: number): SearchTextPart[] {
  if (!ranges.length) return [{ text, matched: false }];
  const boundaries = graphemeOffsets(text);
  const valid = ranges.map(([a,b]) => [Math.max(0,a), Math.min(text.length,b)])
    .filter(([a,b]) => a < b && boundaries.includes(a) && boundaries.includes(b))
    .sort((a,b) => a[0]-b[0]);
  if (!valid.length) return [{ text, matched: false }];
  const lead = scale <= 1 ? 8 : scale <= 21/17 ? 5 : 2;
  const first = boundaries.indexOf(valid[0][0]);
  let offset = first > lead+1 ? boundaries[first-lead] : 0;
  const parts: SearchTextPart[] = [];
  const append = (value: string, matched: boolean) => {
    if (!value) return;
    const previous = parts[parts.length-1];
    if (previous && previous.matched === matched) previous.text += value;
    else parts.push({ text: value, matched });
  };
  if (offset) append('…', false);
  for (const [lo,hi] of valid) {
    if (hi <= offset) continue;
    if (lo > offset) append(text.slice(offset,lo), false);
    append(text.slice(Math.max(lo,offset),hi), true);
    offset=hi;
  }
  append(text.slice(offset), false);
  return parts;
}
