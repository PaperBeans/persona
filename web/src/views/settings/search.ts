import { t } from "../../i18n";
import { TAB_INFO, TABS, type Tab } from "./nav";

// ── search ──────────────────────────────────────────────

/** A group (or a models/agents section) inside a page that the search box can jump to. */
export interface SearchGroup {
  tab: Tab;
  group: string;
  /** The `data-settings-group` marker of a Group, or the element id of a models/agents section. */
  anchor: string;
  hint?: string;
}

/** The group headings the search box matches (a curated static index; titles are the pages' own headings). */
export const SEARCH_INDEX: SearchGroup[] = [
  // General
  { tab: "general", group: t("settings.appearance"), anchor: "appearance" },
  { tab: "general", group: t("settings.sidebar_pages"), anchor: "sidebar-pages" },
  // Persona
  { tab: "general", group: t("settings.agent_preferences"), anchor: "agent-preferences" },
  { tab: "general", group: t("settings-agentopen.label"), hint: `${t("settings-agentopen.panel")} / ${t("settings-agentopen.page")}`, anchor: "agent-preferences" },
  { tab: "general", group: t("settings.this_browser"), anchor: "this-browser" },
  // the assistant's profile
  { tab: "persona", group: t("settings-triagesettings.title"), anchor: "triage" },
  // the hub's
  { tab: "avatar", group: t("settings.avatar"), anchor: "avatar" },
  { tab: "hub", group: t("workspace-board.setting_group"), anchor: "agent-group" },
  { tab: "persona", group: t("settings-standing.title"), anchor: "standing-authorizations" },
  // Models' sections and Agents' sections (their element ids)
  { tab: "models", group: t("settings-nav.tab_providers"), anchor: "settings-providers" },
  { tab: "models", group: t("settings-nav.tab_model_config"), anchor: "settings-model-config" },
  { tab: "agents", group: "Claude Code", anchor: "settings-claude" },
  { tab: "agents", group: "Codex", anchor: "settings-codex" },
  { tab: "agents", group: "Antigravity", anchor: "settings-agy" },
  { tab: "agents", group: "omp", anchor: "settings-omp" },
  { tab: "knowledge", group: t("settings-knowledge.topics_title"), anchor: "knowledge-topics" },
  { tab: "knowledge", group: t("settings-knowledge.items"), anchor: "knowledge-items" },
  { tab: "knowledge", group: t("settings-knowledge.messages"), anchor: "knowledge-messages" },
  { tab: "knowledge", group: t("settings-knowledge.docs"), anchor: "knowledge-docs" },
  // Notifications
  { tab: "notifications", group: t("settings-notifications.system_title"), anchor: "system" },
  { tab: "notifications", group: t("settings-notifications.done_title"), anchor: "done" },
  // Connectors
  { tab: "connectors", group: t("settings-connectors.mail"), anchor: "mail" },
  { tab: "connectors", group: t("settings-connectors.lark"), anchor: "lark" },
  { tab: "connectors", group: t("settings-sent.entry_label"), hint: t("settings-sent.hint"), anchor: "sent" },
  // Extensions
  { tab: "extensions", group: t("settings-extensions.title"), anchor: "extensions" },
  { tab: "extensions", group: t("settings-extensions.market_title"), anchor: "market" },
  // Shortcuts / hub
  { tab: "shortcuts", group: t("settings-shortcutsettings.terminal_title"), anchor: "shortcuts-terminal" },
  { tab: "hub", group: t("settings-hubsettings.version"), anchor: "hub-version" },
  { tab: "hub", group: t("settings-hubsettings.update"), anchor: "hub-update" },
];

/** The search box's matches: categories by label / hint, and page groups by their heading. */
export function searchSettings(query: string): { tabs: Tab[]; groups: SearchGroup[] } {
  const q = query.trim().toLocaleLowerCase();
  if (!q) return { tabs: [], groups: [] };
  return {
    tabs: TABS.filter((t) => (TAB_INFO[t].label + " " + TAB_INFO[t].hint).toLocaleLowerCase().includes(q)),
    groups: SEARCH_INDEX.filter((g) => (g.group + " " + (g.hint ?? "")).toLocaleLowerCase().includes(q)),
  };
}
