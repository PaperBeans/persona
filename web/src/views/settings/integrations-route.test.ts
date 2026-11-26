// Unit tests for Settings › Integrations route codec, including `-sources` and `-sources/<encodedState>` (#15423, #15424).
import { describe, expect, it } from "vitest";
import {
  formatIntegrationsSub,
  integrationsToLegacy,
  isIntegrationDetailRoute,
  isLegacyIntegrationTab,
  legacyToIntegrations,
  parseIntegrationsSub,
  parseSettingsPath,
  type IntegrationsRoute,
} from "./integrations-route";

describe("integrations-route sources and data-flow codec", () => {
  it("treats sources alongside channels/connectors/extensions as a legacy integration tab", () => {
    for (const tab of ["channels", "connectors", "extensions", "sources"]) {
      expect(isLegacyIntegrationTab(tab)).toBe(true);
    }
    expect(isLegacyIntegrationTab("integrations")).toBe(false);
    expect(isLegacyIntegrationTab("general")).toBe(false);
  });

  it("parses and formats -sources and -sources/<encodedState> with optional @manager", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_push_1" });
    const cases: Array<{ route: IntegrationsRoute; sub: string }> = [
      { route: { kind: "sources" }, sub: "-sources" },
      { route: { kind: "sources", manager: "work" }, sub: "-sources@work" },
      {
        route: { kind: "sources", state: sampleState },
        sub: `-sources/${encodeURIComponent(sampleState)}`,
      },
      {
        route: { kind: "sources", state: sampleState, manager: "work" },
        sub: `-sources@work/${encodeURIComponent(sampleState)}`,
      },
    ];

    for (const { route, sub } of cases) {
      expect(formatIntegrationsSub(route)).toBe(sub);
      expect(parseIntegrationsSub(sub)).toEqual(route);
    }

    // Plain JSON without escape sequences remains compatible with older links.
    expect(parseIntegrationsSub(`-sources/${sampleState}`)).toEqual({ kind: "sources", state: sampleState });
    expect(parseIntegrationsSub(`-sources@work/${sampleState}`)).toEqual({
      kind: "sources",
      state: sampleState,
      manager: "work",
    });
  });

  it.each(["connectors", "channels", "extensions", "integrations"])("keeps the manager on %s paths", (tab) => {
    expect(parseSettingsPath(`/settings/${tab}@work`)).toEqual({ tab, manager: "work", sub: null });
  });

  it.each(["mail", "lark", "feishu-bot"])("preserves the owner of built-in %s across rollback", (id) => {
    const legacy = integrationsToLegacy(`builtin:${id}@work`);
    expect(legacy).toEqual({ tab: "connectors", sub: `${id}@work` });
    expect(legacyToIntegrations(legacy.tab, legacy.sub)).toEqual({ sub: `builtin:${id}@work` });
  });

  it("uses the tab owner for detail and UI routes, unless the item names its own", () => {
    expect(parseSettingsPath("/settings/channels@work/chat")?.sub).toBe("chat@work");
    expect(parseSettingsPath("/settings/connectors@work/git")?.sub).toBe("git@work");
    expect(parseSettingsPath("/settings/extensions@work/tools/ui/status")?.sub).toBe("tools@work/ui/status");
    expect(parseSettingsPath("/settings/integrations@work/ext:tools@home")?.sub).toBe("ext:tools@home");
  });

  it("accepts the long extension prefix for objects and pages", () => {
    for (const suffix of ["", "@work", "@work/ui/status"]) {
      expect(parseIntegrationsSub(`extension:tools${suffix}`)).toEqual(parseIntegrationsSub(`ext:tools${suffix}`));
    }
  });

  it("consumes exactly one layer of escaping for path-style Data flow state", () => {
    const state = JSON.stringify({ id: "a%20b/%2F@x" });
    for (const route of ["integrations/-sources@work", "integrations@work/-sources"]) {
      const sub = parseSettingsPath(`/settings/${route}/${encodeURIComponent(state)}`)?.sub;
      expect(parseIntegrationsSub(sub)).toEqual({ kind: "sources", manager: "work", state });
    }
  });

  it("maps legacy sources routes into -sources with preserved @manager and encoded state", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(legacyToIntegrations("sources", null)).toEqual({ sub: "-sources" });
    expect(legacyToIntegrations("sources", null, "work")).toEqual({ sub: "-sources@work" });
    expect(legacyToIntegrations("sources", sampleState)).toEqual({ sub: `-sources/${encoded}` });
    expect(legacyToIntegrations("sources", sampleState, "work")).toEqual({ sub: `-sources@work/${encoded}` });
    expect(legacyToIntegrations("sources", `@work/${sampleState}`)).toEqual({ sub: `-sources@work/${encoded}` });
  });

  it("keeps global Data Flow out of split-pane detail mode while allowing single-source details in split-pane", () => {
    expect(isIntegrationDetailRoute({ kind: "sources" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", manager: "work" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}' })).toBe(true);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}', manager: "work" })).toBe(true);
  });

  it("maps -sources back to legacy Settings › Inbox Sources while preserving manager and state", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(integrationsToLegacy("-sources")).toEqual({ tab: "sources", sub: null });
    expect(integrationsToLegacy("-sources@work")).toEqual({ tab: "sources", sub: null, manager: "work" });
    expect(integrationsToLegacy(`-sources/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
    });
    expect(integrationsToLegacy(`-sources@work/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
      manager: "work",
    });
  });

  it("parses path-based /settings/sources URLs including state and @manager", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(parseSettingsPath("/settings/sources")).toEqual({ tab: "sources", sub: null });
    expect(parseSettingsPath("/settings/sources/")).toEqual({ tab: "sources", sub: null });
    expect(parseSettingsPath(`/settings/sources/${encoded}`)).toEqual({ tab: "sources", sub: sampleState });
    expect(parseSettingsPath("/settings/sources@work")).toEqual({ tab: "sources", sub: null, manager: "work" });
    expect(parseSettingsPath(`/settings/sources@work/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
      manager: "work",
    });
  });
});
