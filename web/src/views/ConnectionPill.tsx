// The pill over the page once the socket has dropped (after the app's ConnectionTitle): which phase ("No network" /
// "Connecting…" / "Hub is updating…" / "Can't reach the hub, retrying…"), and on a click the details (why, the next try, since when) with
// "Retry now". The page that never reached the hub has its own empty state (App.tsx Offline) instead.
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode, type Ref } from "react";
import { t } from "../i18n";
import { BREATHING, connFacts, connPhase, phaseExplanation, phaseTitle, phaseTone, type ConnLink, type ConnPhase } from "../lib/connection";
import { reconnectNow, useStore } from "../lib/store";
import type { Conn } from "../lib/store";
import { Button, cx, EASE, useNow } from "../ui/atoms";

export interface ConnectionState {
  conn: Conn;
  link: ConnLink;
}

export interface ConnectionDetailsDeps {
  /** Trigger an immediate reconnect attempt (defaults to `store.reconnectNow`). */
  reconnectNow?: () => void;
  /** Fixed timestamp or clock reader (ms) for relative timing facts (disables `useNow` interval when supplied). */
  now?: number | (() => number);
}

export interface ConnectionPillDeps extends ConnectionDetailsDeps {
  /** Controlled connection snapshot or reader (bypasses `useStore` when supplied). */
  connection?: ConnectionState | (() => ConnectionState);
  /** Controlled `conn` value (bypasses `useStore(s => s.conn)` when supplied). */
  conn?: Conn;
  /** Controlled `link` value (bypasses `useStore(s => s.link)` when supplied). */
  link?: ConnLink;
  /** Subscribe to external connection updates when `connection` is a reader. */
  subscribe?: (listener: () => void) => () => void;
}

export interface ConnectionPillViewProps {
  conn: Conn;
  link: ConnLink;
  open: boolean;
  onToggle: () => void;
  /** Custom details popover node when `open` (defaults to `<ConnectionDetails>`). */
  details?: ReactNode;
  /** Ref attached to the pill wrapper for outside-click containment. */
  containerRef?: Ref<HTMLDivElement>;
  deps?: ConnectionDetailsDeps;
}

export interface ConnectionDetailsViewProps {
  conn: Conn;
  link: ConnLink;
  now: number;
  trying: boolean;
  onRetry: () => void;
}

/** The dot's color and whether it breathes: still trying vs given up for now. */
function Dot({ phase }: { phase: ConnPhase }) {
  const tone = phaseTone(phase);
  return <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", tone.color)} style={tone.breathing ? BREATHING : undefined} />;
}

/** Pure presentational view for the floating connection pill and optional details popover. */
export function ConnectionPillView({ conn, link, open, onToggle, details, containerRef, deps }: ConnectionPillViewProps) {
  const phase = connPhase(conn, link);
  const title = phaseTitle(phase);
  return (
    <div ref={containerRef} className="pointer-events-auto relative flex flex-col items-center">
      <button
        type="button"
        onClick={onToggle}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={t("lib-connection.show_details", { state: title })}
        data-connection-pill
        className="flex items-center gap-2 rounded-full bg-tooltip-bg px-3 py-1.5 text-[12px] whitespace-nowrap text-tooltip-fg shadow-overlay transition-[opacity,transform] duration-150 hover:opacity-90 active:scale-[0.97]"
      >
        <Dot phase={phase} />
        {title}
      </button>
      {open && (details ?? <ConnectionDetails conn={conn} link={link} deps={deps} />)}
    </div>
  );
}

export function ConnectionPill({ deps }: { deps?: ConnectionPillDeps } = {}) {
  if (deps?.connection !== undefined) {
    if (deps.subscribe && typeof deps.connection === "function") {
      return <SubscribedConnectionPill read={deps.connection} subscribe={deps.subscribe} deps={deps} />;
    }
    const state = typeof deps.connection === "function" ? deps.connection() : deps.connection;
    return <ConnectionPillBody conn={state.conn} link={state.link} deps={deps} />;
  }
  if (deps?.conn !== undefined && deps?.link !== undefined) {
    return <ConnectionPillBody conn={deps.conn} link={deps.link} deps={deps} />;
  }
  if (deps?.conn !== undefined) return <LinkOnlyStoreConnectionPill conn={deps.conn} deps={deps} />;
  if (deps?.link !== undefined) return <ConnOnlyStoreConnectionPill link={deps.link} deps={deps} />;
  return <DefaultStoreConnectionPill deps={deps} />;
}

