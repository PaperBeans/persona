// The screen-share module's descriptor (#14404, part of #13434 and #13430; docs/web/web.md "Modules",
// docs/extensions/computer-use.md "Live view"): the live screen of a computer, /screen/<machine id>, a page of its own
// drawn without the app shell (`standalonePages`). Its code and its strings (screen-share-*.json) load only on that
// address.
//
// Its hub flag is `module.screen-share` (the hub flag rule, core/module-host.ts): off only on a hub that publishes
// module flags without it, so an older hub keeps it on as before. The machine page's "View screen" button follows it.
// The page itself reads the hub's flag from one hello before it loads (src/modules/standalone.ts); off, here or on the
// hub, /screen/<machine> is an unknown address and its chunk is never requested.
import { byLanguage, t, type CatalogModule } from "../../i18n";
import { moduleFeature } from "../../core/module-flags";
import type { ModuleDescriptor } from "../../core/module-host";

export const screenShare: ModuleDescriptor = {
  id: "screen-share",
  feature: moduleFeature("screen-share"),
  title: () => t("sidebar.module_screen_share"),
  description: () => t("sidebar.module_screen_share_hint"),
  standalonePages: ["screen"],
  i18nNamespaces: ["screen-share-displays", "screen-share-screen", "screen-share-screenview", "screen-share-video"],
  locales: byLanguage(import.meta.glob<CatalogModule>("../../i18n/locales/*/_meta.json", { query: "?catalog=module-screen-share" })),
  load: () => import("./standalone").then((m) => ({ Standalone: m.screenStandalone })),
};
