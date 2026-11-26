// Remembered per-hub module flags (core/module-host.ts, docs/web/web.md "When a module is there"). This small storage
// adapter is shared by the registered host and the standalone-page bootstrap, which must not import the host before App.
// The inline startup preloader uses the same key prefix and validation rule without importing client code at run time.
import { BASE } from "../lib/base";
import { MODULES_FEATURE } from "./module-flags";
import { readStoredJson } from "./module-switch";

export const MODULE_HUB_KEY_PREFIX = "persona.modules.hub:";

/** The storage key for one hub: its origin plus its reverse-proxy base, without a trailing slash. */
export function moduleHubStorageKey(
  origin: string | undefined = typeof location < "u" ? location.origin ?? "" : "",
  base: string | undefined = BASE,
): string {
  return `${MODULE_HUB_KEY_PREFIX}${origin}${base.replace(/\/+$/, "")}`;
}

/** The last publishing hub's module flags, or null for a first-seen/older hub and invalid or unreadable storage. */
export function readHubModules(origin?: string, base?: string): string[] | null {
  const value = readStoredJson(moduleHubStorageKey(origin, base));
  return Array.isArray(value) && value.every((x): x is string => typeof x === "string" && x.startsWith("module.")) ? value : null;
}

/** Whether the remembered answer serves a feature. No valid remembered answer keeps the default on. */
export function rememberedHubServes(feature: string | undefined, origin?: string, base?: string): boolean {
  if (!feature) return true;
  const kept = readHubModules(origin, base);
  return !kept || kept.includes(feature);
}

/** Remembers only a publishing hub's module flags, writing when they change; an older hub clears the answer. */
export function rememberHubModules(features: readonly string[], origin?: string, base?: string): void {
  const key = moduleHubStorageKey(origin, base);
  const next = features.includes(MODULES_FEATURE)
    ? JSON.stringify([...new Set(features.filter((f) => f.startsWith("module.")))].sort())
    : null;
  try {
    if (next === localStorage.getItem(key)) return;
    if (next === null) localStorage.removeItem(key);
    else localStorage.setItem(key, next);
  } catch {
    // storage blocked or full: the live hello still applies for this page
  }
}
