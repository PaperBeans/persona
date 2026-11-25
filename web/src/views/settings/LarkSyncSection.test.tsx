// @vitest-environment jsdom
// The daily sync section: the list as the hub sends it, nothing at all on a hub without it, the two-step add (a QR code
// asked again until scanned, then the authorization back to /lark-oauth), the manual and code fallbacks, and each
// binding's switch, "Sync now" and the reminder to authorize again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LarkSyncBinding, LarkSyncView } from "../../lib/lark-sync";

const api = vi.hoisted(() => ({
  syncView: vi.fn(),
  updateSyncBinding: vi.fn(),
  runSyncBinding: vi.fn(),
  removeSyncBinding: vi.fn(),
  removeSyncBot: vi.fn(),
  setSyncSchedule: vi.fn(),
  setSyncIMPoll: vi.fn(),
  createSyncBot: vi.fn(),
  addSyncScopes: vi.fn(),
  registerSyncBot: vi.fn(),
  authorizeSyncBot: vi.fn(),
  pollSyncAuthorize: vi.fn(),
  leaveFor: vi.fn(),
}));
vi.mock("../../lib/lark-sync", async (original) => ({
  ...(await original<typeof import("../../lib/lark-sync")>()),
  syncView: (...a: unknown[]) => api.syncView(...a),
  updateSyncBinding: (...a: unknown[]) => api.updateSyncBinding(...a),
  runSyncBinding: (...a: unknown[]) => api.runSyncBinding(...a),
  removeSyncBinding: (...a: unknown[]) => api.removeSyncBinding(...a),
  removeSyncBot: (...a: unknown[]) => api.removeSyncBot(...a),
  setSyncSchedule: (...a: unknown[]) => api.setSyncSchedule(...a),
  setSyncIMPoll: (...a: unknown[]) => api.setSyncIMPoll(...a),
  createSyncBot: (...a: unknown[]) => api.createSyncBot(...a),
  addSyncScopes: (...a: unknown[]) => api.addSyncScopes(...a),
  registerSyncBot: (...a: unknown[]) => api.registerSyncBot(...a),
  authorizeSyncBot: (...a: unknown[]) => api.authorizeSyncBot(...a),
  pollSyncAuthorize: (...a: unknown[]) => api.pollSyncAuthorize(...a),
  leaveFor: (...a: unknown[]) => api.leaveFor(...a),
}));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: () => {} }));
import { t } from "../../i18n";
import { readReturn } from "../../lib/lark-sync";
import { LarkSyncSection } from "./LarkSyncSection";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HOUR = 3600_000;
const binding = (over: Partial<LarkSyncBinding> = {}): LarkSyncBinding => ({
  key: "primary~a1",
  bot_id: "primary",
  account_id: "feishu:t1:ou_1",
  enabled: true,
  health: "ok",
  last_run_at: new Date(Date.now() - 3 * HOUR).toISOString(),
  next_run_at: new Date(Date.now() + 20 * HOUR).toISOString(),
  last_messages: 12,
  last_docs: 4,
  ...over,
});
const view = (over: Partial<LarkSyncView> = {}): LarkSyncView => ({
  available: true,
  schedule_hour: 3,
  accounts: [{ id: "feishu:t1:ou_1", label: "Work account" }, { id: "lark:t2:ou_2", label: "Side account" }],
  bots: [
    { bot_id: "s1", label: "Other company", role: "sync_only", tier: "full_with_im", approval: "awaiting_approval", has_secret: true },
    { bot_id: "primary", label: "", role: "chat_and_sync", tier: "docs_standard", primary: true, has_secret: true, domain: "https://open.feishu.cn" },
  ],
  bindings: [binding(), binding({ key: "s1~a2", bot_id: "s1", account_id: "lark:t2:ou_2", health: "needs_reauth", last_run_at: undefined })],
  ...over,
});

let host: HTMLDivElement;
let root: Root;
const text = () => host.textContent ?? "";
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === label || b.getAttribute("aria-label") === label);
const click = (label: string) => act(async () => button(label)!.click());
const rowOf = (key: string) => host.querySelector<HTMLElement>(`[data-lark-sync-binding="${key}"]`)!;

