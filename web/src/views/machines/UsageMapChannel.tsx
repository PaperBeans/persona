// The channel between the two sides of the disk usage map (#14576): one ribbon from a local box to each offloaded
// item it held, drawn only for the pair hovered, focused or selected (a picture of a link, not a second way in: the
// badge, the menu and the detail line say the same in words). Laid over the whole map, it takes no pointer events.
import type { Rect } from "../../lib/usage-map";

export interface Ribbon {
  key: string;
  from: Rect;
  to: Rect;
}

/** A band from the right edge of `from` to the left edge of `to` (or bottom to top when the sides are stacked). */
export function ribbonPath(from: Rect, to: Rect, stacked: boolean): string {
  if (stacked) {
    const x1 = from.x + from.w / 2;
    const y1 = from.y + from.h;
    const x2 = to.x + to.w / 2;
    const y2 = to.y;
    const half1 = Math.min(from.w / 2, 10);
    const half2 = Math.min(to.w / 2, 10);
    const my = (y1 + y2) / 2;
    return `M${x1 - half1},${y1} C${x1 - half1},${my} ${x2 - half2},${my} ${x2 - half2},${y2} L${x2 + half2},${y2} C${x2 + half2},${my} ${x1 + half1},${my} ${x1 + half1},${y1} Z`;
  }
  const x1 = from.x + from.w;
  const y1 = from.y + from.h / 2;
  const x2 = to.x;
  const y2 = to.y + to.h / 2;
  const half1 = Math.min(from.h / 2, 10);
  const half2 = Math.min(to.h / 2, 10);
  const mx = (x1 + x2) / 2;
  return `M${x1},${y1 - half1} C${mx},${y1 - half1} ${mx},${y2 - half2} ${x2},${y2 - half2} L${x2},${y2 + half2} C${mx},${y2 + half2} ${mx},${y1 + half1} ${x1},${y1 + half1} Z`;
}

export function UsageMapChannel({ ribbons, width, height, stacked }: { ribbons: readonly Ribbon[]; width: number; height: number; stacked: boolean }) {
  if (!ribbons.length) return null;
  return (
    <svg aria-hidden className="pointer-events-none absolute top-0 left-0" width={width} height={height} data-testid="usage-ribbons">
      {ribbons.map((r) => (
        <path key={r.key} d={ribbonPath(r.from, r.to, stacked)} fill="var(--accent)" fillOpacity={0.22} stroke="var(--accent)" strokeOpacity={0.55} strokeWidth={1} />
      ))}
    </svg>
  );
}
