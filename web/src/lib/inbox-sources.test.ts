import { describe, expect, it } from "vitest";
import type { HubConnector } from "./connectors";
import { inboxSources } from "./inbox-sources";

const c = (over: Partial<HubConnector>): HubConnector => ({ id: "telegram", name: "Telegram", status: "not_signed_in", capabilities: ["login", "inbox"], ...over });

describe("inboxSources", () => {
  it("is not connected with no mail, no Feishu and no signed-in connector, and offers mail, Feishu and the sign-ins", () => {
    const r = inboxSources({ mail: 0, lark: false, connectors: [c({}), c({ id: "github", name: "GitHub", status: "not_configured", capabilities: ["login", "inbox"] })] });
    expect(r.connected).toBe(false);
    expect(r.offers.map((o) => o.id)).toEqual(["mail", "lark", "telegram", "github"]);
    expect(r.offers[2]!.href).toBe("/settings?tab=connectors&connector=telegram");
  });

  it("links a sign-in to the copy of the assistant that listed it, when that is not the default's", () => {
    const r = inboxSources({ mail: 0, connectors: [c({ manager: "m-1" })] });
    expect(decodeURIComponent(r.offers[1]!.href)).toBe("/settings?tab=connectors&connector=telegram@m-1");
  });

  it("is connected by a mail account, Feishu set up, or a signed-in connector that brings items in", () => {
    expect(inboxSources({ mail: 1, connectors: [] }).connected).toBe(true);
    expect(inboxSources({ mail: 0, lark: true, connectors: [] }).connected).toBe(true);
    expect(inboxSources({ mail: 0, connectors: [c({ status: "ok" })] }).connected).toBe(true);
  });

  it("does not count a connector that is off, keeps out of the inbox, or only talks (a bot)", () => {
    expect(inboxSources({ mail: 0, connectors: [c({ status: "ok", enabled: false })] }).connected).toBe(false);
    expect(inboxSources({ mail: 0, connectors: [c({ status: "ok", inbox: false })] }).connected).toBe(false);
    const bot = c({ id: "wechat", name: "微信", status: "ok", capabilities: ["login"] });
    expect(inboxSources({ mail: 0, connectors: [bot] }).connected).toBe(false);
    // and a bot is not offered as a source either
    expect(inboxSources({ mail: 0, connectors: [{ ...bot, status: "not_signed_in" }] }).offers.map((o) => o.id)).toEqual(["mail"]);
  });

  it("offers Feishu only on a hub that has it, and at most three connectors", () => {
    expect(inboxSources({ mail: 0, connectors: [] }).offers.map((o) => o.id)).toEqual(["mail"]);
    const many = ["a", "b", "c", "d"].map((id) => c({ id, name: id }));
    expect(inboxSources({ mail: 0, connectors: many }).offers.map((o) => o.id)).toEqual(["mail", "a", "b", "c"]);
  });
});
