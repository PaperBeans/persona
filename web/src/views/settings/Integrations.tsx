// Lazy-loaded unified Settings › Integrations & Extensions view (integrations_v2, #14874 / Refs #14802).
// Aggregates built-in services, channels, connectors, and installed extensions into one deduplicated list
// with capability filter chips, Extension Market access, and inline IntegrationDetail (#14868) mounting.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { hubServes, moduleFeature } from "../../core/module-host";
import { t } from "../../i18n";
import type { MailAccount } from "../../lib/agent-settings";
import { api, enc, errorText } from "../../lib/api";
import { listChannels, type Channel } from "../../lib/channels";
import { connectorBrand, listConnectors, type HubConnector } from "../../lib/connectors";
import { scoped, useScope } from "../../lib/current-assistant";
import { hubHasExt } from "../../lib/extension-api";
import { onAuthChange, withAuth } from "../../lib/extension-auth";
import { watchOperations } from "../../lib/extension-operations";
import { EXTENSIONS_CHANGED } from "../../lib/extension-prompts";
import { extensionDetail, extensionTone, listExtensionsInfo, type HubExtension, type HubExtensionDetail } from "../../lib/extensions";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { botStatus, type FeishuBotStatus } from "../../lib/feishu-bot";
import { defaultManager, otherManager, pinManagerAt, useChosenManager } from "../../lib/manager-choice";
import { isResumed } from "../../lib/reconnect";
import { onHubEvent, useLarkOn, useStore } from "../../lib/store";
import { Button, cx, IconButton, Loading } from "../../ui/atoms";
import { BrandMark } from "../../ui/BrandMark";
import { ChannelMark } from "../../ui/ChannelMark";
import { ExtensionLogo } from "../../ui/ExtensionLogo";
import { Icon } from "../../ui/icons";
import { OfflineState, useShownOffline } from "../ManagerParts";
import { MarketCatalog, MarketItem, Markets } from "./ExtensionMarket";
import { ExtensionUI } from "./ExtensionView";
import { IntegrationDetail } from "./IntegrationDetail";
import { SentSettings } from "./SentSettings";
import { Group, Pill, Row, SettingsHint } from "./SettingsParts";
import {
  formatIntegrationsSub,
  isIntegrationDetailRoute,
  parseIntegrationsSub,
  type IntegrationFilter,
  type IntegrationsRoute,
} from "./integrations-route";
import {
  buildIntegrationCatalog,
  filterIntegrationItems,
  isIntegrationRowSelected,
  resolveIntegrationTarget,
  type IntegrationCapability,
  type IntegrationItem,
  type IntegrationTone,
  type LarkGatewaySummary,
} from "./integrations/catalog";

export interface IntegrationsProps {
  open: string | null;
  onOpen: (sub: string | null) => void;
  /** Desktop split-pane layout (`min-width: 900px`): list stays beside the open integration detail. */
  twoCol?: boolean;
  /** The page's top bar already provides Back (phone), so subviews omit their own Back button. */
  headerBack?: boolean;
  /** Initial capability filter (for example when redirected from legacy `/settings/channels` or `/settings/connectors`). */
  initialFilter?: IntegrationFilter;
  /** Navigate to a Settings URL linked from a channel override notice. */
  onSettings?: (url: string) => void;
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

/** A declarative page contributed by an extension, rendered under Settings › Integrations. */
function IntegrationExtensionPage({
  id,
  ui,
  manager,
  onBack,
}: {
  id: string;
  ui: string;
  manager?: string;
  onBack?: () => void;
}) {
  useLayoutEffect(() => pinManagerAt([`/extensions/${enc(id)}`, `/plugins/${enc(id)}`], manager), [id, manager]);
  const [detail, setDetail] = useState<HubExtensionDetail | null>(null);
  const [reloadCount, setReloadCount] = useState(0);
  useEffect(() => {
    extensionDetail(id).then(setDetail, () => {});
  }, [id]);
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
        <ExtensionUI key={reloadCount} extension={id} ui={ui} title={title} manager={manager} />
      </ExtensionOwnerScope>
    </>
  );
}

