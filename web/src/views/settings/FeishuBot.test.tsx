// @vitest-environment jsdom
// The Feishu bot dialog keeps App ID / App Secret edits that are not saved yet: flipping a switch, pairing and the
// pairing poll only take the hub's other fields, and only "verify and save" puts the hub's credentials back in the form.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectorState, type HubConnector } from "../../lib/connectors";
import type { FeishuBotStatus } from "../../lib/feishu-bot";

type Call = { resolve: (s: FeishuBotStatus) => void };
const calls = vi.hoisted(() => ({ status: [] as Call[], update: [] as (Call & { body: unknown })[] }));
const connectorList = vi.hoisted(() => ({ value: [] as HubConnector[], calls: 0 }));
const connectorLogin = vi.hoisted(() => vi.fn());
const setConnector = vi.hoisted(() => vi.fn());
vi.mock("../../lib/feishu-bot", async (original) => ({
  ...(await original<typeof import("../../lib/feishu-bot")>()),
  botStatus: () => new Promise<FeishuBotStatus>((resolve) => calls.status.push({ resolve })),
  updateBot: (body: unknown) => new Promise<FeishuBotStatus>((resolve) => calls.update.push({ resolve, body })),
  pairBot: () => new Promise<FeishuBotStatus>(() => {}),
}));
vi.mock("../../lib/connectors", async (original) => ({
  ...(await original<typeof import("../../lib/connectors")>()),
  listConnectors: () => {
    connectorList.calls++;
    return Promise.resolve(connectorList.value);
  },
  connectorLogin: (...args: unknown[]) => connectorLogin(...args),
  setConnector: (...args: unknown[]) => setConnector(...args),
}));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: () => {} }));
import { t } from "../../i18n";
import { FeishuBot } from "./FeishuBot";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const saved = (over: Partial<FeishuBotStatus> = {}): FeishuBotStatus => ({
  enabled: true,
  app_id: "cli_saved",
  domain: "https://open.feishu.cn",
  has_secret: true,
  from_config: false,
  features: { main: true, cards: true, all_cards: false, pushes: false, sync: false, agents: false, reports: true, board: false },
  connected: true,
  owner: null,
  pairing: null,
  ...over,
});

let host: HTMLDivElement;
let root: Root;
let visibility: DocumentVisibilityState = "visible";
const show = (v: DocumentVisibilityState) =>
  act(async () => {
    visibility = v;
    document.dispatchEvent(new Event("visibilitychange"));
  });
const input = (label: string) => document.body.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
const type = (label: string, value: string) =>
  act(() => {
    const el = input(label);
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
const switches = () => [...document.body.querySelectorAll<HTMLButtonElement>('[role="switch"]')];

// Draws the row, answers its status read, and opens the dialog.
async function open(status: FeishuBotStatus) {
  act(() => root.render(<FeishuBot />));
  await act(async () => calls.status[0].resolve(status));
  await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
}

beforeEach(() => {
  calls.status.length = 0;
  calls.update.length = 0;
  connectorList.value = [];
  connectorList.calls = 0;
  connectorLogin.mockReset();
  setConnector.mockReset();
  visibility = "visible";
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visibility });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  Reflect.deleteProperty(document, "visibilityState");
  vi.useRealTimers();
});

