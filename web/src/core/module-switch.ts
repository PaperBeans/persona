// Settings › General › Optional features (docs/web/web.md "Modules") uses the generic configuration form
// (core/module-features.ts) for these lasting browser preferences. Labs owns time-limited experiments.
// A module defaults to on. Switching it off makes it absent here (core/module-host.ts) until it is switched on again.
//
// Scope: this browser only (localStorage). It grants nothing and reaches neither
// the hub nor another device: a module the hub does not serve stays absent whatever is stored here.
//
// Storage: `persona.modules.off`, a JSON array of the module ids switched off; nothing stored while every one is on.
// What this build cannot read is taken as nothing switched off and left until a choice replaces it.

export const MODULE_SWITCH_KEY = "persona.modules.off";

const listeners = new Set<() => void>();

export function readStoredJson(key: string): unknown {
  try {
    return JSON.parse(localStorage.getItem(key) ?? "null");
  } catch {
    return null;
  }
}

function readOff(): string[] {
  const v = readStoredJson(MODULE_SWITCH_KEY);
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
}

// read once per change, not per call: the router asks on every render
let cache: string[] | null = null;
export const readModuleSwitches = () => (cache ??= readOff());

/** Whether this browser switched the module off. */
export function moduleSwitchedOff(id: string): boolean {
  return readModuleSwitches().includes(id);
}

/** Clears the cached switch list, and tells subscribers when `notify` is true (core/module-switch-write.ts). */
export function forgetModuleSwitches(notify = false): void {
  cache = null;
  if (notify) listeners.forEach((l) => l());
}

/** Called when a switch may have changed: here, or in another tab of this browser. */
export function subscribeModuleSwitches(l: () => void): () => void {
  listeners.add(l);
  const storage = (e: StorageEvent) => {
    if (e.key === null || e.key === MODULE_SWITCH_KEY) {
      cache = null;
      l();
    }
  };
  const w = typeof window < "u" ? window : undefined;
  w?.addEventListener("storage", storage);
  return () => {
    listeners.delete(l);
    w?.removeEventListener("storage", storage);
  };
}

export { setModuleSwitchedOff } from "./module-switch-write";
