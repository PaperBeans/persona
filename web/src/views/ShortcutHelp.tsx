// ⌘/: every shortcut with the chord it has now (the user's own, if rebound), grouped by where it works.
import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactElement, type ReactNode } from "react";
import { errorText } from "../lib/api";
import { closeHelp, openShortcutSettings, useHelpOpen } from "../lib/shortcut-help";
import { chordParts, currentPlatform, LOCAL_SHORTCUTS, SCOPE_LABEL, SHORTCUTS, useBindings, useShortcutPrefs, type Bindings, type LocalShortcut, type Platform, type ShortcutDef, type ShortcutPrefs, type ShortcutScope } from "../lib/shortcuts";
import { navigate } from "../lib/router";
import { sameValue } from "../lib/stable-lines";
import { Button, Dialog, Kbd } from "../ui/atoms";
import { t } from "../i18n";

const SCOPES: readonly ShortcutScope[] = ["global", "session", "sidebar"];
const noopSubscribe = () => () => {};

export interface ShortcutHelpViewProps {
  open: boolean;
  onClose: () => void;
  onEditInSettings: () => void;
  bindings: Readonly<Bindings>;
  prefs: Pick<ShortcutPrefs, "off" | "disabled">;
  platform: Platform;
  scopes?: readonly ShortcutScope[];
  scopeLabels?: Readonly<Record<ShortcutScope, string>>;
  shortcuts?: readonly ShortcutDef[];
  localShortcuts?: readonly LocalShortcut[];
  formatChordParts?: (chord: string, platform: Platform) => string[];
  busy?: boolean;
  error?: string;
}

export interface ShortcutHelpDeps {
  /** Optional scope partition (value or getter); switching it resets busy/error state, aborts in-flight actions, and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled open state (value or getter; bypasses `useHelpOpen` when supplied). */
  open?: boolean | (() => boolean);
  /** Subscribe to open state updates when `open` is a getter. */
  subscribeOpen?: (listener: () => void) => () => void;
  /** Controlled active bindings map (value or getter; bypasses `useBindings` when supplied). */
  bindings?: Readonly<Bindings> | (() => Readonly<Bindings>);
  /** Subscribe to bindings updates when `bindings` is a getter. */
  subscribeBindings?: (listener: () => void) => () => void;
  /** Controlled shortcut preferences (value or getter; bypasses `useShortcutPrefs` when supplied). */
  prefs?: Pick<ShortcutPrefs, "off" | "disabled"> | (() => Pick<ShortcutPrefs, "off" | "disabled">);
  /** Subscribe to preferences updates when `prefs` is a getter. */
  subscribePrefs?: (listener: () => void) => () => void;
  /** Controlled platform (value or getter; bypasses `currentPlatform` when supplied). */
  platform?: Platform | (() => Platform);
  /** Subscribe to platform updates when `platform` is a getter. */
  subscribePlatform?: (listener: () => void) => () => void;
  /** Controlled shortcut definitions (value or getter; defaults to `SHORTCUTS`). */
  shortcuts?: readonly ShortcutDef[] | (() => readonly ShortcutDef[]);
  /** Subscribe to shortcut definition updates when `shortcuts` is a getter. */
  subscribeShortcuts?: (listener: () => void) => () => void;
  /** Controlled terminal-fixed shortcut definitions (value or getter; defaults to `LOCAL_SHORTCUTS`). */
  localShortcuts?: readonly LocalShortcut[] | (() => readonly LocalShortcut[]);
  /** Subscribe to local shortcut updates when `localShortcuts` is a getter. */
  subscribeLocalShortcuts?: (listener: () => void) => () => void;
  /** Controlled scope list (value or getter; defaults to `["global", "session", "sidebar"]`). */
  scopes?: readonly ShortcutScope[] | (() => readonly ShortcutScope[]);
  /** Controlled scope headings (value or getter; defaults to `SCOPE_LABEL`). */
  scopeLabels?: Readonly<Record<ShortcutScope, string>> | (() => Readonly<Record<ShortcutScope, string>>);
  /** Custom chord formatter (defaults to `lib/shortcuts.chordParts`). */
  formatChordParts?: (chord: string, platform: Platform) => string[];
  /** Shared fallback subscription for any getter dep without a dedicated subscriber. */
  subscribe?: (listener: () => void) => () => void;
  /** Controlled close handler (sync or async; bypasses `closeHelp` when supplied). */
  onClose?: (signal: AbortSignal) => void | Promise<unknown>;
  /** Controlled "Change in Settings" handler (sync or async; bypasses `openShortcutSettings` and `navigate` when supplied). */
  openSettings?: (signal: AbortSignal) => void | Promise<unknown>;
  /** Controlled router navigation passed to `openShortcutSettings` when `openSettings` is omitted. */
  navigate?: (to: string) => void;
  /** Format thrown action errors (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Error observer invoked when closing or opening settings throws or rejects in the active scope. */
  onError?: (message: string) => void;
  /** Optional custom view renderer (defaults to `<ShortcutHelpView>`). */
  renderView?: (props: ShortcutHelpViewProps) => ReactNode;
}

