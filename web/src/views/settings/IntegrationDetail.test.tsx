// @vitest-environment jsdom
// Unit & integration tests for the lazy-loaded unified IntegrationDetail view (integrations_v2, #14847).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { ChannelDetail } from "../../lib/channels";
import type { HubConnector } from "../../lib/connectors";
import type { HubExtensionDetail } from "../../lib/extensions";
import type { FeishuBotStatus } from "../../lib/feishu-bot";
import { resetManagerChoice } from "../../lib/manager-choice";
import { setRoster } from "../../lib/managers";
import { applyCompleteSessions } from "../../lib/store";
import { IntegrationDetail, isIntegrationsV2Active, partitionExtensionFields } from "./IntegrationDetail";

vi.mock("../../ui/atoms", async (original) => ({
  ...(await original<typeof import("../../ui/atoms")>()),
  toast: vi.fn(),
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface RecordedRequest {
  url: string;
  method: string;
  body: unknown;
}

const ok = (body: unknown) => ({
  ok: true,
  status: 200,
  text: async () => JSON.stringify(body),
  headers: new Headers(),
});
const notFound = () => ({
  ok: false,
  status: 404,
  text: async () => JSON.stringify({ error: "not found" }),
  headers: new Headers(),
});

const ROSTER = {
  default: "assistant",
  managers: [
    { id: "assistant", name: "Default", builtin: true, online: true },
    { id: "other", name: "Other", builtin: false, online: true },
  ],
};

const COMPOSITE_EXTENSION: HubExtensionDetail = {
  id: "larkx",
  name: "Lark Extended",
  kind: "process",
  source: "installed",
  status: "running",
  version: "2.1.0",
  enabled: true,
  origin: "market:official/larkx",
  auto_upgrade: true,
  description: "Composite connector, channel, and inbox extension.",
  contributes: {
    connectors: ["larkx_conn"],
    channel: ["larkx_chat"],
  },
  auth: {
    title: "Workspace OAuth",
    state: "signed_in",
    account: "owner@example.com",
  },
  form: {
    fields: [
      { name: "endpoint", type: "string", format: "uri", title: "API Endpoint", section: "connection" },
      { name: "mention_prefix", type: "string", title: "Mention Prefix", section: "channel" },
      { name: "triage_rules", type: "string", title: "Triage Rules", section: "inbox" },
      { name: "display_alias", type: "string", title: "Display Alias" },
      { name: "debug_trace", type: "boolean", title: "Debug Trace", section: "advanced" },
    ],
  },
  config: {
    endpoint: "https://api.example.com",
    mention_prefix: "@bot",
    triage_rules: "Only urgent items",
    display_alias: "Team Lark",
    debug_trace: false,
  },
  secrets: [
    { name: "app_secret", title: "App Secret", required: true, set: true, hint: "1234" },
  ],
  permissions: [
    { key: "network", title: "Network access", detail: "Connects to workspace APIs" },
  ],
};

const COMPOSITE_CHANNEL: ChannelDetail = {
  id: "larkx_chat",
  name: "Lark Chat",
  kind: "im",
  source: "plugin",
  plugin: "larkx",
  installed: true,
  enabled: true,
  state: "connected",
  account: { name: "Owner" },
  capabilities: { push: true, threads: true, format: "markdown" },
  bind: "qr",
  target: { kind: "main" },
  thread_mode: "channel_default",
  thread_idle_hours: 0,
  features: [{ id: "asks", title: "Questions & approvals", on: true }],
  waiting: { answers: 0, asks: 0 },
};

const COMPOSITE_CONNECTOR: HubConnector = {
  id: "larkx_conn",
  name: "Lark Workspace",
  status: "ok",
  account: "owner@example.com",
  enabled: true,
  archive: true,
  inbox: true,
  capabilities: ["login", "archive", "inbox"],
};

const FEISHU_BOT_STATUS: FeishuBotStatus = {
  enabled: true,
  connected: true,
  has_secret: true,
  from_config: false,
  app_id: "cli_example_123456",
  domain: "feishu",
  bot_name: "Persona Feishu",
  owner: { name: "Alice", paired_at: 1700000000 },
  features: { main: true, cards: true, all_cards: false, pushes: true, sync: true, reports: true, board: false, workbench: false, only_conclusions: false, agents: true },
  nudge_after: 15,
};

const FEISHU_CHANNEL: ChannelDetail = {
  id: "feishu",
  name: "Feishu",
  kind: "im",
  source: "builtin",
  installed: true,
  enabled: true,
  state: "connected",
  account: { name: "Alice" },
  capabilities: { push: true, threads: true, asks: "buttons" },
  bind: "fields",
  target: { kind: "main" },
  thread_mode: "per_topic",
  thread_idle_hours: 24,
  waiting: { answers: 0, asks: 0 },
};

let host: HTMLDivElement;
let root: Root;
let requests: RecordedRequest[];
let currentExt: HubExtensionDetail;
let currentChannels: Record<string, ChannelDetail>;
let currentConnectors: HubConnector[];
let currentMailAccounts: { id: string; email: string; provider: string; status: string }[];
let currentFeishuBot: FeishuBotStatus;

const flush = async () => {
  for (let i = 0; i < 14; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const SEED_SESSION = { machine_id: "hub", session_id: "seed-1", status: "idle", updated_at: "2026-01-01T00:00:00Z" } as never;

function setStoreAssistant(integrations_v2: boolean | undefined, manager = "assistant") {
  // Ensure the seed session is present so applyCompleteSessions always hits its `also`-only branch.
  applyCompleteSessions([SEED_SESSION]);
  applyCompleteSessions([SEED_SESSION], [], {
    features: ["channels", "connectors", "extensions"],
    assistant: {
      machine_id: "hub",
      session_id: "main-1",
      manager,
      integrations_v2,
      conversations: [],
    } as never,
  });
}

function section(id: string): HTMLElement | null {
  return host.querySelector<HTMLElement>(`[data-integration-section="${id}"]`);
}

function sectionIds(): string[] {
  return [...host.querySelectorAll<HTMLElement>("[data-integration-section]")].map((el) => el.getAttribute("data-integration-section") || "");
}

function setInputValue(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  setter.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

beforeEach(() => {
  requests = [];
  currentExt = structuredClone(COMPOSITE_EXTENSION);
  currentChannels = {
    larkx_chat: structuredClone(COMPOSITE_CHANNEL),
    feishu: structuredClone(FEISHU_CHANNEL),
  };
  currentConnectors = [
    structuredClone(COMPOSITE_CONNECTOR),
    { id: "mail", name: "Mail", status: "ok", enabled: true, inbox: true, capabilities: ["inbox"] },
    {
      id: "github",
      name: "GitHub",
      status: "ok",
      account: "octocat",
      enabled: true,
      archive: true,
      inbox: true,
      capabilities: ["login", "archive", "inbox"],
      options: { ci: "mine", muted_repos: ["org/noisy"], ignore_branches: ["train/*"] },
    },
  ];
  currentMailAccounts = [{ id: "m1", email: "me@example.com", provider: "imap", status: "ok" }];
  currentFeishuBot = structuredClone(FEISHU_BOT_STATUS);

  resetManagerChoice();
  setRoster(ROSTER);
  setStoreAssistant(true);

  vi.stubGlobal("fetch", vi.fn(async (input: string | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method || "GET").toUpperCase();
    let body: unknown = undefined;
    if (typeof init?.body === "string" && init.body) {
      try { body = JSON.parse(init.body); } catch { body = init.body; }
    }
    requests.push({ url, method, body });

    if (url.includes("/mail/accounts")) {
      return ok({ accounts: currentMailAccounts });
    }
    if (url.includes("/bots/feishu")) {
      if (method === "PUT" && body && typeof body === "object") {
        currentFeishuBot = { ...currentFeishuBot, ...(body as Partial<FeishuBotStatus>) };
      }
      return ok(currentFeishuBot);
    }
    if (url.includes("/connectors/github/auth")) {
      return ok({ source: "device", machines: [], missing: [] });
    }
    if (url.includes("/connectors/") && url.endsWith("/settings") && method === "PUT") {
      const m = /\/connectors\/([^/]+)\/settings/.exec(url);
      const cid = m ? decodeURIComponent(m[1]) : "";
      const found = currentConnectors.find((c) => c.id === cid);
      if (found && body && typeof body === "object") {
        Object.assign(found, body);
        return ok(found);
      }
      return notFound();
    }
    if (/\/connectors(\?|$)/.test(url)) {
      return ok({ connectors: currentConnectors });
    }
    if (url.includes("/channels/")) {
      const testMatch = /\/channels\/([^/?]+)\/test/.exec(url);
      if (testMatch && method === "POST") {
        return ok({ ok: true, note: "Test delivered" });
      }
      const chMatch = /\/channels\/([^/?]+)/.exec(url);
      const cid = chMatch ? decodeURIComponent(chMatch[1]) : "";
      const ch = currentChannels[cid];
      if (!ch) return notFound();
      if (method === "PUT" && body && typeof body === "object") {
        const patch = body as Partial<ChannelDetail>;
        const next: ChannelDetail = {
          ...ch,
          ...patch,
          target_override:
            (patch.thread_mode ?? ch.thread_mode) === "per_group_and_topic" || (patch.thread_mode ?? ch.thread_mode) === "per_topic"
              ? { mode: "topics" }
              : undefined,
        };
        currentChannels[cid] = next;
        return ok(next);
      }
      return ok(ch);
    }
    if (url.includes("/-/upgrades")) {
      return ok({ auto: true });
    }
    if (url.includes("/-/config") && method === "PUT") {
      const cfg = (body as { config?: Record<string, unknown> })?.config ?? {};
      currentExt = { ...currentExt, config: cfg as HubExtensionDetail["config"] };
      return ok(currentExt);
    }
    if (url.includes("/-/enabled") && method === "PUT") {
      const enabled = (body as { enabled?: boolean })?.enabled ?? true;
      currentExt = { ...currentExt, enabled, status: enabled ? "running" : "disabled" };
      return ok(currentExt);
    }
    if (url.includes("/extensions/") || url.includes("/plugins/")) {
      return ok(currentExt);
    }
    if (url.includes("/subscriptions/summary")) {
      return ok({ lines: [] });
    }
    if (url.includes("/trust")) {
      return notFound();
    }
    return ok({});
  }));

  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.documentElement.classList.remove("dark");
  vi.unstubAllGlobals();
  setRoster(null);
  resetManagerChoice();
});

describe("IntegrationDetail pure helpers & lazy import", () => {
  it("can be loaded via dynamic import() with both named and default exports", async () => {
    const mod = await import("./IntegrationDetail");
    expect(mod.IntegrationDetail).toBe(IntegrationDetail);
    expect(mod.default).toBe(IntegrationDetail);
  });

  it("partitions fields by section and preserves legacy/unknown fields in general", () => {
    const parts = partitionExtensionFields([
      { name: "a", type: "string", section: "connection" },
      { name: "b", type: "string" },
      { name: "c", type: "string", section: "channel" },
      { name: "d", type: "string", section: "inbox" },
      { name: "e", type: "boolean", section: "advanced" },
      { name: "f", type: "string", section: "future_section" as never },
    ]);
    expect(parts.connection.map((x) => x.name)).toEqual(["a"]);
    expect(parts.channel.map((x) => x.name)).toEqual(["c"]);
    expect(parts.inbox.map((x) => x.name)).toEqual(["d"]);
    expect(parts.general.map((x) => x.name)).toEqual(["b", "f"]);
    expect(parts.advanced.map((x) => x.name)).toEqual(["e"]);
  });

  it("gates integrations_v2 fail-closed and respects assistant manager scope", () => {
    expect(isIntegrationsV2Active(null)).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: false })).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant" })).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: true }, "assistant")).toBe(true);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: true }, "other")).toBe(false);
  });
});

describe("integrations_v2 gating & legacy isolation", () => {
  it("renders fallback and issues zero API requests when integrations_v2 is false", async () => {
    setStoreAssistant(false);
    act(() => {
      root.render(
        <IntegrationDetail
          target={{ kind: "extension", id: "larkx" }}
          fallback={<div data-legacy-fallback>Legacy view</div>}
        />,
      );
    });
    await flush();

    expect(host.querySelector("[data-legacy-fallback]")?.textContent).toBe("Legacy view");
    expect(host.querySelector("[data-integration-detail]")).toBeNull();
    expect(requests).toHaveLength(0);
  });

  it("renders nothing by default when integrations_v2 is omitted (older hub)", async () => {
    setStoreAssistant(undefined);
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "mail" }} />);
    });
    await flush();

    expect(host.innerHTML).toBe("");
    expect(requests).toHaveLength(0);
  });
});

