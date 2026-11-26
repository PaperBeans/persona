// Shared channel detail controller: pins manager scope, sequences reads, serializes writes, and fences channel switches.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import {
  channelDetail, forgetChannels, testChannel, unbindChannel, updateChannel,
  type ChannelDetail, type ChannelUpdateBody,
} from "../../lib/channels";
import { otherManager, pinManager } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";

export interface ChannelController {
  detail: ChannelDetail | null;
  error: string;
  loading: boolean;
  busy: string;
  reloadKey: number;
  testFeedback: { tone: "ok" | "error"; text: string } | null;
  load: () => Promise<void>;
  change: (what: string, body: ChannelUpdateBody) => Promise<void>;
  unbind: () => Promise<boolean>;
  runTest: () => Promise<void>;
  updateRouting: (body: Pick<ChannelUpdateBody, "thread_mode" | "thread_idle_hours">) => Promise<ChannelDetail | null>;
}

function notifyError(error: unknown) {
  toast(errorText(error), "error");
}

/** Loads and mutates one channel with request sequencing, exclusive writes, and channel-switch fencing. */
export function useChannelController(channelId: string | null, manager?: string): ChannelController {
  const owner = otherManager(manager);
  const scope = useMemo(() => ({}), [channelId, owner]);
  const scopeRef = useRef(scope);
  scopeRef.current = scope;

  useLayoutEffect(() => {
    if (!channelId) return undefined;
    return pinManager(`/channels/${encodeURIComponent(channelId)}`, owner);
  }, [channelId, owner]);

  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [testFeedback, setTestFeedback] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  const mounted = useRef(true);
  const epochRef = useRef(0);
  const seq = useRef(0);
  const loadSeq = useRef(0);
  const loadedGeneration = useRef(0);
  const activeWrite = useRef(0);
  const detailRef = useRef<ChannelDetail | null>(null);
  detailRef.current = detail;

  const load = useCallback(async () => {
    const epoch = epochRef.current;
    const n = ++seq.current;
    const r = ++loadSeq.current;
    if (!channelId) {
      setDetail(null);
      setError("");
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const next = await channelDetail(channelId);
      if (!mounted.current || epoch !== epochRef.current || scope !== scopeRef.current || n !== seq.current) return;
      setDetail(next);
      setError("");
      loadedGeneration.current++;
      setReloadKey(loadedGeneration.current);
    } catch (e) {
      if (mounted.current && epoch === epochRef.current && scope === scopeRef.current && n === seq.current) {
        setError(errorText(e));
      }
    } finally {
      if (mounted.current && epoch === epochRef.current && scope === scopeRef.current && r === loadSeq.current) {
        setLoading(false);
      }
    }
  }, [channelId, scope]);

  useEffect(() => {
    mounted.current = true;
    epochRef.current++;
    activeWrite.current = 0;
    setDetail(null);
    setError("");
    setBusy("");
    setTestFeedback(null);
    void load();
    return () => {
      mounted.current = false;
      epochRef.current++;
      seq.current++;
      loadSeq.current++;
      activeWrite.current = 0;
    };
  }, [load]);

  const exclusive = useCallback(async <T extends ChannelDetail | void>(what: string, save: () => Promise<T>) => {
    const epoch = epochRef.current;
    const current = () => mounted.current && scope === scopeRef.current && epoch === epochRef.current;
    if (!channelId || !current() || activeWrite.current !== 0) return null;
    const n = ++seq.current;
    activeWrite.current = n;
    setBusy(what);
    try {
      const next = await save();
      if (next) forgetChannels();
      if (!current() || (next && next.id !== channelId)) return null;
      if (next && n !== seq.current) {
        // A refresh raced the write. Re-read its committed result without publishing the stale response.
        void load();
        return { snapshot: null };
      }
      if (next) setDetail(next);
      return { snapshot: next };
    } catch (error) {
      if (current()) throw error;
      return null;
    } finally {
      if (activeWrite.current === n) activeWrite.current = 0;
      if (current()) setBusy("");
    }
  }, [channelId, load, scope]);

  const updateRouting = useCallback<ChannelController["updateRouting"]>(async (body) => {
    if (!channelId || reloadKey !== loadedGeneration.current) return null;
    const result = await exclusive("routing", () => updateChannel(channelId, body));
    return result?.snapshot ?? null;
  }, [channelId, reloadKey, exclusive]);

  const change = useCallback(async (what: string, body: ChannelUpdateBody) => {
    if (!channelId) return;
    await exclusive(what, () => updateChannel(channelId, body)).catch(notifyError);
  }, [channelId, exclusive]);

  const unbind = useCallback(async () => {
    if (!channelId) return false;
    const result = await exclusive("unbind", () => unbindChannel(channelId)).catch(notifyError);
    return !!result;
  }, [channelId, exclusive]);

  const runTest = useCallback(async () => {
    const current = detailRef.current;
    if (!channelId || !current) return;
    const epoch = epochRef.current;
    const replyOnly = !!current.capabilities.reply_only;
    await exclusive("test", async () => {
      if (mounted.current && epoch === epochRef.current) setTestFeedback(null);
      try {
        const res = await testChannel(channelId);
        const text = replyOnly || res?.reply_only
          ? (res?.note || t("settings-channels.connection_ok", { name: current.name }))
          : (res?.note || t("settings-channels.test_sent", { name: current.name }));
        if (mounted.current && epoch === epochRef.current) {
          setTestFeedback({ tone: "ok", text });
          toast(text, "ok");
        }
      } catch (e) {
        if (mounted.current && epoch === epochRef.current) {
          setTestFeedback({ tone: "error", text: errorText(e) });
        }
        throw e;
      }
    }).catch(notifyError);
  }, [channelId, exclusive]);

  return { detail, error, loading, busy, reloadKey, testFeedback, load, change, unbind, runTest, updateRouting };
}
