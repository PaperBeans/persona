// What the switch divider's chip opens (#5054, EngineSwitchDivider.tsx): the handoff the new engine was given, under a
// title saying what it is, selectable; the rest of a handoff the hub cut for the page loads on "View full text". Off the
// first screen: the divider loads it as it is drawn, so it is in before the chip is first pressed.
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ENGINE_SWITCH_KIND, engineSwitchOf, type EngineSwitch } from "../../lib/engine-switch";
import { bytes } from "../../lib/format";
import { withoutCutMark } from "../../lib/tool-detail";
import { parseUserText } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { t } from "../../i18n";
import { cx, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { useFullText } from "./FullText";

/** The handoff in the whole text of the message, once fetched. */
function wholeHandoff(raw: string): string | undefined {
  for (const c of parseUserText(raw).contexts) if (c.kind === ENGINE_SWITCH_KIND) return engineSwitchOf(c)?.text;
  return undefined;
}

export type EngineHandoffFullItem = string | Pick<Item, "text" | "source_fingerprint">;

export interface EngineHandoffPanelProps {
  /** The panel's DOM id (referenced by the switch chip's `aria-controls`). */
  id: string;
  sw: EngineSwitch;
  /** The message's transcript item, for the rest of a handoff the hub cut for the page (`full_size`). */
  item?: Item;
  /** Session machine id for the default full-text fetcher; also scopes controlled full-text state when provided. */
  mid?: string;
  /** Session id for the default full-text fetcher; also scopes controlled full-text state when provided. */
  sid?: string;
  /** Optional stable source scope for controlled full-text state when `mid`/`sid` are absent or do not identify the loader source. */
  scopeKey?: string;
  /** Explicit complete handoff text owned by a controlled panel; skips the default full-text loader. */
  fullText?: string;
  /** Controlled loader for a truncated item (`item.full_size`); replaces the default `fullItem` API call. */
  loadFullItem?: (item: Item) => Promise<EngineHandoffFullItem>;
  /** Optional custom footer control (or `null`) when a controlled panel owns full-text loading UI itself. */
  more?: ReactNode;
  /** Optional notification when `loadFullItem` rejects or fails fingerprint validation. */
  onLoadError?: (error: unknown) => void;
}

function resolveLoadedHandoff(cut: Item, result: EngineHandoffFullItem): string | undefined {
  if (typeof result === "string") {
    return wholeHandoff(result) ?? (result || undefined);
  }
  if (cut.source_fingerprint && cut.source_fingerprint !== result.source_fingerprint) {
    throw new Error(t("chat-fulltext.failed"));
  }
  return wholeHandoff(result.text ?? "");
}

function isControlled(props: EngineHandoffPanelProps): boolean {
  return props.fullText !== undefined || props.loadFullItem !== undefined || props.more !== undefined;
}

/** `id`: the panel's (the chip's aria-controls). `item`: the message's transcript item, for the rest of a handoff the
 *  hub cut for the page (`full_size`). */
export function EngineHandoffPanel(props: EngineHandoffPanelProps) {
  if (isControlled(props)) return <ControlledEngineHandoffPanel {...props} />;
  return <DefaultEngineHandoffPanel id={props.id} sw={props.sw} item={props.item} mid={props.mid} sid={props.sid} />;
}

function DefaultEngineHandoffPanel({ id, sw, item, mid, sid }: Pick<EngineHandoffPanelProps, "id" | "sw" | "item" | "mid" | "sid">) {
  const cut = item?.full_size ? item : undefined;
  const full = useFullText(cut, mid, sid);
  const handoff = cut ? ((!full.more && wholeHandoff(full.text)) || withoutCutMark(sw.text)) : sw.text;
  return <EngineHandoffPanelView id={id} handoff={handoff} more={full.more} />;
}

function controlledFullTextKey(
  cut: Item | undefined,
  canLoad: boolean,
  mid: string | undefined,
  sid: string | undefined,
  scopeKey: string | undefined,
): string {
  if (!cut || !canLoad) return "";
  return JSON.stringify([
    scopeKey ?? null,
    mid ?? null,
    sid ?? null,
    cut.id,
    cut.text ?? null,
    cut.full_size,
    cut.source_fingerprint ?? null,
  ]);
}

function ControlledEngineHandoffPanel({
  id,
  sw,
  item,
  mid,
  sid,
  scopeKey,
  fullText,
  loadFullItem,
  more,
  onLoadError,
}: EngineHandoffPanelProps) {
  const cut = item?.full_size ? item : undefined;
  const canLoad = Boolean(cut && loadFullItem && fullText === undefined && more === undefined);
  const key = controlledFullTextKey(cut, canLoad, mid, sid, scopeKey);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key, epoch: 1 }));
  const [loaded, setLoaded] = useState<{ epoch: number; text: string } | null>(null);
  const [status, setStatus] = useState<{ epoch: number; state: "idle" | "loading" | "failed" } | null>(null);
  if (scope.key !== key) {
    setScope({ key, epoch: scope.epoch + 1 });
    setLoaded(null);
    setStatus(null);
  }
  const activeEpoch = useRef(0);
  const pending = useRef<{ epoch: number; promise: Promise<void> } | null>(null);
  const onLoadErrorRef = useRef(onLoadError);

  useLayoutEffect(() => {
    onLoadErrorRef.current = onLoadError;
  }, [onLoadError]);

  useLayoutEffect(() => {
    activeEpoch.current = scope.epoch;
    return () => {
      activeEpoch.current = 0;
      if (pending.current?.epoch === scope.epoch) pending.current = null;
    };
  }, [scope.epoch]);

  const state = status?.epoch === scope.epoch ? status.state : "idle";
  const resolved = loaded?.epoch === scope.epoch ? loaded.text : undefined;

  const load = () => {
    const loadEpoch = scope.epoch;
    if (!cut || !loadFullItem || !canLoad || resolved !== undefined || pending.current?.epoch === loadEpoch) return;
    setStatus({ epoch: loadEpoch, state: "loading" });
    let request: Promise<EngineHandoffFullItem>;
    try {
      request = Promise.resolve(loadFullItem(cut));
    } catch (error) {
      request = Promise.reject(error);
    }
    const promise = request
      .then((result) => {
        if (activeEpoch.current !== loadEpoch) return;
        const extracted = resolveLoadedHandoff(cut, result);
        setLoaded({ epoch: loadEpoch, text: extracted || withoutCutMark(sw.text) });
        setStatus({ epoch: loadEpoch, state: "idle" });
      })
      .catch((error: unknown) => {
        if (activeEpoch.current === loadEpoch) {
          setStatus({ epoch: loadEpoch, state: "failed" });
          onLoadErrorRef.current?.(error);
        }
      })
      .finally(() => {
        if (pending.current?.promise === promise) pending.current = null;
      });
    pending.current = { epoch: loadEpoch, promise };
  };

  const handoff = fullText !== undefined ? fullText : cut ? (resolved ?? withoutCutMark(sw.text)) : sw.text;
  const footer =
    more !== undefined
      ? more
      : fullText === undefined && cut && loadFullItem && resolved === undefined
        ? <HandoffFullTextButton size={cut.full_size!} state={state} onClick={load} />
        : null;

  return <EngineHandoffPanelView id={id} handoff={handoff} more={footer} />;
}

