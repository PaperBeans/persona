// Shared request, polling, toggle and action state for a machine's extension control sections (BrowserSection,
// ComputerSection, IPhoneSection): one latest-wins request line per machine (#5455), visibility-aware polling,
// and switch/action error handling.
import { useEffect, useRef, useState } from "react";
import { api, enc, errorText } from "../../lib/api";
import { documentPage, latestRequest, visiblePoll } from "../../lib/visible-poll";
import { toast } from "../../ui/atoms";

export interface MachineExtensionControlOptions<T extends { enabled: boolean }> {
  mid: string;
  resource: string;
  initial: T;
  online: boolean;
  readTimeoutMs?: number;
  merge?: (fresh: T | null, initial: T) => T;
  pollEvery: (shown: T) => number;
  toggleError: (on: boolean, error: string) => string;
}

export function useMachineExtensionControl<T extends { enabled: boolean }>({
  mid,
  resource,
  initial,
  online,
  readTimeoutMs,
  merge,
  pollEvery,
  toggleError,
}: MachineExtensionControlOptions<T>) {
  // What the hub said since the page opened (newer than the machine list's).
  const [fresh, setFresh] = useState<T | null>(null);
  const [saving, setSaving] = useState(false);
  const [acting, setActing] = useState(false);
  const [checking, setChecking] = useState(false);

  const shown = merge ? merge(fresh, initial) : (fresh ?? initial);
  const enabled = shown.enabled;
  const pollMs = pollEvery(shown);
  const endpoint = `/machines/${enc(mid)}/${resource}`;

  // One request line per machine: disposed when another machine is shown or the page unmounts, so older answers never land.
  // Declared before the effects that read through it, so it exists when they first run.
  const reads = useRef<ReturnType<typeof latestRequest<T>> | null>(null);
  useEffect(() => {
    setFresh(null);
    // A failed read leaves the machine list's copy (or the last answer) on screen.
    const line = latestRequest<T>(setFresh, () => {});
    reads.current = line;
    return () => {
      line.dispose();
      if (reads.current === line) reads.current = null;
    };
  }, [mid, resource]);

  const refresh = () =>
    reads.current?.run(() =>
      api<T>(endpoint, readTimeoutMs !== undefined ? { timeout: readTimeoutMs, dedupe: false } : { dedupe: false }),
    ) ?? Promise.resolve();
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  // Runs a write and shows its answer as the latest one (an older read still in flight is dropped). If another machine
  // is shown by the time it comes back, both its answer and its failure belong to the machine left behind and are dropped.
  const write = async (send: () => Promise<T>) => {
    const line = reads.current;
    try {
      const value = await send();
      void line?.run(() => Promise.resolve(value));
    } catch (e) {
      if (line && line === reads.current) throw e;
    }
  };

  useEffect(() => {
    if (online && enabled) void refreshRef.current();
  }, [mid, resource, online, enabled]);

  useEffect(() => {
    if (!pollMs) return;
    // visiblePoll asks right away when it starts; the page just asked (on open or with the action that started this).
    let started = false;
    const stop = visiblePoll(() => { if (started) void refreshRef.current(); }, pollMs, documentPage);
    started = true;
    return stop;
  }, [mid, resource, pollMs]);

  const toggle = async (on: boolean) => {
    setSaving(true);
    try {
      await write(() => api<T>(endpoint, { method: "PUT", json: { enabled: on } }));
    } catch (e) {
      toast(toggleError(on, errorText(e)), "error");
    } finally {
      setSaving(false);
    }
  };

  const postAction = async (action: string, json?: Record<string, unknown>) => {
    setActing(true);
    try {
      await write(() => api<T>(`${endpoint}/${action}`, json !== undefined ? { method: "POST", json } : { method: "POST" }));
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      setActing(false);
    }
  };

  const recheck = async () => {
    setChecking(true);
    try {
      await refresh();
    } finally {
      setChecking(false);
    }
  };

  return { shown, enabled, saving, acting, checking, toggle, postAction, recheck };
}