export function Integrations({
  open,
  onOpen,
  twoCol = false,
  headerBack = false,
  initialFilter = "all",
  onSettings,
}: IntegrationsProps) {
  const route = useMemo(() => parseIntegrationsSub(open), [open]);
  const go = useCallback((next: IntegrationsRoute) => onOpen(formatIntegrationsSub(next)), [onOpen]);

  const picked = useChosenManager();
  const owner = otherManager(picked ?? undefined);
  const scope = useScope();
  const offline = useShownOffline();

  const hasExtensions = useStore((s) => hubHasExt(s.features, "manager"));
  const hasChannels = useStore((s) => s.features.includes("channels"));
  const hasConnectors = useStore((s) => s.features.includes("connectors"));
  const hasMail = useStore((s) => hubServes(moduleFeature("mail"), s));
  const hasLark = useLarkOn();

  const [filter, setFilter] = useState<IntegrationFilter>(initialFilter);
  useEffect(() => {
    if (initialFilter !== "all") setFilter(initialFilter);
  }, [initialFilter]);

  const [extensions, setExtensions] = useState<HubExtension[]>([]);
  const [markets, setMarkets] = useState(false);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [connectors, setConnectors] = useState<HubConnector[]>([]);
  const [mailAccounts, setMailAccounts] = useState<MailAccount[] | null>(null);
  const [larkStatus, setLarkStatus] = useState<LarkGatewaySummary | null>(null);
  const [feishuBotStatus, setFeishuBotStatus] = useState<FeishuBotStatus | null>(null);
  const [hasFeishuBot, setHasFeishuBot] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [partialErrors, setPartialErrors] = useState<string[]>([]);

  const seq = useRef(0);
  const loadAll = useCallback(async () => {
    const mine = ++seq.current;
    setLoading(true);
    const errors: string[] = [];

    const [extRes, chRes, connRes, mailRes, larkRes, botRes] = await Promise.all([
      hasExtensions
        ? listExtensionsInfo().catch((e) => {
            errors.push(errorText(e));
            return null;
          })
        : Promise.resolve(null),
      hasChannels
        ? listChannels(owner).catch((e) => {
            errors.push(errorText(e));
            return null;
          })
        : Promise.resolve(null),
      hasConnectors
        ? listConnectors().catch((e) => {
            errors.push(errorText(e));
            return null;
          })
        : Promise.resolve(null),
      hasMail
        ? api<{ accounts?: MailAccount[] }>("/mail/accounts").then((r) => r.accounts ?? [], () => null)
        : Promise.resolve(null),
      hasLark
        ? api<LarkGatewaySummary>("/lark/status").catch(() => null)
        : Promise.resolve(null),
      botStatus().catch(() => null),
    ]);

    if (mine !== seq.current) return;
    if (extRes) {
      setExtensions(extRes.extensions);
      setMarkets(extRes.markets);
    } else if (!hasExtensions) {
      setExtensions([]);
      setMarkets(false);
    }
    if (chRes) setChannels(chRes);
    else if (!hasChannels) setChannels([]);
    if (connRes) setConnectors(connRes);
    else if (!hasConnectors) setConnectors([]);
    setMailAccounts(mailRes);
    setLarkStatus(larkRes);
    setFeishuBotStatus(botRes);
    setHasFeishuBot(botRes !== null);
    setPartialErrors(errors);
    setLoaded(true);
    setLoading(false);
  }, [hasExtensions, hasChannels, hasConnectors, hasMail, hasLark, owner]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

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
        hasMail,
        hasLark,
        hasFeishuBot,
        mailAccounts,
        larkStatus,
        feishuBotStatus,
        ownerManager: owner,
      }),
    [ownExtensions, channels, ownConnectors, hasMail, hasLark, hasFeishuBot, mailAccounts, larkStatus, feishuBotStatus, owner],
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
  const installedExtensions = useMemo(() => filtered.filter((it) => it.kind === "extension"), [filtered]);

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
      <MarketCatalog
        backLabel={t("settings-integrations.back")}
        onBack={headerBack ? undefined : () => onOpen(null)}
        onOpen={(it) => go({ kind: "item", market: it.market, id: it.id })}
        onManageMarkets={() => go({ kind: "markets" })}
      />
    );
  }
  if (route.kind === "item") {
    return (
      <MarketItem
        key={open ?? ""}
        market={route.market}
        id={route.id}
        manager={route.manager ?? owner}
        onBack={
          headerBack
            ? undefined
            : () => go(route.manager ? { kind: "extension", id: route.id, manager: route.manager } : { kind: "catalog" })
        }
        onInstalled={(id) => go({ kind: "extension", id, manager: route.manager })}
      />
    );
  }
  if (route.kind === "markets") {
    return <Markets onBack={headerBack ? undefined : () => go({ kind: "catalog" })} />;
  }
  if (route.kind === "page") {
    return (
      <IntegrationExtensionPage
        key={`${open ?? ""}@${route.manager ?? ""}`}
        id={route.id}
        ui={route.ui}
        manager={route.manager ?? owner}
        onBack={headerBack ? undefined : () => go({ kind: "extension", id: route.id, manager: route.manager })}
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

  const resolvedTarget = showDetail ? resolveIntegrationTarget(route, items, picked) : null;

  const renderRow = (item: IntegrationItem) => {
    const selected = isIntegrationRowSelected(open, item, picked);
    const status = (
      <span
        className={cx(
          "flex items-center gap-1.5 whitespace-nowrap",
          compactList ? "mt-0.5 min-w-0 text-submeta" : "shrink-0 text-label",
          item.alertTone === "red" ? "text-red" : "text-ink-2",
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
    <div className={cx(compactList ? "mb-2.5 flex items-center gap-1.5" : "mb-3 flex flex-wrap items-center gap-2")}>
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
      <IconButton icon="retry" label={t("settings-integrations.refresh")} onClick={() => void loadAll()} disabled={loading} />
    </div>
  );

  const filterBar = (
    <div
      role="group"
      aria-label={t("settings-integrations.filter_label")}
      data-integrations-filters
      className={cx(
        compactList
          ? "-mx-1 -my-0.5 flex flex-nowrap items-center gap-1 overflow-x-auto overscroll-x-contain px-1 py-0.5 scrollbar-none"
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

      {!loaded && (
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

      {loaded && builtins.length > 0 && (
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

      {loaded && installedExtensions.length > 0 && (
        <Group
          anchor={builtins.length === 0 ? "integrations" : "extensions"}
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

  const detailPane = showDetail && resolvedTarget ? (
    <IntegrationDetail
      target={resolvedTarget}
      integrationsV2={true}
      onBack={headerBack || twoCol ? undefined : () => onOpen(null)}
      onUninstalled={() => onOpen(null)}
      onPage={(extId, uiId, manager) => go({ kind: "page", id: extId, ui: uiId, manager })}
      onUpgrade={(market, extId, manager) => go({ kind: "item", market, id: extId, manager })}
      onSettings={onSettings}
      onOpenConnector={(id, manager) => go({ kind: "connector", id, manager })}
    />
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
      <p className="mb-4 px-1 text-meta leading-relaxed text-ink-3">
        {t("settings-integrations.list_intro")}
      </p>
      {filterBar}
      {listBody}
    </div>
  );
}

export default Integrations;