describe("Built-in services", () => {
  it("renders Mail with Account & Connection accounts and Inbox & Sync switch", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "mail" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "inbox"]);
    expect(section("connection")!.textContent).toContain("me@example.com");
    expect(section("inbox")!.textContent).toContain(t("settings-hubconnector.to_inbox"));
    expect(requests.some((r) => r.url.includes("/bots/feishu"))).toBe(false);
  });

  it("renders Feishu Bot inline with connection, channel routing, and bot features", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "feishu-bot" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "channel"]);
    expect(section("connection")!.textContent).toContain("Alice");
    expect(section("channel")!.textContent).toContain(t("settings-integrations.thread_mode_title"));
    expect(section("channel")!.textContent).toContain(t("settings-feishubot.topic_groups"));
  });

  it("renders built-in GitHub connector across Account & Connection and Inbox & Sync", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "github" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "inbox"]);
    expect(section("connection")!.textContent).toContain("octocat");
    expect(section("inbox")!.textContent).toContain("org/noisy");
    expect(requests.some((r) => r.url.includes("/bots/feishu"))).toBe(false);
  });

  it("omits the Back button when onBack is undefined (desktop split-pane / phone) and renders it when provided", async () => {
    const onBack = vi.fn();
    for (const target of [
      { kind: "builtin", id: "mail" } as const,
      { kind: "builtin", id: "feishu-bot" } as const,
      { kind: "extension", id: "larkx" } as const,
    ]) {
      act(() => {
        root.render(<IntegrationDetail target={target} />);
      });
      await flush();
      const hiddenBack = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-integrations.back")));
      expect(hiddenBack, `${target.id} without onBack`).toBeUndefined();

      act(() => {
        root.render(<IntegrationDetail target={target} onBack={onBack} />);
      });
      await flush();
      const visibleBack = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-integrations.back")));
      expect(visibleBack, `${target.id} with onBack`).toBeDefined();
      act(() => visibleBack!.click());
    }
    expect(onBack).toHaveBeenCalledTimes(3);
  });
});

