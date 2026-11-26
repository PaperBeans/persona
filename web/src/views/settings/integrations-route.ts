// Pure bidirectional route codec between unified Settings › Integrations (`integrations_v2`) and
// legacy `connectors` / `channels` / `extensions` / `sources` routes (query and path forms, preserving `@manager` and `state`).
import { splitSettingsOwner, type LegacyIntegrationTab } from "../../lib/deep-links";
import { decodeURIComponentSafe } from "../../lib/uri";

export { isLegacyIntegrationTab, parseSettingsPath, type LegacyIntegrationTab } from "../../lib/deep-links";

export type IntegrationFilter = "all" | "channels" | "data" | "tools";

export type IntegrationsRoute =
  | { kind: "list" }
  | { kind: "builtin"; id: string; manager?: string }
  | { kind: "extension"; id: string; manager?: string }
  | { kind: "channel"; id: string; manager?: string }
  | { kind: "connector"; id: string; manager?: string }
  | { kind: "page"; id: string; ui: string; manager?: string }
  | { kind: "sources"; state?: string; manager?: string }
  | { kind: "catalog" }
  | { kind: "item"; market: string; id: string; manager?: string }
  | { kind: "markets" }
  | { kind: "sent" };

const KNOWN_BUILTIN_IDS = new Set(["mail", "feishu-bot", "lark", "imessage", "github", "larkcli"]);
const BUILTIN_CHANNEL_IDS = new Set(["apps", "voice", "feishu-mention", "feishu"]);

export function normalizeIntegrationFilter(value: string | null | undefined): IntegrationFilter {
  if (value === "channels" || value === "data" || value === "tools") return value;
  return "all";
}

function withManager(id: string, manager?: string): string {
  return manager ? `${id}@${manager}` : id;
}

/** Parses `?tab=integrations&integration=<sub>` into a structured route. */
export function parseIntegrationsSub(sub: string | null | undefined): IntegrationsRoute {
  if (!sub) return { kind: "list" };
  if (sub.startsWith("extension:")) sub = "ext:" + sub.slice("extension:".length);
  if (sub === "-sent") return { kind: "sent" };
  if (sub === "-market") return { kind: "catalog" };
  if (sub === "-markets") return { kind: "markets" };
  if (sub === "-sources") return { kind: "sources" };
  if (sub.startsWith("-sources@") || sub.startsWith("-sources/")) {
    const rest = sub.slice("-sources".length);
    const slash = rest.indexOf("/");
    const head = slash >= 0 ? rest.slice(0, slash) : rest;
    const manager = head.startsWith("@") ? head.slice(1) : "";
    const rawState = slash >= 0 ? rest.slice(slash + 1) : "";
    const state = rawState ? decodeURIComponentSafe(rawState) : "";
    return {
      kind: "sources",
      ...(state ? { state } : {}),
      ...(manager ? { manager } : {}),
    };
  }

  const item = /^-market\/([^/]+)\/([^/@]+)(?:@([^/]+))?$/.exec(sub);
  if (item) {
    return { kind: "item", market: item[1]!, id: item[2]!, ...(item[3] ? { manager: item[3] } : {}) };
  }

  const page = /^(?:ext:)?([^/@:-][^/@]*)(?:@([^/]+))?\/ui\/([^/]+)$/.exec(sub);
  if (page) {
    return { kind: "page", id: page[1]!, ui: page[3]!, ...(page[2] ? { manager: page[2] } : {}) };
  }

  if (sub.startsWith("builtin:")) {
    const rest = sub.slice("builtin:".length);
    if (!rest || rest.includes("/")) return { kind: "list" };
    const { id, manager } = splitSettingsOwner(rest);
    return id ? { kind: "builtin", id, ...(manager ? { manager } : {}) } : { kind: "list" };
  }
  if (sub.startsWith("ext:")) {
    const rest = sub.slice("ext:".length);
    if (!rest || rest.includes("/")) return { kind: "list" };
    const { id, manager } = splitSettingsOwner(rest);
    return id ? { kind: "extension", id, ...(manager ? { manager } : {}) } : { kind: "list" };
  }
  if (sub.startsWith("channel:")) {
    const rest = sub.slice("channel:".length);
    if (!rest || rest.includes("/")) return { kind: "list" };
    const { id, manager } = splitSettingsOwner(rest);
    return id ? { kind: "channel", id, ...(manager ? { manager } : {}) } : { kind: "list" };
  }
  if (sub.startsWith("connector:")) {
    const rest = sub.slice("connector:".length);
    if (!rest || rest.includes("/")) return { kind: "list" };
    const { id, manager } = splitSettingsOwner(rest);
    return id ? { kind: "connector", id, ...(manager ? { manager } : {}) } : { kind: "list" };
  }

  if (sub.startsWith("-") || sub.includes("/")) return { kind: "list" };
  const { id, manager } = splitSettingsOwner(sub);
  if (!id) return { kind: "list" };
  if (KNOWN_BUILTIN_IDS.has(id)) {
    return { kind: "builtin", id, ...(manager ? { manager } : {}) };
  }
  return { kind: "extension", id, ...(manager ? { manager } : {}) };
}

