// A request from a script outside an agent session has a card but no chat.
import { Fragment, useCallback, useRef, useSyncExternalStore, type ReactNode } from "react";
import { useStore, type AnsweredCard as AnsweredCardState } from "../lib/store";
import { navigate, type Route } from "../lib/router";
import { sameValue } from "../lib/stable-lines";
import type { Interaction } from "../lib/types";
import { IconButton, Loading } from "../ui/atoms";
import { AnsweredCard, InteractionCard } from "./chat/Interactions";
import { t } from "../i18n";

export interface RequestPageState {
  loaded: boolean;
  request?: Interaction | null;
  answered?: AnsweredCardState | null;
}

export interface RequestPageDeps {
  /** Controlled request-page state snapshot or reader (bypasses `useStore` when supplied). */
  state?: RequestPageState | ((id: string) => RequestPageState);
  /** Subscribe to external state updates when a reader dep is supplied. */
  subscribe?: (listener: () => void) => () => void;
  /** Controlled `loaded` flag or reader (bypasses `useStore(s => s.loaded)` when supplied). */
  loaded?: boolean | (() => boolean);
  /** Controlled pending interaction or reader (`null` for absent; bypasses `useStore`). */
  request?: Interaction | null | ((id: string) => Interaction | null | undefined);
  /** Controlled interactions list or reader used to find `id` when `request` is omitted (bypasses `useStore`). */
  interactions?: readonly Interaction[] | (() => readonly Interaction[]);
  /** Controlled answered receipt or reader (`null` for absent; bypasses `useStore`). */
  answered?: AnsweredCardState | null | ((id: string) => AnsweredCardState | null | undefined);
  /** Controlled answered receipt map or reader used when `answered` is omitted (bypasses `useStore`). */
  answeredMap?: Readonly<Record<string, AnsweredCardState | undefined>> | (() => Readonly<Record<string, AnsweredCardState | undefined>>);
  /** Navigate to another route (defaults to `router.navigate`). */
  navigate?: (to: Route) => void;
  /** Explicit Back button handler (defaults to `navigate({ name: "home" })`). */
  onBack?: () => void;
  /** Custom renderer for the active interaction card (defaults to `<InteractionCard />`). */
  renderInteraction?: (ix: Interaction) => ReactNode;
  /** Custom renderer for the answered receipt card (defaults to `<AnsweredCard />`). */
  renderAnswered?: (card: AnsweredCardState) => ReactNode;
  /** Optional scope key that resets rendered card identities when switched. */
  scopeKey?: string;
}

export interface RequestPageProps {
  id: string;
  onToggleSidebar: () => void;
  deps?: RequestPageDeps;
}

export interface RequestPageViewProps {
  id?: string;
  loaded: boolean;
  request?: Interaction | null;
  answered?: AnsweredCardState | null;
  onToggleSidebar: () => void;
  onBack: () => void;
  renderInteraction?: (ix: Interaction) => ReactNode;
  renderAnswered?: (card: AnsweredCardState) => ReactNode;
  cardScopeKey?: string;
}

const noopSubscribe = () => () => {};

interface ResolvedRequestSnapshot {
  id: string;
  loaded: boolean;
  request: Interaction | undefined;
  answered: AnsweredCardState | undefined;
}

interface PartialControlledSnapshot {
  id: string;
  loaded: boolean | undefined;
  request: { set: boolean; value: Interaction | undefined };
  answered: { set: boolean; value: AnsweredCardState | undefined };
}

function readControlledPartial(id: string, deps: RequestPageDeps | undefined): PartialControlledSnapshot {
  if (deps?.state !== undefined) {
    const snap = typeof deps.state === "function" ? deps.state(id) : deps.state;
    return {
      id,
      loaded: snap.loaded,
      request: { set: true, value: snap.request ?? undefined },
      answered: { set: true, value: snap.answered ?? undefined },
    };
  }
  const loaded = deps?.loaded !== undefined ? (typeof deps.loaded === "function" ? deps.loaded() : deps.loaded) : undefined;
  let request: { set: boolean; value: Interaction | undefined } = { set: false, value: undefined };
  if (deps?.request !== undefined) {
    const r = typeof deps.request === "function" ? deps.request(id) : deps.request;
    request = { set: true, value: r ?? undefined };
  } else if (deps?.interactions !== undefined) {
    const list = typeof deps.interactions === "function" ? deps.interactions() : deps.interactions;
    request = { set: true, value: list.find((ix) => ix.id === id) };
  }
  let answered: { set: boolean; value: AnsweredCardState | undefined } = { set: false, value: undefined };
  if (deps?.answered !== undefined) {
    const a = typeof deps.answered === "function" ? deps.answered(id) : deps.answered;
    answered = { set: true, value: a ?? undefined };
  } else if (deps?.answeredMap !== undefined) {
    const map = typeof deps.answeredMap === "function" ? deps.answeredMap() : deps.answeredMap;
    answered = { set: true, value: map[id] };
  }
  return { id, loaded, request, answered };
}

