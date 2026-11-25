// Settings › Connectors › Lark › "Show messages on home" (#13404, docs/extensions/lark-migration.md "Entry point"): off
// by default, kept by this browser. The lark module's sidebar entry shows only while it is on (module.ts `shown`), so
// this file is read with the descriptor, before the module's code: keep it this small.
import { useSyncExternalStore } from "react";

const HOME_KEY = "lark.homeMessages";
const homeListeners = new Set<() => void>();

export function getLarkHome(): boolean {
  try {
    return localStorage.getItem(HOME_KEY) === "1";
  } catch {
    return false;
  }
}

export function setLarkHome(on: boolean) {
  if (on) localStorage.setItem(HOME_KEY, "1");
  else localStorage.removeItem(HOME_KEY);
  homeListeners.forEach((l) => l());
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    if (e.key === HOME_KEY || e.key === null) homeListeners.forEach((l) => l());
  });
}

const subscribeHome = (l: () => void) => {
  homeListeners.add(l);
  return () => {
    homeListeners.delete(l);
  };
};

/** Whether this browser shows the "Messages" page (off unless turned on). */
export function useLarkHome(): boolean {
  return useSyncExternalStore(subscribeHome, getLarkHome, () => false);
}

/** The switch as the module host reads a sidebar entry's condition (core/module-host.ts ModuleNavItem `shown`). */
export const larkHomeShown = { get: getLarkHome, subscribe: subscribeHome };
