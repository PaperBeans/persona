// Resizable directory-tree sidebar hook and vertical splitter (#14926), shared by FilesPanel and ChangesPanel.
// Follows Persona's ResizeHandle pointer-capture and keyboard conventions while drawing a slim 1px hairline inside
// a wider pointer hit target that highlights softly on hover, focus, or active drag.
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { TREE_PANE_DEFAULT, TREE_PANE_MAX, TREE_PANE_MIN, TREE_PANE_VIEWER_MIN } from "../lib/layout";
import { cx } from "./atoms";
import { RESIZE_STEP } from "./ResizeHandle";

export interface ResizableTreeWidthOptions {
  storageKey: string;
  defaultWidth?: number;
  minWidth?: number;
  maxWidth?: number;
  minViewerWidth?: number;
  containerRef: RefObject<HTMLElement | null>;
  /** Re-bind the container ResizeObserver when the split layout mounts conditionally. */
  observeKey?: unknown;
}

export interface ResizableTreeWidth {
  /** Displayed width in pixels, clamped to [min, max]. */
  width: number;
  min: number;
  /** Upper bound in pixels, dynamically capped so the right viewer retains minViewerWidth. */
  max: number;
  setWidth: (width: number) => void;
  commitWidth: (width: number) => void;
  resetWidth: () => void;
}

function clamp(value: number, min: number, max: number): number {
  return Math.round(Math.max(min, Math.min(max, value)));
}

function readStoredWidth(storageKey: string, defaultWidth: number, minWidth: number, maxWidth: number): number {
  try {
    if (typeof localStorage === "undefined") return defaultWidth;
    const raw = localStorage.getItem(storageKey);
    if (raw === null || raw === "") return defaultWidth;
    const parsed = Number(raw);
    return Number.isFinite(parsed) && parsed > 0 ? clamp(parsed, minWidth, maxWidth) : defaultWidth;
  } catch {
    return defaultWidth;
  }
}

export function useResizableTreeWidth({
  storageKey,
  defaultWidth = TREE_PANE_DEFAULT,
  minWidth = TREE_PANE_MIN,
  maxWidth = TREE_PANE_MAX,
  minViewerWidth = TREE_PANE_VIEWER_MIN,
  containerRef,
  observeKey,
}: ResizableTreeWidthOptions): ResizableTreeWidth {
  const [stored, setStored] = useState(() => readStoredWidth(storageKey, defaultWidth, minWidth, maxWidth));
  const [room, setRoom] = useState(maxWidth + minViewerWidth);

  useLayoutEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const measure = () => {
      const measured = element.getBoundingClientRect().width;
      if (measured > 0) setRoom(measured);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [containerRef, observeKey]);

  const max = Math.max(minWidth, Math.min(maxWidth, Math.floor(room - minViewerWidth)));
  const width = clamp(stored, minWidth, max);

  const setWidth = useCallback((next: number) => {
    setStored(clamp(next, minWidth, maxWidth));
  }, [minWidth, maxWidth]);

  const commitWidth = useCallback((next: number) => {
    const clamped = clamp(next, minWidth, max);
    setStored(clamped);
    try {
      localStorage.setItem(storageKey, String(clamped));
    } catch {
      // storage refused (private mode): keep the choice for this session only
    }
  }, [storageKey, minWidth, max]);

  const resetWidth = useCallback(() => {
    setStored(defaultWidth);
    try {
      localStorage.removeItem(storageKey);
    } catch {
      // as above
    }
  }, [storageKey, defaultWidth]);

  return { width, min: minWidth, max, setWidth, commitWidth, resetWidth };
}

export function TreePaneSplitter(props: {
  value: number;
  min: number;
  max: number;
  onResize?: (width: number) => void;
  onCommit: (width: number) => void;
  onReset: () => void;
  label: string;
  title?: string;
  controls?: string;
  className?: string;
}) {
  const { value, min, max, onCommit, onReset } = props;
  const [live, setLive] = useState<number | null>(null);
  const [dragging, setDragging] = useState(false);
  const latest = useRef(props);
  latest.current = props;
  const release = useRef<(() => void) | null>(null);
  useEffect(() => () => release.current?.(), []);

  const bound = (w: number) => clamp(w, latest.current.min, latest.current.max);

  const start = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || release.current) return;
    e.preventDefault();
    e.currentTarget.focus({ preventScroll: true });
    const id = e.pointerId;
    const originX = e.clientX;
    const from = value;
    let current = from;
    try {
      e.currentTarget.setPointerCapture(id);
    } catch {
      // pointer no longer tracked: window listeners still follow its moves
    }
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    setDragging(true);

    const move = (ev: PointerEvent) => {
      if (ev.pointerId !== id) return;
      const next = bound(from + (ev.clientX - originX));
      if (next === current) return;
      current = next;
      setLive(current);
      latest.current.onResize?.(current);
    };

    const stop = (ev?: PointerEvent) => {
      if (ev && ev.pointerId !== id) return;
      removeEventListener("pointermove", move);
      removeEventListener("pointerup", stop);
      removeEventListener("pointercancel", stop);
      release.current = null;
      document.body.style.removeProperty("cursor");
      document.body.style.removeProperty("user-select");
      setDragging(false);
      setLive(null);
      if (ev && current !== from) latest.current.onCommit(current);
    };

    release.current = () => stop();
    addEventListener("pointermove", move);
    addEventListener("pointerup", stop);
    addEventListener("pointercancel", stop);
  };

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={props.label}
      title={props.title}
      aria-controls={props.controls}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={live ?? value}
      data-dragging={dragging ? "" : undefined}
      tabIndex={0}
      onKeyDown={(e) => {
        const next = {
          ArrowRight: value + RESIZE_STEP,
          ArrowLeft: value - RESIZE_STEP,
          Home: min,
          End: max,
        }[e.key];
        if (next !== undefined) {
          e.preventDefault();
          onCommit(bound(next));
        } else if (e.key === "Enter") {
          e.preventDefault();
          onReset();
        }
      }}
      onPointerDown={start}
      onDoubleClick={onReset}
      className={cx(
        "group relative flex w-1.5 shrink-0 cursor-col-resize touch-none items-stretch justify-center select-none focus-visible:outline-none",
        props.className,
      )}
    >
      <span
        aria-hidden
        className={cx(
          "w-px transition-colors",
          dragging ? "bg-accent/50" : "bg-line group-hover:bg-accent/50 group-focus-visible:bg-accent/50",
        )}
      />
    </div>
  );
}
