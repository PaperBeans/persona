// Lazy-loaded unified Settings › Integrations & Extensions view (integrations_v2, #14874 / Refs #14802).
// Aggregates built-in services, channels, connectors, standalone push sources, and installed extensions
// into one deduplicated list with capability filter chips, subscription health alerts, Extension Market access,
// inline IntegrationDetail (#14868) mounting, and a lazy-loaded Data Flow view (#15423).
import { Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { loadMailAccounts, useMailboxEnabled } from "../../lib/mail";
import { t } from "../../i18n";
import { ExternalSourceSettingsEntry } from "../../features/external-sources/ExternalSourceSettingsEntry";
import type { MailAccount } from "../../lib/agent-settings";
import { api, enc, errorText } from "../../lib/api";
import { listChannels, type Channel } from "../../lib/channels";
import { connectorBrand, listConnectors, type HubConnector } from "../../lib/connectors";
import { stripBase } from "../../lib/base";
import { navigate, subscribeRoute } from "../../lib/router";
import { scoped, useScope } from "../../lib/current-assistant";
import { hubHasExt } from "../../lib/extension-api";
import { onAuthChange, withAuth } from "../../lib/extension-auth";
import { watchOperations } from "../../lib/extension-operations";
import { EXTENSIONS_CHANGED } from "../../lib/extension-prompts";
import { extensionDetail, extensionTone, listExtensionsInfo, type HubExtension, type HubExtensionDetail } from "../../lib/extensions";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { botStatus, type FeishuBotStatus } from "../../lib/feishu-bot";
import { lazyPage } from "../../lib/lazy-page";
import { chooseManager, defaultManager, otherManager, pinManagerAt, useChosenManager } from "../../lib/manager-choice";
import { isResumed } from "../../lib/reconnect";
import { onHubEvent, useLarkOn, useStore } from "../../lib/store";
import {
  listSubscriptions,
  useInboxSourcesShown,
  useSubscriptionsShown,
  type HubSubscription,
} from "../../lib/subscriptions";
import { Button, cx, IconButton, Loading } from "../../ui/atoms";
import { BrandMark } from "../../ui/BrandMark";
import { ChannelMark } from "../../ui/ChannelMark";
import { ExtensionLogo } from "../../ui/ExtensionLogo";
import { Icon } from "../../ui/icons";
import { Segmented } from "../assistant/parts";
import { OfflineState, useShownOffline } from "../ManagerParts";
import { OwnerMark } from "./OwnerMark";
import { SentSettings } from "./SentSettings";
import { Group, Pill, Row, SettingsHint } from "./SettingsParts";
import {
  formatIntegrationsSub,
  isIntegrationDetailRoute,
  normalizeIntegrationFilter,
  parseIntegrationsSub,
  type IntegrationFilter,
  type IntegrationsRoute,
} from "./integrations-route";
import {
  buildIntegrationCatalog,
  filterIntegrationItems,
  isIntegrationRowSelected,
  resolveIntegrationTarget,
  resolveSourceItem,
  type IntegrationCapability,
  type IntegrationItem,
  type IntegrationSubscriptionAlert,
  type IntegrationTone,
  type LarkGatewaySummary,
} from "./integrations/catalog";

const IntegrationDetail = lazyPage(() => import("./IntegrationDetail").then((m) => m.IntegrationDetail));
const MarketCatalog = lazyPage(() => import("./ExtensionMarket").then((m) => m.MarketCatalog));
const MarketItem = lazyPage(() => import("./ExtensionMarket").then((m) => m.MarketItem));
const Markets = lazyPage(() => import("./ExtensionMarket").then((m) => m.Markets));
const ExtensionUI = lazyPage(() => import("./ExtensionView").then((m) => m.ExtensionUI));

// Lazily load SourcesUI (and its SourcesGraph chunk) only when opening the Data Flow view or a push source detail.
const LazySourcesUI = lazyPage(() => import("../subscriptions/SourcesUI").then((m) => m.SourcesUI));

export interface IntegrationsProps {
  open: string | null;
  onOpen: (sub: string | null, drillDown?: boolean) => void;
  /** Desktop split-pane layout (when the Settings host has room): list stays beside the open integration detail. */
  twoCol?: boolean;
  /** The page's top bar already provides Back (phone), so subviews omit their own Back button. */
  headerBack?: boolean;
  /** Navigate to a Settings URL linked from a channel override notice. */
  onSettings?: (url: string) => void;
  /** Report whether an open extension plugin_page needs the wide page container. */
  onPageWide?: (wide: boolean) => void;
}

const DOT_CLASS: Record<IntegrationTone, string> = {
  green: "bg-green",
  orange: "bg-orange",
  red: "bg-red",
  idle: "bg-line-strong",
};

const EXT_TILE: Record<ReturnType<typeof extensionTone>, string> = {
  red: "bg-red-tint text-red",
  orange: "bg-orange-tint text-orange",
  green: "bg-accent-tint text-accent-ink",
  idle: "bg-inset text-ink-3 shadow-hairline",
};

const FILTER_CHIPS: readonly { id: IntegrationFilter; labelKey: string }[] = [
  { id: "all", labelKey: "settings-integrations.filter_all" },
  { id: "channels", labelKey: "settings-integrations.filter_channels" },
  { id: "data", labelKey: "settings-integrations.filter_data" },
  { id: "tools", labelKey: "settings-integrations.filter_tools" },
];

const CAP_LABEL_KEY: Record<IntegrationCapability, string> = {
  channels: "settings-integrations.cap_channels",
  data: "settings-integrations.cap_data",
  tools: "settings-integrations.cap_tools",
};

function ItemMark({ item }: { item: IntegrationItem }) {
  if (item.kind === "extension" && item.extension) {
    return (
      <ExtensionLogo
        url={item.extension.icon_url}
        manager={item.extension.manager ?? ""}
        iconSize={15}
        className={cx("flex size-8 shrink-0 items-center justify-center rounded-control", EXT_TILE[extensionTone(item.extension)])}
      />
    );
  }
  if (item.kind === "source") {
    const toneClass =
      item.alertTone === "red"
        ? "bg-red-tint text-red"
        : item.alertTone === "orange"
          ? "bg-orange-tint text-orange"
          : "bg-accent-tint text-accent-ink";
    return (
      <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-control", toneClass)}>
        <Icon name="bell" size={15} />
      </span>
    );
  }
  if (item.channel) {
    return <ChannelMark c={item.channel} size={32} />;
  }
  const brand = connectorBrand(item.id === "feishu-bot" ? "lark" : item.id);
  if (brand) {
    return (
      <span className="flex size-8 shrink-0 items-center justify-center rounded-control bg-surface shadow-hairline">
        <BrandMark brand={brand} size={32} fit="tile" />
      </span>
    );
  }
  const icon = item.id === "mail" ? "mail" : item.id === "imessage" || item.id === "lark" ? "chat" : item.id === "feishu-bot" ? "send" : "link";
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-control bg-accent-tint text-accent-ink">
      <Icon name={icon} size={15} />
    </span>
  );
}

