// @vitest-environment jsdom
// Unit & integration tests for the lazy-loaded unified Integrations & Extensions list view,
// object deduplication, capability filter chips, Extension Market entry, IntegrationDetail mounting,
// bidirectional legacy route redirects, and Settings navigation collapse under integrations_v2 (#14874).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  phone: false,
  twoCol: true,
  integrationsV2: true,
  features: ["channels", "extension_manager", "connectors", "lark"] as string[],
  loaded: true,
}));

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    get matches() {
      return /min-width/.test(query) ? state.twoCol && !state.phone : state.phone;
    },
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

vi.mock("../../lib/store", async (original) => ({
  ...(await original<typeof import("../../lib/store")>()),
  useStore: (select: (s: Record<string, unknown>) => unknown) =>
    select({
      features: state.features,
      machines: [],
      sessions: {},
      interactions: [],
      mutes: { spaces: [], sessions: [] },
      conn: "online",
      link: { networkUp: true, restarting: false },
      hubName: "",
      mesh: [],
      loaded: state.loaded,
      assistant: state.loaded ? { manager: "assistant", integrations_v2: state.integrationsV2 } : null,
    }),
  useLarkOn: () => state.features.includes("lark"),
  onHubEvent: () => () => {},
}));

vi.mock("./IntegrationDetail", () => ({
  IntegrationDetail: ({
    target,
    onBack,
    onUpgrade,
    onPage,
  }: {
    target: { kind: string; id: string; channelId?: string; connectorId?: string | null; manager?: string };
    onBack?: () => void;
    onUpgrade?: (market: string, id: string, manager?: string) => void;
    onPage?: (extId: string, uiId: string, manager?: string) => void;
  }) => (
    <div
      data-mock-integration-detail
      data-kind={target.kind}
      data-id={target.id}
      data-channel-id={target.channelId ?? ""}
      data-connector-id={target.connectorId ?? ""}
      data-manager={target.manager ?? ""}
    >
      {onBack && (
        <button type="button" data-mock-detail-back onClick={onBack}>
          Back
        </button>
      )}
      {onUpgrade && (
        <button type="button" data-mock-detail-upgrade onClick={() => onUpgrade("official", target.id, target.manager)}>
          Upgrade
        </button>
      )}
      {onPage && (
        <button type="button" data-mock-detail-page onClick={() => onPage(target.id, "dashboard", target.manager)}>
          Open page
        </button>
      )}
    </div>
  ),
}));

vi.mock("./ExtensionMarket", () => ({
  MarketCatalog: ({
    onBack,
    backLabel,
    onOpen,
    onMarkets,
    onManageMarkets,
  }: {
    onBack?: () => void;
    backLabel?: string;
    onOpen: (it: { market: string; id: string }) => void;
    onMarkets?: () => void;
    onManageMarkets?: () => void;
  }) => {
    const manage = onManageMarkets ?? onMarkets;
    return (
      <div data-mock-market-catalog data-back-label={backLabel ?? ""}>
        {onBack && <button type="button" data-mock-market-back onClick={onBack}>Back</button>}
        <button type="button" data-mock-market-open onClick={() => onOpen({ market: "official", id: "demo-ext" })}>Open item</button>
        {manage && <button type="button" data-mock-markets-open onClick={manage}>Manage markets</button>}
      </div>
    );
  },
  MarketItem: ({ market, id, onBack, onInstalled }: { market: string; id: string; onBack?: () => void; onInstalled: (id: string) => void }) => (
    <div data-mock-market-item data-market={market} data-id={id}>
      {onBack && <button type="button" data-mock-item-back onClick={onBack}>Back</button>}
      <button type="button" data-mock-item-install onClick={() => onInstalled(id)}>Install</button>
    </div>
  ),
  Markets: ({ onBack }: { onBack?: () => void }) => (
    <div data-mock-markets>
      {onBack && <button type="button" data-mock-markets-back onClick={onBack}>Back</button>}
    </div>
  ),
}));

vi.mock("./SentSettings", () => ({
  SentSettings: () => <div data-mock-sent-settings />,
}));

vi.mock("./ExtensionView", () => ({
  ExtensionUI: ({ extension, ui }: { extension: string; ui: string }) => (
    <div data-mock-extension-ui data-extension={extension} data-ui={ui} />
  ),
  LauncherExtensionSections: () => null,
}));

