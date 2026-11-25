// One side of the disk usage map (#14576, docs/server/machine-usage-map.md "The map"): the boxes lib/usage-map.ts laid
// out, each an absolutely positioned <button> (a child after its frame, so it sits on top and takes its own clicks).
// The map is a group with one tab stop (roving tabindex): arrows move to the nearest box that way, Enter zooms,
// Space selects, Escape goes up one level, Delete asks to trash, the menu key (or Shift+F10, a right click, a long
// press) opens the menu. Colors come from the --umap-* tokens; nothing here measures text or fetches.
import { memo, useEffect, useRef, useState } from "react";
import { ageStep, nearestBox, type Direction, type MapBox } from "../../lib/usage-map";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export type Tint = "kind" | "age";

export interface TreemapProps {
  side: "local" | "cloud";
  boxes: readonly MapBox[];
  width: number;
  height: number;
  label: string;
  /** Changes on every zoom: the boxes fade and grow in (a cross-fade under reduced motion). */
  zoomKey: string;
  /** Changes when the focus should go to the first box (a zoom from the keyboard). */
  focusTick: number;
  tint: Tint;
  now: number;
  selected: ReadonlySet<string>;
  suggested: ReadonlySet<string>;
  lit: ReadonlySet<string>;
  ghosts: ReadonlySet<string>;
  busy: ReadonlySet<string>;
  badges: ReadonlyMap<string, number>;
  /** The free box grows in the "what if" preview. */
  freeGain: boolean;
  nameOf: (b: MapBox) => string;
  sizeOf: (b: MapBox) => string;
  a11yOf: (b: MapBox) => string;
  badgeLabel: (count: number) => string;
  canZoom: (b: MapBox) => boolean;
  onZoom: (b: MapBox, byKeyboard: boolean) => void;
  onToggle: (b: MapBox) => void;
  onMenu: (b: MapBox, point: { x: number; y: number }) => void;
  onTrash: (b: MapBox) => void;
  onUp: () => boolean;
  onHover: (b: MapBox | null) => void;
  onBadge?: (b: MapBox) => void;
  canDrag: (b: MapBox) => boolean;
  onDragStart: (b: MapBox) => void;
  onDragEnd: () => void;
  /** This side takes a drop from the other one now. */
  accepts: boolean;
  onDrop: () => void;
  dropHint?: string;
}

const ARROWS: Record<string, Direction> = { ArrowLeft: "left", ArrowRight: "right", ArrowUp: "up", ArrowDown: "down" };
const LONG_PRESS_MS = 500;

