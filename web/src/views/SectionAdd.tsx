import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore, type MouseEvent, type ReactNode } from "react";
import { href, type Route } from "../lib/router";
import { sameValue } from "../lib/stable-lines";
import { cx } from "../ui/atoms";
import { Icon, type IconName } from "../ui/icons";
import type { GoFn } from "./SidebarParts";

export interface SectionAddViewProps {
  label: string;
  icon?: IconName;
  active?: boolean;
  className?: string;
  /** Resolved link URL; when supplied, renders an `<a>` instead of a `<button>`. */
  href?: string;
  onClick?: (event: MouseEvent<HTMLElement>) => void;
  disabled?: boolean;
}

export interface SectionAddDeps {
  /** Optional scope partition (value or getter); switching it resets busy state and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled link href or route formatter (bypasses `router.href` when supplied). */
  href?: string | ((to?: Route) => string | undefined);
  /** Subscribe to external href changes when `href` is a function. */
  subscribeHref?: (listener: () => void) => () => void;
  /** Controlled navigation handler used when `to` is present (overrides `go`). */
  onNavigate?: (to: Route, event: MouseEvent<HTMLElement>, signal: AbortSignal) => void | Promise<unknown>;
  /** Controlled click override (bypasses `go` and `onClick` when supplied). */
  onAction?: (event: MouseEvent<HTMLElement>, signal: AbortSignal) => void | Promise<unknown>;
  /** Error observer invoked when a controlled click/navigation callback throws or rejects in the active scope. */
  onError?: (error: unknown) => void;
  /** Optional custom view renderer (defaults to `<SectionAddView>`). */
  renderView?: (props: SectionAddViewProps) => ReactNode;
}

export interface SectionAddProps {
  label: string;
  to?: Route;
  go?: GoFn;
  onClick?: (event: MouseEvent<HTMLElement>) => void;
  icon?: IconName;
  active?: boolean;
  className?: string;
  deps?: SectionAddDeps;
}

const noopSubscribe = () => () => {};

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return value !== null && (typeof value === "object" || typeof value === "function") && typeof (value as PromiseLike<unknown>).then === "function";
}

function useTrackedScope(
  source: string | (() => string) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
) {
  const isGetter = typeof source === "function";
  const read = useCallback(() => (typeof source === "function" ? source() : (source ?? "")), [source]);
  const readRef = useRef(read);
  const committedRef = useRef<{ value: string; version: number } | null>(null);
  const snapRef = useRef<{ value: string; version: number } | null>(null);
  const sub = useCallback((listener: () => void) => {
    if (!subscribe || !isGetter) return noopSubscribe();
    return subscribe(() => {
      const next = readRef.current();
      const cur = committedRef.current;
      if (!cur || next !== cur.value) {
        committedRef.current = { value: next, version: (cur?.version ?? 0) + 1 };
      }
      listener();
    });
  }, [subscribe, isGetter]);
  const getSnapshot = useCallback(() => {
    const next = read();
    const cur = committedRef.current;
    if (cur && next === cur.value) return cur;
    const nextVersion = cur ? cur.version + 1 : 0;
    if (snapRef.current && snapRef.current.value === next && snapRef.current.version === nextVersion) {
      return snapRef.current;
    }
    snapRef.current = { value: next, version: nextVersion };
    return snapRef.current;
  }, [read]);
  const snap = useSyncExternalStore(sub, getSnapshot, getSnapshot);
  useLayoutEffect(() => {
    readRef.current = read;
    committedRef.current = snap;
  }, [read, snap]);
  const readToken = useCallback(() => {
    const cur = readRef.current();
    const committed = committedRef.current;
    return committed && committed.value === cur ? `${committed.version}:${cur}` : `drift:${cur}`;
  }, []);
  return { value: snap.value, token: `${snap.version}:${snap.value}`, readToken };
}

function useCachedSnapshot<T>(read: () => T, subscribe: ((listener: () => void) => () => void) | undefined): T {
  const cache = useRef<{ has: boolean; value: T | undefined }>({ has: false, value: undefined });
  const getSnapshot = useCallback((): T => {
    const next = read();
    if (cache.current.has && sameValue(cache.current.value, next)) {
      return cache.current.value as T;
    }
    cache.current = { has: true, value: next };
    return next;
  }, [read]);
  return useSyncExternalStore(subscribe ?? noopSubscribe, getSnapshot, getSnapshot);
}

/** Pure presentational view for a sidebar section's add / compose trigger. */
export function SectionAddView({
  label,
  icon: name = "plus",
  active,
  className,
  href: linkHref,
  onClick,
  disabled,
}: SectionAddViewProps) {
  const cls = cx("touch-hit relative z-10 flex size-5 shrink-0 items-center justify-center rounded-[5px] hover:bg-hover hover:text-ink-2", className, active ? "bg-hover text-ink" : "text-ink-3");
  const icon = <Icon name={name} size={12} stroke={2} />;
  if (linkHref !== undefined) {
    return (
      <a href={linkHref} onClick={onClick} title={label} aria-label={label} aria-current={active ? "page" : undefined} aria-disabled={disabled || undefined} className={cls}>
        {icon}
      </a>
    );
  }
  return (
    <button type="button" disabled={disabled} onClick={onClick} title={label} aria-label={label} className={cls}>
      {icon}
    </button>
  );
}