vi.mock("./ShortcutSettings", () => ({ ShortcutSettings: () => <div data-page="shortcuts" /> }));
vi.mock("./NotificationSettings", () => ({ NotificationSettings: () => <div data-page="notifications" /> }));
vi.mock("./ModelConfiguration", () => ({ ModelConfiguration: () => <div data-page="model-config" /> }));
vi.mock("./KnowledgeSelection", () => ({ KnowledgeSelection: () => <div data-page="knowledge-selection" /> }));
vi.mock("./TranslationSettings", () => ({ TranslationSettings: () => <div data-page="translation" /> }));
vi.mock("./AssistantSettings", () => ({ AssistantSettings: () => <div data-page="persona" /> }));
vi.mock("./SidebarPages", () => ({ SidebarPagesGroup: () => <div data-page="sidebar-pages" /> }));
vi.mock("./StartPage", () => ({ StartPageGroup: () => <div data-page="start-page" /> }));
vi.mock("./AgentSettings", () => ({ AgentSettings: () => <div data-page="agent" /> }));
vi.mock("./Extensions", () => ({
  Extensions: ({ open }: { open: string | null }) => <div data-page="legacy-extensions" data-open={open ?? ""} />,
}));
vi.mock("./Connectors", () => ({
  Connectors: ({ open }: { open: string | null }) => <div data-page="legacy-connectors" data-open={open ?? ""} />,
}));
vi.mock("./Channels", () => ({
  ChannelList: () => <div data-page="legacy-channels-list" />,
  ChannelPage: ({ id, manager }: { id: string; manager?: string }) => (
    <div data-page="legacy-channel-detail" data-id={id} data-manager={manager ?? ""} />
  ),
}));
vi.mock("./HubSettings", () => ({ HubSettings: () => <div data-page="hub" /> }));
vi.mock("./AgentGroup", () => ({ AgentGroup: () => <div data-page="agent-group" /> }));
vi.mock("../../lib/pwa", async (original) => ({ ...(await original<typeof import("../../lib/pwa")>()), useInstall: () => "installed" }));

import { initI18n, t } from "../../i18n";
import type { Channel } from "../../lib/channels";
import type { HubConnector } from "../../lib/connectors";
import type { HubExtension } from "../../lib/extensions";
import { loadPages } from "../../lib/lazy-page";
import { resetManagerChoice } from "../../lib/manager-choice";
import { resetRoster } from "../../lib/managers";
import { Settings } from "../Settings";
import { Integrations } from "./Integrations";
import {
  buildIntegrationCatalog,
  filterIntegrationItems,
  isIntegrationRowSelected,
  resolveIntegrationTarget,
} from "./integrations/catalog";
import { parseIntegrationsSub } from "./integrations-route";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SAMPLE_EXTENSIONS: HubExtension[] = [
  {
    id: "larkx",
    name: "LarkX Suite",
    kind: "plugin",
    status: "running",
    enabled: true,
    source: "installed",
    description: "Unified Lark bot, inbox connector, and doc tools.",
    contributes: { connectors: ["larkx"], channel: ["larkx-bot"], tools: ["lark_docs"] },
  },
  {
    id: "wechat",
    name: "WeChat Bot",
    kind: "plugin",
    status: "running",
    enabled: true,
    source: "installed",
    description: "Chat with Persona on WeChat.",
    contributes: { channel: ["wechat"] },
  },
  {
    id: "nord-theme",
    name: "Nord Theme",
    kind: "plugin",
    status: "running",
    enabled: true,
    source: "installed",
    description: "Calm polar color palette.",
    contributes: { themes: ["nord"] },
  },
];

const SAMPLE_CHANNELS: Channel[] = [
  {
    id: "feishu",
    name: "Feishu Bot",
    kind: "im",
    source: "builtin",
    installed: true,
    enabled: true,
    state: "connected",
    capabilities: {},
    bind: "code",
  },
  {
    id: "apps",
    name: "Persona Apps",
    kind: "app",
    source: "builtin",
    installed: true,
    enabled: true,
    state: "connected",
    capabilities: {},
    bind: "none",
    surfaces: ["iPhone", "Web"],
  },
  {
    id: "larkx-bot",
    name: "LarkX Bot",
    kind: "im",
    source: "plugin",
    plugin: "larkx",
    installed: true,
    enabled: true,
    state: "connected",
    capabilities: {},
    bind: "config",
  },
  {
    id: "wechat",
    name: "WeChat",
    kind: "im",
    source: "plugin",
    plugin: "wechat",
    installed: true,
    enabled: true,
    state: "connected",
    capabilities: {},
    bind: "qr",
  },
  {
    id: "market-only-im",
    name: "Market IM",
    kind: "im",
    source: "market",
    market: "official",
    plugin: "market-im",
    installed: false,
    enabled: false,
    state: "not_installed",
    capabilities: {},
    bind: "qr",
  },
];