export const UsageTreemap = memo(function UsageTreemap(p: TreemapProps) {
  const [active, setActive] = useState<string>("");
  const [over, setOver] = useState(false);
  const host = useRef<HTMLDivElement>(null);
  const press = useRef<{ timer?: ReturnType<typeof setTimeout>; fired: boolean }>({ fired: false });
  const activeId = p.boxes.some((b) => b.node.id === active) ? active : (p.boxes[0]?.node.id ?? "");

  // after a zoom from the keyboard the focus goes to the first box of the redrawn map
  const lastTick = useRef(p.focusTick);
  useEffect(() => {
    if (p.focusTick === lastTick.current) return;
    lastTick.current = p.focusTick;
    const first = p.boxes[0];
    if (!first) return;
    setActive(first.node.id);
    host.current?.querySelector<HTMLElement>(`[data-box-index="0"]`)?.focus({ preventScroll: true });
  }, [p.focusTick, p.boxes]);

  const focusBox = (b: MapBox) => {
    setActive(b.node.id);
    const i = p.boxes.indexOf(b);
    host.current?.querySelector<HTMLElement>(`[data-box-index="${i}"]`)?.focus({ preventScroll: true });
  };

  const onKeyDown = (e: React.KeyboardEvent, b: MapBox) => {
    const dir = ARROWS[e.key];
    if (dir) {
      e.preventDefault();
      const next = nearestBox(p.boxes, b, dir);
      if (next) focusBox(next);
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      if (p.canZoom(b)) p.onZoom(b, true);
      return;
    }
    if (e.key === " ") {
      e.preventDefault();
      p.onToggle(b);
      return;
    }
    if (e.key === "Escape") {
      if (p.onUp()) {
        e.preventDefault();
        e.stopPropagation();
      }
      return;
    }
    if (e.key === "Delete" || e.key === "Backspace") {
      e.preventDefault();
      p.onTrash(b);
      return;
    }
    if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
      e.preventDefault();
      const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
      p.onMenu(b, { x: r.left + Math.min(r.width / 2, 24), y: r.top + Math.min(r.height / 2, 24) });
    }
  };

  const cancelPress = () => {
    if (press.current.timer) clearTimeout(press.current.timer);
    press.current.timer = undefined;
  };

  return (
    <div
      ref={host}
      role="group"
      aria-label={p.label}
      data-usage-side={p.side}
      className={cx("relative overflow-hidden rounded-[10px]", p.accepts && over && "umap-drop")}
      style={{ width: p.width, height: p.height }}
      onDragOver={(e) => {
        if (!p.accepts) return;
        e.preventDefault();
        if (!over) setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        if (!p.accepts) return;
        e.preventDefault();
        setOver(false);
        p.onDrop();
      }}
      onMouseLeave={() => p.onHover(null)}
    >
      <div key={p.zoomKey} className="umap-zoom absolute inset-0">
        {p.boxes.map((b, i) => {
          const n = b.node;
          const step = p.tint === "age" && n.category !== "free" ? (ageStep(n.used_at, p.now) ?? "none") : undefined;
          const cat = String(n.category);
          const style = {
            left: b.x,
            top: b.y,
            width: b.w,
            height: b.h,
            "--u": step ? `var(--umap-age-${step})` : `var(--umap-${cat})`,
            ...(step ? { "--u-ink": `var(--umap-age-${step}-ink)` } : {}),
          } as React.CSSProperties;
          const big = !b.nested && b.w >= 160 && b.h >= 72;
          const zoom = p.canZoom(b);
          const badge = p.badges.get(n.id) ?? 0;
          const cold = !b.nested && b.w >= 44 && b.h >= 44 && ageStep(n.used_at, p.now) === "year";
          return (
            <button
              key={n.id}
              type="button"
              data-box-index={i}
              data-box={n.id}
              data-depth={b.depth}
              data-cat={cat}
              data-frame={b.nested || undefined}
              data-big={big || undefined}
              data-inline={(b.nested || b.inline) || undefined}
              data-zoom={zoom || undefined}
              data-selected={p.selected.has(n.id) || undefined}
              data-suggested={p.suggested.has(n.id) || undefined}
              data-lit={p.lit.has(n.id) || undefined}
              data-ghost={p.ghosts.has(n.id) || undefined}
              data-twin={n.ghost || undefined}
              data-busy={p.busy.has(n.id) || undefined}
              data-gain={(cat === "free" && p.freeGain) || undefined}
              aria-label={p.a11yOf(b)}
              aria-pressed={p.selected.has(n.id) || undefined}
              tabIndex={n.id === activeId ? 0 : -1}
              className="umap-box"
              style={style}
              draggable={p.canDrag(b) || undefined}
              onDragStart={(e) => {
                e.dataTransfer?.setData("text/plain", n.display ?? n.name);
                if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
                p.onDragStart(b);
              }}
              onDragEnd={p.onDragEnd}
              onFocus={() => {
                setActive(n.id);
                p.onHover(b);
              }}
              onMouseEnter={() => p.onHover(b)}
              onKeyDown={(e) => onKeyDown(e, b)}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                p.onMenu(b, { x: e.clientX, y: e.clientY });
              }}
              onPointerDown={(e) => {
                p.onHover(b);
                if (e.pointerType !== "touch") return;
                press.current.fired = false;
                cancelPress();
                const { clientX: x, clientY: y } = e;
                press.current.timer = setTimeout(() => {
                  press.current.fired = true;
                  p.onMenu(b, { x, y });
                }, LONG_PRESS_MS);
              }}
              onPointerUp={cancelPress}
              onPointerCancel={cancelPress}
              onPointerMove={(e) => {
                if (e.pointerType === "touch" && (Math.abs(e.movementX) > 4 || Math.abs(e.movementY) > 4)) cancelPress();
              }}
              onClick={(e) => {
                e.stopPropagation();
                if (press.current.fired) {
                  press.current.fired = false;
                  return;
                }
                setActive(n.id);
                if (p.onBadge && (e.target as HTMLElement).closest?.("[data-badge]")) p.onBadge(b);
                else if (e.metaKey || e.ctrlKey || e.shiftKey) p.onToggle(b);
                else if (zoom) p.onZoom(b, e.detail === 0);
              }}
            >
              {b.showName && <span className="umap-name">{p.nameOf(b)}</span>}
              {b.showSize && <span className="umap-size">{p.sizeOf(b)}</span>}
              {cold && (
                <span aria-hidden className="absolute right-1.5 bottom-1 opacity-80">
                  <Icon name="snow" size={12} />
                </span>
              )}
              {badge > 0 && (!b.nested || b.depth === 1) && b.w >= 44 && b.h >= 26 && (
                <span aria-hidden className="umap-badge" style={{ right: 4, top: b.nested ? 2 : 4 }} title={p.badgeLabel(badge)} data-badge={n.id}>
                  <Icon name="cloud" size={11} />
                  {badge}
                </span>
              )}
            </button>
          );
        })}
      </div>
      {p.accepts && p.dropHint && (
        <div className="pointer-events-none absolute inset-x-2 bottom-2 rounded-control bg-surface/90 px-2 py-1 text-center text-submeta text-accent-ink shadow-btn">{p.dropHint}</div>
      )}
    </div>
  );
});