export interface ShortcutHelpProps {
  open?: boolean;
  onClose?: () => void;
  deps?: ShortcutHelpDeps;
}

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return value !== null && (typeof value === "object" || typeof value === "function") && typeof (value as PromiseLike<unknown>).then === "function";
}

function safeFormatError(fmt: ((error: unknown) => string) | undefined, error: unknown): string {
  if (!fmt) return errorText(error);
  try {
    return fmt(error);
  } catch {
    return errorText(error);
  }
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

function useCachedExternalValue<T>(
  source: T | (() => T) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
): T | undefined {
  const cache = useRef<{ has: boolean; value: T | undefined }>({ has: false, value: undefined });
  const getSnapshot = useCallback((): T | undefined => {
    const next = typeof source === "function" ? (source as () => T)() : source;
    if (cache.current.has && sameValue(cache.current.value, next)) {
      return cache.current.value;
    }
    cache.current = { has: true, value: next };
    return next;
  }, [source]);
  const sub = subscribe && typeof source === "function" ? subscribe : noopSubscribe;
  return useSyncExternalStore(sub, getSnapshot, getSnapshot);
}

/** Pure presentational view for the keyboard shortcuts help dialog. */
export function ShortcutHelpView({
  open,
  onClose,
  onEditInSettings,
  bindings,
  prefs,
  platform,
  scopes = SCOPES,
  scopeLabels = SCOPE_LABEL,
  shortcuts = SHORTCUTS,
  localShortcuts = LOCAL_SHORTCUTS,
  formatChordParts = chordParts,
  busy,
  error,
}: ShortcutHelpViewProps) {
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t("shortcuthelp.title")}
      width={460}
      footer={
        <Button tone="quiet" busy={busy} onClick={onEditInSettings}>
          {t("shortcuthelp.edit_in_settings")}
        </Button>
      }
    >
      <div className="flex flex-col gap-4 pt-1">
        {error ? <p role="alert" className="text-submeta text-red">{error}</p> : null}
        {scopes.map((scope) => (
          <section key={scope} aria-label={scopeLabels[scope]}>
            <h3 className="mb-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{scopeLabels[scope]}</h3>
            <ul className="divide-y divide-line">
              {shortcuts.filter((s) => s.scope === scope).map((s) => (
                <li key={s.id} className="flex items-center gap-3 py-1.5">
                  <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{s.label}</span>
                  <span className="flex shrink-0 items-center gap-1">
                    {bindings[s.id] ? (
                      formatChordParts(bindings[s.id]!, platform).map((k, i) => <Kbd key={i}>{k}</Kbd>)
                    ) : (
                      // cleared, turned off, or every shortcut off
                      <span className="text-[12px] text-ink-3">{t(prefs.off || prefs.disabled.includes(s.id) ? "shortcuthelp.off" : "shortcuthelp.unset")}</span>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ))}
        <section aria-label={t("shortcuthelp.in_terminal")}>
          <h3 className="mb-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{t("shortcuthelp.in_terminal_fixed")}</h3>
          <ul className="divide-y divide-line">
            {localShortcuts.map((s) => (
              <li key={s.id} className="py-1.5">
                <div className="flex items-center gap-3">
                  <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{s.label}</span>
                  <span className="flex shrink-0 items-center gap-1">
                    {formatChordParts(s.chord[platform], platform).map((k, i) => (
                      <Kbd key={i}>{k}</Kbd>
                    ))}
                  </span>
                </div>
                {s.note && platform === "other" && <p className="mt-0.5 text-[11.5px] text-ink-3">{s.note}</p>}
              </li>
            ))}
          </ul>
        </section>
      </div>
    </Dialog>
  );
}

function DefaultHelpOpenBridge({ render }: { render: (open: boolean) => ReactNode }) {
  const open = useHelpOpen();
  return <>{render(open)}</>;
}

function DefaultBindingsBridge({ render }: { render: (bindings: Readonly<Bindings>) => ReactNode }) {
  const bindings = useBindings();
  return <>{render(bindings)}</>;
}

function DefaultPrefsBridge({ render }: { render: (prefs: Pick<ShortcutPrefs, "off" | "disabled">) => ReactNode }) {
  const prefs = useShortcutPrefs();
  return <>{render(prefs)}</>;
}

interface ShortcutHelpBodyProps {
  open: boolean;
  propOnClose?: () => void;
  bindings: Readonly<Bindings>;
  prefs: Pick<ShortcutPrefs, "off" | "disabled">;
  platform: Platform;
  scopes: readonly ShortcutScope[];
  scopeLabels: Readonly<Record<ShortcutScope, string>>;
  shortcuts: readonly ShortcutDef[];
  localShortcuts: readonly LocalShortcut[];
  formatChordParts: (chord: string, platform: Platform) => string[];
  outerScopeToken: string;
  readOuterScopeToken: () => string;
  deps?: ShortcutHelpDeps;
}

function ShortcutHelpBody({
  open,
  propOnClose,
  bindings,
  prefs,
  platform,
  scopes,
  scopeLabels,
  shortcuts,
  localShortcuts,
  formatChordParts,
  outerScopeToken,
  readOuterScopeToken,
  deps,
}: ShortcutHelpBodyProps) {
  const helpScope = JSON.stringify([outerScopeToken, open]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: helpScope, epoch: 1 }));
  const effectiveEpoch = scope.key === helpScope ? scope.epoch : scope.epoch + 1;
  if (scope.key !== helpScope) {
    setScope({ key: helpScope, epoch: effectiveEpoch });
  }

  const [busyState, setBusyState] = useState<{ epoch: number; busy: boolean }>({ epoch: 0, busy: false });
  const [errorState, setErrorState] = useState<{ epoch: number; error: string }>({ epoch: 0, error: "" });
  const busy = busyState.epoch === effectiveEpoch && busyState.busy;
  const error = errorState.epoch === effectiveEpoch ? errorState.error : "";

  const activeEpochRef = useRef(0);
  const opSeqRef = useRef(0);
  const pendingRef = useRef<{ epoch: number; seq: number; controller: AbortController } | null>(null);
  const depsRef = useRef(deps);
  const propOnCloseRef = useRef(propOnClose);
  const readScopeTokenRef = useRef(readOuterScopeToken);

  useLayoutEffect(() => {
    depsRef.current = deps;
    propOnCloseRef.current = propOnClose;
    readScopeTokenRef.current = readOuterScopeToken;
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
  const capturedScopeToken = outerScopeToken;

  const surfaceError = (runEpoch: number, err: unknown) => {
    const msg = safeFormatError(depsRef.current?.formatError, err);
    setErrorState({ epoch: runEpoch, error: msg });
    try {
      depsRef.current?.onError?.(msg);
    } catch {
      // Error observer failures stay non-blocking so recovery never leaves busy stuck.
    }
  };

  const runControlled = (task: (signal: AbortSignal) => void | Promise<unknown>) => {
    if (activeEpochRef.current !== capturedEpoch || readScopeTokenRef.current() !== capturedScopeToken) return;
    if (pendingRef.current !== null) return;
    const controller = new AbortController();
    const runSeq = ++opSeqRef.current;
    const isRunCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === capturedEpoch
      && opSeqRef.current === runSeq
      && readScopeTokenRef.current() === capturedScopeToken;

    setErrorState({ epoch: capturedEpoch, error: "" });
    let result: unknown;
    try {
      result = task(controller.signal);
    } catch (err) {
      if (isRunCurrent()) surfaceError(capturedEpoch, err);
      return;
    }
    if (!isThenable(result)) return;

    pendingRef.current = { epoch: capturedEpoch, seq: runSeq, controller };
    setBusyState({ epoch: capturedEpoch, busy: true });
    void (async () => {
      try {
        await result;
      } catch (err) {
        if (isRunCurrent()) surfaceError(capturedEpoch, err);
      } finally {
        if (pendingRef.current?.seq === runSeq) pendingRef.current = null;
        if (activeEpochRef.current === capturedEpoch && opSeqRef.current === runSeq) {
          setBusyState({ epoch: capturedEpoch, busy: false });
        }
      }
    })();
  };

  const invokeClose = (signal: AbortSignal): void | Promise<unknown> => {
    if (depsRef.current?.onClose) return depsRef.current.onClose(signal);
    if (propOnCloseRef.current) return propOnCloseRef.current();
    closeHelp();
  };

  const handleClose = () => {
    runControlled((signal) => invokeClose(signal));
  };

  const handleEditInSettings = () => {
    runControlled((signal) => {
      const customOpenSettings = depsRef.current?.openSettings;
      if (customOpenSettings) {
        const res = customOpenSettings(signal);
        if (isThenable(res)) {
          return Promise.resolve(res).then(() => {
            if (!signal.aborted && activeEpochRef.current === capturedEpoch && readScopeTokenRef.current() === capturedScopeToken) {
              return invokeClose(signal);
            }
          });
        }
        return invokeClose(signal);
      }
      const closeRes = invokeClose(signal);
      const nav = depsRef.current?.navigate ?? navigate;
      if (isThenable(closeRes)) {
        return Promise.resolve(closeRes).then(() => {
          if (!signal.aborted && activeEpochRef.current === capturedEpoch && readScopeTokenRef.current() === capturedScopeToken) {
            openShortcutSettings(nav);
          }
        });
      }
      openShortcutSettings(nav);
    });
  };

  const viewProps: ShortcutHelpViewProps = {
    open,
    onClose: handleClose,
    onEditInSettings: handleEditInSettings,
    bindings,
    prefs,
    platform,
    scopes,
    scopeLabels,
    shortcuts,
    localShortcuts,
    formatChordParts,
    busy,
    error,
  };
  if (deps?.renderView) return <>{deps.renderView(viewProps)}</>;
  return <ShortcutHelpView {...viewProps} />;
}

export function ShortcutHelp(props?: ShortcutHelpProps): ReactElement;
export function ShortcutHelp(props: ShortcutHelpProps & { open: boolean; onClose: () => void }): ReactElement;
export function ShortcutHelp(props: ShortcutHelpProps = {}): ReactElement {
  const { open: propOpen, onClose: propOnClose, deps } = props;
  const trackedScope = useTrackedScope(deps?.scopeKey, deps?.subscribeScopeKey ?? deps?.subscribe);

  // Keep controlled inputs and scope tracking in the identity-stable outer component so direct <-> getter+subscribe switches never remount.
  const controlledOpen = useCachedExternalValue(deps?.open, deps?.subscribeOpen ?? deps?.subscribe);
  const controlledBindings = useCachedExternalValue(deps?.bindings, deps?.subscribeBindings ?? deps?.subscribe);
  const controlledPrefs = useCachedExternalValue(deps?.prefs, deps?.subscribePrefs ?? deps?.subscribe);
  const controlledPlatform = useCachedExternalValue(deps?.platform, deps?.subscribePlatform ?? deps?.subscribe);
  const controlledShortcuts = useCachedExternalValue(deps?.shortcuts, deps?.subscribeShortcuts ?? deps?.subscribe);
  const controlledLocalShortcuts = useCachedExternalValue(deps?.localShortcuts, deps?.subscribeLocalShortcuts ?? deps?.subscribe);
  const controlledScopes = useCachedExternalValue(deps?.scopes, deps?.subscribe);
  const controlledScopeLabels = useCachedExternalValue(deps?.scopeLabels, deps?.subscribe);

  const needDefaultOpen = deps?.open === undefined && propOpen === undefined;
  const needDefaultBindings = deps?.bindings === undefined;
  const needDefaultPrefs = deps?.prefs === undefined;
  const platform = controlledPlatform ?? currentPlatform();

  const renderBody = (open: boolean, bindings: Readonly<Bindings>, prefs: Pick<ShortcutPrefs, "off" | "disabled">) => (
    <ShortcutHelpBody
      open={open}
      propOnClose={propOnClose}
      bindings={bindings}
      prefs={prefs}
      platform={platform}
      scopes={controlledScopes ?? SCOPES}
      scopeLabels={controlledScopeLabels ?? SCOPE_LABEL}
      shortcuts={controlledShortcuts ?? SHORTCUTS}
      localShortcuts={controlledLocalShortcuts ?? LOCAL_SHORTCUTS}
      formatChordParts={deps?.formatChordParts ?? chordParts}
      outerScopeToken={trackedScope.token}
      readOuterScopeToken={trackedScope.readToken}
      deps={deps}
    />
  );

  const renderWithPrefs = (open: boolean, bindings: Readonly<Bindings>) => (
    needDefaultPrefs
      ? <DefaultPrefsBridge render={(prefs) => renderBody(open, bindings, prefs)} />
      : renderBody(open, bindings, controlledPrefs ?? { off: false, disabled: [] })
  );

  const renderWithBindings = (open: boolean) => (
    needDefaultBindings
      ? <DefaultBindingsBridge render={(bindings) => renderWithPrefs(open, bindings)} />
      : renderWithPrefs(open, controlledBindings ?? {})
  );

  if (needDefaultOpen) {
    return <DefaultHelpOpenBridge render={renderWithBindings} />;
  }
  return renderWithBindings(deps?.open !== undefined ? Boolean(controlledOpen) : Boolean(propOpen));
}
