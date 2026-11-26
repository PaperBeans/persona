import { useSyncExternalStore } from "react";
import { onCredentialChange } from "./api";
import { BASE } from "./base";
import { feedCacheScope, scopedKey } from "./feed-cache";
import { LABS_CATALOG, isUnseenLab, unseenLabs, visibleLabs, type LabId } from "./labs-catalog";
import { readTerminalRenderer, subscribeTerminalRenderer } from "./labs";
import { useShownManager } from "./manager-choice";
import { useStore } from "./store";
import type { AssistantInfo } from "./types";

const EMPTY: readonly LabId[] = [];
let pending = new Map<string, readonly LabId[]>();
const fallbackMemory = new Map<string, string>();
const listeners = new Set<() => void>();
const notify = () => listeners.forEach((listener) => listener());
const storageChanged = ({ key }: StorageEvent) => {
  if (key === null || key.includes(":labs-seen:")) notify();
};

function subscribe(listener: () => void) {
  if (!listeners.size) window.addEventListener("storage", storageChanged);
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (!listeners.size) window.removeEventListener("storage", storageChanged);
  };
}

onCredentialChange(() => {
  pending = new Map();
  fallbackMemory.clear();
  notify();
});

function fallbackKey(manager: string) {
  return scopedKey(feedCacheScope(), `labs-seen:${manager}`) ?? `persona${BASE}:labs-seen:${manager}`;
}

function readFallbackRaw(key: string): string {
  try {
    return fallbackMemory.get(key) ?? localStorage.getItem(key) ?? "";
  } catch {
    return fallbackMemory.get(key) ?? "";
  }
}

function fallbackIds(raw: string): readonly LabId[] {
  if (raw) {
    try {
      const ids: unknown = JSON.parse(raw);
      if (Array.isArray(ids)) return LABS_CATALOG.filter((id) => ids.includes(id));
    } catch { /* Unreadable older-hub receipts leave the experiments new. */ }
  }
  return EMPTY;
}

export function saveLocalLabsSeen(manager: string, ids: readonly LabId[]) {
  const key = fallbackKey(manager);
  const raw = JSON.stringify([...new Set([...fallbackIds(readFallbackRaw(key)), ...ids])]);
  try {
    localStorage.setItem(key, raw);
    fallbackMemory.delete(key);
  } catch {
    fallbackMemory.set(key, raw);
  }
  notify();
}

export function pendingLabsSeen(manager: string, ids: readonly LabId[]) {
  const writes = pending;
  const waiting = writes.get(manager) ?? EMPTY;
  const requested = ids.filter((id) => !waiting.includes(id));
  if (!requested.length) return null;
  writes.set(manager, [...waiting, ...requested]);
  notify();
  return {
    ids: requested,
    settle: () => {
      if (pending !== writes) return;
      const left = writes.get(manager)?.filter((id) => !requested.includes(id));
      if (left?.length) writes.set(manager, left);
      else writes.delete(manager);
      notify();
    },
  };
}

const webglEnabled = () => readTerminalRenderer().value === "webgl";

function matches(assistant: AssistantInfo | null, manager: string): assistant is AssistantInfo {
  return typeof assistant?.machine_id === "string" && (!assistant.manager || assistant.manager === manager);
}

function useLabsInputs(manager: string, legacy: boolean) {
  const key = legacy ? fallbackKey(manager) : null;
  const fallback = fallbackIds(useSyncExternalStore(subscribe, () => key ? readFallbackRaw(key) : "", () => ""));
  const waiting = useSyncExternalStore(subscribe, () => pending.get(manager) ?? EMPTY, () => EMPTY);
  const webgl = useSyncExternalStore(subscribeTerminalRenderer, webglEnabled, () => false);
  return { fallback, waiting, webgl };
}

export function useHasUnseenLabs(): boolean {
  const manager = useShownManager();
  const legacy = useStore(({ assistant, labsSeen }) => matches(assistant, manager) && labsSeen === null);
  const { fallback, waiting, webgl } = useLabsInputs(manager, legacy);
  return useStore(({ assistant, labsSeen }) => labsSeen !== undefined && matches(assistant, manager)
    && LABS_CATALOG.some((id) => isUnseenLab(id, assistant, webgl, labsSeen ?? fallback, waiting)));
}

export function useLabsState() {
  const manager = useShownManager();
  const confirmed = useStore(({ assistant, labsSeen }) => matches(assistant, manager) ? labsSeen : undefined);
  const { fallback, waiting, webgl } = useLabsInputs(manager, confirmed === null);
  const visible = useStore(({ assistant }) => matches(assistant, manager) ? visibleLabs(assistant) : EMPTY);
  const unseen = useStore(({ assistant, labsSeen }) => labsSeen !== undefined && matches(assistant, manager) ? unseenLabs(assistant, webgl, labsSeen ?? fallback, waiting) : EMPTY);
  return { visible, unseen, seen: confirmed ?? fallback, waiting, synced: Array.isArray(confirmed), ready: confirmed !== undefined };
}