function SubscriptionAlertBadge({ alert }: { alert: IntegrationSubscriptionAlert }) {
  return (
    <span
      data-subscription-alert-badge={alert.state}
      className={cx(
        "inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-submeta font-medium whitespace-nowrap",
        alert.tone === "red" ? "bg-red-tint text-red" : "bg-orange-tint text-orange",
      )}
    >
      <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", DOT_CLASS[alert.tone])} />
      {alert.badgeText}
    </span>
  );
}

/** Detail pane for a standalone push source (or single source deep link), backed by lazy SourcesUI. */
function PushSourceDetailPane({
  state,
  item,
  manager,
  onBack,
  reloadKey,
  onReload,
}: {
  state: string;
  item: IntegrationItem | null;
  manager?: string;
  onBack?: () => void;
  reloadKey: number;
  onReload: () => void;
}) {
  const title = item?.name ?? t("subscriptions.title_sources");
  return (
    <div data-integration-detail={item?.id ?? "source"} data-integration-kind="source">
      <div className="mb-3 flex items-center gap-2">
        {onBack && (
          <Button tone="quiet" icon="chevronLeft" onClick={onBack} className="-ml-2">
            {t("settings-integrations.back")}
          </Button>
        )}
        <OwnerMark manager={manager} />
        <span className="flex-1" />
        <IconButton icon="retry" label={t("settings-integrations.refresh")} onClick={onReload} />
      </div>

      {item && (
        <section className="mb-6 overflow-hidden rounded-card bg-surface shadow-card">
          <div className="flex items-center gap-3 px-4 py-3.5">
            <ItemMark item={item} />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-2">
                <h1 className="truncate text-card-title font-semibold text-ink">{item.name}</h1>
                <Pill>{t("settings-integrations.push_source_badge")}</Pill>
              </div>
              <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-label text-ink-3">
                <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", DOT_CLASS[item.tone])} />
                <span className="truncate">{item.statusText}</span>
              </div>
            </div>
          </div>
          {item.description && (
            <div className="border-t border-line px-4 py-3">
              <p className="text-meta leading-relaxed text-ink-2">{item.description}</p>
            </div>
          )}
        </section>
      )}

      <Suspense fallback={<Loading />}>
        <LazySourcesUI key={`${manager ?? ""}:${state}:${reloadKey}`} title={title} state={state} manager={manager} />
      </Suspense>
      {item?.kind === "source" && <ExternalSourceSettingsEntry provider="subscription" channel="" manager={manager} />}
    </div>
  );
}

