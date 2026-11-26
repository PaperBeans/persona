// Lazy-loaded unified Integration Detail view (integrations_v2, #14847 / Refs #14802).
// Composes Account & Connection, Chat & Delivery (with thread_mode & thread_idle_hours),
// Inbox & Sync, section-grouped Extension Settings, and Plugin Management on a single page
// for both built-in services and installed extensions. Strictly gated by integrations_v2.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { formatList, t } from "../../i18n";
import { ConnectorExternalSourceSettings } from "../../features/external-sources/ExternalSourceSettingsEntry";
import { api, enc, errorText } from "../../lib/api";
import { settingsTarget } from "../../lib/channels";
import {
  connectorBrand, connectorState, connectorTone, listConnectors,
  type ConnectorTone, type HubConnector,
} from "../../lib/connectors";
import { getAuth, onAuthChange, shownPages, withAuth, type ExtensionAuth } from "../../lib/extension-auth";
import { marketOf } from "../../lib/extension-market";
import { dependencyText, dependentError, type Contribution, type Dependent } from "../../lib/extension-model";
import {
  changed, channelOf, checkError, clearedSecrets, compact, configError, extensionDetail,
  extensionTone, fieldLabel, fieldPath, isDirty, isEntryList, numberProblems, pointText,
  readNumber, rebaseDraft, runNote, saveFooter, secretState, setAt, setContribution,
  setExtensionAutoUpgrade, setExtensionConfig, setExtensionConfigPreservingEntries,
  setExtensionEnabled, setupSecret, stateLine, valueAt,
  type ExtensionField, type ExtensionSecret, type HubExtensionDetail, type Json, type JsonObject,
} from "../../lib/extensions";
import { defaultManager, itemHeaders, otherManager, pinManagerAt, sameManager, useChosenManager } from "../../lib/manager-choice";
import { useRoster } from "../../lib/managers";
import { isResumed } from "../../lib/reconnect";
import { navigate } from "../../lib/router";
import { onHubEvent, useStore } from "../../lib/store";
import { Button, cx, Dialog, IconButton, Loading, Spinner, toast } from "../../ui/atoms";
import { BrandMark } from "../../ui/BrandMark";
import { ChannelMark } from "../../ui/ChannelMark";
import { ExtensionLogo } from "../../ui/ExtensionLogo";
import { Icon } from "../../ui/icons";
import { countsOf, useSubscriptionSummary } from "../../lib/subscriptions";
import { SubscriptionsEntry } from "../subscriptions/SubscriptionsEntry";
import { Feishu, Mail, MailInboxSwitch } from "./Connectors";
import { AuthGroup } from "./ExtensionAuth";
import { ExtensionInfoAccess } from "./ExtensionInfoAccess";
import { PackMemberRow, UninstallDialog, useInstalledNames } from "./ExtensionMarket";
import {
  FieldRow, hasLauncherField, SandboxGroup, SecretDialog, SecretProviderGroups,
  useFormLaunchers, type FieldProps, type Typed,
} from "./Extensions";
import {
  enableFooter, Information as ExtensionInformationGroup, lifecycleReason,
  Updates as ExtensionUpdatesGroup,
} from "./ExtensionSettingsPage";
import { OwnerMark } from "./OwnerMark";
import { Disclosure } from "./ProviderFields";
import { settingsHref } from "./href";
import { Group, Pill, Row, SettingsHint, Switch } from "./SettingsParts";
import { ChannelBindingBlock, ChannelDeliveryBlock, ChannelStateInline, useChannelController } from "./integrations/ChannelSection";
import { ConnectorConnectionBlock, connectorHasInbox, ConnectorInboxBlock } from "./integrations/ConnectorSection";
import { FeishuBotConnectionPanel, FeishuBotDeliveryPanel, useFeishuBotController } from "./integrations/FeishuBotSection";
import {
  fieldsHaveError, isIntegrationsV2Active, partitionExtensionFields,
  resolveBuiltinChannelId, resolveBuiltinConnectorId,
  type BuiltinIntegrationId, type IntegrationSectionId, type IntegrationTarget,
} from "./integrations/model";

export type { BuiltinIntegrationId, IntegrationSectionId, IntegrationTarget };
export { isIntegrationsV2Active, partitionExtensionFields };

export interface IntegrationDetailProps {
  target: IntegrationTarget;
  /** Back action; omit when the top bar (phone) or desktop split-pane list already provides navigation. */
  onBack?: () => void;
  /** Called after an extension is uninstalled so callers can close the detail pane even when `onBack` is omitted. */
  onUninstalled?: () => void;
  /** Open a declarative UI page contributed by an extension. */
  onPage?: (extensionId: string, uiId: string, manager?: string) => void;
  /** Open the marketplace upgrade confirmation flow for an extension. */
  onUpgrade?: (market: string, id: string, manager?: string) => void;
  /** Navigate to a legacy settings URL when linked by a channel override hint. */
  onSettings?: (url: string) => void;
  /** Open a related connector integration (for example larkcli from Feishu Bot). */
  onOpenConnector?: (id: string, manager?: string) => void;
  /** Open scoped source subscriptions inside unified Integrations instead of leaving Settings. */
  onOpenSources?: (state?: string, manager?: string) => void;
  /**
   * Optional gate override; when omitted, follows the active assistant's `integrations_v2` flag.
   * Always fails closed when `integrations_v2` is false or absent.
   */
  integrationsV2?: boolean;
  /** Rendered when `integrations_v2` is false (defaults to null so legacy pages stay unchanged). */
  fallback?: ReactNode;
}

const NARROW_LIST: Intl.ListFormatOptions = { type: "conjunction", style: "narrow" };
const EXT_DOT: Record<ReturnType<typeof extensionTone>, string> = {
  red: "bg-red",
  orange: "bg-orange",
  green: "bg-green",
  idle: "bg-line-strong",
};
const EXT_TILE: Record<ReturnType<typeof extensionTone>, string> = {
  red: "bg-red-tint text-red",
  orange: "bg-orange-tint text-orange",
  green: "bg-accent-tint text-accent-ink",
  idle: "bg-inset text-ink-3 shadow-hairline",
};
const CONN_DOT: Record<ConnectorTone, string> = {
  green: "bg-green",
  orange: "bg-orange",
  red: "bg-red",
  idle: "bg-line-strong",
};

function SectionBlock({ id, title, children }: { id: IntegrationSectionId; title: string; children: ReactNode }) {
  return (
    <section data-integration-section={id} aria-label={title} className="mb-6">
      <h2 className="mb-2 px-1 text-label font-semibold text-ink-3">{title}</h2>
      {children}
    </section>
  );
}

