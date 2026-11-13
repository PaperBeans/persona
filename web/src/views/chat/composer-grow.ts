// The shared composer grows to six lines, then scrolls. Phones also offer the existing full-screen editor.
// The dock's CSS input cap retains toolbar space in exceptionally short visible viewports.

/** Where the compact composer applies: a phone-width screen, or a touch screen narrower than the sidebar breakpoint
 *  (a phone on its side). */
export const COMPACT_COMPOSER = "(max-width: 639px), (hover: none) and (max-width: 1023px)";
export const COMPOSER_LINES = 6;

/** The draft's wrapped lines, from the text's height (the box's scrollHeight less its vertical padding). */
export function lineCount(contentHeight: number, lineHeight: number): number {
  if (!(lineHeight > 0)) return 1;
  return Math.max(1, Math.round(contentHeight / lineHeight));
}

/** Expand shows once the draft has more lines than the box shows. With the button the text wraps narrower (more
 *  lines), so whichever state it lands in holds: it never flips back by itself. */
export const offersExpand = (lines: number, limit: number) => lines > limit;