function DefaultStoreConnectionPill({ deps }: { deps?: ConnectionPillDeps }) {
  const conn = useStore((s) => s.conn);
  const link = useStore((s) => s.link);
  return <ConnectionPillBody conn={conn} link={link} deps={deps} />;
}

function LinkOnlyStoreConnectionPill({ conn, deps }: { conn: Conn; deps: ConnectionPillDeps }) {
  const link = useStore((s) => s.link);
  return <ConnectionPillBody conn={conn} link={link} deps={deps} />;
}

function ConnOnlyStoreConnectionPill({ link, deps }: { link: ConnLink; deps: ConnectionPillDeps }) {
  const conn = useStore((s) => s.conn);
  return <ConnectionPillBody conn={conn} link={link} deps={deps} />;
}

function sameConnLink(a: ConnLink, b: ConnLink): boolean {
  return a === b || (
    a.lastOnline === b.lastOnline
    && a.nextTry === b.nextTry
    && a.restarting === b.restarting
    && a.networkUp === b.networkUp
    && a.lastError === b.lastError
  );
}

function SubscribedConnectionPill({
  read, subscribe, deps,
}: { read: () => ConnectionState; subscribe: (listener: () => void) => () => void; deps: ConnectionPillDeps }) {
  const cache = useRef<ConnectionState | null>(null);
  const getSnapshot = useCallback(() => {
    const next = read();
    const prev = cache.current;
    if (prev && prev.conn === next.conn && sameConnLink(prev.link, next.link)) return prev;
    cache.current = next;
    return next;
  }, [read]);
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <ConnectionPillBody conn={state.conn} link={state.link} deps={deps} />;
}

function ConnectionPillBody({ conn, link, deps }: { conn: Conn; link: ConnLink; deps?: ConnectionDetailsDeps }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const down = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    addEventListener("pointerdown", down, true);
    addEventListener("keydown", key);
    return () => {
      removeEventListener("pointerdown", down, true);
      removeEventListener("keydown", key);
    };
  }, [open]);

  return <ConnectionPillView conn={conn} link={link} open={open} onToggle={() => setOpen((o) => !o)} containerRef={ref} deps={deps} />;
}

/** Pure presentational view for the connection details popover. */
export function ConnectionDetailsView({ conn, link, now, trying, onRetry }: ConnectionDetailsViewProps) {
  const phase = connPhase(conn, link);
  const facts = connFacts(phase, link, now);
  return (
    <div
      role="dialog"
      aria-label={phaseTitle(phase)}
      data-connection-details
      className="absolute top-full z-20 mt-2 w-[300px] max-w-[calc(100vw-32px)] rounded-card bg-surface p-4 text-left shadow-overlay"
      style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "top center" }}
    >
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <Dot phase={phase} />
          <span className="min-w-0 truncate text-[13.5px] font-medium text-ink">{phaseTitle(phase)}</span>
        </div>
        <p className="text-[12.5px] leading-relaxed text-ink-2">{phaseExplanation(phase)}</p>
        {facts.length > 0 && (
          <ul className="flex flex-col gap-1 text-[11.5px] text-ink-3 tabular-nums">
            {facts.map((f) => <li key={f}>{f}</li>)}
          </ul>
        )}
        {phase !== "online" && (
          <Button tone="primary" icon="retry" busy={trying} onClick={onRetry} className="w-full">
            {t("lib-connection.retry_now")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** Why the pill says what it says, the next try and since when, and "Retry now". `now` fixed: tests and screenshots. */
export function ConnectionDetails({ conn, link, now: fixed, deps }: { conn: Conn; link: ConnLink; now?: number; deps?: ConnectionDetailsDeps }) {
  const fixedNow = fixed ?? deps?.now;
  const tick = useNow(1000, fixedNow === undefined);
  const now = fixedNow === undefined ? tick : typeof fixedNow === "function" ? fixedNow() : fixedNow;
  // a click shows that it is trying for a moment (the socket answers within that, or the next try's line says when)
  const [trying, setTrying] = useState(false);
  const tryingRef = useRef(false);
  useEffect(() => {
    if (!trying) {
      tryingRef.current = false;
      return;
    }
    const id = setTimeout(() => {
      tryingRef.current = false;
      setTrying(false);
    }, 900);
    return () => clearTimeout(id);
  }, [trying]);
  useEffect(() => () => { tryingRef.current = false; }, []);
  const onRetry = () => {
    if (tryingRef.current) return;
    tryingRef.current = true;
    setTrying(true);
    (deps?.reconnectNow ?? reconnectNow)();
  };
  return <ConnectionDetailsView conn={conn} link={link} now={now} trying={trying} onRetry={onRetry} />;
}
