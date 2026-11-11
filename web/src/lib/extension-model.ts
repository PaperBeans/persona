// The extension model shared by the web app and admin: a confirmation covers every preview in order.

import { t } from "../i18n";
export interface Contribution {
  point: string;
  id: string;
  title?: string;
  enabled: boolean;
  can_disable: boolean;
  via?: string;
}
export interface DependencyState {
  id: string;
  name?: string;
  version?: string;
  installed?: string;
  state: string;
}
export interface PackMember { id: string; market?: string; version?: string }
export interface Dependent { id: string; name: string }
export interface BundlePreview {
  preview_id: string;
  permissions_digest: string;
  expires_at: string;
  bundle?: BundlePreview[];
  unresolved?: string[];
}

export function previewConsent(pv: BundlePreview) {
  return {
    preview_id: pv.preview_id,
    permissions_digest: pv.permissions_digest,
    ...(pv.bundle?.length ? { bundle: pv.bundle.map((p) => ({ preview_id: p.preview_id, permissions_digest: p.permissions_digest })) } : {}),
  };
}

export function previewBlocked(pv: BundlePreview, now: number): boolean {
  return !!pv.unresolved?.length || [pv, ...(pv.bundle ?? [])].some((p) => {
    const at = Date.parse(p.expires_at);
    return !Number.isFinite(at) || at <= now || !!p.unresolved?.length;
  });
}

/** A pack's uninstall that stopped part way (409 `partial_uninstall`, #4072): the pack is still installed, nothing was put back. */
export interface PartialUninstall {
  /** Members uninstalled; they stay uninstalled. */
  removed: string[];
  /** Members the hub couldn't uninstall, each with its reason. */
  failed: { id: string; error: string }[];
  /** Members not tried; still installed. */
  remaining: string[];
}

export function partialUninstall(e: unknown): PartialUninstall | null {
  if (!e || typeof e !== "object" || !("body" in e)) return null;
  const b = e.body as Record<string, unknown>;
  if (b?.code !== "partial_uninstall") return null;
  const ids = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  const failed = (Array.isArray(b.failed_members) ? b.failed_members : [])
    .filter((f): f is { id: string; error?: unknown } => !!f && typeof f.id === "string")
    .map((f) => ({ id: f.id, error: typeof f.error === "string" ? f.error : "" }));
  return { removed: ids(b.removed_members), failed, remaining: ids(b.remaining_members) };
}

export function dependentError(e: unknown): Dependent[] | null {
  if (!e || typeof e !== "object" || !("body" in e)) return null;
  const b = e.body as Record<string, unknown>;
  if (b?.code !== "has_dependents" || !Array.isArray(b.dependents)) return null;
  return b.dependents.filter((d): d is Dependent => !!d && typeof d.id === "string" && typeof d.name === "string");
}

export function dependencyText(d: DependencyState): string {
  switch (d.state) {
    case "missing": return t("lib-extension-model.not_installed");
    case "disabled": return t("lib-extension-model.enable_first");
    case "old": return t("lib-extension-model.needs_version", { version: d.version || t("lib-extension-model.newer_version"), installed: d.installed || t("lib-extension-model.older_version") });
    case "not_ready": return t("lib-extension-model.not_ready");
    case "ok": return [t("lib-extension-model.satisfied"), d.installed].filter(Boolean).join(" · ");
    default: return t("lib-extension-model.unknown_state");
  }
}
