// Settings categories contributed by enabled feature modules (core/module-host.ts `settingsTabs`): loaded with Settings
// rather than on the first screen.
import type { State } from "../lib/store";
import { enabledModules, type ModuleSettingsTab } from "./module-host";

/** The enabled modules' Settings categories. */
export function enabledSettingsTabs(s?: Pick<State, "features" | "loaded">): (ModuleSettingsTab & { module: string })[] {
  return enabledModules(s).flatMap((d) => (d.settingsTabs ?? []).map((tab) => ({ ...tab, module: d.id })));
}
