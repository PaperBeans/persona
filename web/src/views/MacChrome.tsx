import { useLayoutEffect, useState } from "react";
import { t } from "../i18n";
import { setSidebarCollapsed, toggleMaximized } from "../lib/layout";
import { appleG2Contour } from "../lib/mac-chrome";
import { jumpHint, useBindings } from "../lib/shortcuts";
import { Icon } from "../ui/icons";
import { CollapseButton } from "./SidebarRail";

/** Controls parked inside the 44 px Apple G2 bay beside the macOS traffic lights when the sidebar is collapsed or hidden. */
export function MacBayTools({ bay, onSearch }: { bay: "rail" | "hidden"; onSearch: () => void }) {
  const bindings = useBindings();
  return (
    <div
      data-mac-bay-tools
      className="pointer-events-auto absolute z-50 flex h-7 items-center gap-0.5 max-lg:hidden"
      style={{ left: 80, top: 11 }}
    >
      <span aria-hidden className="mr-1 h-3.5 w-px bg-line" />
      <CollapseButton
        collapsed
        chord={bindings["left-sidebar"]}
        size="sm"
        onToggle={bay === "hidden" ? () => {
          setSidebarCollapsed(false);
          toggleMaximized();
        } : undefined}
      />
      <button
        type="button"
        onClick={onSearch}
        aria-label={t("sidebar.search_sessions")}
        title={`${t("sidebar.search_sessions")} (${jumpHint(bindings)})`}
        className="touch-hit relative z-10 flex size-7 shrink-0 items-center justify-center rounded-control text-ink-3 transition-[background-color,color,transform] duration-150 hover:bg-hover hover:text-ink active:scale-[0.94]"
      >
        <Icon name="search" size={14} stroke={1.85} />
      </button>
    </div>
  );
}

function applyMainClip(el: HTMLElement, value: string): void {
  el.style.clipPath = value;
  el.style.setProperty("-webkit-clip-path", value);
}

/** Clips `<main>` with the 44 px Apple G2 continuous-curvature path and strokes its crisp 1 px border + top specular rim. */
export function MacMainContour({ mainEl, bay }: { mainEl: HTMLElement | null; bay: "rail" | "hidden" | null }) {
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });

  useLayoutEffect(() => {
    if (!mainEl) return;
    if (!bay) {
      applyMainClip(mainEl, "");
      return;
    }
    const measure = () => {
      const w = Math.round(mainEl.clientWidth);
      const h = Math.round(mainEl.clientHeight);
      if (w >= 100 && h >= 100) {
        const { clip } = appleG2Contour(w, h, bay);
        applyMainClip(mainEl, `path("${clip}")`);
        setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
      }
    };
    measure();
    if (typeof ResizeObserver === "undefined") return () => applyMainClip(mainEl, "");
    const ro = new ResizeObserver(measure);
    ro.observe(mainEl);
    return () => {
      ro.disconnect();
      applyMainClip(mainEl, "");
    };
  }, [mainEl, bay]);

  if (!bay || size.w < 100 || size.h < 100) return null;
  const { stroke, rim } = appleG2Contour(size.w, size.h, bay);
  return (
    <svg
      aria-hidden
      data-mac-contour
      viewBox={`0 0 ${size.w} ${size.h}`}
      className="pointer-events-none absolute inset-0 z-30 h-full w-full max-lg:hidden"
    >
      <path d={stroke} fill="none" stroke="var(--line)" strokeWidth={1} />
      <path d={rim} fill="none" stroke="var(--mac-rim-top, transparent)" strokeWidth={1} />
    </svg>
  );
}