function EngineHandoffPanelView({ id, handoff, more }: { id: string; handoff: string; more: ReactNode }) {
  return (
    <div id={id} className="mx-auto mt-2 max-w-full rounded-card border border-line-strong bg-inset px-4 py-3 select-text" data-engine-handoff>
      <div className="mb-1.5 text-[11px] font-medium text-ink-3">{t("chat-engine.handoff_title")}</div>
      <pre className="max-h-64 overflow-y-auto font-mono text-[12px] leading-[1.55] whitespace-pre-wrap text-tool-ink [overflow-wrap:anywhere]">{handoff}</pre>
      {more}
    </div>
  );
}

function HandoffFullTextButton({ size, state, onClick }: { size: number; state: "idle" | "loading" | "failed"; onClick: () => void }) {
  const loading = state === "loading";
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        if (!loading) onClick();
      }}
      disabled={loading}
      aria-busy={loading || undefined}
      className={cx(
        "mt-1.5 flex h-7 w-fit items-center gap-1.5 rounded-control px-1 font-sans text-label font-medium whitespace-nowrap transition-colors hover:bg-hover disabled:opacity-60",
        state === "failed" ? "text-red" : "text-accent-ink",
      )}
      data-full-text={state}
    >
      {loading ? <Spinner size={12} /> : <Icon name={state === "failed" ? "retry" : "external"} size={12} />}
      {loading ? t("chat-fulltext.loading") : state === "failed" ? t("chat-fulltext.failed") : t("chat-fulltext.show_all", { size: bytes(size) })}
    </button>
  );
}