/** Serializes a structured route back to the `integration` query parameter value. */
export function formatIntegrationsSub(route: IntegrationsRoute): string | null {
  switch (route.kind) {
    case "list":
      return null;
    case "sent":
      return "-sent";
    case "catalog":
      return "-market";
    case "markets":
      return "-markets";
    case "item":
      return `-market/${route.market}/${withManager(route.id, route.manager)}`;
    case "page":
      return `ext:${withManager(route.id, route.manager)}/ui/${route.ui}`;
    case "sources": {
      const base = withManager("-sources", route.manager);
      return route.state ? `${base}/${encodeURIComponent(route.state)}` : base;
    }
    case "builtin":
      return `builtin:${withManager(route.id, route.manager)}`;
    case "extension":
      return `ext:${withManager(route.id, route.manager)}`;
    case "channel":
      return `channel:${withManager(route.id, route.manager)}`;
    case "connector":
      return `connector:${withManager(route.id, route.manager)}`;
  }
}

/** Whether a route shows an integration detail view eligible for two-column split-pane layout on desktop.
 *  Global Data Flow (`kind: "sources"` without `state`) stays full-width single-scroll so `SourcesGraph` has room;
 *  an individual source detail (`kind: "sources"` with `state`, such as a standalone push source) uses split-pane. */
export function isIntegrationDetailRoute(route: IntegrationsRoute): boolean {
  return (
    route.kind === "builtin" ||
    route.kind === "extension" ||
    route.kind === "channel" ||
    route.kind === "connector" ||
    (route.kind === "sources" && Boolean(route.state))
  );
}