/** Full-width Data Flow pane (`-sources` / `-sources@<manager>`), pinning `/subscriptions` when a manager is named. */
function SourcesFlowPane({
  onOpenSource,
  manager,
  reloadKey,
}: {
  manager?: string;
  reloadKey: number;
  onOpenSource?: (page: string) => void;
}) {
  return (
    <>
      {manager && (
        <div className="mb-3 flex items-center gap-2">
          <OwnerMark manager={manager} />
        </div>
      )}
      <Suspense fallback={<Loading />}>
        <LazySourcesUI
          key={`${manager ?? ""}:${reloadKey}`}
          title={t("subscriptions.title_sources")}
          initialView="graph"
          onOpenSource={onOpenSource}
          manager={manager}
        />
      </Suspense>
    </>
  );
}

/** A declarative page contributed by an extension, rendered under Settings › Integrations. */
function IntegrationExtensionPage({
  id,
  ui,
  manager,
  onBack,
  onWide,
}: {
  id: string;
  ui: string;
  manager?: string;
  onBack?: () => void;
  onWide?: (wide: boolean) => void;
}) {
  useLayoutEffect(() => pinManagerAt([`/extensions/${enc(id)}`, `/plugins/${enc(id)}`], manager), [id, manager]);
  const [detail, setDetail] = useState<HubExtensionDetail | null>(null);
  const [reloadCount, setReloadCount] = useState(0);
  useEffect(() => {
    extensionDetail(id).then(setDetail, () => {});
  }, [id]);
  useEffect(() => () => onWide?.(false), [onWide]);
  const title = detail?.ui?.find((u) => u.id === ui)?.title;
  return (
    <>
      <div className="mb-3 flex items-center gap-2">
        {onBack && (
          <Button tone="quiet" icon="chevronLeft" onClick={onBack} className="-ml-2">
            <span className="max-w-[240px] truncate">{detail?.name ?? id}</span>
          </Button>
        )}
        <span className="flex-1" />
        <IconButton icon="retry" label={t("settings-extensions.redraw")} onClick={() => setReloadCount((x) => x + 1)} />
      </div>
      <ExtensionOwnerScope owner={id}>
        <Suspense fallback={<Loading />}>
          <ExtensionUI key={reloadCount} extension={id} ui={ui} title={title} manager={manager} onWide={onWide} />
        </Suspense>
      </ExtensionOwnerScope>
    </>
  );
}

export function Integrations(props: IntegrationsProps) {
  const picked = useChosenManager();
  return <IntegrationsForManager key={picked ?? ""} {...props} picked={picked} />;
}