describe("Composite extension (connector + channel + sectioned config + lifecycle)", () => {
  it("renders all 5 sections inline, folds advanced fields, and saves unified config atomically", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "channel", "inbox", "extension-settings", "management"]);

    // 1. Account & Connection: auth + connector + section="connection" field
    expect(section("connection")!.textContent).toContain("Workspace OAuth");
    expect(section("connection")!.textContent).toContain("owner@example.com");
    expect(section("connection")!.querySelector('[data-field="endpoint"]')).not.toBeNull();

    // 2. Chat & Delivery: channel toggle, Send to target, thread_mode & thread_idle_hours, section="channel" field
    expect(section("channel")!.textContent).toContain(t("settings-channels.target_row"));
    expect(section("channel")!.textContent).toContain(t("settings-integrations.thread_mode_title"));
    expect(section("channel")!.textContent).toContain(t("settings-integrations.thread_idle_hours_title"));
    expect(section("channel")!.querySelector('[data-field="mention_prefix"]')).not.toBeNull();

    // 3. Inbox & Sync: connector feeds + section="inbox" field
    expect(section("inbox")!.textContent).toContain(t("settings-hubconnector.to_inbox"));
    expect(section("inbox")!.querySelector('[data-field="triage_rules"]')).not.toBeNull();

    // 4. Extension Settings: unsectioned field visible, advanced field folded by default, secret present
    expect(section("extension-settings")!.querySelector('[data-field="display_alias"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector('[data-field="debug_trace"]')).toBeNull();
    expect(section("extension-settings")!.textContent).toContain("App Secret");

    // Expand Advanced disclosure -> reveals section="advanced" field
    const advButton = section("extension-settings")!.querySelector<HTMLButtonElement>("[data-integration-advanced] button")!;
    expect(advButton.getAttribute("aria-expanded")).toBe("false");
    act(() => advButton.click());
    await flush();
    expect(advButton.getAttribute("aria-expanded")).toBe("true");
    expect(section("extension-settings")!.querySelector('[data-field="debug_trace"]')).not.toBeNull();

    // 5. Plugin Management: version, enable, updates, uninstall
    expect(section("management")!.textContent).toContain("2.1.0");
    expect(section("management")!.textContent).toContain(t("settings-extensions.uninstall"));

    // Edit fields in two different sections (connection + general) -> single unified Save persists all fields
    const endpointInput = section("connection")!.querySelector<HTMLInputElement>("#extension-field-endpoint")!;
    const aliasInput = section("extension-settings")!.querySelector<HTMLInputElement>("#extension-field-display_alias")!;
    act(() => {
      setInputValue(endpointInput, "https://next.example.com");
      setInputValue(aliasInput, "Updated Lark");
    });
    await flush();

    const saveBtn = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("common.save"))!;
    expect(saveBtn).toBeDefined();
    await act(async () => saveBtn.click());
    await flush();

    const putCfg = requests.find((r) => r.method === "PUT" && r.url.includes("/-/config"));
    expect(putCfg).toBeDefined();
    expect(putCfg!.body).toEqual({
      config: {
        endpoint: "https://next.example.com",
        mention_prefix: "@bot",
        triage_rules: "Only urgent items",
        display_alias: "Updated Lark",
        debug_trace: false,
      },
    });
  });

  it("saves thread_mode and thread_idle_hours via PUT /api/v1/channels/{id} and sends test messages", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />);
    });
    await flush();

    // Switch thread_mode to per_group_and_topic
    const groupTopicOption = [...section("channel")!.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) =>
      b.textContent?.includes(t("settings-integrations.thread_mode_group_topic_title")),
    )!;
    expect(groupTopicOption).toBeDefined();
    await act(async () => groupTopicOption.click());
    await flush();

    const modePut = requests.find((r) => r.method === "PUT" && r.url.includes("/channels/larkx_chat"));
    expect(modePut?.body).toEqual({ thread_mode: "per_group_and_topic" });
    // Response updated target_override -> override hint appears immediately
    expect(section("channel")!.textContent).toContain(t("settings-channels.target_override_topics"));

    // Update thread_idle_hours to 48 and click Save inside the ConfigurationForm field
    const idleField = section("channel")!.querySelector<HTMLElement>('[data-configuration-field="thread_idle_hours"]')!;
    const idleInput = idleField.querySelector<HTMLInputElement>("input")!;
    act(() => setInputValue(idleInput, "48"));
    await flush();

    const idleSave = [...idleField.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-configuration.save"))!;
    await act(async () => idleSave.click());
    await flush();

    const idlePut = requests.filter((r) => r.method === "PUT" && r.url.includes("/channels/larkx_chat")).pop();
    expect(idlePut?.body).toEqual({ thread_idle_hours: 48 });

    // Send test message
    const testBtn = [...section("channel")!.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-channels.send_test")))!;
    await act(async () => testBtn.click());
    await flush();
    expect(requests.some((r) => r.method === "POST" && r.url.includes("/channels/larkx_chat/test"))).toBe(true);
    expect(section("channel")!.querySelector('[role="status"]')?.textContent).toBe("Test delivered");
  });
});

