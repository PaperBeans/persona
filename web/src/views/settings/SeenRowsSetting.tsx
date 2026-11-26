// Settings › General › Agent preferences › "Seen Agents" (#12585, lib/seen-rows.ts): whether an Agent read from Unread
// or Just finished stays in place marked as Seen until refresh, or moves to Seen once its chat closes. Kept by this browser.
import { t } from "../../i18n";
import { setSeenRowsMode, useSeenRowsMode, type SeenRowsMode } from "../../lib/seen-rows";
import { Segmented } from "../assistant/parts";
import { Row } from "./SettingsParts";

export function SeenRowsRow() {
  const mode = useSeenRowsMode();
  const options: { v: SeenRowsMode; label: string }[] = [
    { v: "stay", label: t("seen-rows.stay") },
    { v: "leave", label: t("seen-rows.leave") },
  ];
  return (
    <Row label={t("seen-rows.label")} hint={t("seen-rows.hint")} wrapHint stackOnNarrow anchor="seen-rows">
      <Segmented<SeenRowsMode> label={t("seen-rows.label")} value={mode} options={options} onChange={setSeenRowsMode} compact />
    </Row>
  );
}
