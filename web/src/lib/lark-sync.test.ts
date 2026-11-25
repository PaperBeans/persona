// @vitest-environment jsdom
// The daily sync's pure parts: what counts as a sync view (absent ⇒ the section hides), the state words, the order of
// the list, the return address and the trip to the open platform and back.
import { afterEach, describe, expect, it } from "vitest";
import { t } from "../i18n";
import { ApiError } from "./api";
import {
  asSyncView, bindingTitle, forgetReturn, healthOf, isAbsent, LARK_SETTINGS_PATH, oauthRedirectURI, oauthReturn,
  needsAuthorize, orderedBots, readReturn, reauthSoon, rememberReturn, tierText, TIER_DOCS, TIER_FULL, type LarkSyncBinding, type LarkSyncView,
} from "./lark-sync";
import { parse, routePath } from "./router";

const binding = (over: Partial<LarkSyncBinding> = {}): LarkSyncBinding => ({ key: "k1", bot_id: "primary", account_id: "feishu:t1:ou_1", enabled: true, health: "ok", ...over });
const view = (over: Partial<LarkSyncView> = {}): LarkSyncView => ({
  available: true,
  schedule_hour: 3,
  accounts: [{ id: "feishu:t1:ou_1", label: "Work" }],
  bots: [
    { bot_id: "b2", label: "Second", role: "sync_only" },
    { bot_id: "primary", label: "", role: "chat_and_sync", primary: true, has_secret: true },
  ],
  bindings: [binding()],
  ...over,
});

afterEach(() => forgetReturn());

describe("sync view", () => {
  it("takes only a hub's sync view; anything else is an older hub", () => {
    expect(asSyncView(view())?.bots).toHaveLength(2);
    expect(asSyncView(null)).toBeNull();
    // another route's answer (the bot's own status) is not a view
    expect(asSyncView({ enabled: true, app_id: "cli_x", features: {} })).toBeNull();
    // a hub without the Lark module says so
    expect(asSyncView({ ...view(), available: false })).toBeNull();
    // a missing or odd hour falls back to the hub's default
    expect(asSyncView({ ...view(), schedule_hour: 99 })?.schedule_hour).toBe(3);
    expect(asSyncView({ available: true, bots: [], bindings: [] })?.accounts).toEqual([]);
    // im_poll is preserved when boolean and omitted when absent (older hub)
    expect(asSyncView({ ...view(), im_poll: true })?.im_poll).toBe(true);
    expect(asSyncView({ ...view(), im_poll: false })?.im_poll).toBe(false);
    expect(asSyncView(view())?.im_poll).toBeUndefined();
  });

  it("treats a missing route as absent and every other failure as one", () => {
    expect(isAbsent(new ApiError(404, "not found", {}))).toBe(true);
    expect(isAbsent(new ApiError(405, "method", {}))).toBe(true);
    expect(isAbsent(new ApiError(500, "boom", {}))).toBe(false);
    expect(isAbsent(new TypeError("offline"))).toBe(false);
  });

  it("lists the chat bot first and names accounts by their label", () => {
    const v = view();
    expect(orderedBots(v).map((b) => b.bot_id)).toEqual(["primary", "b2"]);
    expect(bindingTitle(v, v.bindings[0])).toBe("Work");
    expect(bindingTitle(v, binding({ label: "Mine" }))).toBe("Mine");
    expect(bindingTitle({ ...v, accounts: [] }, v.bindings[0])).toBe(t("settings-larksync.account_fallback", { n: 1 }));
  });

  it("says each state in plain words, a running sync first", () => {
    expect(healthOf(binding())).toEqual({ text: t("settings-larksync.health_ok"), tone: "green" });
    expect(healthOf(binding({ enabled: false })).text).toBe(t("settings-larksync.health_paused"));
    expect(healthOf(binding({ health: "needs_reauth" })).tone).toBe("red");
    expect(healthOf(binding({ health: "rate_limited" })).tone).toBe("orange");
    expect(healthOf(binding({ health: "scope_missing" })).text).toBe(t("settings-larksync.health_scope_missing"));
    expect(needsAuthorize(binding({ health: "scope_missing" }))).toBe(true);
    expect(reauthSoon(binding({ health: "scope_missing", reauth_due_days: 30 }))).toBeNull();
    expect(healthOf(binding({ health: "failing", running: true }))).toEqual({ text: t("settings-larksync.health_running"), tone: "accent" });
    expect(healthOf(binding({ health: "something_new" })).text).toBe(t("settings-larksync.health_unknown"));
    expect(tierText(TIER_FULL)).toBe(t("settings-larksync.tier_full"));
    expect(tierText(TIER_DOCS)).toBe(t("settings-larksync.tier_docs"));
  });

  it("suggests authorizing again only within the last 14 days", () => {
    expect(reauthSoon(binding({ reauth_due_days: 30 }))).toBeNull();
    expect(reauthSoon(binding({ reauth_due_days: 14 }))).toBe(14);
    expect(reauthSoon(binding({ reauth_due_days: 0.5 }))).toBe(0);
    expect(reauthSoon(binding())).toBeNull();
    // already refused: the row says "authorize again" by its state instead
    expect(reauthSoon(binding({ reauth_due_days: 3, health: "needs_reauth" }))).toBeNull();
  });
});

describe("authorization trip", () => {
  it("comes back to /lark-oauth under the hub's sub-path", () => {
    expect(oauthRedirectURI("https://hub.example.com", "")).toBe("https://hub.example.com/lark-oauth");
    expect(oauthRedirectURI("https://hub.example.com", "/persona")).toBe("https://hub.example.com/persona/lark-oauth");
  });

  it("is a page of the app", () => {
    expect(parse("/lark-oauth", "?code=c&state=s")).toEqual({ name: "lark-oauth" });
    expect(routePath({ name: "lark-oauth" })).toBe("/lark-oauth");
    expect(parse("/lark-oauth/x").name).toBe("not-found");
  });

  it("reads the platform's answer", () => {
    expect(oauthReturn("?code=c1&state=s1")).toEqual({ code: "c1", state: "s1", error: "" });
    expect(oauthReturn("?error=access_denied&state=s1").error).toBe("access_denied");
  });

  it("goes back where the owner started, never to another origin", () => {
    expect(readReturn()).toEqual({ back: LARK_SETTINGS_PATH });
    rememberReturn("/settings?tab=integrations&integration=builtin:feishu-bot", "assistant-2");
    expect(readReturn()).toEqual({ back: "/settings?tab=integrations&integration=builtin:feishu-bot", manager: "assistant-2" });
    // reading leaves it for a page that draws twice
    expect(readReturn().manager).toBe("assistant-2");
    forgetReturn();
    expect(readReturn()).toEqual({ back: LARK_SETTINGS_PATH });
    rememberReturn("//evil.example.com/x", "");
    expect(readReturn()).toEqual({ back: LARK_SETTINGS_PATH });
  });
});