describe("Older Hub and legacy extension graceful degradation", () => {
  it("degrades routing controls to read-only on an older Hub omitting thread_mode/thread_idle_hours", async () => {
    currentChannels.larkx_chat = {
      ...COMPOSITE_CHANNEL,
      thread_mode: undefined,
      thread_idle_hours: undefined,
    };

    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />);
    });
    await flush();

    const ch = section("channel")!;
    expect(ch.textContent).toContain(t("settings-integrations.routing_hub_unsupported"));
    const radios = [...ch.querySelectorAll<HTMLButtonElement>('[data-configuration-field="thread_mode"] [role="radio"]')];
    expect(radios.length).toBe(3);
    // Fieldset is disabled so clicking does not issue an unsupported PUT
    const fieldset = ch.querySelector<HTMLFieldSetElement>('[data-configuration-field="thread_mode"] fieldset');
    expect(fieldset?.disabled).toBe(true);
    expect(requests.some((r) => r.method === "PUT" && r.url.includes("/channels/"))).toBe(false);
  });

  it("renders legacy extensions without section metadata seamlessly and omits unused sections", async () => {
    currentExt = {
      id: "legacy-tool",
      name: "Legacy Tool",
      kind: "process",
      source: "installed",
      status: "running",
      version: "1.0.0",
      enabled: true,
      origin: "market:official/legacy-tool",
      auto_upgrade: false,
      form: {
        fields: [
          { name: "api_url", type: "string", title: "API URL" },
          { name: "max_items", type: "integer", title: "Max Items", default: 20 },
        ],
      },
      config: { api_url: "https://legacy.example.com" },
    };

    // Verify both light and dark themes render without error or hard-coded theme issues
    document.documentElement.classList.add("dark");
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "legacy-tool" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["extension-settings", "management"]);
    expect(section("extension-settings")!.querySelector('[data-field="api_url"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector('[data-field="max_items"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector("[data-integration-advanced]")).toBeNull();
  });
});
