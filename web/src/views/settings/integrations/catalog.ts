// Pure aggregation, deduplication, capability classification, and deep-link target resolution
// for the lazy-loaded unified Integrations & Extensions view (integrations_v2, #14874 / Refs #14802).
import { formatList, t } from "../../../i18n";
import { mailSummary, type MailAccount } from "../../../lib/agent-settings";
import { channelStateText, channelSubtitle, channelTone, type Channel } from "../../../lib/channels";
import { connectorSummary, connectorTone, storesData, type HubConnector } from "../../../lib/connectors";
import { listAuthNote } from "../../../lib/extension-auth";
import { channelOf, extensionState, extensionTone, type HubExtension } from "../../../lib/extensions";
import { botSummary, botTone, type FeishuBotStatus } from "../../../lib/feishu-bot";
import { defaultManager, otherManager, sameManager } from "../../../lib/manager-choice";
import { formatIntegrationsSub, parseIntegrationsSub, type IntegrationFilter, type IntegrationsRoute } from "../integrations-route";
import type { IntegrationTarget } from "./model";

export type IntegrationCapability = "channels" | "data" | "tools";
export type IntegrationTone = "green" | "orange" | "red" | "idle";

export interface LarkGatewaySummary {
  configured: boolean;
  connected: boolean;
  name?: string;
  error?: string;
}

export interface IntegrationItem {
  key: string;
  kind: "builtin" | "extension";
  id: string;
  name: string;
  description?: string;
  manager?: string;
  capabilities: IntegrationCapability[];
  tone: IntegrationTone;
  statusText: string;
  subtitle?: string;
  alertTone?: "red" | "orange";
  channel?: Channel;
  connector?: HubConnector;
  extension?: HubExtension;
  /** Canonical `?integration=` sub-value for opening this item. */
  sub: string;
  /** Target passed to `<IntegrationDetail />`. */
  target: IntegrationTarget;
}

export interface IntegrationCatalogInput {
  extensions: readonly HubExtension[];
  channels: readonly Channel[];
  connectors: readonly HubConnector[];
  hasMail: boolean;
  hasLark: boolean;
  hasFeishuBot: boolean;
  mailAccounts?: readonly MailAccount[] | null;
  larkStatus?: LarkGatewaySummary | null;
  feishuBotStatus?: FeishuBotStatus | null;
  ownerManager?: string;
}

const NARROW_LIST: Intl.ListFormatOptions = { type: "conjunction", style: "narrow" };
const RESERVED_BUILTIN_CONNECTORS = new Set(["mail", "lark", "larkcli", "imessage", "github"]);
const CHANNEL_POINTS = new Set(["channel", "channels", "voice"]);
const DATA_POINTS = new Set(["connectors", "connector", "messaging", "inbox", "knowledge", "sources", "sync"]);

function nonDefaultManager(manager?: string): string | undefined {
  return manager && manager !== defaultManager() ? manager : undefined;
}

function classifyExtensionCapabilities(
  ext: HubExtension,
  matchedChannel?: Channel,
  matchedConnectors: readonly HubConnector[] = [],
): IntegrationCapability[] {
  const caps = new Set<IntegrationCapability>();
  const points = Object.keys(ext.contributes ?? {}).filter((k) => (ext.contributes?.[k]?.length ?? 0) > 0);

  if (matchedChannel || points.some((p) => CHANNEL_POINTS.has(p))) {
    caps.add("channels");
  }
  if (
    matchedConnectors.some((c) => storesData(c)) ||
    points.some((p) => DATA_POINTS.has(p))
  ) {
    caps.add("data");
  }
  if (matchedConnectors.some((c) => !storesData(c))) {
    caps.add("channels");
  }
  if (points.some((p) => !CHANNEL_POINTS.has(p) && !DATA_POINTS.has(p))) {
    caps.add("tools");
  }
  if (caps.size === 0) {
    caps.add("tools");
  }
  return (["channels", "data", "tools"] as const).filter((c) => caps.has(c));
}