describe("Feishu bot dialog", () => {
  it("combines the bot and lark-cli into one Lark row", async () => {
    const onOpen = vi.fn();
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me" };
    act(() => root.render(<FeishuBot larkcli={larkcli} onOpen={onOpen} />));
    await act(async () => calls.status[0].resolve(saved()));

    const row = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.lark_name")));
    expect(row).toBeDefined();
    expect(row?.textContent).toContain("cli_saved");
    expect(row?.closest("section")?.textContent).toContain(t("settings-feishubot.combined_footer"));
    expect(row?.querySelector("img")?.getAttribute("alt")).toBe("");
  });

  it("keeps the original group when lark-cli is absent", async () => {
    act(() => root.render(<FeishuBot />));
    await act(async () => calls.status[0].resolve(saved()));

    expect(host.textContent).toContain(t("settings-feishubot.footer"));
    expect(host.textContent).not.toContain(t("settings-feishubot.read_section"));
    expect(host.textContent).not.toContain(t("settings-feishubot.combined_footer"));
    expect(host.querySelectorAll("button")).toHaveLength(1);
  });

  it("shows lark-cli in the bot dialog and opens its connector page from More settings", async () => {
    const onOpen = vi.fn();
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me" };
    act(() => root.render(<FeishuBot larkcli={larkcli} onOpen={onOpen} />));
    await act(async () => calls.status[0].resolve(saved()));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    expect(document.body.textContent).toContain(t("settings-feishubot.read_section"));
    const more = [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.read_more")));
    await act(async () => more!.click());

    expect(onOpen).toHaveBeenCalledWith("larkcli");
  });

  it("loads lark-cli for a URL-opened dialog when the page has not passed it yet", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me" };
    connectorList.value = [larkcli];
    act(() => root.render(<FeishuBot />));
    await act(async () => calls.status[0].resolve(saved()));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    await act(async () => {});

    const row = [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.lark_name")));
    expect(row).toBeDefined();
    expect(connectorList.calls).toBe(1);
  });

  it("uses the lark-cli warning as the combined row's status", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "no_permission", account: "me", missing_scopes: ["im:chat:read"] };
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));

    const row = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.lark_name")))!;
    expect(row.textContent).toContain(t("settings-feishubot.read_status_prefix", { status: connectorState(larkcli) }));
    expect([...row.querySelectorAll<HTMLElement>("*")].some((element) => element.classList.contains("bg-orange-tint"))).toBe(true);
  });

  it("orders credentials, read-as-me, then the bot's controls", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me", capabilities: ["login"] };
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    const headings = [...document.querySelectorAll<HTMLElement>("[role=dialog] section h2")].map((x) => x.textContent);
    expect(headings.indexOf(t("settings-feishubot.credentials"))).toBeLessThan(headings.indexOf(t("settings-feishubot.read_section")));
    expect(headings.indexOf(t("settings-feishubot.read_section"))).toBeLessThan(headings.indexOf(t("settings-feishubot.bot_section")));
    // Signed in: the section is titled for the identity, and signing in again is the quiet secondary action.
    const dialog = document.querySelector<HTMLElement>("[role=dialog]")!;
    expect(dialog.textContent).toContain(t("settings-feishubot.read_as_you"));
    const again = [...dialog.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.read_reauthorize")));
    expect(again).toBeDefined();
    expect([...dialog.querySelectorAll("button")].some((button) => button.textContent === t("settings-feishubot.read_authorize"))).toBe(false);
  });

  it("switches reading as you off from the Lark page, the same switch as the connector page", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark · my messages", status: "ok", account: "me", enabled: true };
    setConnector.mockResolvedValue({ ...larkcli, enabled: false });
    connectorList.value = [{ ...larkcli, enabled: false }];
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    const sw = document.querySelector<HTMLButtonElement>(`[role=dialog] [role="switch"][aria-label="${t("settings-feishubot.read_section")}"]`)!;
    expect(sw.getAttribute("aria-checked")).toBe("true");
    await act(async () => sw.click());
    expect(setConnector).toHaveBeenCalledWith("larkcli", { enabled: false });
  });

  it("starts lark-cli authorization inline and renders a device-code step", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "not_signed_in", capabilities: ["login"] };
    connectorLogin.mockResolvedValue({ step: "device", kind: "device_code", user_code: "ABC123", url: "https://example.test/authorize" });
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    const authorize = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.read_authorize")))!;
    await act(async () => authorize.click());
    expect(connectorLogin).toHaveBeenCalledWith("larkcli", "", {});
    expect(document.body.textContent).toContain("ABC123");
  });

  it("opens the permission URL when lark-cli scopes are missing", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    const larkcli: HubConnector = {
      id: "larkcli",
      name: "Lark via lark-cli",
      status: "no_permission",
      missing_scopes: ["im:chat:read"],
      guide_url: "https://open.feishu.cn/permissions",
    };
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    const permission = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes(t("settings-feishubot.read_open_platform")))!;
    await act(async () => permission.click());
    expect(open).toHaveBeenCalledWith("https://open.feishu.cn/permissions", "_blank", "noopener,noreferrer");
    open.mockRestore();
  });

  it("lists lark-cli profiles and saves the selected choice", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me" };
    connectorList.value = [larkcli];
    await open(saved({
      larkcli: {
        profile: "example",
        app_id: "cli_example",
        same_app: true,
        mode: "chosen",
        choice: "example",
        profiles: [
          { name: "example", app_id: "cli_example", user: "Example User", token_status: "valid" },
          { name: "other", app_id: "cli_other", user: "", token_status: "signed_out" },
        ],
      },
    }));

    // The profile in use is shown under the account; the select opens behind "Change".
    expect(document.body.textContent).toContain(t("settings-feishubot.larkcli_profile_using", { name: "example" }));
    expect(document.querySelector(`select[aria-label="${t("settings-feishubot.larkcli_profile_title")}"]`)).toBeNull();
    const change = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-feishubot.larkcli_profile_change"))!;
    await act(async () => change.click());
    const select = document.querySelector<HTMLSelectElement>(`select[aria-label="${t("settings-feishubot.larkcli_profile_title")}"]`)!;
    expect([...select.options].map((option) => option.textContent)).toEqual([
      t("settings-feishubot.larkcli_profile_auto"),
      "example · Example User · cli_example",
      "other · " + t("settings-feishubot.larkcli_profile_not_logged_in") + " · cli_other",
    ]);
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")!.set!.call(select, "other");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(calls.update[0].body).toEqual({ larkcli_profile: "other" });
    await act(async () => calls.update[0].resolve(saved({ larkcli: { ...saved().larkcli!, mode: "chosen", choice: "other", profiles: [] } })));
    await act(async () => calls.status[1].resolve(saved({ larkcli: { ...saved().larkcli!, mode: "chosen", choice: "other", profiles: [] } })));
  });

  it("shows the profile manager.json fixes, with no way to change it", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "ok", account: "me" };
    connectorList.value = [larkcli];
    await open(saved({
      larkcli: {
        profile: "fixed",
        app_id: "cli_fixed",
        same_app: true,
        mode: "config",
        choice: "",
        profiles: [{ name: "fixed", app_id: "cli_fixed", user: "Example User", token_status: "valid" }],
      },
    }));
    expect(document.body.textContent).toContain(t("settings-feishubot.larkcli_profile_using", { name: t("settings-feishubot.larkcli_profile_fixed", { name: "fixed" }) }));
    expect([...document.querySelectorAll("button")].some((b) => b.textContent === t("settings-feishubot.larkcli_profile_change"))).toBe(false);
    expect(document.querySelector(`select[aria-label="${t("settings-feishubot.larkcli_profile_title")}"]`)).toBeNull();
  });

  it("hides the permission button without a link and re-reads the connector on recheck", async () => {
    const larkcli: HubConnector = { id: "larkcli", name: "Lark via lark-cli", status: "no_permission", missing_scopes: ["im:chat:read"] };
    connectorList.value = [{ ...larkcli, status: "ok" }];
    act(() => root.render(<FeishuBot larkcli={larkcli} />));
    await act(async () => calls.status[0].resolve(saved({ owner: { name: "owner", paired_at: 1 } })));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    const buttons = () => [...document.querySelectorAll<HTMLButtonElement>("button")];
    expect(buttons().some((button) => button.textContent?.includes(t("settings-feishubot.read_open_platform")))).toBe(false);
    const recheck = buttons().find((button) => button.textContent?.includes(t("settings-feishubot.recheck")) && !button.getAttribute("aria-label"))!;
    await act(async () => recheck.click());
    expect(connectorList.calls).toBe(1);
  });

  it("leaves the bot dialog unchanged when lark-cli is absent", async () => {
    act(() => root.render(<FeishuBot larkcli={null} />));
    await act(async () => calls.status[0].resolve(saved()));
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());

    expect(host.textContent).toContain(t("settings-feishubot.footer"));
    expect(host.textContent).not.toContain(t("settings-feishubot.read_section"));
    expect(host.textContent).not.toContain(t("settings-feishubot.larkcli_dialog_footer"));
  });

  it("fills the saved App ID into a dialog opened by URL before the status arrived", async () => {
    const before = location.href;
    history.replaceState(null, "", "?tab=connectors&connector=feishu-bot");
    try {
      act(() => root.render(<FeishuBot larkcli={null} />));
      expect(input("App ID").value).toBe("");
      await act(async () => calls.status[0].resolve(saved()));
      expect(input("App ID").value).toBe("cli_saved");
    } finally {
      history.replaceState(null, "", before);
    }
  });

  it("keeps a typed App ID when a feature switch is flipped", async () => {
    await open(saved());
    type("App ID", "cli_typed");
    // The enable switch comes first, then one switch per feature.
    await act(async () => switches()[2].click());
    expect(calls.update).toHaveLength(1);
    expect(calls.update[0].body).toHaveProperty("features");
    await act(async () => calls.update[0].resolve(saved({ features: { ...saved().features, cards: false } })));
    expect(input("App ID").value).toBe("cli_typed");
  });

  it("sends only the switch flipped, not the whole set this page last saw (#74)", async () => {
    await open(saved());
    // Enable, then main, cards, all cards, pushes: the fifth is "Push to Lark".
    await act(async () => switches()[4].click());
    expect(calls.update).toHaveLength(1);
    expect(calls.update[0].body).toEqual({ features: { pushes: true } });
    // The board was turned on elsewhere meanwhile; the hub's answer carries it, and the next switch leaves it alone.
    await act(async () => calls.update[0].resolve(saved({ features: { ...saved().features, pushes: true, board: true } })));
    await act(async () => switches()[5].click());
    expect(calls.update[1].body).toEqual({ features: { sync: true } });
  });

  it("names both of the card switches when turning questions and approvals off", async () => {
    await open(saved());
    await act(async () => switches()[2].click());
    expect(calls.update[0].body).toEqual({ features: { cards: false, all_cards: false } });
  });

  it("greys out only conclusions while task progress is off: the hub then sends neither progress nor conclusions", async () => {
    const named = (key: string) => document.body.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t(key)}"]`)!;
    const onlyConclusions = () => named("settings-feishubot.feature_only_conclusions_title");
    await open(saved({ features: { ...saved().features, reports: false, only_conclusions: false } }));
    expect(onlyConclusions().disabled).toBe(true);
    await act(async () => named("settings-feishubot.feature_reports_title").click());
    expect(calls.update[0].body).toEqual({ features: { reports: true } });
    await act(async () => calls.update[0].resolve(saved({ features: { ...saved().features, reports: true, only_conclusions: false } })));
    expect(onlyConclusions().disabled).toBe(false);
    await act(async () => onlyConclusions().click());
    expect(calls.update[1].body).toEqual({ features: { only_conclusions: true } });
  });

  it("hides execution steps for a hub from before it", async () => {
    await open(saved());
    expect(document.body.querySelector(`[role="switch"][aria-label="${t("settings-feishubot.feature_report_steps_title")}"]`)).toBeNull();
  });

  it("execution steps: off by default, and only conclusions waits for it", async () => {
    const named = (key: string) => document.body.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t(key)}"]`);
    const steps = () => named("settings-feishubot.feature_report_steps_title");
    const onlyConclusions = () => named("settings-feishubot.feature_only_conclusions_title")!;
    await open(saved({ features: { ...saved().features, report_steps: false, only_conclusions: false } }));
    expect(steps()!.getAttribute("aria-checked")).toBe("false");
    expect(onlyConclusions().disabled).toBe(true);
    await act(async () => steps()!.click());
    expect(calls.update[0].body).toEqual({ features: { report_steps: true } });
    await act(async () => calls.update[0].resolve(saved({ features: { ...saved().features, report_steps: true, only_conclusions: false } })));
    expect(onlyConclusions().disabled).toBe(false);
  });

  it("switches the workbench on its own, off by default", async () => {
    const workbench = () => document.body.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t("settings-feishubot.feature_workbench_title")}"]`)!;
    await open(saved({ features: { ...saved().features, workbench: false } }));
    expect(workbench().getAttribute("aria-checked")).toBe("false");
    await act(async () => workbench().click());
    expect(calls.update[0].body).toEqual({ features: { workbench: true } });
  });

  it("sends every switch to a hub from before 0.1.12, which replaces the whole set", async () => {
    const { reports: _reports, board: _board, ...old } = saved().features;
    await open(saved({ features: old }));
    await act(async () => switches()[4].click());
    expect(calls.update[0].body).toEqual({ features: { ...old, pushes: true } });
  });

  it("keeps a typed App ID when the bot is switched off", async () => {
    await open(saved());
    type("App ID", "cli_typed");
    await act(async () => switches()[0].click());
    expect(calls.update[0].body).toEqual({ enabled: false });
    await act(async () => calls.update[0].resolve(saved({ enabled: false })));
    expect(input("App ID").value).toBe("cli_typed");
  });

  it("keeps a typed App Secret while the pairing poll answers", async () => {
    vi.useFakeTimers();
    await open(saved({ pairing: { code: "123456", expires_at: Math.floor(Date.now() / 1000) + 600 } }));
    type("App Secret", "typed-secret");
    await act(async () => void vi.advanceTimersByTime(3000));
    expect(calls.status).toHaveLength(2);
    await act(async () => calls.status[1].resolve(saved({ pairing: { code: "123456", expires_at: Math.floor(Date.now() / 1000) + 597 } })));
    expect(input("App Secret").value).toBe("typed-secret");
  });

  it("stops looking for the pairing while the tab is hidden and looks once right away on coming back", async () => {
    vi.useFakeTimers();
    await open(saved({ pairing: { code: "123456", expires_at: Math.floor(Date.now() / 1000) + 600 } }));
    await show("hidden");
    await act(async () => void vi.advanceTimersByTime(9000));
    expect(calls.status).toHaveLength(1); // only the row's first read
    await show("visible");
    expect(calls.status).toHaveLength(2);
    await act(async () => calls.status[1].resolve(saved({ owner: { name: "owner-1", paired_at: Math.floor(Date.now() / 1000) } })));
    expect(document.body.textContent).toContain("owner-1");
    await act(async () => void vi.advanceTimersByTime(9000)); // paired: nothing left to look for
    expect(calls.status).toHaveLength(2);
  });

  it("takes the hub's answer after verify and save", async () => {
    await open(saved());
    type("App ID", " cli_new ");
    type("App Secret", "new-secret");
    const save = [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-feishubot.verify_save"));
    await act(async () => save!.click());
    expect(calls.update[0].body).toEqual({ app_id: "cli_new", domain: "feishu", app_secret: "new-secret" });
    await act(async () => calls.update[0].resolve(saved({ app_id: "cli_new" })));
    expect(input("App ID").value).toBe("cli_new");
    expect(input("App Secret").value).toBe("");
  });

  it("sets how long an unanswered card waits before the nudge, an hour when the hub says none", async () => {
    await open(saved());
    const select = () => document.body.querySelector<HTMLSelectElement>(`select[aria-label="${t("settings-feishubot.nudge_title")}"]`)!;
    expect(select().value).toBe("60");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")!.set!.call(select(), "0");
      select().dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(calls.update[0].body).toEqual({ nudge_after: 0 });
    await act(async () => calls.update[0].resolve(saved({ nudge_after: 0 })));
    expect(select().value).toBe("0");
  });

  it("fills the form from a switch's answer when nothing was typed", async () => {
    await open(saved());
    await act(async () => switches()[0].click());
    await act(async () => calls.update[0].resolve(saved({ enabled: false, app_id: "cli_elsewhere" })));
    expect(input("App ID").value).toBe("cli_elsewhere");
  });
});