const SAMPLE_CONNECTORS: HubConnector[] = [
  { id: "mail", name: "Mail", status: "ok", capabilities: ["inbox", "archive"] },
  { id: "larkcli", name: "Lark CLI", status: "ok", account: "alice", capabilities: ["login", "inbox"] },
  { id: "github", name: "GitHub", status: "ok", account: "@octocat", capabilities: ["login", "inbox"] },
  { id: "larkx", name: "LarkX Connector", status: "ok", account: "corp-tenant", capabilities: ["inbox", "archive"] },
];

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

beforeAll(async () => {
  await initI18n("zh-Hans");
  await loadPages();
});

beforeEach(() => {
  state.phone = false;
  state.twoCol = true;
  state.integrationsV2 = true;
  state.features = ["channels", "extension_manager", "connectors", "lark"];
  state.loaded = true;
  localStorage.clear();
  resetManagerChoice();
  resetRoster();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const flush = async () => {
  for (let i = 0; i < 8; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

function stubFullHubFetch(overrides?: {
  markets?: boolean;
  failMail?: boolean;
  failBot?: boolean;
  extensions?: HubExtension[];
  channels?: Channel[];
  connectors?: HubConnector[];
}) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://hub.example.com").pathname;
      if (path === "/api/v1/extensions" || path === "/api/v1/plugins") {
        return Promise.resolve(json({ extensions: overrides?.extensions ?? SAMPLE_EXTENSIONS, markets: overrides?.markets ?? true }));
      }
      if (path === "/api/v1/channels") {
        return Promise.resolve(json({ channels: overrides?.channels ?? SAMPLE_CHANNELS }));
      }
      if (path === "/api/v1/connectors") {
        return Promise.resolve(json({ connectors: overrides?.connectors ?? SAMPLE_CONNECTORS }));
      }
      if (path === "/api/v1/mail/accounts") {
        return overrides?.failMail
          ? Promise.resolve(json({ error: "not found" }, 404))
          : Promise.resolve(json({ accounts: [{ id: "m1", email: "me@example.com", status: "ok" }] }));
      }
      if (path === "/api/v1/lark/status") {
        return Promise.resolve(json({ configured: true, connected: true, name: "Workspace" }));
      }
      if (path === "/api/v1/bots/feishu") {
        return overrides?.failBot
          ? Promise.resolve(json({ error: "not found" }, 404))
          : Promise.resolve(json({ enabled: true, has_secret: true, connected: true, bot_name: "Feishu Bot" }));
      }
      if (path === "/api/v1/managers") {
        return Promise.resolve(json({ default: "assistant", managers: [{ id: "assistant", name: "助手", online: true, builtin: true }] }));
      }
      return Promise.resolve(json({}));
    }),
  );
}