function worstTone(...tones: IntegrationTone[]): IntegrationTone {
  if (tones.includes("red")) return "red";
  if (tones.includes("orange")) return "orange";
  if (tones.includes("green")) return "green";
  return "idle";
}

/**
 * Aggregates built-in services, hub connectors, channels, and installed extensions into a single
 * deduplicated list. A multi-capability extension (for example `larkx` contributing connector +
 * channel + tools) appears once with combined capability tags and merged target metadata.
 */
export function buildIntegrationCatalog(input: IntegrationCatalogInput): IntegrationItem[] {
  const owner = nonDefaultManager(input.ownerManager);
  const installedChannels = input.channels.filter((c) => c.source !== "market");
  const claimedChannels = new Set<string>();
  const claimedConnectors = new Set<string>();

  // 1. Installed extensions first so their contributed channels and connectors merge into one object.
  const extensionItems: IntegrationItem[] = input.extensions.map((ext) => {
    const extManager = nonDefaultManager(ext.manager ?? owner);
    const contribConnectors = (ext.contributes?.connectors ?? []).filter((id) => id.trim() !== "");
    const contribChannels = (ext.contributes?.channel ?? []).filter((id) => id.trim() !== "");

    const matchedConnectors: HubConnector[] = [];
    for (const c of input.connectors) {
      if (claimedConnectors.has(c.id)) continue;
      if (contribConnectors.includes(c.id) || (!RESERVED_BUILTIN_CONNECTORS.has(c.id) && c.id === ext.id)) {
        matchedConnectors.push(c);
        claimedConnectors.add(c.id);
      }
    }

    const matchedChannels: Channel[] = [];
    for (const ch of installedChannels) {
      if (claimedChannels.has(ch.id)) continue;
      if (
        contribChannels.includes(ch.id) ||
        (ch.source === "plugin" && (ch.plugin === ext.id || ch.id === ext.id))
      ) {
        matchedChannels.push(ch);
        claimedChannels.add(ch.id);
      }
    }

    const primaryChannel = matchedChannels[0];
    const primaryConnector = matchedConnectors[0];
    const channelId = primaryChannel?.id ?? channelOf(ext) ?? undefined;
    const connectorId = primaryConnector?.id ?? contribConnectors[0] ?? undefined;

    const capabilities = classifyExtensionCapabilities(ext, primaryChannel, matchedConnectors);
    const extTone = extensionTone(ext);
    const tone = worstTone(
      extTone,
      ...(primaryChannel ? [channelTone(primaryChannel)] : []),
      ...(primaryConnector ? [connectorTone(primaryConnector)] : []),
    );

    const needs = ext.needs_setup?.length
      ? t("settings-extensions.needs_setup", { items: formatList(ext.needs_setup, NARROW_LIST) })
      : "";
    const statusText = `${extensionState(ext)}${listAuthNote(ext.auth)}${ext.upgrade?.state === "needs_permission" ? t("settings-extensions.upgrade_pending") : ""}`;
    const errorNote = ext.error || (primaryChannel?.state === "error" ? primaryChannel.error : undefined) || (primaryConnector?.status === "error" ? primaryConnector.error : undefined);
    const subtitle = errorNote || needs || primaryChannel?.account?.name || primaryConnector?.account || ext.description;
    const alertTone: "red" | "orange" | undefined = errorNote ? "red" : needs ? "orange" : undefined;

    const sub = formatIntegrationsSub({ kind: "extension", id: ext.id, manager: extManager }) ?? `ext:${ext.id}`;
    return {
      key: `ext:${extManager ?? ""}:${ext.id}`,
      kind: "extension",
      id: ext.id,
      name: ext.name,
      description: ext.description,
      manager: extManager,
      capabilities,
      tone,
      statusText,
      subtitle,
      alertTone,
      channel: primaryChannel,
      connector: primaryConnector,
      extension: ext,
      sub,
      target: {
        kind: "extension",
        id: ext.id,
        name: ext.name,
        ...(channelId ? { channelId } : {}),
        ...(connectorId ? { connectorId } : {}),
        ...(extManager ? { manager: extManager } : {}),
      },
    };
  });

  // 2. Built-in services (deduplicated against built-in channels and connectors).
  const builtinItems: IntegrationItem[] = [];

  const mailConnector = input.connectors.find((c) => c.id === "mail");
  if (input.hasMail || mailConnector) {
    claimedConnectors.add("mail");
    const accounts = input.mailAccounts ?? null;
    const hasError = accounts?.some((a) => a.status === "error") || mailConnector?.status === "error";
    const connected = (accounts && accounts.length > 0 && !hasError) || mailConnector?.status === "ok";
    const tone: IntegrationTone = hasError ? "red" : connected ? "green" : "idle";
    const statusText = accounts
      ? (accounts.length > 0 ? mailSummary([...accounts]) : t("settings-integrations.status_not_configured"))
      : (mailConnector ? connectorSummary(mailConnector) : t("settings-integrations.status_not_configured"));
    const sub = formatIntegrationsSub({ kind: "builtin", id: "mail", manager: owner }) ?? "builtin:mail";
    builtinItems.push({
      key: `builtin:${owner ?? ""}:mail`,
      kind: "builtin",
      id: "mail",
      name: t("settings-connectors.mail"),
      description: t("settings-integrations.mail_description"),
      manager: owner,
      capabilities: ["data"],
      tone,
      statusText,
      subtitle: t("settings-integrations.mail_description"),
      alertTone: hasError ? "red" : undefined,
      connector: mailConnector,
      sub,
      target: {
        kind: "builtin",
        id: "mail",
        name: t("settings-connectors.mail"),
        description: t("settings-integrations.mail_description"),
        ...(owner ? { manager: owner } : {}),
      },
    });
  }

  const feishuChannel = installedChannels.find((c) => c.id === "feishu" && !claimedChannels.has("feishu"));
  const larkcliConnector = input.connectors.find((c) => c.id === "larkcli" && !claimedConnectors.has("larkcli"));
  if (input.hasFeishuBot || feishuChannel || larkcliConnector) {
    claimedChannels.add("feishu");
    claimedConnectors.add("larkcli");
    const bot = input.feishuBotStatus ?? null;
    const tone: IntegrationTone = bot
      ? worstTone(botTone(bot) ?? "idle", ...(feishuChannel ? [channelTone(feishuChannel)] : []), ...(larkcliConnector ? [connectorTone(larkcliConnector)] : []))
      : feishuChannel
        ? worstTone(channelTone(feishuChannel), ...(larkcliConnector ? [connectorTone(larkcliConnector)] : []))
        : larkcliConnector
          ? connectorTone(larkcliConnector)
          : "idle";
    const statusText = bot
      ? botSummary(bot)
      : feishuChannel
        ? channelStateText(feishuChannel)
        : larkcliConnector
          ? connectorSummary(larkcliConnector)
          : t("settings-integrations.status_not_configured");
    const name = bot?.bot_name || feishuChannel?.name || t("settings-feishubot.title");
    const description = t("settings-integrations.feishu_bot_description");
    const subtitle = (feishuChannel?.state === "error" && feishuChannel.error)
      || (larkcliConnector?.status === "error" && larkcliConnector.error)
      || feishuChannel?.account?.name
      || description;
    const caps: IntegrationCapability[] = larkcliConnector ? ["channels", "data"] : ["channels"];
    const sub = formatIntegrationsSub({ kind: "builtin", id: "feishu-bot", manager: owner }) ?? "builtin:feishu-bot";
    builtinItems.push({
      key: `builtin:${owner ?? ""}:feishu-bot`,
      kind: "builtin",
      id: "feishu-bot",
      name,
      description,
      manager: owner,
      capabilities: caps,
      tone,
      statusText,
      subtitle,
      alertTone: tone === "red" ? "red" : undefined,
      channel: feishuChannel,
      connector: larkcliConnector,
      sub,
      target: {
        kind: "builtin",
        id: "feishu-bot",
        name,
        description,
        channelId: feishuChannel?.id ?? "feishu",
        ...(larkcliConnector ? { connectorId: larkcliConnector.id } : {}),
        ...(owner ? { manager: owner } : {}),
      },
    });
  }

  const larkConnector = input.connectors.find((c) => c.id === "lark" && !claimedConnectors.has("lark"));
  if (input.hasLark) {
    claimedConnectors.add("lark");
    const ls = input.larkStatus ?? null;
    const tone: IntegrationTone = ls
      ? (ls.error ? "red" : ls.connected ? "green" : "idle")
      : (larkConnector ? connectorTone(larkConnector) : "idle");
    const statusText = ls
      ? (ls.error ? t("settings-integrations.status_error") : ls.connected ? t("settings-integrations.status_connected") : t("settings-integrations.status_not_configured"))
      : (larkConnector ? connectorSummary(larkConnector) : t("settings-integrations.status_not_configured"));
    const sub = formatIntegrationsSub({ kind: "builtin", id: "lark", manager: owner }) ?? "builtin:lark";
    builtinItems.push({
      key: `builtin:${owner ?? ""}:lark`,
      kind: "builtin",
      id: "lark",
      name: t("settings-connectors.lark"),
      description: t("settings-integrations.lark_gateway_description"),
      manager: owner,
      capabilities: ["data"],
      tone,
      statusText,
      subtitle: ls?.error || ls?.name || t("settings-integrations.lark_gateway_description"),
      alertTone: ls?.error ? "red" : undefined,
      connector: larkConnector,
      sub,
      target: {
        kind: "builtin",
        id: "lark",
        name: t("settings-connectors.lark"),
        description: t("settings-integrations.lark_gateway_description"),
        ...(owner ? { manager: owner } : {}),
      },
    });
  }

  // Remaining unclaimed connectors (for example GitHub, Messages / iMessage, or legacy connectors).
  for (const c of input.connectors) {
    if (claimedConnectors.has(c.id)) continue;
    claimedConnectors.add(c.id);
    const matchedChannel = installedChannels.find((ch) => !claimedChannels.has(ch.id) && ch.id === c.id);
    if (matchedChannel) claimedChannels.add(matchedChannel.id);

    const cManager = nonDefaultManager(c.manager ?? owner);
    const caps: IntegrationCapability[] = [];
    if (matchedChannel || !storesData(c)) caps.push("channels");
    if (storesData(c)) caps.push("data");
    if (!caps.length) caps.push("data");

    const tone = worstTone(connectorTone(c), ...(matchedChannel ? [channelTone(matchedChannel)] : []));
    const sub = formatIntegrationsSub({ kind: "builtin", id: c.id, manager: cManager }) ?? `builtin:${c.id}`;
    builtinItems.push({
      key: `builtin:${cManager ?? ""}:${c.id}`,
      kind: "builtin",
      id: c.id,
      name: c.name,
      manager: cManager,
      capabilities: caps,
      tone,
      statusText: connectorSummary(c),
      subtitle: c.error || (matchedChannel ? channelSubtitle(matchedChannel) : c.account),
      alertTone: tone === "red" ? "red" : undefined,
      channel: matchedChannel,
      connector: c,
      sub,
      target: {
        kind: "builtin",
        id: c.id,
        name: c.name,
        connectorId: c.id,
        ...(matchedChannel ? { channelId: matchedChannel.id } : {}),
        ...(cManager ? { manager: cManager } : {}),
      },
    });
  }

  // Remaining unclaimed installed channels (Persona apps, voice, /agent mention, or plugin channels without extension_manager).
  for (const ch of installedChannels) {
    if (claimedChannels.has(ch.id)) continue;
    claimedChannels.add(ch.id);
    const sub = formatIntegrationsSub({ kind: "channel", id: ch.id, manager: owner }) ?? `channel:${ch.id}`;
    const tone = channelTone(ch);
    builtinItems.push({
      key: `channel:${owner ?? ""}:${ch.id}`,
      kind: "builtin",
      id: ch.id,
      name: ch.name,
      description: ch.description,
      manager: owner,
      capabilities: ["channels"],
      tone,
      statusText: ch.kind === "app" ? t("settings-integrations.status_connected") : channelStateText(ch),
      subtitle: channelSubtitle(ch),
      alertTone: ch.state === "error" ? "red" : undefined,
      channel: ch,
      sub,
      target: {
        kind: "builtin",
        id: ch.id,
        name: ch.name,
        description: ch.description,
        channelId: ch.id,
        connectorId: null,
        ...(owner ? { manager: owner } : {}),
      },
    });
  }

  return [...builtinItems, ...extensionItems];
}

