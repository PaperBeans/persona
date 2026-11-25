// The lark module's code (module.ts `load`): the "Show messages on home" row, and the Messages page as a page of its
// own chunk, so Settings › Connectors draws the row without loading the page and its chat view.
import type { ModuleCode } from "../../core/module-host";
import { lazyPage } from "../../lib/lazy-page";
import { LarkHomeSwitch } from "./LarkHomeSwitch";

export const code: ModuleCode = {
  Page: lazyPage(() => import("./Messages").then((m) => m.MessagesPage), { listed: false }),
  settingsRows: { "connectors.lark": LarkHomeSwitch },
};
