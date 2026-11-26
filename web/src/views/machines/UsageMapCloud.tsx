// The disk usage map's other side (#14576, docs/server/machine-usage-map.md "The map", "No provider"): what is already
// offloaded, drawn by UsageTreemap from the providers' inventories on the local side's scale. Without a provider it is
// a quiet empty state that points to Settings › Extensions; core names no product, the provider's own name is shown.
// A drop from the local side lands here even while nothing is drawn yet.
import { useState } from "react";
import { t } from "../../i18n";
import type { OffloadInventory, OffloadProvider } from "../../lib/usage-map";
import { navigate } from "../../lib/router";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export interface CloudState {
  state: "loading" | "ok" | "failed";
  inv?: OffloadInventory;
  text?: string;
}

function stateLine(p: OffloadProvider, s: CloudState): string {
  if (s.state === "loading" && !s.inv) return t("machines-usagemap.cloud_loading", { provider: p.name });
  if (s.state === "failed") return t("machines-usagemap.cloud_failed", { provider: p.name });
  switch (s.inv?.state) {
    case "needs_install":
      return t("machines-usagemap.cloud_needs_install", { provider: p.name });
    case "needs_setup":
      return t("machines-usagemap.cloud_needs_setup", { provider: p.name });
    case "unavailable":
      return t("machines-usagemap.cloud_unavailable", { provider: p.name });
    default:
      return s.inv?.items.length ? "" : t("machines-usagemap.cloud_none", { provider: p.name });
  }
}

export function UsageMapCloud({
  states,
  providers,
  boxesEmpty,
  accepts,
  onDrop,
  dropHint,
  children,
}: {
  states: { provider: OffloadProvider; state: CloudState }[];
  providers: OffloadProvider[] | null;
  boxesEmpty: boolean;
  accepts: boolean;
  onDrop: () => void;
  dropHint?: string;
  children: React.ReactNode;
}) {
  const [over, setOver] = useState(false);
  const lines = states.map((s) => ({ key: `${s.provider.extension}/${s.provider.id}`, text: stateLine(s.provider, s.state), detail: s.state.inv?.detail })).filter((l) => l.text);
  return (
    <div
      data-testid="usage-cloud"
      onDragOver={(e) => {
        if (!accepts || !boxesEmpty) return;
        e.preventDefault();
        if (!over) setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        if (!accepts || !boxesEmpty) return;
        e.preventDefault();
        setOver(false);
        onDrop();
      }}
    >
      {children}
      {providers !== null && providers.length === 0 && (
        <div className="rounded-[10px] border border-dashed border-line-strong px-3 py-3 text-meta text-ink-3" data-testid="usage-cloud-empty">
          <Icon name="cloud" size={16} className="mb-1 text-ink-3" />
          <p>{t("machines-usagemap.cloud_empty")}</p>
          <button type="button" className="mt-1 text-accent-ink hover:underline" onClick={() => navigate("/settings?tab=extensions")} data-testid="usage-cloud-settings">
            {t("machines-usagemap.cloud_settings")}
          </button>
        </div>
      )}
      {providers !== null && providers.length > 0 && boxesEmpty && (
        <div className={cx("rounded-[10px] border border-dashed px-3 py-3 text-meta text-ink-3", accepts && over ? "umap-drop border-accent" : "border-line-strong")} data-testid="usage-cloud-none">
          <Icon name="cloud" size={16} className="mb-1 text-ink-3" />
          {lines.map((l) => (
            <p key={l.key}>
              {l.text}
              {l.detail ? <span className="block text-submeta">{l.detail}</span> : null}
            </p>
          ))}
          {accepts && dropHint && <p className="mt-1 text-accent-ink">{dropHint}</p>}
        </div>
      )}
      {!boxesEmpty && lines.length > 0 && (
        <ul className="mt-1 space-y-0.5 text-submeta text-ink-3">
          {lines.map((l) => (
            <li key={l.key}>{l.text}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