function IntegrationsForManager({
  picked,
  open,
  onOpen,
  twoCol = false,
  headerBack = false,
  onSettings,
  onPageWide,
}: IntegrationsProps & { picked: string | null }) {
  const route = useMemo(() => parseIntegrationsSub(open), [open]);
  const go = (next: IntegrationsRoute) => onOpen(formatIntegrationsSub(next));
  const drillDown = (next: IntegrationsRoute) => onOpen(formatIntegrationsSub(next), true);

  const owner = otherManager(picked ?? undefined);
  const effectiveManager = ("manager" in route ? route.manager : undefined) ?? owner;
  const scope = useScope();
  const offline = useShownOffline();

  const hasExtensions = useStore((s) => hubHasExt(s.features, "manager"));
  const hasChannels = useStore((s) => s.features.includes("channels"));
  const hasConnectors = useStore((s) => s.features.includes("connectors"));
  const hasMail = useMailboxEnabled();
  const hasLark = useLarkOn();
  const hasSubscriptions = useSubscriptionsShown();
  const hasInboxSources = useInboxSourcesShown();

  const filter = useSyncExternalStore(subscribeRoute, () => normalizeIntegrationFilter(new URLSearchParams(location.search).get("filter")));
  const setFilter = (next: IntegrationFilter) => {
    const url = new URL(location.href);
    if (next === "all") url.searchParams.delete("filter");
    else url.searchParams.set("filter", next);
    navigate(stripBase(url.pathname) + url.search + url.hash, true);
  };

  const [extensions, setExtensions] = useState<HubExtension[]>([]);
  const [markets, setMarkets] = useState(false);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [connectors, setConnectors] = useState<HubConnector[]>([]);
  const [subscriptions, setSubscriptions] = useState<HubSubscription[]>([]);
  const [mailAccounts, setMailAccounts] = useState<MailAccount[] | null>();
  const [larkStatus, setLarkStatus] = useState<LarkGatewaySummary | null>();
  const [feishuBotStatus, setFeishuBotStatus] = useState<FeishuBotStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [partialErrors, setPartialErrors] = useState<string[]>([]);
  const [sourcesReload, setSourcesReload] = useState(0);

  const seq = useRef(0);
  const loadSubscriptionsOnly = useCallback(async () => {
    if (!hasSubscriptions && !hasInboxSources) {
      setSubscriptions([]);
      return;
    }
    const mine = seq.current;
    const list = await listSubscriptions(owner).catch(() => null);
    if (mine === seq.current && list) setSubscriptions(list);
  }, [hasSubscriptions, hasInboxSources, owner]);

  const loadAll = useCallback(async () => {
    const mine = ++seq.current;
    setLoading(true);
    setPartialErrors([]);
    // Publish each result on arrival. Only the empty state and final counts need every answer.
    const publish = <T,>(apply: (result: T) => void) => (result: T) => {
      if (mine === seq.current) apply(result);
    };
    const report = publish((error: unknown) => setPartialErrors((errors) => [...errors, errorText(error)]));

    const applyExtensions = (extensions: HubExtension[], markets: boolean) => {
      setExtensions(extensions);
      setMarkets(markets);
    };
    await Promise.all([
      hasExtensions
        ? listExtensionsInfo().then(publish((result) => applyExtensions(result.extensions, result.markets)), report)
        : applyExtensions([], false),
      hasChannels ? listChannels(owner).then(publish(setChannels), report) : setChannels([]),
      hasConnectors ? listConnectors().then(publish(setConnectors), report) : setConnectors([]),
      hasMail ? loadMailAccounts(owner).catch(() => null).then(publish(setMailAccounts)) : setMailAccounts(null),
      hasLark ? api<LarkGatewaySummary>("/lark/status").catch(() => null).then(publish(setLarkStatus)) : setLarkStatus(null),
      botStatus().catch(() => null).then(publish(setFeishuBotStatus)),
      loadSubscriptionsOnly(),
    ]);

    if (mine !== seq.current) return;
    setLoaded(true);
    setLoading(false);
  }, [hasExtensions, hasChannels, hasConnectors, hasMail, hasLark, loadSubscriptionsOnly, owner]);

  useEffect(() => {
    void loadAll();
    return () => { seq.current++; };
  }, [loadAll]);

  // Refresh subscriptions when a subscription state changes on the hub (at most once per second).
  useEffect(() => {
    if (!hasSubscriptions && !hasInboxSources) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = onHubEvent("subscription", () => {
      clearTimeout(timer);
      timer = setTimeout(() => void loadSubscriptionsOnly(), 1000);
    });
    return () => {
      clearTimeout(timer);
      off();
    };
  }, [hasSubscriptions, hasInboxSources, loadSubscriptionsOnly]);

  // Reload list when an extension is installed, upgraded, or uninstalled.
  useEffect(() => {
    let queued = false;
    const again = () => {
      if (queued) return;
      queued = true;
      queueMicrotask(() => {
        queued = false;
        void loadAll();
      });
    };
    window.addEventListener(EXTENSIONS_CHANGED, again);
    const stop = watchOperations(() => {});
    return () => {
      window.removeEventListener(EXTENSIONS_CHANGED, again);
      stop();
    };
  }, [loadAll]);

  // Keep extension auth updates reflected immediately on list rows.
  useEffect(
    () =>
      onAuthChange((id, auth, from) =>
        setExtensions((cur) => cur.map((x) => ((x.manager || defaultManager()) === from ? withAuth(x, id, auth) : x))),
      ),
    [],
  );

  useEffect(() => onHubEvent("hello", (m) => !isResumed(m) && void loadAll()), [loadAll]);

  const ownExtensions = useMemo(() => scoped(extensions, scope), [extensions, scope]);
  const ownConnectors = useMemo(() => scoped(connectors, scope), [connectors, scope]);

  const items = useMemo(
    () =>
      buildIntegrationCatalog({
        extensions: ownExtensions,
        channels,
        connectors: ownConnectors,
        subscriptions,
        hasMail: hasMail && mailAccounts !== undefined,
        hasLark: hasLark && larkStatus !== undefined,
        hasFeishuBot: feishuBotStatus !== null,
        mailAccounts,
        larkStatus,
        feishuBotStatus,
        ownerManager: owner,
      }),
    [ownExtensions, channels, ownConnectors, subscriptions, hasMail, hasLark, mailAccounts, larkStatus, feishuBotStatus, owner],
  );

  const counts = useMemo(
    () => ({
      all: items.length,
      channels: items.filter((it) => it.capabilities.includes("channels")).length,
      data: items.filter((it) => it.capabilities.includes("data")).length,
      tools: items.filter((it) => it.capabilities.includes("tools")).length,
    }),
    [items],
  );

  const filtered = useMemo(() => filterIntegrationItems(items, filter), [items, filter]);
  const builtins = useMemo(() => filtered.filter((it) => it.kind === "builtin"), [filtered]);
  const pushSources = useMemo(() => filtered.filter((it) => it.kind === "source"), [filtered]);
  const installedExtensions = useMemo(() => filtered.filter((it) => it.kind === "extension"), [filtered]);

  const isSourcesView = route.kind === "sources" && !route.state;
  const sourceDetailState = route.kind === "sources" && route.state ? route.state : null;
  const showDetail = isIntegrationDetailRoute(route);
  const compactList = showDetail && twoCol;
  const listScrollRef = useRef<HTMLDivElement>(null);
  const detailScrollRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    if (compactList && detailScrollRef.current) {
      detailScrollRef.current.scrollTop = 0;
    }
  }, [compactList, open]);

  // Full-width subviews (Market catalog, Market item, Manage markets, Extension UI page, Sent records).
  if (route.kind === "catalog") {
    return (
      <Suspense fallback={<Loading />}>
        <MarketCatalog
          backLabel={t("settings-integrations.back")}
          onBack={headerBack ? undefined : () => onOpen(null)}
          onOpen={(it) => go({ kind: "item", market: it.market, id: it.id })}
          onManageMarkets={() => go({ kind: "markets" })}
        />
      </Suspense>
    );
  }
  if (route.kind === "item") {
    return (
      <Suspense fallback={<Loading />}>
        <MarketItem
          key={open ?? ""}
          market={route.market}
          id={route.id}
          manager={effectiveManager}
          onBack={
            headerBack
              ? undefined
              : () => go(route.manager ? { kind: "extension", id: route.id, manager: route.manager } : { kind: "catalog" })
          }
          onInstalled={(id) => go({ kind: "extension", id, manager: effectiveManager })}
        />
      </Suspense>
    );
  }
  if (route.kind === "markets") {
    return (
      <Suspense fallback={<Loading />}>
        <Markets onBack={headerBack ? undefined : () => go({ kind: "catalog" })} />
      </Suspense>
    );
  }
  if (route.kind === "page") {
    return (
      <IntegrationExtensionPage
        key={`${open ?? ""}@${route.manager ?? ""}`}
        id={route.id}
        ui={route.ui}
        manager={effectiveManager}
        onBack={headerBack ? undefined : () => go({ kind: "extension", id: route.id, manager: route.manager })}
        onWide={onPageWide}
      />
    );
  }
  if (route.kind === "sent") {
    return (
      <>
        {!headerBack && (
          <Button tone="quiet" icon="chevronLeft" onClick={() => onOpen(null)} className="-ml-2 mb-3">
            {t("settings-integrations.back")}
          </Button>
        )}
        <h2 className="mb-2 px-1 text-card-title font-semibold text-ink">{t("settings-sent.entry_label")}</h2>
        <SentSettings />
      </>
    );
  }

  const resolvedTarget = showDetail && !sourceDetailState ? resolveIntegrationTarget(route, items, picked) : null;
  const resolvedSourceItem = sourceDetailState ? resolveSourceItem(route, items, picked) : null;

  const renderRow = (item: IntegrationItem) => {
    const selected = isIntegrationRowSelected(open, item, picked);
    const status = (
      <span
        className={cx(
          "flex items-center gap-1.5 whitespace-nowrap",
          compactList ? "mt-0.5 min-w-0 text-submeta" : "shrink-0 text-label",
          item.alertTone === "red" ? "text-red" : item.alertTone === "orange" ? "text-orange" : "text-ink-2",
        )}
      >
        <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", DOT_CLASS[item.tone])} />
        <span className={cx(compactList && "truncate")}>{item.statusText}</span>
      </span>
    );
    return (
      <button
        key={item.key}
        type="button"
        data-integration-row={item.key}
        data-integration-kind={item.kind}
        data-integration-id={item.id}
        data-integration-sub-alert={item.subscriptionAlert?.state}
        aria-current={selected || undefined}
        onClick={() => onOpen(item.sub)}
        className={cx(
          "flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors",
          selected ? "bg-accent-tint" : "hover:bg-hover",
        )}
      >
        <ItemMark item={item} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
            <span className="max-w-full truncate text-meta font-medium text-ink">{item.name}</span>
            {!compactList &&
              item.capabilities.map((cap) => (
                <Pill key={cap}>{t(CAP_LABEL_KEY[cap])}</Pill>
              ))}
            {!compactList && item.subscriptionAlert && item.kind !== "source" && (
              <SubscriptionAlertBadge alert={item.subscriptionAlert} />
            )}
          </div>
          {compactList && status}
          {item.subtitle && (!compactList || item.alertTone) && (
            <div
              className={cx(
                "mt-0.5 truncate text-submeta",
                item.alertTone === "red" ? "text-red" : item.alertTone === "orange" ? "text-orange" : "text-ink-3",
              )}
            >
              {item.subtitle}
            </div>
          )}
        </div>
        {!compactList && status}
        <Icon name="chevronRight" size={12} stroke={2} className="shrink-0 text-ink-3" />
      </button>
    );
  };

  const listHeader = (
    <div className={cx(compactList ? "mb-2 flex items-center gap-1.5" : "mb-3 flex flex-wrap items-center gap-2")}>
      {!headerBack && (
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-card-title font-semibold text-ink">{t("settings-integrations.list_title")}</h1>
        </div>
      )}
      {headerBack && <span className="flex-1" />}
      {markets && (
        <Button
          tone="secondary"
          size={compactList ? "sm" : undefined}
          icon="search"
          title={compactList ? t("settings-integrations.browse_market_action") : undefined}
          testId="integrations-market-entry"
          onClick={() => go({ kind: "catalog" })}
        >
          {t(compactList ? "settings-integrations.browse_market_compact" : "settings-integrations.browse_market_action")}
        </Button>
      )}
      <IconButton
        icon="retry"
        label={t("settings-integrations.refresh")}
        onClick={() => (isSourcesView ? setSourcesReload((n) => n + 1) : void loadAll())}
        disabled={!isSourcesView && loading}
      />
    </div>
  );

  const viewSwitch = hasInboxSources ? (
    <div className={cx(compactList ? "mb-2" : "mb-3 flex")} data-integrations-view-switch>
      <Segmented
        label={t("settings-integrations.view_label")}
        value={isSourcesView ? "sources" : "list"}
        compact={compactList}
        options={[
          { v: "list", label: t("settings-integrations.view_list") },
          { v: "sources", label: t("settings-integrations.view_sources") },
        ]}
        onChange={(v) => {
          if (v === "sources") go({ kind: "sources", manager: otherManager(effectiveManager) });
          else {
            chooseManager(effectiveManager ?? null);
            go({ kind: "list" });
          }
        }}
      />
    </div>
  ) : null;

  // Full-width global Data Flow view (`route.kind === "sources"` without `state`):
  // natural single-scroll container so SourcesGraph can use the full wide canvas, with the forwarding setting.
  if (isSourcesView) {
    return (
      <div data-integrations-sources-view>
        {listHeader}
        {!headerBack && viewSwitch}
        <SourcesFlowPane manager={effectiveManager} reloadKey={sourcesReload}
          onOpenSource={headerBack ? (page) => drillDown({ kind: "sources", state: page, manager: effectiveManager }) : undefined}
        />
      </div>
    );
  }

  const filterBar = (
    <div
      role="group"
      aria-label={t("settings-integrations.filter_label")}
      data-integrations-filters
      className={cx(
        compactList
          ? "-mx-1 -my-0.5 flex flex-nowrap items-center gap-1 overflow-x-auto overscroll-x-contain px-1 py-0.5 integrations-chip-scroll"
          : "mb-4 flex flex-wrap items-center gap-1.5",
      )}
    >
      {FILTER_CHIPS.map((chip) => {
        const active = filter === chip.id;
        const count = counts[chip.id];
        return (
          <button
            key={chip.id}
            type="button"
            data-integration-filter={chip.id}
            aria-pressed={active}
            onClick={() => setFilter(chip.id)}
            className={cx(
              "inline-flex shrink-0 items-center rounded-full whitespace-nowrap transition-colors",
              compactList ? "h-6 gap-1 px-1.5 text-submeta" : "h-7 gap-1.5 px-3 text-label",
              active
                ? "bg-ink font-medium text-surface"
                : "bg-surface text-ink-2 shadow-hairline hover:bg-hover hover:text-ink",
            )}
          >
            <span>{t(chip.labelKey)}</span>
            {loaded && <span className={cx("tabular-nums", active ? "opacity-80" : "text-ink-3")}>{count}</span>}
          </button>
        );
      })}
    </div>
  );

  const listBody = (
    <>
      {partialErrors.length > 0 && (
        <div className="mb-4">
          <Group>
            <SettingsHint alert>{t("settings-integrations.list_partial_error", { error: partialErrors.join(" · ") })}</SettingsHint>
          </Group>
        </div>
      )}

      {!loaded && filtered.length === 0 && (
        <Group anchor="integrations">
          <div className="px-4 py-3">
            <Loading />
          </div>
        </Group>
      )}

      {loaded && filtered.length === 0 && (
        <Group anchor="integrations">
          {offline && items.length === 0 ? (
            <OfflineState offline={offline} ring="ring-surface" className="py-8" />
          ) : (
            <div className="flex flex-col items-center gap-2 px-6 py-8 text-center" data-integrations-empty>
              <span className="flex size-10 items-center justify-center rounded-card bg-inset text-ink-3 shadow-hairline">
                <Icon name="plug" size={18} />
              </span>
              <p className="text-meta font-medium text-ink">
                {t(filter === "all" ? "settings-integrations.empty_all_title" : "settings-integrations.empty_filtered_title")}
              </p>
              <p className="max-w-[420px] text-label leading-relaxed text-ink-3">
                {t(filter === "all" ? "settings-integrations.empty_all_detail" : "settings-integrations.empty_filtered_detail")}
              </p>
              <div className="mt-1 flex flex-wrap items-center justify-center gap-2">
                {filter !== "all" && (
                  <Button tone="secondary" onClick={() => setFilter("all")}>
                    {t("settings-integrations.show_all")}
                  </Button>
                )}
                {markets && (
                  <Button tone="accent" icon="search" onClick={() => go({ kind: "catalog" })}>
                    {t("settings-integrations.browse_market_action")}
                  </Button>
                )}
              </div>
            </div>
          )}
        </Group>
      )}

      {builtins.length > 0 && (
        <Group
          anchor="integrations"
          title={
            <span className="flex items-center gap-2">
              {t("settings-integrations.group_builtin")}
              <span className="font-normal">{t("settings-integrations.count", { count: builtins.length })}</span>
            </span>
          }
        >
          {builtins.map((item) => renderRow(item))}
        </Group>
      )}

      {pushSources.length > 0 && (
        <Group
          anchor="push-sources"
          title={
            <span className="flex items-center gap-2">
              {t("settings-integrations.group_push_sources")}
              <span className="font-normal">{t("settings-integrations.count", { count: pushSources.length })}</span>
            </span>
          }
        >
          {pushSources.map((item) => renderRow(item))}
        </Group>
      )}

      {installedExtensions.length > 0 && (
        <Group
          anchor={builtins.length === 0 && pushSources.length === 0 ? "integrations" : "extensions"}
          title={
            <span className="flex items-center gap-2">
              {t("settings-integrations.group_extensions")}
              <span className="font-normal">{t("settings-integrations.count", { count: installedExtensions.length })}</span>
            </span>
          }
        >
          {installedExtensions.map((item) => renderRow(item))}
        </Group>
      )}

      <Group anchor="sent">
        <Row icon="send" label={t("settings-sent.entry_label")} hint={t("settings-sent.hint")} onClick={() => go({ kind: "sent" })} />
      </Group>
    </>
  );

  const detailPane = sourceDetailState ? (
    <PushSourceDetailPane
      state={sourceDetailState}
      item={resolvedSourceItem}
      manager={effectiveManager}
      onBack={headerBack || twoCol ? undefined : () => onOpen(null)}
      reloadKey={sourcesReload}
      onReload={() => setSourcesReload((n) => n + 1)}
    />
  ) : showDetail && resolvedTarget ? (
    <Suspense fallback={<Loading />}>
      <IntegrationDetail
        target={resolvedTarget}
        integrationsV2={true}
        onBack={headerBack || twoCol ? undefined : () => onOpen(null)}
        onUninstalled={() => onOpen(null)}
        onPage={(extId, uiId, manager) => go({ kind: "page", id: extId, ui: uiId, manager })}
        onUpgrade={(market, extId, manager) => go({ kind: "item", market, id: extId, manager })}
        onSettings={onSettings}
        onOpenConnector={(id, manager) => drillDown({ kind: "connector", id, manager })}
        onOpenSources={(sourceState, manager) => drillDown({ kind: "sources", ...(sourceState ? { state: sourceState } : {}), ...(manager ? { manager } : {}) })}
      />
    </Suspense>
  ) : null;

  if (detailPane && !twoCol) {
    return detailPane;
  }

  if (compactList && detailPane) {
    return (
      <div
        className="flex h-full min-h-0 min-w-0 flex-1 overflow-hidden"
        data-integrations-split=""
      >
        <div
          data-integrations-list
          className="flex h-full min-h-0 w-[300px] shrink-0 flex-col border-r border-line"
        >
          <div
            data-integrations-list-header
            className="shrink-0 border-b border-line px-3 pt-4 pb-3"
          >
            {listHeader}
            {viewSwitch}
            {filterBar}
          </div>
          <div
            ref={listScrollRef}
            data-integrations-list-scroll
            className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pt-3 pb-8"
          >
            {listBody}
          </div>
        </div>
        <div
          ref={detailScrollRef}
          data-integrations-detail-scroll
          className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
        >
          <div className="mx-auto max-w-[820px] px-6 pt-4 pb-12 sm:px-8">
            {detailPane}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div data-integrations-list>
      {listHeader}
      {viewSwitch}
      <p className="mb-4 px-1 text-meta leading-relaxed text-ink-3">
        {t("settings-integrations.list_intro")}
      </p>
      {filterBar}
      {listBody}
    </div>
  );
}

export default Integrations;