describe("buildIntegrationCatalog deduplication and capability classification", () => {
  it("deduplicates multi-capability extensions and built-in services into single canonical rows", () => {
    const items = buildIntegrationCatalog({
      extensions: SAMPLE_EXTENSIONS,
      channels: SAMPLE_CHANNELS,
      connectors: SAMPLE_CONNECTORS,
      hasMail: true,
      hasLark: true,
      hasFeishuBot: true,
      mailAccounts: [{ id: "m1", email: "me@example.com", status: "ok" }],
      larkStatus: { configured: true, connected: true, name: "Workspace" },
      feishuBotStatus: { enabled: true, has_secret: true, connected: true, bot_name: "Feishu Bot" } as never,
    });

    // larkx (extension + channel larkx-bot + connector larkx) appears exactly once with all 3 capabilities.
    const larkxMatches = items.filter((it) => it.id === "larkx" || it.channel?.id === "larkx-bot" || it.connector?.id === "larkx");
    expect(larkxMatches).toHaveLength(1);
    expect(larkxMatches[0]!.kind).toBe("extension");
    expect(larkxMatches[0]!.capabilities).toEqual(["channels", "data", "tools"]);
    expect(larkxMatches[0]!.target).toEqual({
      kind: "extension",
      id: "larkx",
      name: "LarkX Suite",
      channelId: "larkx-bot",
      connectorId: "larkx",
    });

    // Feishu Bot merges feishu channel + larkcli connector into one built-in item.
    const feishuMatches = items.filter((it) => it.id === "feishu-bot" || it.id === "feishu" || it.id === "larkcli");
    expect(feishuMatches).toHaveLength(1);
    expect(feishuMatches[0]!.capabilities).toEqual(["channels", "data"]);

    // Market-only uninstalled channels are excluded from the installed list.
    expect(items.find((it) => it.id === "market-only-im")).toBeUndefined();

    // Built-in channel-only item (`apps`) sets connectorId: null so IntegrationDetail skips connector lookup.
    const apps = items.find((it) => it.id === "apps");
    expect(apps?.target).toEqual(expect.objectContaining({ kind: "builtin", id: "apps", channelId: "apps", connectorId: null }));

    // Filtering by each capability returns the exact matching subset.
    expect(filterIntegrationItems(items, "channels").map((it) => it.id)).toEqual(["feishu-bot", "apps", "larkx", "wechat"]);
    expect(filterIntegrationItems(items, "data").map((it) => it.id)).toEqual(["mail", "feishu-bot", "lark", "github", "larkx"]);
    expect(filterIntegrationItems(items, "tools").map((it) => it.id)).toEqual(["larkx", "nord-theme"]);

    // Legacy channel/connector deep links resolve to the merged larkx extension target and highlight its row.
    expect(resolveIntegrationTarget(parseIntegrationsSub("channel:larkx-bot"), items, null)).toEqual(larkxMatches[0]!.target);
    expect(resolveIntegrationTarget(parseIntegrationsSub("connector:larkx"), items, null)).toEqual(larkxMatches[0]!.target);
    expect(isIntegrationRowSelected("channel:larkx-bot", larkxMatches[0]!, null)).toBe(true);
    expect(isIntegrationRowSelected("connector:larkx", larkxMatches[0]!, null)).toBe(true);
  });
});

