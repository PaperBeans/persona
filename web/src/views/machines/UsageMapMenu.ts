// The disk usage map's context menu (#14576, docs/server/machine-usage-map.md "The map"): which actions a box offers,
// from the node's flags alone (can_trash, can_offload, refusal, cache_id) and the enabled providers. An action the
// rules refuse stays in the menu, disabled, and says why; Offload is absent without a provider. Pure, so every rule is
// tested without opening a menu.
import { t } from "../../i18n";
import { zoomable, type OffloadProvider, type UsageNode } from "../../lib/usage-map";
import type { MenuItem } from "../../ui/atoms";

export interface MenuHandlers {
  zoom: () => void;
  toggle: () => void;
  trash: () => void;
  offload: (p: OffloadProvider) => void;
  clean: () => void;
  copy: () => void;
  restore: () => void;
  showOffloaded: () => void;
}

/** Why the node will not move an item, in words (the refusal code's sentence, or a plain fallback). */
export function refusalText(code: string | undefined): string {
  return code ? t(`machines-usagemap.refusal_${code}`) : t("machines-usagemap.menu_not_allowed");
}

function providerTakes(p: OffloadProvider, n: UsageNode): boolean {
  const kind = n.kind === "app" ? "app" : n.kind === "file" ? "file" : "dir";
  return (p.kinds?.length ? p.kinds : ["file", "dir"]).includes(kind);
}

export function localMenu(n: UsageNode, o: { selected: boolean; providers: readonly OffloadProvider[]; offloadedCount: number }, h: MenuHandlers): MenuItem[] {
  const items: MenuItem[] = [];
  if (zoomable(n)) items.push({ key: "zoom", label: t("machines-usagemap.menu_zoom"), icon: "expand", onSelect: h.zoom });
  if (!n.path) return items;
  const catalog = !!n.cache_id;
  items.push({ key: "select", label: o.selected ? t("machines-usagemap.menu_deselect") : t("machines-usagemap.menu_select"), icon: "check", checked: o.selected, onSelect: h.toggle });
  if (catalog) {
    items.push({
      key: "clean",
      label: t("machines-usagemap.menu_clean"),
      icon: "sparkles",
      divider: true,
      disabled: !!n.cache_in_use,
      detail: n.cache_in_use ? t("machines-usagemap.menu_cache_in_use") : undefined,
      onSelect: h.clean,
    });
  } else {
    items.push({
      key: "trash",
      label: t("machines-usagemap.menu_trash"),
      icon: "trash",
      divider: true,
      danger: !!n.can_trash,
      disabled: !n.can_trash,
      detail: n.can_trash ? undefined : refusalText(n.refusal),
      onSelect: h.trash,
    });
  }
  for (const p of o.providers) {
    const takes = providerTakes(p, n);
    const ok = !!n.can_offload && takes && !catalog;
    items.push({
      key: `offload:${p.extension}/${p.id}`,
      label: t("machines-usagemap.menu_offload", { provider: p.name }),
      icon: "upload",
      disabled: !ok,
      detail: ok ? undefined : catalog ? t("machines-usagemap.menu_cache_cleaned") : !n.can_offload ? refusalText(n.refusal) : t("machines-usagemap.menu_not_taken", { provider: p.name }),
      onSelect: () => h.offload(p),
    });
  }
  if (o.offloadedCount > 0) items.push({ key: "offloaded", label: t("machines-usagemap.menu_show_offloaded", { count: o.offloadedCount }), icon: "cloud", onSelect: h.showOffloaded });
  items.push({ key: "copy", label: t("machines-usagemap.menu_copy"), icon: "copy", divider: true, detail: n.display ?? n.path, onSelect: h.copy });
  return items;
}

export function cloudMenu(n: UsageNode, o: { selected: boolean; provider?: OffloadProvider }, h: MenuHandlers): MenuItem[] {
  const items: MenuItem[] = [];
  if (n.children?.length) items.push({ key: "zoom", label: t("machines-usagemap.menu_zoom"), icon: "expand", onSelect: h.zoom });
  const it = n.cloud;
  if (!it) return items;
  const ok = it.restorable && !!o.provider?.restore;
  items.push({ key: "select", label: o.selected ? t("machines-usagemap.menu_deselect") : t("machines-usagemap.menu_select"), icon: "check", checked: o.selected, disabled: !ok, onSelect: h.toggle });
  items.push({
    key: "restore",
    label: t("machines-usagemap.menu_restore"),
    icon: "download",
    divider: true,
    disabled: !ok,
    detail: ok ? undefined : t("machines-usagemap.menu_not_restorable"),
    onSelect: h.restore,
  });
  if (it.origin_path) items.push({ key: "copy", label: t("machines-usagemap.menu_copy"), icon: "copy", divider: true, detail: it.origin_path, onSelect: h.copy });
  return items;
}
