// The model panel's rail (#5032, after Cindy's UnifiedModelRail): a 48 px column of view cells, Recent and Favorites (what
// you keep), a short rule, then All and one cell per provider in the order given. The cell that is on is filled in the
// ink; the others are quiet and light up under the pointer. It scrolls on its own, without a bar: an edge with more cells
// beyond it fades out, so a long list of providers shows it goes on.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { sameView, type PanelProvider, type PanelView } from "../../lib/model-panel";
import { quotaReset, weeklyQuota, type LauncherQuotaAnswer } from "../../lib/quota";
import { cx } from "../atoms";
import { Icon } from "../icons";
import { ProviderMark } from "./ProviderMark";

/** How far an edge with more beyond it fades. */
const FADE = "20px";

/** The mask for a column that has more above (`up`) and / or below (`down`); undefined for none. */
export function railFade(up: boolean, down: boolean): string | undefined {
  if (!up && !down) return undefined;
  return `linear-gradient(180deg,${up ? `transparent,#000 ${FADE}` : "#000"},${down ? `#000 calc(100% - ${FADE}),transparent` : "#000"})`;
}

/** Whether the column scrolled to `scrollTop` has cells hidden above and below. */
export function railEdges(b: { scrollTop: number; clientHeight: number; scrollHeight: number }): { up: boolean; down: boolean } {
  return { up: b.scrollTop > 1, down: b.scrollTop + b.clientHeight < b.scrollHeight - 1 };
}

export function Rail({ providers, view, onView, disabled, quotas }: { providers: PanelProvider[]; view: PanelView; onView: (v: PanelView) => void; disabled?: boolean; quotas?: Readonly<Record<string, LauncherQuotaAnswer>> }) {
  // `tip`: the tooltip when it says more than the name (a provider's startup ID, #5029)
  const cell = (v: PanelView, label: string, icon: React.ReactNode, tip = label) => {
    const on = sameView(v, view);
    const quota = v.kind === "provider" ? weeklyQuota(quotas?.[v.provider]) : undefined;
    const detail = quota ? [t("ui-modelpanel.weekly_left", { label: quota.window.label, percent: quota.remaining }), quotaReset(quota.window.resets_at ?? "")].filter(Boolean).join("\n") : "";
    return (
      <button
        key={v.kind === "provider" ? `provider:${v.provider}` : v.kind}
        type="button"
        disabled={disabled}
        aria-label={label}
        aria-pressed={on}
        title={[tip, detail].filter(Boolean).join("\n")}
        aria-description={detail || undefined}
        data-rail-cell={v.kind === "provider" ? v.provider : v.kind}
        onClick={() => onView(v)}
        className={cx(
          "relative flex h-[38px] w-[34px] shrink-0 flex-col items-center justify-center rounded-[9px] transition-colors duration-150",
          on ? "bg-ink text-surface" : "text-ink-3 enabled:hover:bg-hover-2 enabled:hover:text-ink-2",
          disabled && "cursor-not-allowed opacity-50",
        )}
      >
        <span className="flex h-6 items-center justify-center">{icon}</span>
        {quota && (
          <span aria-hidden className="absolute bottom-[3px] h-[3px] w-[22px] overflow-hidden rounded-full" style={{ backgroundColor: "color-mix(in srgb, currentColor 18%, transparent)" }}>
            <span
              data-weekly-remaining={quota.remaining}
              className={cx("block h-full rounded-full", quota.tone === "red" ? "bg-red" : quota.tone === "orange" ? "bg-orange" : "bg-accent")}
              style={{ width: `${quota.remaining}%` }}
            />
          </span>
        )}
      </button>
    );
  };
  const box = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ up: false, down: false });
  const measure = useCallback(() => {
    const el = box.current;
    if (!el) return;
    const e = railEdges(el);
    setEdges((v) => (v.up === e.up && v.down === e.down ? v : e));
  }, []);
  useLayoutEffect(measure, [measure, providers.length]);
  useEffect(() => {
    const el = box.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [measure]);
  const mask = railFade(edges.up, edges.down);
  return (
    <div
      ref={box}
      onScroll={measure}
      data-rail-fade={[edges.up && "up", edges.down && "down"].filter(Boolean).join(" ") || undefined}
      style={mask ? { maskImage: mask, WebkitMaskImage: mask } : undefined}
      role="group" aria-label={t("ui-modelpanel.views")} className="flex min-h-0 w-12 shrink-0 scrollbar-none flex-col items-center gap-0.5 overflow-x-hidden overflow-y-auto border-r border-line px-1.5 py-2">
      {cell({ kind: "recent" }, t("ui-modelpanel.recent"), <Icon name="history" size={16} />)}
      {cell({ kind: "favorites" }, t("ui-modelpanel.favorites"), <Icon name="star" size={16} className={view.kind === "favorites" ? "fill-current" : undefined} />)}
      <div aria-hidden className="my-[3px] w-[22px] shrink-0 border-t border-line" />
      {cell({ kind: "all" }, t("ui-modelpanel.all"), <Icon name="grid" size={16} />)}
      {providers.map((p) => cell({ kind: "provider", provider: p.id }, p.name, <ProviderMark name={p.name} />, p.idNote ? `${p.name} (${p.idNote})` : p.name))}
    </div>
  );
}