describe("Integrations unified list view", () => {
  it("renders deduplicated items, filters by all 4 capability chips, opens Extension Market, and mounts IntegrationDetail", async () => {
    stubFullHubFetch();
    let currentSub: string | null = null;
    const render = async (sub: string | null, twoCol = true) => {
      const mount = (next: string | null) => {
        currentSub = next;
        root.render(<Integrations open={next} twoCol={twoCol} onOpen={mount} />);
      };
      await act(async () => {
        mount(sub);
      });
      await flush();
    };

    await render(null, true);

    // Single persistent header Market button in single-column list view; no duplicate bottom Extension Market group (#14958).
    const singleHeaderMarket = host.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
    expect(singleHeaderMarket).not.toBeNull();
    expect(singleHeaderMarket.textContent).toContain(t("settings-integrations.browse_market_action"));
    expect(host.querySelector('[data-settings-group="market"]')).toBeNull();
    expect(host.textContent).not.toContain(t("settings-extensions.browse_market"));
    expect(host.textContent).not.toContain(t("settings-extensions.manage_markets"));

    // Deduplicated rows: larkx appears once, feishu-bot once, mail/lark/github/apps/wechat/nord-theme once each.
    const rows = () => [...host.querySelectorAll<HTMLButtonElement>("[data-integration-row]")];
    expect(rows().map((r) => r.dataset.integrationId)).toEqual([
      "mail", "feishu-bot", "lark", "github", "apps", "larkx", "wechat", "nord-theme",
    ]);

    // Filter chips: All / Chat channels / Data sources / Tools & appearance.
    const chip = (id: string) => host.querySelector<HTMLButtonElement>(`[data-integration-filter="${id}"]`)!;
    expect(chip("all").getAttribute("aria-pressed")).toBe("true");

    await act(async () => chip("channels").click());
    expect(chip("channels").getAttribute("aria-pressed")).toBe("true");
    expect(rows().map((r) => r.dataset.integrationId)).toEqual(["feishu-bot", "apps", "larkx", "wechat"]);

    await act(async () => chip("data").click());
    expect(rows().map((r) => r.dataset.integrationId)).toEqual(["mail", "feishu-bot", "lark", "github", "larkx"]);

    await act(async () => chip("tools").click());
    expect(rows().map((r) => r.dataset.integrationId)).toEqual(["larkx", "nord-theme"]);

    await act(async () => chip("all").click());
    expect(rows()).toHaveLength(8);

    // Clicking larkx opens IntegrationDetail in desktop two-column split pane with larkx highlighted,
    // independent left/right scroll containers, single-row filter chips, and no redundant Back button.
    const larkxRow = rows().find((r) => r.dataset.integrationId === "larkx")!;
    await act(async () => larkxRow.click());
    await flush();
    expect(currentSub).toBe("ext:larkx");
    const split = host.querySelector<HTMLElement>("[data-integrations-split]")!;
    expect(split).not.toBeNull();
    expect(split.classList.contains("overflow-hidden")).toBe(true);
    expect(split.classList.contains("h-full")).toBe(true);
    const listHeader = host.querySelector<HTMLElement>("[data-integrations-list-header]")!;
    expect(listHeader).not.toBeNull();
    expect(listHeader.classList.contains("shrink-0")).toBe(true);
    // Split-pane narrow header keeps the single persistent compact Market button and no bottom market group (#14958).
    const compactHeaderMarket = listHeader.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
    expect(compactHeaderMarket).not.toBeNull();
    expect(compactHeaderMarket.textContent).toContain(t("settings-integrations.browse_market_compact"));
    expect(compactHeaderMarket.getAttribute("title")).toBe(t("settings-integrations.browse_market_action"));
    expect(compactHeaderMarket.classList.contains("whitespace-nowrap")).toBe(true);
    expect(compactHeaderMarket.classList.contains("shrink-0")).toBe(true);
    expect(host.querySelector('[data-settings-group="market"]')).toBeNull();
    const listScroll = host.querySelector<HTMLElement>("[data-integrations-list-scroll]")!;
    expect(listScroll.classList.contains("overflow-y-auto")).toBe(true);
    expect(listScroll.classList.contains("overscroll-contain")).toBe(true);
    expect(listScroll.classList.contains("pb-8")).toBe(true);
    const detailScroll = host.querySelector<HTMLElement>("[data-integrations-detail-scroll]")!;
    expect(detailScroll.classList.contains("overflow-y-auto")).toBe(true);
    expect(detailScroll.classList.contains("overscroll-contain")).toBe(true);
    // Filter chips stay single-row non-wrapping in the 300px compact list header.
    const compactFilters = listHeader.querySelector<HTMLElement>("[data-integrations-filters]")!;
    expect(compactFilters).not.toBeNull();
    expect(compactFilters.classList.contains("flex-nowrap")).toBe(true);
    expect(compactFilters.classList.contains("flex-wrap")).toBe(false);
    expect(compactFilters.classList.contains("overflow-x-auto")).toBe(true);
    expect(compactFilters.classList.contains("scrollbar-none")).toBe(true);
    for (const btn of compactFilters.querySelectorAll<HTMLButtonElement>("button")) {
      expect(btn.classList.contains("shrink-0")).toBe(true);
      expect(btn.classList.contains("whitespace-nowrap")).toBe(true);
    }
    const detail = host.querySelector<HTMLElement>("[data-mock-integration-detail]")!;
    expect(detail.dataset.kind).toBe("extension");
    expect(detail.dataset.id).toBe("larkx");
    expect(detail.dataset.channelId).toBe("larkx-bot");
    expect(detail.dataset.connectorId).toBe("larkx");
    // Desktop split-pane hides redundant Back button in right pane.
    expect(host.querySelector("[data-mock-detail-back]")).toBeNull();
    expect(host.querySelector('[data-integration-id="larkx"]')?.getAttribute("aria-current")).toBe("true");

    // Switching items in split-pane resets the right detail scroll without touching the left list scroll.
    listScroll.scrollTop = 180;
    detailScroll.scrollTop = 320;
    await render("builtin:mail", true);
    expect(host.querySelector<HTMLElement>("[data-integrations-list-scroll]")?.scrollTop).toBe(180);
    expect(host.querySelector<HTMLElement>("[data-integrations-detail-scroll]")?.scrollTop).toBe(0);

    // Narrow single-column layout (twoCol=false, headerBack=false) drills down without nested scrollers and keeps Back.
    await render("ext:larkx", false);
    expect(host.querySelector("[data-integrations-split]")).toBeNull();
    expect(host.querySelector("[data-integrations-list]")).toBeNull();
    expect(host.querySelector("[data-integrations-list-scroll]")).toBeNull();
    expect(host.querySelector("[data-integrations-detail-scroll]")).toBeNull();
    expect(host.querySelector("[data-mock-integration-detail]")?.getAttribute("data-id")).toBe("larkx");
    expect(host.querySelector("[data-mock-detail-back]")).not.toBeNull();

    // Phone layout (twoCol=false, headerBack=true) omits inner Back because the top bar provides Back.
    await act(async () => {
      root.render(<Integrations open="ext:larkx" twoCol={false} headerBack={true} onOpen={(n) => (currentSub = n)} />);
    });
    await flush();
    expect(host.querySelector("[data-integrations-split]")).toBeNull();
    expect(host.querySelector("[data-mock-detail-back]")).toBeNull();

    // Clicking the compact header Market button in split-pane opens MarketCatalog and exposes Manage Markets inside it.
    await render("ext:larkx", true);
    const splitMarketBtn = host.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
    await act(async () => splitMarketBtn.click());
    await flush();
    expect(currentSub).toBe("-market");
    const catalog = host.querySelector<HTMLElement>("[data-mock-market-catalog]")!;
    expect(catalog).not.toBeNull();
    expect(catalog.dataset.backLabel).toBe(t("settings-integrations.back"));

    const manageMarketsBtn = host.querySelector<HTMLButtonElement>("[data-mock-markets-open]")!;
    expect(manageMarketsBtn).not.toBeNull();
    await act(async () => manageMarketsBtn.click());
    await flush();
    expect(currentSub).toBe("-markets");
    expect(host.querySelector("[data-mock-markets]")).not.toBeNull();

    // Clicking the header Market button in single-column mode also opens MarketCatalog.
    await render(null, false);
    const singleMarketBtn = host.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
    expect(singleMarketBtn.textContent).toContain(t("settings-integrations.browse_market_action"));
    await act(async () => singleMarketBtn.click());
    await flush();
    expect(currentSub).toBe("-market");
    expect(host.querySelector("[data-mock-market-catalog]")).not.toBeNull();
  });

  it("keeps empty-state CTA to browse Extension Market while omitting the duplicate bottom market group", async () => {
    state.features = ["extension_manager"];
    stubFullHubFetch({ markets: true, extensions: [], channels: [], connectors: [], failMail: true, failBot: true });
    let currentSub: string | null = null;
    const render = async (sub: string | null) => {
      currentSub = sub;
      await act(async () => {
        root.render(
          <Integrations
            open={sub}
            twoCol={false}
            onOpen={(next) => {
              currentSub = next;
              root.render(<Integrations open={next} twoCol={false} onOpen={(n) => (currentSub = n)} />);
            }}
          />,
        );
      });
      await flush();
    };

    await render(null);
    // Filter to tools (empty) and verify empty state CTA navigates to MarketCatalog without any bottom market group.
    const toolsChip = host.querySelector<HTMLButtonElement>('[data-integration-filter="tools"]')!;
    await act(async () => toolsChip.click());
    await flush();
    const empty = host.querySelector<HTMLElement>("[data-integrations-empty]")!;
    expect(empty).not.toBeNull();
    expect(host.querySelector('[data-settings-group="market"]')).toBeNull();
    const emptyCta = [...empty.querySelectorAll<HTMLButtonElement>("button")].find((b) =>
      b.textContent?.includes(t("settings-integrations.browse_market_action")),
    )!;
    expect(emptyCta).not.toBeNull();
    await act(async () => emptyCta.click());
    await flush();
    expect(currentSub).toBe("-market");
    expect(host.querySelector("[data-mock-market-catalog]")).not.toBeNull();
  });

  it("degrades gracefully on older hubs without extension_manager, channels, or connectors", async () => {
    state.features = [];
    stubFullHubFetch({ markets: false, failMail: true, failBot: true });
    await act(async () => {
      root.render(<Integrations open={null} onOpen={() => {}} />);
    });
    await flush();
    // Still renders Mail built-in without crashing, hides Extension Market header button, and shows filter chips.
    expect(host.querySelector("[data-integrations-filters]")).not.toBeNull();
    expect(host.querySelector('[data-testid="integrations-market-entry"]')).toBeNull();
    expect(host.querySelector('[data-settings-group="market"]')).toBeNull();
    expect(host.querySelector('[data-integration-id="mail"]')).not.toBeNull();
  });

  it.each(["en", "zh-Hans", "ja"])("renders localized labels, compact Market entry, and single-row narrow capability chips in %s", async (lang) => {
    stubFullHubFetch();
    try {
      await initI18n(lang);
      await act(async () => {
        root.render(<Integrations open="ext:larkx" twoCol={true} onOpen={() => {}} />);
      });
      await flush();
      expect(host.textContent).toContain(t("settings-integrations.list_title"));
      expect(host.textContent).toContain(t("settings-integrations.filter_all"));
      expect(host.textContent).toContain(t("settings-integrations.filter_channels"));
      expect(host.textContent).toContain(t("settings-integrations.filter_data"));
      expect(host.textContent).toContain(t("settings-integrations.filter_tools"));
      const compactMarket = host.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
      expect(compactMarket.textContent).toContain(t("settings-integrations.browse_market_compact"));
      expect(host.querySelector('[data-settings-group="market"]')).toBeNull();
      const filters = host.querySelector<HTMLElement>("[data-integrations-filters]")!;
      expect(filters.classList.contains("flex-nowrap")).toBe(true);
      expect(filters.classList.contains("overflow-x-auto")).toBe(true);

      await act(async () => {
        root.render(<Integrations open={null} twoCol={true} onOpen={() => {}} />);
      });
      await flush();
      const singleMarket = host.querySelector<HTMLButtonElement>('[data-testid="integrations-market-entry"]')!;
      expect(singleMarket.textContent).toContain(t("settings-integrations.browse_market_action"));
    } finally {
      await initI18n("zh-Hans");
    }
  });
});