/** Pure presentational view for a standalone script request page. */
export function RequestPageView({
  id,
  loaded,
  request,
  answered,
  onToggleSidebar,
  onBack,
  renderInteraction,
  renderAnswered,
  cardScopeKey,
}: RequestPageViewProps) {
  const scopePrefix = cardScopeKey ? `${cardScopeKey}:` : "";
  const requestKey = request ? `${scopePrefix}${request.id}` : undefined;
  const answeredKey = answered ? `${scopePrefix}${id ?? answered.ix.id}` : undefined;
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("request-page.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <IconButton icon="chevronLeft" label={t("request-page.back")} onClick={onBack} />
        <h1 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{t("app.title_request")}</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl p-3 sm:p-4">
          {!loaded ? (
            <Loading />
          ) : request ? (
            renderInteraction ? <Fragment key={requestKey}>{renderInteraction(request)}</Fragment> : <InteractionCard key={requestKey} ix={request} />
          ) : answered ? (
            renderAnswered ? <Fragment key={answeredKey}>{renderAnswered(answered)}</Fragment> : <AnsweredCard key={answeredKey} card={answered} />
          ) : (
            <p role="status" className="text-sm text-ink-2">{t("request-page.ended")}</p>
          )}
        </div>
      </div>
    </div>
  );
}

export function RequestPage({ id, onToggleSidebar, deps }: RequestPageProps) {
  const hasLoaded = deps?.state !== undefined || deps?.loaded !== undefined;
  const hasRequest = deps?.state !== undefined || deps?.request !== undefined || deps?.interactions !== undefined;
  const hasAnswered = deps?.state !== undefined || deps?.answered !== undefined || deps?.answeredMap !== undefined;
  const onBack = () => {
    if (deps?.onBack) deps.onBack();
    else (deps?.navigate ?? navigate)({ name: "home" });
  };
  if (hasLoaded && hasRequest && hasAnswered) {
    return <ControlledRequestPage id={id} onToggleSidebar={onToggleSidebar} onBack={onBack} deps={deps!} />;
  }
  return <StoreRequestPage id={id} onToggleSidebar={onToggleSidebar} onBack={onBack} deps={deps} />;
}

function ControlledRequestPage({
  id,
  onToggleSidebar,
  onBack,
  deps,
}: RequestPageProps & { onBack: () => void; deps: RequestPageDeps }) {
  const cache = useRef<ResolvedRequestSnapshot | null>(null);
  const subscribe = deps.subscribe ?? noopSubscribe;
  const stateDep = deps.state;
  const loadedDep = deps.loaded;
  const requestDep = deps.request;
  const interactionsDep = deps.interactions;
  const answeredDep = deps.answered;
  const answeredMapDep = deps.answeredMap;
  const getSnapshot = useCallback((): ResolvedRequestSnapshot => {
    const partial = readControlledPartial(id, {
      state: stateDep,
      loaded: loadedDep,
      request: requestDep,
      interactions: interactionsDep,
      answered: answeredDep,
      answeredMap: answeredMapDep,
    });
    const next: ResolvedRequestSnapshot = {
      id,
      loaded: Boolean(partial.loaded),
      request: partial.request.value,
      answered: partial.answered.value,
    };
    if (cache.current && sameValue(cache.current, next)) return cache.current;
    cache.current = next;
    return next;
  }, [id, stateDep, loadedDep, requestDep, interactionsDep, answeredDep, answeredMapDep]);
  const snap = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return (
    <RequestPageView
      id={id}
      loaded={snap.loaded}
      request={snap.request}
      answered={snap.answered}
      onToggleSidebar={onToggleSidebar}
      onBack={onBack}
      renderInteraction={deps.renderInteraction}
      renderAnswered={deps.renderAnswered}
      cardScopeKey={deps.scopeKey}
    />
  );
}

function StoreRequestPage({
  id,
  onToggleSidebar,
  onBack,
  deps,
}: RequestPageProps & { onBack: () => void }) {
  const storeLoaded = useStore((s) => s.loaded);
  const storeRequest = useStore((s) => s.interactions.find((ix) => ix.id === id));
  const storeAnswered = useStore((s) => s.answered[id]);
  const cache = useRef<PartialControlledSnapshot | null>(null);
  const subscribe = deps?.subscribe ?? noopSubscribe;
  const stateDep = deps?.state;
  const loadedDep = deps?.loaded;
  const requestDep = deps?.request;
  const interactionsDep = deps?.interactions;
  const answeredDep = deps?.answered;
  const answeredMapDep = deps?.answeredMap;
  const getControlled = useCallback((): PartialControlledSnapshot => {
    const next = readControlledPartial(id, {
      state: stateDep,
      loaded: loadedDep,
      request: requestDep,
      interactions: interactionsDep,
      answered: answeredDep,
      answeredMap: answeredMapDep,
    });
    if (cache.current && sameValue(cache.current, next)) return cache.current;
    cache.current = next;
    return next;
  }, [id, stateDep, loadedDep, requestDep, interactionsDep, answeredDep, answeredMapDep]);
  const controlled = useSyncExternalStore(subscribe, getControlled, getControlled);
  return (
    <RequestPageView
      id={id}
      loaded={controlled.loaded !== undefined ? controlled.loaded : storeLoaded}
      request={controlled.request.set ? controlled.request.value : storeRequest}
      answered={controlled.answered.set ? controlled.answered.value : storeAnswered}
      onToggleSidebar={onToggleSidebar}
      onBack={onBack}
      renderInteraction={deps?.renderInteraction}
      renderAnswered={deps?.renderAnswered}
      cardScopeKey={deps?.scopeKey}
    />
  );
}