/** `className`: added to its own (a row packing it beside other icons gives it a touch screen's 44 px box). */
export function SectionAdd({ label, to, go, onClick, icon = "plus", active, className, deps }: SectionAddProps) {
  const trackedScope = useTrackedScope(deps?.scopeKey, deps?.subscribeScopeKey);

  const hrefDep = deps?.href;
  const readControlledHref = useCallback((): string | undefined => {
    if (hrefDep === undefined) return undefined;
    return typeof hrefDep === "function" ? hrefDep(to) : hrefDep;
  }, [hrefDep, to]);
  const controlledHref = useCachedSnapshot(readControlledHref, typeof hrefDep === "function" ? deps?.subscribeHref : undefined);
  const linkHref = hrefDep !== undefined
    ? controlledHref
    : (to && (go || deps?.onNavigate) ? href(to) : undefined);

  const actionScope = JSON.stringify([trackedScope.token, linkHref ?? null]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: actionScope, epoch: 1 }));
  const effectiveEpoch = scope.key === actionScope ? scope.epoch : scope.epoch + 1;
  if (scope.key !== actionScope) {
    setScope({ key: actionScope, epoch: effectiveEpoch });
  }

  const [busyEpoch, setBusyEpoch] = useState<number | null>(null);
  const busy = busyEpoch === effectiveEpoch;

  const activeEpochRef = useRef(0);
  const opSeqRef = useRef(0);
  const pendingRef = useRef<{ epoch: number; seq: number; controller: AbortController } | null>(null);
  const depsRef = useRef(deps);
  const toRef = useRef(to);
  const goRef = useRef(go);
  const onClickRef = useRef(onClick);
  const readScopeTokenRef = useRef(trackedScope.readToken);

  useLayoutEffect(() => {
    depsRef.current = deps;
    toRef.current = to;
    goRef.current = go;
    onClickRef.current = onClick;
    readScopeTokenRef.current = trackedScope.readToken;
  });

  useLayoutEffect(() => {
    activeEpochRef.current = effectiveEpoch;
    return () => {
      if (activeEpochRef.current === effectiveEpoch) activeEpochRef.current = 0;
      pendingRef.current?.controller.abort();
      pendingRef.current = null;
      opSeqRef.current += 1;
    };
  }, [effectiveEpoch]);

  const capturedEpoch = effectiveEpoch;
  const capturedScopeToken = trackedScope.token;

  const hasHandler = Boolean(deps?.onAction || (to && (deps?.onNavigate || go)) || onClick);
  const handleClick = (event: MouseEvent<HTMLElement>) => {
    if (activeEpochRef.current !== capturedEpoch || readScopeTokenRef.current() !== capturedScopeToken) {
      if (linkHref !== undefined) event.preventDefault();
      return;
    }
    if (pendingRef.current !== null) {
      if (linkHref !== undefined) event.preventDefault();
      return;
    }
    const curDeps = depsRef.current;
    const controlledAction = curDeps?.onAction !== undefined || (toRef.current !== undefined && curDeps?.onNavigate !== undefined);
    const controller = new AbortController();
    const runSeq = ++opSeqRef.current;
    const isRunCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === capturedEpoch
      && opSeqRef.current === runSeq
      && readScopeTokenRef.current() === capturedScopeToken;

    const reportError = (err: unknown) => {
      if (!isRunCurrent()) return;
      try {
        depsRef.current?.onError?.(err);
      } catch {
        // Error observer failures stay non-blocking so recovery never leaves busy stuck.
      }
    };

    let result: unknown;
    try {
      if (curDeps?.onAction) {
        result = curDeps.onAction(event, controller.signal);
      } else if (curDeps?.onNavigate && toRef.current) {
        result = curDeps.onNavigate(toRef.current, event, controller.signal);
      } else if (toRef.current && goRef.current) {
        result = goRef.current(toRef.current)(event);
      } else if (onClickRef.current) {
        result = onClickRef.current(event);
      } else {
        return;
      }
    } catch (err) {
      if (curDeps?.onError) reportError(err);
      else if (!controlledAction) throw err;
      return;
    }

    if (!isThenable(result)) return;
    pendingRef.current = { epoch: capturedEpoch, seq: runSeq, controller };
    setBusyEpoch(capturedEpoch);
    void (async () => {
      try {
        await result;
      } catch (err) {
        reportError(err);
      } finally {
        if (pendingRef.current?.seq === runSeq) pendingRef.current = null;
        if (activeEpochRef.current === capturedEpoch && opSeqRef.current === runSeq) {
          setBusyEpoch((cur) => (cur === capturedEpoch ? null : cur));
        }
      }
    })();
  };

  const viewProps: SectionAddViewProps = {
    label,
    icon,
    active,
    className,
    href: linkHref,
    onClick: hasHandler ? handleClick : undefined,
    disabled: busy || undefined,
  };
  if (deps?.renderView) return <>{deps.renderView(viewProps)}</>;
  return <SectionAddView {...viewProps} />;
}