function openSettingsTarget(url: string, onSettings?: (url: string) => void) {
  if (onSettings) {
    onSettings(url);
    return;
  }
  const parsed = settingsTarget(url);
  if (!parsed) return;
  navigate(settingsHref(parsed.tab, parsed.sub, false));
}

function isGitHubIntegration(id: string, connectorIds: readonly string[] = [], repoProviders?: readonly string[]): boolean {
  return id === "github" || connectorIds.includes("github") || (repoProviders?.includes("github") ?? false);
}

function SavedRepositoriesShortcut({ manager, onSettings }: { manager?: string; onSettings?: (url: string) => void }) {
  const [count, setCount] = useState<number | null>(null);
  useEffect(() => {
    let alive = true;
    api<{ repositories?: unknown[] }>("/assistant/repositories", manager ? { headers: itemHeaders(manager) } : undefined)
      .then((res) => {
        if (alive && Array.isArray(res?.repositories)) setCount(res.repositories.length);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [manager]);

  return (
    <Group anchor="integration-saved-repositories">
      <Row
        anchor="saved-repositories-shortcut"
        icon="git"
        label={t("settings-integrations.saved_repositories_title")}
        hint={t("settings-integrations.saved_repositories_hint")}
        wrapHint
        onClick={() => openSettingsTarget("settings/persona#repositories", onSettings)}
      >
        {count !== null ? (
          <span className="tabular-nums">
            {count === 0
              ? t("settings-integrations.saved_repositories_empty")
              : t("settings-integrations.saved_repositories_count", { count })}
          </span>
        ) : undefined}
      </Row>
    </Group>
  );
}

function KnowledgeScopeShortcut({ onSettings }: { onSettings?: (url: string) => void }) {
  return (
    <Group anchor="integration-knowledge-scope">
      <Row
        anchor="knowledge-scope-shortcut"
        icon="filter"
        label={t("settings-integrations.knowledge_scope_title")}
        hint={t("settings-integrations.knowledge_scope_hint")}
        wrapHint
        onClick={() => openSettingsTarget("settings/knowledge#knowledge-items", onSettings)}
      />
    </Group>
  );
}

/** Renders a slice of extension config fields (flat fields + object groups) sharing the unified draft. */
function SectionFieldGroups({
  fields,
  title,
  footer,
  fieldProps,
}: {
  fields: ExtensionField[];
  title?: string;
  footer?: string;
  fieldProps: Omit<FieldProps, "field" | "path">;
}) {
  if (!fields.length) return null;
  const flat = fields.filter((f) => f.type !== "object");
  const groups = fields.filter((f) => f.type === "object");
  return (
    <>
      {flat.length > 0 && (
        <Group title={title} footer={footer}>
          {flat.map((f) => (
            <FieldRow key={f.name} {...fieldProps} field={f} path={[f.name]} />
          ))}
        </Group>
      )}
      {groups.map((g) => (
        <Group key={g.name} title={fieldLabel(g)} footer={g.description}>
          {(g.fields ?? []).map((f) => (
            <FieldRow key={f.name} {...fieldProps} field={f} path={[g.name, f.name]} />
          ))}
        </Group>
      ))}
    </>
  );
}

/**
 * Unified Integration Detail page under `integrations_v2`.
 * When `integrations_v2` is false, renders `fallback` (default null) with zero side effects or API requests.
 */
export function IntegrationDetail(props: IntegrationDetailProps) {
  const picked = useChosenManager();
  const effectiveManager = props.target.manager ?? otherManager(picked ?? undefined);
  const storeEnabled = useStore((s) => isIntegrationsV2Active(s.assistant, effectiveManager, picked));
  const enabled = props.integrationsV2 ?? storeEnabled;

  if (!enabled) return <>{props.fallback ?? null}</>;

  if (props.target.kind === "extension") {
    return (
      <ExtensionIntegrationDetail
        key={`ext:${props.target.id}@${effectiveManager ?? ""}`}
        target={props.target}
        manager={effectiveManager}
        onBack={props.onBack}
        onUninstalled={props.onUninstalled}
        onPage={props.onPage}
        onUpgrade={props.onUpgrade}
        onSettings={props.onSettings}
        onOpenSources={props.onOpenSources}
      />
    );
  }

  if (props.target.id === "feishu-bot") {
    return (
      <FeishuBotBuiltinDetail
        key={`builtin:feishu-bot@${effectiveManager ?? ""}`}
        target={props.target}
        manager={effectiveManager}
        onBack={props.onBack}
        onSettings={props.onSettings}
        onOpenConnector={props.onOpenConnector}
        onOpenSources={props.onOpenSources}
      />
    );
  }

  return (
    <BuiltinIntegrationDetail
      key={`builtin:${props.target.id}@${effectiveManager ?? ""}`}
      target={props.target}
      manager={effectiveManager}
      onBack={props.onBack}
      onSettings={props.onSettings}
      onOpenSources={props.onOpenSources}
    />
  );
}

export default IntegrationDetail;

// ── Built-in services (Mail, Feishu Bot, Lark Gateway, native hub connectors) ────────────

function BuiltinMark({ id, channel }: { id: string; channel?: ReturnType<typeof useChannelController>["detail"] }) {
  if (channel) return <ChannelMark c={channel} size={44} />;
  const brand = connectorBrand(id === "feishu-bot" ? "lark" : id);
  if (brand) {
    return (
      <span className="flex size-11 shrink-0 items-center justify-center rounded-card bg-surface shadow-hairline">
        <BrandMark brand={brand} size={44} fit="tile" />
      </span>
    );
  }
  const icon = id === "mail" ? "mail" : id === "imessage" || id === "lark" ? "chat" : id === "feishu-bot" ? "send" : "link";
  return (
    <span className="flex size-11 shrink-0 items-center justify-center rounded-card bg-accent-tint text-accent-ink">
      <Icon name={icon} size={20} />
    </span>
  );
}

function FeishuBotBuiltinDetail({
  target,
  manager,
  onBack,
  onSettings,
  onOpenConnector,
  onOpenSources,
}: {
  target: Extract<IntegrationTarget, { kind: "builtin" }>;
  manager?: string;
  onBack?: () => void;
  onSettings?: (url: string) => void;
  onOpenConnector?: (id: string, manager?: string) => void;
  onOpenSources?: (state?: string, manager?: string) => void;
}) {
  const channelId = resolveBuiltinChannelId(target) ?? "feishu";
  useLayoutEffect(() => pinManagerAt(["/bots", "/connectors/larkcli"], otherManager(manager)), [manager]);

  const channelCtl = useChannelController(channelId, manager);
  const [larkcli, setLarkcli] = useState<HubConnector | null>(null);
  const [connectorLoading, setConnectorLoading] = useState(false);

  const loadLarkcli = useCallback(async () => {
    setConnectorLoading(true);
    try {
      const list = await listConnectors();
      setLarkcli(list.find((x) => x.id === "larkcli" && sameManager(x.manager, manager)) ?? null);
    } catch {
      setLarkcli(null);
    } finally {
      setConnectorLoading(false);
    }
  }, [manager]);

  useEffect(() => {
    void loadLarkcli();
  }, [loadLarkcli]);

  const feishuCtl = useFeishuBotController(true, loadLarkcli);

  const refreshAll = () => {
    void loadLarkcli();
    void channelCtl.load();
    void feishuCtl.load();
  };

  const title = target.name ?? feishuCtl.status?.bot_name ?? t("settings-feishubot.title");
  const description = target.description ?? t("settings-integrations.feishu_bot_description");
  const showInboxSection = !!larkcli && connectorHasInbox(larkcli);

  return (
    <div data-integration-detail={target.id} data-integration-kind="builtin">
      <div className="mb-3 flex items-center gap-2">
        {onBack && (
          <Button tone="quiet" icon="chevronLeft" onClick={onBack} className="-ml-2">
            {t("settings-integrations.back")}
          </Button>
        )}
        <OwnerMark manager={manager} />
        <span className="flex-1" />
        <IconButton
          icon="retry"
          label={t("settings-integrations.refresh")}
          onClick={refreshAll}
          disabled={connectorLoading || channelCtl.loading || feishuCtl.loading}
        />
      </div>

      <section className="mb-6 overflow-hidden rounded-card bg-surface shadow-card">
        <div className="flex items-center gap-3 px-4 py-3.5">
          <BuiltinMark id={target.id} channel={channelCtl.detail} />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h1 className="truncate text-card-title font-semibold text-ink">{title}</h1>
              <Pill>{t("settings-integrations.builtin_badge")}</Pill>
            </div>
            {channelCtl.detail && (
              <div className="mt-0.5 flex min-w-0 items-center gap-2 text-label text-ink-3">
                <ChannelStateInline c={channelCtl.detail} />
                {channelCtl.detail.account?.name && <span className="truncate">{channelCtl.detail.account.name}</span>}
              </div>
            )}
          </div>
          {(channelCtl.loading || feishuCtl.loading) && !channelCtl.detail && !feishuCtl.status && <Spinner />}
        </div>
        {description && (
          <div className="border-t border-line px-4 py-3">
            <p className="text-meta leading-relaxed text-ink-2">{description}</p>
          </div>
        )}
      </section>

      {/* 1. Account & Connection */}
      <SectionBlock id="connection" title={t("settings-integrations.section_connection")}>
        <FeishuBotConnectionPanel
          ctl={feishuCtl}
          larkcli={larkcli}
          onOpenConnector={(id) => onOpenConnector?.(id, manager)}
        />
      </SectionBlock>

      {/* 2. Chat & Delivery */}
      <SectionBlock id="channel" title={t("settings-integrations.section_channel")}>
        <ChannelDeliveryBlock ctl={channelCtl} onSettings={onSettings} />
        <FeishuBotDeliveryPanel ctl={feishuCtl} />
      </SectionBlock>
      <ConnectorExternalSourceSettings ids={[channelId]} manager={manager} />

      {/* 3. Inbox & Sync */}
      <SectionBlock id="inbox" title={t("settings-integrations.section_inbox")}>
        {showInboxSection && larkcli && (
          <>
            <ConnectorInboxBlock connector={larkcli} reload={loadLarkcli} onSaved={setLarkcli} />
            {!manager && (
              <SubscriptionsEntry
                pick={(l) => l.connector === "larkcli"}
                scope={{ c: "larkcli" }}
                onOpen={onOpenSources ? (st) => onOpenSources(st, manager) : undefined}
              />
            )}
          </>
        )}
        <KnowledgeScopeShortcut onSettings={onSettings} />
      </SectionBlock>
    </div>
  );
}

function BuiltinIntegrationDetail({
  target,
  manager,
  onBack,
  onSettings,
  onOpenSources,
}: {
  target: Extract<IntegrationTarget, { kind: "builtin" }>;
  manager?: string;
  onBack?: () => void;
  onSettings?: (url: string) => void;
  onOpenSources?: (state?: string, manager?: string) => void;
}) {
  const isMail = target.id === "mail";
  const isLarkGateway = target.id === "lark";
  const channelId = resolveBuiltinChannelId(target);
  const connectorId = resolveBuiltinConnectorId(target);

  useLayoutEffect(() => {
    const prefixes: string[] = [];
    if (isMail) prefixes.push("/mail", "/connectors/mail");
    if (isLarkGateway) prefixes.push("/lark");
    if (connectorId) {
      prefixes.push(`/connectors/${enc(connectorId)}`);
      if (connectorId === "imessage") prefixes.push("/imessage");
    }
    if (!prefixes.length) return undefined;
    return pinManagerAt(prefixes, otherManager(manager));
  }, [isMail, isLarkGateway, connectorId, manager]);

  const channelCtl = useChannelController(channelId, manager);

  const [connector, setConnector] = useState<HubConnector | null>(null);
  const [connectorError, setConnectorError] = useState("");
  const [connectorLoading, setConnectorLoading] = useState(false);

  const loadConnector = useCallback(async () => {
    if (!connectorId) return;
    setConnectorLoading(true);
    try {
      const list = await listConnectors();
      const found = list.find((x) => x.id === connectorId && sameManager(x.manager, manager)) ?? null;
      setConnector(found);
      setConnectorError(found ? "" : t("settings-hubconnector.not_on_hub"));
    } catch (e) {
      setConnectorError(errorText(e));
    } finally {
      setConnectorLoading(false);
    }
  }, [connectorId, manager]);

  useEffect(() => {
    void loadConnector();
  }, [loadConnector]);

  const refreshAll = () => {
    void loadConnector();
    if (channelId) void channelCtl.load();
  };

  const title =
    target.name ??
    (isMail
      ? t("settings-connectors.mail")
      : isLarkGateway
        ? t("settings-connectors.lark")
        : connector?.name ?? (target.id === "github" ? "GitHub" : target.id));

  const description =
    target.description ??
    (isMail
      ? t("settings-integrations.mail_description")
      : isLarkGateway
        ? t("settings-integrations.lark_gateway_description")
        : channelCtl.detail?.description);

  const subscriptionConnectorId = connectorId ?? (isMail ? "mail" : isLarkGateway ? "lark" : null);
  const larkSubCounts = countsOf(useSubscriptionSummary(), (l) => isLarkGateway && l.connector === "lark");
  const isGitHub = isGitHubIntegration(target.id, connectorId ? [connectorId] : []);
  const showKnowledgeScope =
    isMail ||
    isLarkGateway ||
    connectorId === "larkcli" ||
    (connector?.capabilities?.includes("history") ?? false);
  const showInboxSection =
    isMail ||
    showKnowledgeScope ||
    (isLarkGateway && !manager && larkSubCounts !== null) ||
    (!!connector && connectorHasInbox(connector)) ||
    (!!connectorId && !manager);

  return (
    <div data-integration-detail={target.id} data-integration-kind="builtin">
      <div className="mb-3 flex items-center gap-2">
        {onBack && (
          <Button tone="quiet" icon="chevronLeft" onClick={onBack} className="-ml-2">
            {t("settings-integrations.back")}
          </Button>
        )}
        <OwnerMark manager={manager} />
        <span className="flex-1" />
        <IconButton
          icon="retry"
          label={t("settings-integrations.refresh")}
          onClick={refreshAll}
          disabled={connectorLoading || channelCtl.loading}
        />
      </div>

      {/* Header card */}
      <section className="mb-6 overflow-hidden rounded-card bg-surface shadow-card">
        <div className="flex items-center gap-3 px-4 py-3.5">
          <BuiltinMark id={target.id} channel={channelCtl.detail} />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h1 className="truncate text-card-title font-semibold text-ink">{title}</h1>
              <Pill>{t("settings-integrations.builtin_badge")}</Pill>
            </div>
            {connector && (
              <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-label text-ink-3">
                <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", CONN_DOT[connectorTone(connector)])} />
                <span className="truncate">
                  {connectorState(connector)}
                  {connector.account ? ` · ${connector.account}` : ""}
                </span>
              </div>
            )}
            {!connector && channelCtl.detail && (
              <div className="mt-0.5 flex min-w-0 items-center gap-2 text-label text-ink-3">
                <ChannelStateInline c={channelCtl.detail} />
                {channelCtl.detail.account?.name && <span className="truncate">{channelCtl.detail.account.name}</span>}
              </div>
            )}
          </div>
          {(connectorLoading || channelCtl.loading) && !connector && !channelCtl.detail && <Spinner />}
        </div>
        {description && (
          <div className="border-t border-line px-4 py-3">
            <p className="text-meta leading-relaxed text-ink-2">{description}</p>
          </div>
        )}
      </section>

      {/* 1. Account & Connection */}
      <SectionBlock id="connection" title={t("settings-integrations.section_connection")}>
        {isMail && <Mail hideInboxSwitch manager={manager} />}
        {isLarkGateway && <Feishu manager={manager} />}
        {connectorId && (
          <>
            {connector ? (
              <ConnectorConnectionBlock connector={connector} reload={loadConnector} loading={connectorLoading} />
            ) : connectorError ? (
              <Group>
                <SettingsHint alert>{t("settings-integrations.connector_unavailable", { error: connectorError })}</SettingsHint>
              </Group>
            ) : (
              <Group><div className="px-4 py-3"><Loading /></div></Group>
            )}
          </>
        )}
        {channelId && <ChannelBindingBlock ctl={channelCtl} />}
        {isGitHub && <SavedRepositoriesShortcut manager={manager} onSettings={onSettings} />}
      </SectionBlock>

      {/* 2. Chat & Delivery */}
      {channelId && (
        <SectionBlock id="channel" title={t("settings-integrations.section_channel")}>
          <ChannelDeliveryBlock ctl={channelCtl} onSettings={onSettings} />
        </SectionBlock>
      )}

      {/* 3. Inbox & Sync */}
      {showInboxSection && (
        <SectionBlock id="inbox" title={t("settings-integrations.section_inbox")}>
          {isMail && (
            <Group>
              <MailInboxSwitch />
            </Group>
          )}
          {connector && connectorHasInbox(connector) && (
            <ConnectorInboxBlock connector={connector} reload={loadConnector} onSaved={setConnector} />
          )}
          {subscriptionConnectorId && !manager && (
            <SubscriptionsEntry
              pick={(l) => l.connector === subscriptionConnectorId}
              scope={{ c: subscriptionConnectorId }}
              onOpen={onOpenSources ? (st) => onOpenSources(st, manager) : undefined}
            />
          )}
          {showKnowledgeScope && <KnowledgeScopeShortcut onSettings={onSettings} />}
        </SectionBlock>
      )}
      {!isMail && !isLarkGateway && <ConnectorExternalSourceSettings ids={[connectorId ?? "", channelId ?? ""]} manager={manager} />}
    </div>
  );
}

// ── External extensions (including composite connector + channel + config plugins like larkx) ──

function ExtensionIntegrationDetail({
  target,
  manager,
  onBack: back,
  onUninstalled,
  onPage,
  onUpgrade,
  onSettings,
  onOpenSources,
}: {
  target: Extract<IntegrationTarget, { kind: "extension" }>;
  manager?: string;
  onBack?: () => void;
  onUninstalled?: () => void;
  onPage?: (extensionId: string, uiId: string, manager?: string) => void;
  onUpgrade?: (market: string, id: string, manager?: string) => void;
  onSettings?: (url: string) => void;
  onOpenSources?: (state?: string, manager?: string) => void;
}) {
  const id = target.id;
  useLayoutEffect(() => pinManagerAt([`/extensions/${enc(id)}`, `/plugins/${enc(id)}`], manager), [id, manager]);

  const roster = useRoster();
  const offline = roster?.managers.find((m) => m.id === (manager || defaultManager()))?.online === false;

  const [detail, setDetail] = useState<HubExtensionDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [draft, setDraft] = useState<JsonObject>({});
  const [typed, setTyped] = useState<Typed>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [checkFailed, setCheckFailed] = useState("");
  const [saving, setSaving] = useState(false);
  const [toggling, setToggling] = useState(false);
  const [secret, setSecret] = useState<ExtensionSecret | null>(null);
  const [removing, setRemoving] = useState(false);
  const [dependents, setDependents] = useState<Dependent[] | null>(null);
  const [packOff, setPackOff] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const [connectors, setConnectors] = useState<HubConnector[]>([]);
  const [connectorsLoading, setConnectorsLoading] = useState(false);

  const order = useRef({ asked: 0, shown: 0 });
  const editRevision = useRef(0);
  const draftNow = useRef(draft);
  const typedNow = useRef(typed);
  draftNow.current = draft;
  typedNow.current = typed;

  const apply = useCallback((d: HubExtensionDetail) => {
    setDetail(d);
    setDraft(d.config ?? {});
    setError("");
  }, []);

  const readDetail = useCallback((then: (d: HubExtensionDetail) => void, failed?: (e: unknown) => void) => {
    const n = ++order.current.asked;
    return extensionDetail(id).then(
      (d) => {
        if (n <= order.current.shown) return;
        order.current.shown = n;
        then(d);
      },
      (e) => void (n > order.current.shown && failed?.(e)),
    );
  }, [id]);

  const writing = () => ++order.current.asked;
  const wrote = (n: number, d: HubExtensionDetail) => {
    if (n <= order.current.shown) return false;
    order.current.shown = n;
    setDetail(d);
    return true;
  };

  const load = useCallback(() => {
    setLoading(true);
    void readDetail((d) => {
      apply(d);
      setTyped({});
      setFieldErrors({});
    }, (e) => setError(errorText(e))).finally(() => setLoading(false));
  }, [apply, readDetail]);

  useEffect(() => {
    load();
  }, [load]);

  // Keep extension auth updates in sync.
  useEffect(
    () => onAuthChange((x, auth, from) => (manager || defaultManager()) === from && setDetail((cur) => (cur ? withAuth(cur, x, auth) : cur))),
    [manager],
  );
  const authChanged = useCallback((a?: ExtensionAuth) => {
    if (a) setDetail((cur) => (cur ? { ...cur, auth: a } : cur));
    else void getAuth(id).then((v) => setDetail((cur) => (cur ? { ...cur, auth: v } : cur)), () => {});
  }, [id]);
  const signsIn = !!detail?.auth;
  useEffect(() => (signsIn ? onHubEvent("hello", (m) => !isResumed(m) && authChanged()) : undefined), [id, signsIn, authChanged]);

  // Associated channel (from explicit target override or extension's contributes.channel).
  const channelId = target.channelId ?? (detail ? channelOf(detail) : null);
  const channelCtl = useChannelController(channelId, manager);

  // Associated connectors (from explicit target override or extension's contributes.connectors).
  const connectorIds = useMemo(() => {
    const ids = new Set<string>();
    if (target.connectorId) ids.add(target.connectorId);
    for (const cid of detail?.contributes?.connectors ?? []) {
      if (cid.trim()) ids.add(cid);
    }
    return [...ids];
  }, [target.connectorId, detail?.contributes?.connectors]);
  const connectorKey = connectorIds.join(",");

  const loadConnectors = useCallback(async () => {
    if (!connectorKey) {
      setConnectors([]);
      return;
    }
    setConnectorsLoading(true);
    try {
      const all = await listConnectors();
      const wanted = connectorKey.split(",");
      setConnectors(wanted.flatMap((cid) => all.find((c) => c.id === cid && sameManager(c.manager, manager)) ?? []));
    } catch {
      setConnectors([]);
    } finally {
      setConnectorsLoading(false);
    }
  }, [connectorKey, manager]);

  useEffect(() => {
    void loadConnectors();
  }, [loadConnectors]);

  const problems = useMemo(() => numberProblems(typed), [typed]);
  const allFields = useMemo(() => detail?.form?.fields ?? [], [detail?.form?.fields]);
  const sections = useMemo(() => partitionExtensionFields(allFields), [allFields]);
  const requiredEntryLists = useMemo(
    () => new Set(allFields.filter((f) => isEntryList(f) && f.required).map((f) => f.name)),
    [allFields],
  );
  const dirty = !!detail && (isDirty(draft, detail.config, requiredEntryLists) || Object.keys(problems).length > 0);
  const dirtyNow = useRef(dirty);
  dirtyNow.current = dirty;
  const canSave = dirty && !Object.keys(problems).length && !saving && !offline;
  const editable = !!detail && !detail.read_only && detail.source !== "dev" && detail.source !== "hub.json";

  // Auto-open the Advanced disclosure when a validation error targets a field inside it.
  const advancedError = useMemo(
    () => fieldsHaveError(sections.advanced, fieldErrors, problems),
    [sections.advanced, fieldErrors, problems],
  );
  useEffect(() => {
    if (advancedError) setAdvancedOpen(true);
  }, [advancedError]);

  const edit = (path: string[], value: Json | undefined) => {
    editRevision.current++;
    setDraft((d) => setAt(d, path, value));
    const key = fieldPath(path);
    if (fieldErrors[key] || fieldErrors[""]) setFieldErrors(({ [key]: _a, [""]: _b, ...rest }) => rest);
    setCheckFailed("");
  };
  const type = (path: string[], text: string, integer: boolean) => {
    const key = fieldPath(path);
    setTyped((prev) => ({ ...prev, [key]: { text, integer } }));
    setCheckFailed("");
    const n = readNumber(text, integer);
    if (!n.error) edit(path, n.value);
    else editRevision.current++;
  };
  const revert = () => {
    if (!detail) return;
    editRevision.current++;
    setDraft(detail.config ?? {});
    setTyped({});
    setFieldErrors({});
    setCheckFailed("");
  };

  const save = async () => {
    if (!canSave) return;
    setSaving(true);
    setFieldErrors({});
    setCheckFailed("");
    const n = writing();
    const submitted = draft;
    const revision = editRevision.current;
    try {
      const d = await setExtensionConfig(id, compact(draft, requiredEntryLists));
      const unchanged = editRevision.current === revision;
      if (wrote(n, d)) {
        if (unchanged) apply(d);
        else {
          const fresh = d.config ?? {};
          const next = rebaseDraft({ base: submitted, draft: draftNow.current, typed: typedNow.current }, fresh, requiredEntryLists);
          setDraft(next.draft);
          setTyped(next.typed);
          setError("");
        }
      }
      if (unchanged) setTyped({});
      toast(t("settings-extensions.saved_restart"), "ok");
    } catch (e) {
      const ce = configError(e);
      const refused = checkError(e);
      if (ce) setFieldErrors({ [ce.field]: ce.message });
      else if (refused) setCheckFailed(refused);
      else toast(t("settings-extensions.not_saved", { error: errorText(e) }), "error");
      if (clearedSecrets(e).length) {
        changed(null);
        void readDetail(setDetail);
      }
    } finally {
      setSaving(false);
    }
  };

  const saveNow = async (path: string[], value: Json | undefined) => {
    if (!detail) return;
    const saved = detail.config ?? {};
    editRevision.current++;
    setDraft((d) => setAt(d, path, value));
    const n = writing();
    try {
      const d = await setExtensionConfigPreservingEntries(id, compact(setAt(saved, path, value), requiredEntryLists));
      if (wrote(n, d)) setDraft((cur) => setAt(cur, path, valueAt(d.config ?? {}, path)));
    } catch (e) {
      setDraft((cur) => setAt(cur, path, valueAt(saved, path)));
      toast(t("settings-extensions.not_saved", { error: errorText(e) }), "error");
    }
  };

  const toggle = async (on: boolean, cascade = false) => {
    setToggling(true);
    const n = writing();
    try {
      const d = await setExtensionEnabled(id, on, cascade);
      wrote(n, d);
      setDependents(null);
      setPackOff(false);
      toast(t(on ? "settings-extensions.enabled_name" : "settings-extensions.disabled_name", { name: d.name }), "ok");
    } catch (e) {
      const deps = dependentError(e);
      setPackOff(false);
      if (!on && deps) setDependents(deps);
      else toast(t(on ? "settings-extensions.enable_failed" : "settings-extensions.disable_failed", { error: errorText(e) }), "error");
    } finally {
      setToggling(false);
    }
  };

  const toggleAutoUpgrade = async (on: boolean) => {
    setToggling(true);
    const n = writing();
    try {
      wrote(n, await setExtensionAutoUpgrade(id, on));
      toast(on ? t("settings-extensions.auto_upgrade_on") : t("settings-extensions.auto_upgrade_off"), "ok");
    } catch (e) {
      toast(t(on ? "settings-extensions.auto_upgrade_on_failed" : "settings-extensions.auto_upgrade_off_failed", { error: errorText(e) }), "error");
    } finally {
      setToggling(false);
    }
  };

  const toggleContribution = async (c: Contribution, on: boolean) => {
    setToggling(true);
    const n = writing();
    try {
      wrote(n, await setContribution(id, c.point, c.id, on));
      toast(on ? t("settings-extensions.contribution_on") : t("settings-extensions.contribution_off"), "ok");
    } catch (e) {
      toast(t("settings-extensions.change_failed", { error: errorText(e) }), "error");
    } finally {
      setToggling(false);
    }
  };

  const computers = !!detail?.contributes?.computer;
  const nodes = detail?.node_machines ? { field: detail.node_machines, states: detail.nodes ?? [] } : undefined;
  const launchers = useFormLaunchers(hasLauncherField(detail?.form?.fields), detail?.id);
  const memberNames = useInstalledNames(!!detail?.extension_pack?.length);

  const sharedFieldProps: Omit<FieldProps, "field" | "path"> = {
    draft,
    typed,
    errors: fieldErrors,
    problems,
    onEdit: edit,
    onType: type,
    onSaveNow: saveNow,
    computers,
    nodes,
    launchers,
    secrets: detail?.secrets,
    onSecret: setSecret,
  };

  const p = detail;
  const setupTarget = editable ? setupSecret(p?.needs_setup, p?.secrets) : undefined;
  const topSecrets = (p?.secrets ?? []).filter((s) => !s.list);
  const uiPages = p ? shownPages(p.ui, p.auth) : [];

  const isGitHub = isGitHubIntegration(id, connectorIds, p?.contributes?.repository_providers);
  const showKnowledgeScope = connectors.some((c) => c.capabilities?.includes("history")) || id === "larkx";

  const hasConnectionSection =
    !!p && (
      !!p.auth ||
      connectors.length > 0 ||
      !!channelId ||
      isGitHub ||
      (editable && sections.connection.length > 0)
    );
  const hasChannelSection = !!p && (!!channelId || (editable && sections.channel.length > 0));
  const inboxConnectors = connectors.filter(connectorHasInbox);
  const hasInboxSection =
    !!p && (
      inboxConnectors.length > 0 ||
      showKnowledgeScope ||
      (!manager && (p.enabled ?? true) && !!p.contributes?.connectors?.length) ||
      (editable && sections.inbox.length > 0)
    );
  const hasAnyConfigElsewhere =
    sections.connection.length > 0 || sections.channel.length > 0 || sections.inbox.length > 0 ||
    !!p?.auth || connectors.length > 0 || !!channelId;
  const noConfiguration =
    !!p && editable && !allFields.length && !topSecrets.length && !uiPages.length && !hasAnyConfigElsewhere;

  const market = p ? (marketOf(p.origin) || p.upgrade?.market || "") : "";
  const marketManaged = !!p && p.source === "installed" && !p.attached && !p.read_only && !!market;
  const uninstallable = !!p && p.source === "installed" && !p.attached && !p.read_only && !!marketOf(p.origin);
  const automatic = !!p && p.source === "installed" && !p.attached && !p.read_only && p.auto_upgrade !== undefined;
  const marketRemoved = p?.auto_upgrade_paused === "market_removed";
  const noUpdates = !p
    ? ""
    : offline
      ? t("settings-extensions.manager_offline")
      : marketRemoved
        ? t("settings-extensions.market_removed_check")
        : marketManaged && !automatic
          ? t("settings-extensions.action_unknown")
          : lifecycleReason(p, false);
  const noUninstall = !p ? "" : offline ? t("settings-extensions.manager_offline") : lifecycleReason(p, true);
  const blocked = toggling || saving || offline;

  const refreshAll = () => {
    load();
    if (channelId) void channelCtl.load();
    void loadConnectors();
  };

  return (
    <div data-integration-detail={id} data-integration-kind="extension">
      <div className="mb-3 flex items-center gap-2">
        {back && (
          <Button tone="quiet" icon="chevronLeft" onClick={back} className="-ml-2">
            {t("settings-integrations.back")}
          </Button>
        )}
        <OwnerMark manager={manager} />
        <span className="flex-1" />
        <IconButton
          icon="retry"
          label={t("settings-integrations.refresh")}
          onClick={refreshAll}
          disabled={loading || channelCtl.loading || connectorsLoading}
        />
      </div>

      {/* Header card */}
      <section className="mb-6 overflow-hidden rounded-card bg-surface shadow-card">
        <div className="flex items-center gap-3 px-4 py-3.5">
          <ExtensionLogo
            url={p?.icon_url}
            manager={manager ?? ""}
            iconSize={20}
            className={cx("flex size-11 shrink-0 items-center justify-center rounded-card", EXT_TILE[extensionTone(p)])}
          />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h1 className="truncate text-card-title font-semibold text-ink">{p?.name ?? target.name ?? id}</h1>
              <Pill>{t("settings-integrations.extension_badge")}</Pill>
            </div>
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-label text-ink-3">
              <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", EXT_DOT[extensionTone(p)])} />
              <span className="truncate">
                {p ? stateLine(p) : loading ? t("settings-extensions.loading") : t("settings-extensions.unavailable")}
              </span>
            </div>
          </div>
          {loading && !p && <Spinner />}
        </div>
        {(p?.description || (p && runNote(p)) || p?.needs_setup?.length || p?.error || error) && (
          <div className="flex flex-col gap-1.5 border-t border-line px-4 py-3">
            {p?.description && <p className="text-meta leading-relaxed text-ink-2">{p.description}</p>}
            {p && runNote(p) && <p className="text-submeta text-ink-3">{runNote(p)}</p>}
            {!!p?.needs_setup?.length && (setupTarget ? (
              <button
                type="button"
                onClick={() => setSecret(setupTarget)}
                className="flex items-center gap-1 text-left text-label text-orange hover:underline"
                data-extension-needs-setup
              >
                {t("settings-extensions.needs_setup", { items: formatList(p.needs_setup, NARROW_LIST) })}
                <Icon name="chevron" size={10} stroke={2.4} className="shrink-0" style={{ transform: "rotate(-90deg)" }} />
              </button>
            ) : (
              <p className="text-label text-orange" data-extension-needs-setup>
                {t("settings-extensions.needs_setup", { items: formatList(p.needs_setup, NARROW_LIST) })}
              </p>
            ))}
            {p?.error && <p className="text-label break-words text-red select-text">{p.error}</p>}
            {error && <p className="text-label break-words text-red select-text">{t("settings-extensions.detail_error", { error })}</p>}
          </div>
        )}
      </section>

      {!p && !error && (
        <Group><div className="px-4 py-3"><Loading /></div></Group>
      )}

      {p && (
        <>
          {fieldErrors[""] && (
            <p role="alert" className="mb-4 rounded-control bg-red-tint px-3 py-2 text-label break-words text-red">
              {t("settings-extensions.not_saved", { error: fieldErrors[""] })}
            </p>
          )}

          {/* 1. Account & Connection */}
          {hasConnectionSection && (
            <SectionBlock id="connection" title={t("settings-integrations.section_connection")}>
              {p.auth && <AuthGroup id={p.id} auth={p.auth} enabled={p.enabled ?? true} onChange={authChanged} />}
              {connectors.map((c) => (
                <ConnectorConnectionBlock
                  key={`${c.manager ?? ""}:${c.id}`}
                  connector={c}
                  reload={loadConnectors}
                  loading={connectorsLoading}
                />
              ))}
              {channelId && <ChannelBindingBlock ctl={channelCtl} />}
              {editable && sections.connection.length > 0 && (
                <SectionFieldGroups
                  fields={sections.connection}
                  title={t("settings-integrations.connection_fields_title")}
                  footer={saveFooter(sections.connection)}
                  fieldProps={sharedFieldProps}
                />
              )}
              {isGitHub && <SavedRepositoriesShortcut manager={manager} onSettings={onSettings} />}
            </SectionBlock>
          )}

          {/* 2. Chat & Delivery */}
          {hasChannelSection && (
            <SectionBlock id="channel" title={t("settings-integrations.section_channel")}>
              {channelId && <ChannelDeliveryBlock ctl={channelCtl} onSettings={onSettings} />}
              {editable && sections.channel.length > 0 && (
                <SectionFieldGroups
                  fields={sections.channel}
                  title={t("settings-integrations.channel_fields_title")}
                  footer={saveFooter(sections.channel)}
                  fieldProps={sharedFieldProps}
                />
              )}
            </SectionBlock>
          )}

          {/* 3. Inbox & Sync */}
          {hasInboxSection && (
            <SectionBlock id="inbox" title={t("settings-integrations.section_inbox")}>
              {inboxConnectors.map((c) => (
                <ConnectorInboxBlock
                  key={`${c.manager ?? ""}:${c.id}`}
                  connector={c}
                  reload={loadConnectors}
                  onSaved={(next) => setConnectors((cur) => cur.map((x) => (x.id === next.id && sameManager(x.manager, next.manager) ? next : x)))}
                />
              ))}
              {(p.enabled ?? true) && !manager && !!p.contributes?.connectors?.length && (
                <SubscriptionsEntry
                  pick={(l) => l.extension === p.id}
                  scope={{ ext: p.id }}
                  onOpen={onOpenSources ? (st) => onOpenSources(st, manager) : undefined}
                />
              )}
              {editable && sections.inbox.length > 0 && (
                <SectionFieldGroups
                  fields={sections.inbox}
                  title={t("settings-integrations.inbox_fields_title")}
                  footer={saveFooter(sections.inbox)}
                  fieldProps={sharedFieldProps}
                />
              )}
              {showKnowledgeScope && <KnowledgeScopeShortcut onSettings={onSettings} />}
            </SectionBlock>
          )}
          <ConnectorExternalSourceSettings ids={[...connectorIds, channelId ?? ""]} manager={manager} />

          {/* 4. Extension Settings (unsectioned legacy fields + folded advanced fields + secrets + UI pages) */}
          <SectionBlock id="extension-settings" title={t("settings-integrations.section_extension_settings")}>
            {!editable && (
              <Group>
                <Row label={t(p.read_only || p.source === "hub.json" ? "settings-extensions.read_only" : "settings-extensions.externally_managed")} wrapHint />
              </Group>
            )}
            {noConfiguration && (
              <Group>
                <Row label={t("settings-extensions.no_configuration")} />
              </Group>
            )}
            {uiPages.length > 0 && onPage && (
              <Group title={t("settings-extensions.pages_title")} footer={t("settings-extensions.pages_footer")}>
                {uiPages.map((u) => (
                  <Row
                    key={u.id}
                    label={
                      <span className="flex items-center gap-2">
                        <Icon name="layers" size={13} className="shrink-0 text-ink-3" />
                        {u.title || u.id}
                      </span>
                    }
                    onClick={() => onPage(p.id, u.id, manager)}
                  />
                ))}
              </Group>
            )}
            {editable && sections.general.length > 0 && (
              <SectionFieldGroups
                fields={sections.general}
                title={t("settings-extensions.fields_title")}
                footer={saveFooter(sections.general)}
                fieldProps={sharedFieldProps}
              />
            )}
            {editable && sections.advanced.length > 0 && (
              <div className="mb-6" data-integration-advanced>
                <Disclosure
                  title={t("settings-integrations.advanced_title")}
                  badge={t("settings-integrations.advanced_count", { count: sections.advanced.length })}
                  open={advancedOpen}
                  onToggle={() => setAdvancedOpen((v) => !v)}
                >
                  <SectionFieldGroups
                    fields={sections.advanced}
                    footer={saveFooter(sections.advanced)}
                    fieldProps={sharedFieldProps}
                  />
                </Disclosure>
              </div>
            )}
            {editable && topSecrets.length > 0 && (
              <Group title={t("settings-extensions.secrets_title")} footer={t("settings-extensions.secrets_footer")}>
                {topSecrets.map((s) => (
                  <Row
                    key={s.name}
                    label={
                      <span className="flex items-center gap-2">
                        <Icon name="key" size={13} className="shrink-0 text-ink-3" />
                        {s.title}
                      </span>
                    }
                    hint={s.reference ? s.reference.title || s.reference.reference : s.description}
                    onClick={() => setSecret(s)}
                  >
                    <span className={cx(!s.set && s.required && "text-orange")}>{secretState(s)}</span>
                  </Row>
                ))}
              </Group>
            )}
            {dirty && (
              <Group anchor="integration-save-bar">
                {checkFailed && <SettingsHint alert>{t("settings-extensions.not_saved", { error: checkFailed })}</SettingsHint>}
                <Row label={Object.keys(problems).length ? t("settings-extensions.not_a_number") : t("settings-extensions.unsaved")}>
                  <Button tone="quiet" onClick={revert} disabled={saving}>{t("settings-extensions.revert")}</Button>
                  <Button tone="accent" busy={saving} disabled={!canSave} onClick={() => void save()}>{t("common.save")}</Button>
                </Row>
              </Group>
            )}
          </SectionBlock>

          {/* 5. Plugin Management */}
          <SectionBlock id="management" title={t("settings-integrations.section_management")}>
            <Group footer={offline ? t("settings-extensions.manager_offline") : editable ? enableFooter(p, memberNames) : t(p.read_only ? "settings-extensions.read_only" : "settings-extensions.externally_managed")}>
              <Row label={t("settings-extensions.enable")}>
                <Switch
                  on={p.enabled ?? true}
                  onChange={(on) => (!on && p.pack_members_follow && p.pack_on?.length ? setPackOff(true) : void toggle(on))}
                  disabled={blocked || !editable}
                  label={t("settings-extensions.enable")}
                />
              </Row>
            </Group>

            <ExtensionUpdatesGroup
              d={p}
              manager={manager}
              busy={blocked}
              market={market}
              canCheck={marketManaged && !offline && !marketRemoved}
              automatic={automatic}
              reason={noUpdates}
              onToggle={(on) => void toggleAutoUpgrade(on)}
              onUpgrade={(m, extId) => onUpgrade?.(m, extId, manager)}
              onChecked={async () => {
                await readDetail((d) => (dirtyNow.current ? setDetail(d) : apply(d)), (err) => { throw err; });
              }}
            />

            <ExtensionInformationGroup d={p} />

            <SecretProviderGroups
              extension={id}
              users={p.secret_users}
              rules={p.handoff_rules}
              editable={editable}
              onStart={writing}
              onDone={wrote}
            />
            {!manager && <ExtensionInfoAccess extension={id} editable={editable} />}
            {!!p.depends?.length && (
              <Group title={t("settings-extensions.depends_title")} footer={t("settings-extensions.depends_footer")}>
                {p.depends.map((d) => (
                  <Row key={d.id} label={d.name || d.id} hint={d.version}>
                    <span className={cx(d.state !== "ok" && "text-orange")}>{dependencyText(d)}</span>
                  </Row>
                ))}
              </Group>
            )}
            {!!p.extension_pack?.length && (
              <div data-pack-members>
                <Group title={t("settings-extensions.pack_title")} footer={t("settings-extensions.pack_footer")}>
                  {p.extension_pack.map((m) => <PackMemberRow key={m.id} member={m} name={memberNames[m.id]} mono />)}
                </Group>
              </div>
            )}
            {!!p.contributions?.length && (
              <Group title={t("settings-extensions.contributions_title")} footer={t("settings-extensions.contributions_footer")}>
                {p.contributions.map((c) => (
                  <Row
                    key={`${c.point}/${c.id}`}
                    label={c.title || c.id}
                    hint={`${pointText(c.point)} · ${c.id}${c.via === "connector" ? t("settings-extensions.via_connector") : c.via === "config" ? t("settings-extensions.via_config") : ""}`}
                  >
                    <Switch
                      label={c.title || c.id}
                      on={c.enabled}
                      disabled={!editable || toggling || offline || !c.can_disable}
                      onChange={(on) => void toggleContribution(c, on)}
                    />
                  </Row>
                ))}
              </Group>
            )}
            {!p.contributions?.length && p.contributes && Object.keys(p.contributes).length > 0 && (
              <Group title={t("settings-extensions.provides_title")}>
                {Object.keys(p.contributes).sort().map((point) => (
                  <Row key={point} label={pointText(point)} mono>
                    <span className="truncate">{formatList(p.contributes?.[point] ?? [], NARROW_LIST)}</span>
                  </Row>
                ))}
              </Group>
            )}
            {p.sandbox && <SandboxGroup s={p.sandbox} id={p.id} name={p.name} />}
            {!!p.permissions?.length && (
              <Group title={t("settings-extensions.permissions_title")} footer={t("settings-extensions.permissions_footer")}>
                {p.permissions.map((perm) => (
                  <div key={perm.key} className="px-4 py-2.5">
                    <div className="text-meta text-ink">{perm.title}</div>
                    {perm.detail && <div className="mt-0.5 text-submeta leading-snug text-ink-3">{perm.detail}</div>}
                  </div>
                ))}
              </Group>
            )}

            <Group title={t("settings-extensions.danger_title")} footer={uninstallable && !offline ? t("settings-extensions.market_installed_footer") : noUninstall}>
              <Row label={t("settings-extensions.uninstall")}>
                <Button tone="danger" icon="trash" disabled={blocked || !uninstallable} onClick={() => setRemoving(true)}>
                  {t("settings-extensions.uninstall")}
                </Button>
              </Row>
            </Group>
          </SectionBlock>
        </>
      )}

      {removing && p && (
        <UninstallDialog
          id={p.id}
          name={p.name}
          version={p.version}
          pack={p.extension_pack}
          onClose={() => setRemoving(false)}
          onDone={() => (onUninstalled ?? back)?.()}
        />
      )}
      {packOff && p && !dependents && (
        <Dialog
          open
          title={t("settings-extensions.disable_pack_title", { name: p.name })}
          onClose={() => !toggling && setPackOff(false)}
          footer={
            <>
              <Button tone="quiet" disabled={toggling} onClick={() => setPackOff(false)}>{t("common.cancel")}</Button>
              <Button tone="danger" busy={toggling} onClick={() => void toggle(false)}>{t("settings-extensions.disable_all")}</Button>
            </>
          }
        >
          <p className="text-ink-2">
            {t("settings-extensions.disable_pack_body", {
              names: (p.pack_on ?? []).map((m) => m.name || m.id).join(t("settings-extensionmarket.list_separator")),
            })}
          </p>
        </Dialog>
      )}
      {dependents && p && (
        <Dialog
          open
          title={t("settings-extensions.disable_dependents_title", { name: p.name })}
          onClose={() => !toggling && setDependents(null)}
          footer={
            <>
              <Button tone="quiet" disabled={toggling} onClick={() => setDependents(null)}>{t("common.cancel")}</Button>
              <Button tone="danger" busy={toggling} onClick={() => void toggle(false, true)}>{t("settings-extensions.disable_all")}</Button>
            </>
          }
        >
          <p className="text-ink-2">
            {t("settings-extensions.dependents_body", { names: formatList(dependents.map((d) => d.name), NARROW_LIST) })}
          </p>
        </Dialog>
      )}
      {secret && (
        <SecretDialog
          extension={id}
          secret={secret.list && secret.entry_title ? { ...secret, title: `${secret.entry_title} · ${secret.title}` } : secret}
          onClose={() => setSecret(null)}
          onStart={writing}
          onDone={wrote}
        />
      )}
    </div>
  );
}