/** Maps a legacy `connectors` / `channels` / `extensions` / `sources` tab+sub into the unified `integrations` sub + optional filter. */
export function legacyToIntegrations(
  tab: LegacyIntegrationTab,
  sub: string | null | undefined,
  manager?: string | null,
): { sub: string | null; filter?: IntegrationFilter } {
  if (tab === "sources") {
    let resolvedManager = manager?.trim() || undefined;
    let resolvedState = sub?.trim() || undefined;
    if (resolvedState?.startsWith("@")) {
      const slash = resolvedState.indexOf("/");
      const head = slash >= 0 ? resolvedState.slice(1, slash) : resolvedState.slice(1);
      if (head && !resolvedManager) resolvedManager = head;
      resolvedState = slash >= 0 ? resolvedState.slice(slash + 1) || undefined : undefined;
    }
    return {
      sub: formatIntegrationsSub({
        kind: "sources",
        ...(resolvedState ? { state: resolvedState } : {}),
        ...(resolvedManager ? { manager: resolvedManager } : {}),
      }),
    };
  }

  if (tab === "connectors") {
    if (!sub) return { sub: null, filter: "data" };
    if (sub === "-sent") return { sub: "-sent" };
    const { id, manager: ownedManager } = splitSettingsOwner(sub);
    if (id === "mail" || id === "feishu-bot" || id === "lark") {
      return { sub: formatIntegrationsSub({ kind: "builtin", id, manager: ownedManager }) };
    }
    return { sub: formatIntegrationsSub({ kind: "connector", id, manager: ownedManager }) };
  }

  if (tab === "channels") {
    if (!sub) return { sub: null, filter: "channels" };
    const { id, manager: ownedManager } = splitSettingsOwner(sub);
    if (id === "feishu") {
      return { sub: formatIntegrationsSub({ kind: "builtin", id: "feishu-bot", manager: ownedManager }) };
    }
    return { sub: formatIntegrationsSub({ kind: "channel", id, manager: ownedManager }) };
  }

  // tab === "extensions"
  if (!sub) return { sub: null };
  if (sub === "-channels") return { sub: null, filter: "channels" };
  if (sub.startsWith("-channels/")) {
    return legacyToIntegrations("channels", sub.slice("-channels/".length));
  }
  if (sub === "-market" || sub === "-markets") return { sub };
  const marketItem = /^-market\/([^/]+)\/([^/@]+)(?:@([^/]+))?$/.exec(sub);
  if (marketItem) return { sub };
  const page = /^([^/@-][^/@]*)(?:@([^/]+))?\/ui\/([^/]+)$/.exec(sub);
  if (page) {
    return { sub: formatIntegrationsSub({ kind: "page", id: page[1]!, ui: page[3]!, ...(page[2] ? { manager: page[2] } : {}) }) };
  }
  if (sub.startsWith("-") || sub.includes("/")) return { sub: null };
  const { id, manager: ownedManager } = splitSettingsOwner(sub);
  return { sub: formatIntegrationsSub({ kind: "extension", id, manager: ownedManager }) };
}

/** Maps a unified `integrations` sub + optional filter back to the appropriate legacy tab + sub when `integrations_v2` is off. */
export function integrationsToLegacy(
  sub: string | null | undefined,
  filter?: string | null,
): { tab: LegacyIntegrationTab; sub: string | null; manager?: string } {
  const route = parseIntegrationsSub(sub);
  switch (route.kind) {
    case "list": {
      const normalized = normalizeIntegrationFilter(filter);
      if (normalized === "channels") return { tab: "channels", sub: null };
      if (normalized === "data") return { tab: "connectors", sub: null };
      return { tab: "extensions", sub: null };
    }
    case "sent":
      return { tab: "connectors", sub: "-sent" };
    case "sources":
      return {
        tab: "sources",
        sub: route.state ?? null,
        ...(route.manager ? { manager: route.manager } : {}),
      };
    case "catalog":
      return { tab: "extensions", sub: "-market" };
    case "markets":
      return { tab: "extensions", sub: "-markets" };
    case "item":
      return { tab: "extensions", sub: `-market/${route.market}/${withManager(route.id, route.manager)}` };
    case "page":
      return { tab: "extensions", sub: `${withManager(route.id, route.manager)}/ui/${route.ui}` };
    case "extension":
      return { tab: "extensions", sub: withManager(route.id, route.manager) };
    case "channel":
      return { tab: "channels", sub: withManager(route.id, route.manager) };
    case "connector":
      return { tab: "connectors", sub: withManager(route.id, route.manager) };
    case "builtin":
      if (!route.manager && (route.id === "mail" || route.id === "lark")) {
        return { tab: "connectors", sub: null };
      }
      if (BUILTIN_CHANNEL_IDS.has(route.id)) {
        return { tab: "channels", sub: withManager(route.id, route.manager) };
      }
      return { tab: "connectors", sub: withManager(route.id, route.manager) };
  }
}
