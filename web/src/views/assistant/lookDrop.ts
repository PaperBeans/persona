// Per-slot file drag-and-drop helpers for the avatar picker and upload/edit dialog (#15612).
// Kept in its own lazy module (imported only by PersonaPicker, LookUpload and LookUploadExtras)
// so nothing here ever enters the cold first screen.
import { useEffect, useRef, useState, type DragEvent } from "react";

/**
 * A targeted picker card's dashed outline, inline so no new utility class reaches the global stylesheet the first
 * screen loads (the other outline classes are already in it).
 */
export const DASHED_OUTLINE = { outlineStyle: "dashed" } as const;

/** True when a drag event carries OS files (ignores in-page text drags). */
export function hasDragFiles(dt: DataTransfer | null | undefined): boolean {
  return !!dt && [...dt.types].includes("Files");
}

/**
 * Drop-target state and handlers for one avatar picture slot or card.
 * Stops propagation so nested slots handle only the innermost target under the pointer,
 * uses an enter/leave depth counter so dragging over children does not flicker, and
 * swallows drops cleanly while disabled.
 */
export function useFileDrop({ disabled = false, onFile }: { disabled?: boolean; onFile: (file: File) => void }) {
  const [over, setOver] = useState(false);
  const depth = useRef(0);

  useEffect(() => {
    if (!disabled) return;
    depth.current = 0;
    setOver(false);
  }, [disabled]);

  return {
    over: !disabled && over,
    props: {
      onDragEnter: (e: DragEvent) => {
        if (!hasDragFiles(e.dataTransfer)) return;
        e.preventDefault();
        e.stopPropagation();
        if (disabled) {
          e.dataTransfer.dropEffect = "none";
          return;
        }
        depth.current += 1;
        setOver(true);
      },
      onDragOver: (e: DragEvent) => {
        if (!hasDragFiles(e.dataTransfer)) return;
        e.preventDefault();
        e.stopPropagation();
        if (disabled) {
          e.dataTransfer.dropEffect = "none";
          return;
        }
        e.dataTransfer.dropEffect = "copy";
        setOver(true);
      },
      onDragLeave: (e: DragEvent) => {
        if (!hasDragFiles(e.dataTransfer)) return;
        e.stopPropagation();
        depth.current = Math.max(0, depth.current - 1);
        if (depth.current === 0) setOver(false);
      },
      onDrop: (e: DragEvent) => {
        if (!hasDragFiles(e.dataTransfer)) return;
        e.preventDefault();
        e.stopPropagation();
        depth.current = 0;
        setOver(false);
        if (disabled) {
          e.dataTransfer.dropEffect = "none";
          return;
        }
        const file = e.dataTransfer.files?.[0];
        if (file) onFile(file);
      },
    },
  };
}

/**
 * Container-level near-miss guard: prevents the browser from navigating to a file dropped
 * between slots or on non-droppable cards, while signaling dropEffect = "none".
 */
export const fileDropGuard = {
  onDragOver: (e: DragEvent) => {
    if (!hasDragFiles(e.dataTransfer)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "none";
  },
  onDrop: (e: DragEvent) => {
    if (!hasDragFiles(e.dataTransfer)) return;
    e.preventDefault();
  },
};

/**
 * While `active` (the upload / edit dialog is open), a file dropped anywhere outside a slot (the dialog's title,
 * footer or backdrop included) is swallowed instead of opening in the tab. Slots stop propagation, so they never
 * reach this listener.
 */
export function useWindowFileDropGuard(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const swallow = (e: globalThis.DragEvent) => {
      if (!hasDragFiles(e.dataTransfer)) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
    };
    window.addEventListener("dragover", swallow);
    window.addEventListener("drop", swallow);
    return () => {
      window.removeEventListener("dragover", swallow);
      window.removeEventListener("drop", swallow);
    };
  }, [active]);
}