/** Filters items by the active capability filter chip. */
export function filterIntegrationItems(items: readonly IntegrationItem[], filter: IntegrationFilter): IntegrationItem[] {
  if (filter === "all") return [...items];
  return items.filter((it) => it.capabilities.includes(filter));
}

/** Resolves an active IntegrationsRoute into an IntegrationTarget for `<IntegrationDetail />`. */
export function resolveIntegrationTarget(
  route: IntegrationsRoute,
  items: readonly IntegrationItem[],
  pickedManager: string | null | undefined,
): IntegrationTarget | null {
  const effective = (named?: string) => named ?? otherManager(pickedManager ?? undefined);

  if (route.kind === "extension") {
    const targetManager = effective(route.manager);
    const matched = items.find((it) => it.kind === "extension" && it.id === route.id && sameManager(it.manager, targetManager));
    return matched?.target ?? {
      kind: "extension",
      id: route.id,
      ...(route.manager ? { manager: route.manager } : {}),
    };
  }

  if (route.kind === "builtin") {
    const targetManager = effective(route.manager);
    const matched = items.find((it) => it.kind === "builtin" && it.id === route.id && sameManager(it.manager, targetManager));
    return matched?.target ?? {
      kind: "builtin",
      id: route.id,
      ...(route.manager ? { manager: route.manager } : {}),
    };
  }

  if (route.kind === "channel") {
    const targetManager = effective(route.manager);
    const matched = items.find((it) => it.channel?.id === route.id && sameManager(it.manager, targetManager));
    if (matched) return matched.target;
    return {
      kind: "builtin",
      id: route.id,
      channelId: route.id,
      connectorId: null,
      ...(route.manager ? { manager: route.manager } : {}),
    };
  }

  if (route.kind === "connector") {
    const targetManager = effective(route.manager);
    // Keep larkcli openable directly as its own connector detail when navigated via connector:larkcli.
    if (route.id === "larkcli") {
      return {
        kind: "builtin",
        id: "larkcli",
        connectorId: "larkcli",
        ...(route.manager ? { manager: route.manager } : {}),
      };
    }
    const matched = items.find(
      (it) =>
        (it.connector?.id === route.id || it.extension?.contributes?.connectors?.includes(route.id)) &&
        sameManager(it.manager, targetManager),
    );
    if (matched) return matched.target;
    return {
      kind: "builtin",
      id: route.id,
      connectorId: route.id,
      ...(route.manager ? { manager: route.manager } : {}),
    };
  }

  return null;
}

/** Whether `openSub` currently points to `item` (including via legacy channel/connector redirects). */
export function isIntegrationRowSelected(
  openSub: string | null | undefined,
  item: IntegrationItem,
  pickedManager: string | null | undefined,
): boolean {
  if (!openSub) return false;
  const route = parseIntegrationsSub(openSub);
  const effective = (named?: string) => named ?? otherManager(pickedManager ?? undefined);

  if (route.kind === "extension") {
    return item.kind === "extension" && item.id === route.id && sameManager(effective(route.manager), item.manager);
  }
  if (route.kind === "builtin") {
    return item.kind === "builtin" && item.id === route.id && sameManager(effective(route.manager), item.manager);
  }
  if (route.kind === "channel") {
    return item.channel?.id === route.id && sameManager(effective(route.manager), item.manager);
  }
  if (route.kind === "connector") {
    if (route.id === "larkcli") return false;
    return (
      (item.connector?.id === route.id || !!item.extension?.contributes?.connectors?.includes(route.id)) &&
      sameManager(effective(route.manager), item.manager)
    );
  }
  return false;
}