async function draw(v: LarkSyncView | null = view()) {
  api.syncView.mockResolvedValue(v);
  await act(async () => root.render(<LarkSyncSection appID="cli_x" />));
}

beforeEach(() => {
  Object.values(api).forEach((f) => f.mockReset());
  sessionStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe("Lark daily sync section", () => {
  it("explains missing permissions and offers reauthorization even far from expiry", async () => {
    await draw(view({ bindings: [binding({ health: "scope_missing", reauth_due_days: 300, tier: "full_with_im" })] }));
    const row = rowOf("primary~a1");
    expect(row.textContent).toContain(t("settings-larksync.health_scope_missing"));
    expect(row.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled).toBe(false);
    expect(row.textContent).not.toContain(t("settings-larksync.run_now"));
    const authorize = [...row.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === t("settings-larksync.authorize_again"));
    expect(authorize).toBeDefined();
    api.authorizeSyncBot.mockResolvedValue({ mode: "browser", url: "https://accounts.example.com/authorize" });
    await act(async () => authorize!.click());
    expect(api.authorizeSyncBot).toHaveBeenCalledWith("primary", expect.stringMatching(/\/lark-oauth$/), "full_with_im");
  });

  it("lists the chat bot first, then each bot's accounts with their state", async () => {
    await draw();
    expect(text()).toContain(t("settings-larksync.title"));
    const all = text();
    expect(all.indexOf(t("settings-larksync.chat_bot"))).toBeLessThan(all.indexOf("Other company"));
    expect(all).toContain(t("settings-larksync.approval_awaiting"));

    const ok = rowOf("primary~a1").textContent ?? "";
    expect(ok).toContain("Work account");
    expect(ok).toContain(t("settings-larksync.health_ok"));
    expect(ok).toContain(t("settings-larksync.count_messages", { count: 12 }));
    expect(ok).toContain(t("settings-larksync.count_docs", { count: 4 }));
    expect(ok).toContain(t("settings-larksync.run_now"));

    // refused: authorize again instead of syncing, and its switch waits for that
    const refused = rowOf("s1~a2");
    expect(refused.textContent).toContain(t("settings-larksync.health_needs_reauth"));
    expect(refused.textContent).toContain(t("settings-larksync.never_synced"));
    expect(refused.textContent).not.toContain(t("settings-larksync.run_now"));
    expect(refused.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled).toBe(true);
  });

  it("draws nothing on a hub without the sync", async () => {
    await draw(null);
    expect(api.syncView).toHaveBeenCalledTimes(1);
    expect(host.innerHTML).toBe("");
  });

  it("draws nothing when the hub cannot be reached, and says a failed read with a retry", async () => {
    api.syncView.mockRejectedValue(new TypeError("Failed to fetch"));
    await act(async () => root.render(<LarkSyncSection />));
    expect(host.innerHTML).toBe("");

    act(() => root.unmount());
    root = createRoot(host);
    api.syncView.mockReset();
    api.syncView.mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(view());
    await act(async () => root.render(<LarkSyncSection />));
    expect(text()).toContain(t("settings-larksync.load_failed", { error: "boom" }));
    await click(t("settings-larksync.retry"));
    expect(text()).toContain("Work account");
  });

  it("turns a binding off and on through the hub", async () => {
    await draw();
    api.updateSyncBinding.mockResolvedValue(view({ bindings: [binding({ enabled: false }), view().bindings[1]] }));
    await act(async () => rowOf("primary~a1").querySelector<HTMLButtonElement>('[role="switch"]')!.click());
    expect(api.updateSyncBinding).toHaveBeenCalledWith("primary~a1", { enabled: false });
    expect(rowOf("primary~a1").querySelector('[role="switch"]')!.getAttribute("aria-checked")).toBe("false");
    expect(rowOf("primary~a1").textContent).toContain(t("settings-larksync.health_paused"));
  });

  it("starts a sync now and shows it running", async () => {
    await draw();
    api.runSyncBinding.mockResolvedValue(view({ bindings: [binding({ running: true }), view().bindings[1]] }));
    await act(async () => rowOf("primary~a1").querySelector<HTMLButtonElement>("button:not([role])")!.click());
    expect(api.runSyncBinding).toHaveBeenCalledWith("primary~a1");
    expect(rowOf("primary~a1").textContent).toContain(t("settings-larksync.health_running"));
  });

  it("says when the authorization ends soon", async () => {
    await draw(view({ bindings: [binding({ reauth_due_days: 7 })] }));
    const row = rowOf("primary~a1").textContent ?? "";
    expect(row).toContain(t("settings-larksync.reauth_soon", { count: 7 }));
    expect(row).toContain(t("settings-larksync.authorize_again"));
  });

  it("adds a bot in two steps: the QR code until scanned, then the authorization back to /lark-oauth", async () => {
    vi.useFakeTimers();
    await draw();
    await click(t("settings-larksync.add"));
    api.createSyncBot.mockResolvedValue({ bot_id: "s9", prompt: { step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } } });
    await click(t("settings-larksync.create_bot"));
    expect(api.createSyncBot).toHaveBeenCalledWith({ label: "", tier: "full_with_im", region: "feishu" });
    expect(host.querySelector(`svg[aria-label="${t("settings-larksync.qr_label")}"]`)).not.toBeNull();
    expect(text()).toContain(t("settings-larksync.step_create"));

    // not scanned yet: asked again after the interval, still the QR code
    api.registerSyncBot.mockResolvedValueOnce({ step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } });
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(api.registerSyncBot).toHaveBeenLastCalledWith("s9", "register");
    expect(api.authorizeSyncBot).not.toHaveBeenCalled();

    // scanned: step 2 asks the hub how to authorize, with the return address
    api.registerSyncBot.mockResolvedValueOnce({ done: true });
    api.authorizeSyncBot.mockResolvedValue({ mode: "browser", url: "https://accounts.example.com/authorize?state=s" });
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(api.authorizeSyncBot).toHaveBeenCalledTimes(1);
    const [bot, redirect, tier] = api.authorizeSyncBot.mock.calls[0];
    expect(bot).toBe("s9");
    expect(redirect).toMatch(/^http:\/\/[^/]+\/lark-oauth$/);
    expect(tier).toBe("full_with_im");
    expect(text()).toContain(t("settings-larksync.step_authorize"));

    await click(t("settings-larksync.authorize_continue"));
    expect(api.leaveFor).toHaveBeenCalledWith("https://accounts.example.com/authorize?state=s");
    expect(readReturn().back).toBe(location.pathname + location.search);
  });

  it("defaults to full_with_im and warns that docs_standard cannot read chats", async () => {
    await draw();
    await click(t("settings-larksync.add"));
    const select = host.querySelector<HTMLSelectElement>(`select[aria-label="${t("settings-larksync.tier_label")}"]`)!;
    expect(select.value).toBe("full_with_im");
    expect(text()).toContain(t("settings-larksync.tier_full_hint"));
    expect(text()).not.toContain(t("settings-larksync.tier_docs_no_chat"));

    await act(async () => {
      select.value = "docs_standard";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(text()).toContain(t("settings-larksync.tier_docs_hint"));
    expect(text()).toContain(t("settings-larksync.tier_docs_no_chat"));

    api.createSyncBot.mockResolvedValue({ bot_id: "s10", prompt: { step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } } });
    await click(t("settings-larksync.create_bot"));
    expect(api.createSyncBot).toHaveBeenCalledWith({ label: "", tier: "docs_standard", region: "feishu" });
  });

  it("gives the chat bot the permission instead of creating a bot", async () => {
    await draw();
    await click(t("settings-larksync.add"));
    api.addSyncScopes.mockResolvedValue({ done: true });
    api.authorizeSyncBot.mockResolvedValue({ mode: "browser", url: "https://accounts.example.com/a" });
    await click(t("settings-larksync.use_chat_bot"));
    expect(api.addSyncScopes).toHaveBeenCalledWith("primary", "full_with_im");
    expect(api.authorizeSyncBot).toHaveBeenCalledWith("primary", expect.stringMatching(/\/lark-oauth$/), "full_with_im");
  });

  it("shows the return address to add by hand when the app could not be set up for it", async () => {
    await draw();
    api.authorizeSyncBot.mockResolvedValue({
      mode: "manual",
      url: "https://accounts.example.com/authorize?state=m",
      redirect_uri: "https://hub.example.com/lark-oauth",
      console_url: "https://open.example.com/app/cli_x/safe",
    });
    await click(t("settings-larksync.connect_account"));
    expect(host.querySelector("[data-lark-sync-redirect]")!.textContent).toBe("https://hub.example.com/lark-oauth");
    expect(button(t("settings-larksync.copy_address"))).toBeDefined();
    expect(host.querySelector<HTMLAnchorElement>('a[href="https://open.example.com/app/cli_x/safe"]')?.textContent).toBe(t("settings-larksync.open_app_settings"));
    await click(t("settings-larksync.manual_continue"));
    expect(api.leaveFor).toHaveBeenCalledWith("https://accounts.example.com/authorize?state=m");
  });

  it("authorizes again with a code on screen, asked again until confirmed", async () => {
    vi.useFakeTimers();
    await draw();
    // the hub's device step: a QR code, the code it asks for and the link to open on this device
    const qr = { step: "authorize", kind: "qr", url: "https://accounts.example.com/device?c=1", user_code: "WXYZ-1234", interval: 2, qr: { size: 2, rows: ["10", "01"] }, link: "https://accounts.example.com/device?c=1", link_text: "Open" };
    api.authorizeSyncBot.mockResolvedValue({ mode: "device", prompt: qr });
    await act(async () => rowOf("s1~a2").querySelector<HTMLButtonElement>("button:not([role])")!.click());
    expect(api.authorizeSyncBot).toHaveBeenCalledWith("s1", expect.stringMatching(/\/lark-oauth$/), "full_with_im");
    expect(text()).toContain("WXYZ-1234");
    expect(host.querySelector<HTMLAnchorElement>('a[href="https://accounts.example.com/device?c=1"]')?.textContent).toBe("Open");
    expect(text()).toContain(t("settings-larksync.authorize_title"));

    // still waiting: the poll's answer has no QR code of its own, the one on screen stays
    api.pollSyncAuthorize.mockResolvedValueOnce({ step: "authorize", interval: 2 });
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(api.pollSyncAuthorize).toHaveBeenCalledTimes(1);
    expect(host.querySelector(`svg[aria-label="${t("settings-larksync.qr_label")}"]`)).not.toBeNull();

    api.pollSyncAuthorize.mockResolvedValueOnce({ done: true });
    api.syncView.mockResolvedValue(view({ bindings: [binding(), binding({ key: "s1~a2", bot_id: "s1" })] }));
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(api.pollSyncAuthorize).toHaveBeenCalledTimes(2);
    expect(api.pollSyncAuthorize).toHaveBeenCalledWith("s1");
    expect(text()).not.toContain("WXYZ-1234");
    expect(rowOf("s1~a2").textContent).toContain(t("settings-larksync.health_ok"));
  });

  it("stops asking when the code on screen failed, and starts the authorization again", async () => {
    vi.useFakeTimers();
    await draw();
    api.authorizeSyncBot.mockResolvedValue({ mode: "device", prompt: { step: "authorize", kind: "qr", user_code: "AB-12", interval: 2, qr: { size: 2, rows: ["10", "01"] } } });
    await act(async () => rowOf("s1~a2").querySelector<HTMLButtonElement>("button:not([role])")!.click());
    api.pollSyncAuthorize.mockResolvedValueOnce({ step: "authorize", error: "The code expired." });
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(text()).toContain("The code expired.");
    await act(async () => vi.advanceTimersByTimeAsync(10_000));
    expect(api.pollSyncAuthorize).toHaveBeenCalledTimes(1);
    await click(t("settings-larksync.start_again"));
    expect(api.authorizeSyncBot).toHaveBeenCalledTimes(2);
  });

  it("drops a new bot whose registration failed when the owner starts again", async () => {
    vi.useFakeTimers();
    await draw();
    await click(t("settings-larksync.add"));
    api.createSyncBot.mockResolvedValue({ bot_id: "s9", prompt: { step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } } });
    await click(t("settings-larksync.create_bot"));
    api.registerSyncBot.mockResolvedValue({ step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] }, error: "The QR code expired." });
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(text()).toContain("The QR code expired.");
    await act(async () => vi.advanceTimersByTimeAsync(20_000));
    expect(api.registerSyncBot).toHaveBeenCalledTimes(1);
    api.removeSyncBot.mockResolvedValue(view());
    await click(t("settings-larksync.start_again"));
    expect(api.removeSyncBot).toHaveBeenCalledWith("s9");
    expect(text()).toContain(t("settings-larksync.add_title"));
  });

  it("goes on with a sync bot's setup left half way", async () => {
    await draw(view({ bots: [...view().bots, { bot_id: "s5", label: "Half", role: "sync_only", tier: "docs_standard", has_secret: false, registering: true }] }));
    expect(text()).toContain(t("settings-larksync.bot_setup_open"));
    api.registerSyncBot.mockResolvedValue({ step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } });
    await click(t("settings-larksync.continue_setup"));
    expect(api.registerSyncBot).toHaveBeenCalledWith("s5", "register");
    expect(text()).toContain(t("settings-larksync.step_create"));
  });

  it("marks docs_standard bindings as no-chat and upgrades them via add-scopes QR then re-authorization", async () => {
    vi.useFakeTimers();
    await draw();

    const docsRow = rowOf("primary~a1");
    const fullRow = rowOf("s1~a2");
    expect(docsRow.textContent).toContain(t("settings-larksync.no_chat_badge"));
    expect(docsRow.textContent).toContain(t("settings-larksync.upgrade_to_full"));
    expect(fullRow.textContent).not.toContain(t("settings-larksync.no_chat_badge"));
    expect(fullRow.textContent).not.toContain(t("settings-larksync.upgrade_to_full"));

    api.addSyncScopes.mockResolvedValue({ step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } });
    const upgradeBtn = [...docsRow.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-larksync.upgrade_to_full"))!;
    await act(async () => upgradeBtn.click());
    expect(api.addSyncScopes).toHaveBeenCalledWith("primary", "full_with_im");
    expect(text()).toContain(t("settings-larksync.step_scopes"));
    expect(host.querySelector(`svg[aria-label="${t("settings-larksync.qr_label")}"]`)).not.toBeNull();

    // Scanned: moves straight to re-authorization with full_with_im
    api.registerSyncBot.mockResolvedValueOnce({ done: true });
    api.authorizeSyncBot.mockResolvedValue({ mode: "browser", url: "https://accounts.example.com/authorize?state=u" });
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(api.registerSyncBot).toHaveBeenCalledWith("primary", "register");
    expect(api.authorizeSyncBot).toHaveBeenCalledWith("primary", expect.stringMatching(/\/lark-oauth$/), "full_with_im");
    expect(text()).toContain(t("settings-larksync.authorize_title"));
  });

  it("lets a docs_standard sync-only bot upgrade with bot-scoped QR title (with or without bindings)", async () => {
    await draw(view({
      bots: [{ bot_id: "s2", label: "Docs only", role: "sync_only", tier: "docs_standard", has_secret: true }],
      bindings: [],
    }));
    expect(text()).toContain(t("settings-larksync.no_chat_badge"));
    api.addSyncScopes.mockResolvedValue({ step: "register", kind: "qr", interval: 5, qr: { size: 2, rows: ["10", "01"] } });
    await click(t("settings-larksync.upgrade_to_full"));
    expect(api.addSyncScopes).toHaveBeenCalledWith("s2", "full_with_im");
    expect(text()).toContain(t("settings-larksync.step_bot_scopes"));
  });

  it("shows and toggles the 15-minute chat sync switch when the hub supports it, and hides it on older hubs", async () => {
    await draw();
    expect(host.querySelector(`[aria-label="${t("settings-larksync.im_poll_label")}"]`)).toBeNull();

    act(() => root.unmount());
    root = createRoot(host);
    await draw(view({ im_poll: true }));
    const sw = host.querySelector<HTMLButtonElement>(`button[role="switch"][aria-label="${t("settings-larksync.im_poll_label")}"]`)!;
    expect(sw).not.toBeNull();
    expect(sw.getAttribute("aria-checked")).toBe("true");

    api.setSyncIMPoll.mockResolvedValue(view({ im_poll: false }));
    await act(async () => sw.click());
    expect(api.setSyncIMPoll).toHaveBeenCalledWith(false);
    expect(host.querySelector<HTMLButtonElement>(`button[role="switch"][aria-label="${t("settings-larksync.im_poll_label")}"]`)!.getAttribute("aria-checked")).toBe("false");
  });
});