describe("Settings navigation collapse and bidirectional redirects under integrations_v2", () => {
  it("keeps legacy Channels/Connectors/Extensions tabs untouched when integrations_v2 is off and redirects /settings/integrations/*", async () => {
    state.integrationsV2 = false;
    stubFullHubFetch();
    history.replaceState(null, "", "/settings/integrations/ext:larkx@work");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();

    // Redirected cleanly back to legacy Extensions with @work preserved, zero 404/white screen.
    expect(location.pathname + location.search).toBe("/settings?tab=extensions&extension=larkx%40work");
    const opens = [...host.querySelectorAll('[data-page="legacy-extensions"]')].map((el) => el.getAttribute("data-open"));
    expect(opens).toContain("larkx@work");

    const navText = host.querySelector("nav")?.textContent ?? "";
    expect(navText).toContain(t("settings-nav.tab_channels"));
    expect(navText).toContain(t("settings-nav.tab_connectors"));
    expect(navText).toContain(t("settings-nav.tab_extensions"));
    expect(navText).not.toContain(t("settings-nav.tab_integrations"));
  });

  it("collapses Channels/Connectors/Extensions into one Integrations entry when integrations_v2 is on and redirects legacy deep links", async () => {
    state.integrationsV2 = true;
    stubFullHubFetch();
    history.replaceState(null, "", "/settings/connectors/github@work");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();

    expect(location.pathname + location.search).toBe("/settings?tab=integrations&integration=connector%3Agithub%40work");
    expect(host.querySelector("[data-mock-integration-detail]")?.getAttribute("data-id")).toBe("github");
    expect(host.querySelector("[data-mock-integration-detail]")?.getAttribute("data-manager")).toBe("work");

    const navButtons = [...host.querySelectorAll<HTMLButtonElement>("nav button")].map((b) => b.textContent ?? "");
    expect(navButtons).toContain(t("settings-nav.tab_integrations"));
    expect(navButtons).not.toContain(t("settings-nav.tab_channels"));
    expect(navButtons).not.toContain(t("settings-nav.tab_connectors"));
    expect(navButtons).not.toContain(t("settings-nav.tab_extensions"));
  });

  it("reconciles seamlessly when integrations_v2 toggles off while viewing an integration detail", async () => {
    state.integrationsV2 = true;
    stubFullHubFetch();
    history.replaceState(null, "", "/settings?tab=integrations&integration=ext%3Alarkx");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();
    expect(host.querySelector("[data-mock-integration-detail]")?.getAttribute("data-id")).toBe("larkx");

    // Flip integrations_v2 off and re-render: smoothly redirects to legacy Extensions without white screen.
    state.integrationsV2 = false;
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();
    expect(location.pathname + location.search).toBe("/settings?tab=extensions&extension=larkx");
    const opens = [...host.querySelectorAll('[data-page="legacy-extensions"]')].map((el) => el.getAttribute("data-open"));
    expect(opens).toContain("larkx");
    // Legacy Extensions stays on the original single outer scroller (zero split-host change).
    const region = host.querySelector<HTMLElement>('[role="region"]')!;
    expect(region.hasAttribute("data-integrations-split-host")).toBe(false);
    expect(region.classList.contains("overflow-y-auto")).toBe(true);
  });

  it("locks the Settings content host to viewport height only in desktop integrations_v2 split-pane detail state", async () => {
    state.integrationsV2 = true;
    state.twoCol = true;
    state.phone = false;
    stubFullHubFetch();

    // 1. Desktop list-only view: single natural scroll container (not viewport-locked split host).
    history.replaceState(null, "", "/settings?tab=integrations");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();
    const listRegion = host.querySelector<HTMLElement>('[role="region"]')!;
    expect(listRegion.hasAttribute("data-integrations-split-host")).toBe(false);
    expect(listRegion.classList.contains("overflow-y-auto")).toBe(true);
    expect(listRegion.classList.contains("overflow-hidden")).toBe(false);
    expect(host.querySelector("[data-integrations-list-scroll]")).toBeNull();
    expect(host.querySelector("[data-integrations-detail-scroll]")).toBeNull();

    // 2. Clicking a row enters desktop split-pane: outer region locks height, two independent scrollers mount, no redundant Back.
    const larkxRow = host.querySelector<HTMLButtonElement>('[data-integration-id="larkx"]')!;
    await act(async () => larkxRow.click());
    await flush();
    const splitRegion = host.querySelector<HTMLElement>('[role="region"]')!;
    expect(splitRegion.hasAttribute("data-integrations-split-host")).toBe(true);
    expect(splitRegion.classList.contains("flex")).toBe(true);
    expect(splitRegion.classList.contains("h-full")).toBe(true);
    expect(splitRegion.classList.contains("min-h-0")).toBe(true);
    expect(splitRegion.classList.contains("overflow-hidden")).toBe(true);
    expect(splitRegion.classList.contains("overflow-y-auto")).toBe(false);
    expect(host.querySelector("[data-integrations-list-scroll]")).not.toBeNull();
    expect(host.querySelector("[data-integrations-detail-scroll]")).not.toBeNull();
    expect(host.querySelector("[data-mock-detail-back]")).toBeNull();

    // 3. Narrow desktop (< 900px, twoCol=false, phone=false): single natural scroller, Back button kept.
    state.twoCol = false;
    await act(async () => root.render(null));
    history.replaceState(null, "", "/settings?tab=integrations&integration=ext%3Alarkx");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();
    const narrowRegion = host.querySelector<HTMLElement>('[role="region"]')!;
    expect(narrowRegion.hasAttribute("data-integrations-split-host")).toBe(false);
    expect(narrowRegion.classList.contains("overflow-y-auto")).toBe(true);
    expect(host.querySelector("[data-integrations-list-scroll]")).toBeNull();
    expect(host.querySelector("[data-integrations-detail-scroll]")).toBeNull();
    expect(host.querySelector("[data-mock-detail-back]")).not.toBeNull();

    // 4. Phone (twoCol=false, phone=true): single natural scroller, top-bar Back present, no inner Back.
    state.phone = true;
    await act(async () => root.render(null));
    history.replaceState(null, "", "/settings?tab=integrations&integration=ext%3Alarkx");
    await act(async () => {
      root.render(<Settings onToggleSidebar={() => {}} />);
    });
    await flush();
    const phoneRegion = host.querySelector<HTMLElement>('[role="region"]')!;
    expect(phoneRegion.hasAttribute("data-integrations-split-host")).toBe(false);
    expect(phoneRegion.classList.contains("overflow-y-auto")).toBe(true);
    expect(host.querySelector("[data-integrations-list-scroll]")).toBeNull();
    expect(host.querySelector("[data-integrations-detail-scroll]")).toBeNull();
    expect(host.querySelector("[data-mock-detail-back]")).toBeNull();
    expect(host.querySelector("header button:first-child")).not.toBeNull();
  });
});
