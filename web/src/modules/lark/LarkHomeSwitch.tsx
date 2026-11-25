// Settings › Connectors › Lark › Show messages on home (iOS FeishuConnectorView's switch): off by default, kept by this browser.
import { t } from "../../i18n";
import { setLarkHome, useLarkHome } from "./home";
import { Switch } from "../../views/settings/SettingsParts";

export function LarkHomeSwitch() {
  const on = useLarkHome();
  return (
    <div className="flex items-center gap-3 px-4 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="text-[13px] text-ink">{t("lark-homeswitch.label")}</div>
        <div className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{t("lark-homeswitch.hint")}</div>
      </div>
      <Switch on={on} onChange={setLarkHome} label={t("lark-homeswitch.label")} />
    </div>
  );
}
