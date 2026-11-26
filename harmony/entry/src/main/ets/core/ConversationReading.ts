export interface ReadingAnchor { id: string; offset: number; atEnd: boolean }
export interface ReadingBoundary { nextIndex: number; fromBottom: number }

/** The inverted native transcript lays out the next viewport of rows ahead of the reader.
 * Keep the trailing edge of that measured range if it ends before the newest message. */
export function measuredReadingBoundary(lastVisible: number, bottom: number, viewport: number, gap: number,
  count: number, measuredHeight: (index: number) => number, requested: number = -1): ReadingBoundary | undefined {
  if (lastVisible < 0 || viewport <= 0 || !Number.isFinite(bottom)) return undefined;
  let nextTop = bottom + gap;
  for (let index = lastVisible + 1; index < count; index++) {
    if (requested < 0 ? nextTop >= viewport * 2 : index === requested) {
      return { nextIndex: index, fromBottom: nextTop - viewport };
    }
    const height = measuredHeight(index);
    if (!(height > 0) || !Number.isFinite(height)) return undefined;
    nextTop += height + gap;
  }
  return { nextIndex: count, fromBottom: nextTop - gap - viewport };
}

// The 17pt reading face ends its standard sizes at 23pt and starts its accessibility sizes at 28pt.
// Harmony exposes a continuous scale. Values between those two categories use the nearer one.
export function readingSizeClassChanges(before: number, after: number): boolean {
  const boundary = (23 + 28) / (2 * 17);
  return Number.isFinite(before) && before > 0 && Number.isFinite(after) && after > 0 &&
    (before >= boundary) !== (after >= boundary);
}

export function parseReadingAnchor(value: string): ReadingAnchor {
  try {
    const data = JSON.parse(value) as ReadingAnchor;
    if (data && typeof data.id === 'string' && data.id.length <= 4096 && typeof data.offset === 'number' && Number.isFinite(data.offset) && typeof data.atEnd === 'boolean') {
      return { id: data.id, offset: Math.max(-100000, Math.min(100000, data.offset)), atEnd: data.atEnd };
    }
  } catch (_) {}
  return { id: '', offset: 0, atEnd: true };
}
export function readingAnchorIndex(ids: string[], anchor: ReadingAnchor): number { return anchor.id ? ids.indexOf(anchor.id) : -1; }
