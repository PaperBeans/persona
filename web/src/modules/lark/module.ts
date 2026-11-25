// The lark module's descriptor (#13404, web half; part of #13434 and #13430; docs/web/web.md "Modules",
// docs/extensions/lark-migration.md "Entry point"): the Messages page (every Feishu chat in one list, `/messages`), its
// sidebar entry right after Inbox, and the "Show messages on home" row in Settings › Connectors › Lark. Its code and its
// strings (lark-*.json) load when the page or that row first draws; the page's own code when the page opens.
//
// Its hub flag is `module.lark` (the hub flag rule, core/module-host.ts): off only on a hub that publishes module flags
// without it, so an older hub keeps it on as before. Settings › Labs › Features turns it off in a browser. Off: no
// entry, no row, `/messages` is an unknown address, and neither its chunk nor its strings are requested. The Inbox's
// Feishu chat view (views/inbox/LarkThread.tsx) is core and stays.
//
// The entry shows, as before, only while this browser turned the switch on and the hub reads Feishu (feature `lark`).
import { byLanguage, t, type CatalogModule } from "../../i18n";
import { moduleFeature } from "../../core/module-flags";
import type { ModuleDescriptor } from "../../core/module-host";
import { larkOn, subscribe } from "../../lib/store";
import { getLarkHome, larkHomeShown } from "./home";

export const lark: ModuleDescriptor = {
  id: "lark",
  feature: moduleFeature("lark"),
  title: () => t("sidebar.module_lark"),
  description: () => t("sidebar.module_lark_hint"),
  routes: [{ base: "messages", title: () => t("app.title_messages") }],
  navItems: [{
    key: "messages", path: "/messages", icon: "chat", label: () => t("sidebar.nav_messages"), color: "green", after: "inbox",
    shown: {
      get: () => getLarkHome() && larkOn(),
      subscribe: (l) => {
        const offHome = larkHomeShown.subscribe(l);
        const offStore = subscribe(l);
        return () => {
          offHome();
          offStore();
        };
      },
    },
  }],
  settingsRows: ["connectors.lark"],
  i18nNamespaces: ["lark-chats", "lark-homeswitch", "lark-messages"],
  locales: byLanguage(import.meta.glob<CatalogModule>("../../i18n/locales/*/_meta.json", { query: "?catalog=module-lark" })),
  load: () => import("./lark-code").then((m) => m.code),
};
