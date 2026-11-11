// A settings sub-page of another assistant's thing than the current one's (opened by an address `…=id@manager`): names
// whose it is beside the page's title, as the group title names the current assistant (docs/assistant/managers.md "The
// current assistant"). Nothing for the current assistant's own.
import { t } from "../../i18n";
import { managerOf, useRoster } from "../../lib/managers";
import { sameManager, useShownManager } from "../../lib/manager-choice";
import { AssistantAvatar } from "../../ui/AssistantAvatar";

export function OwnerMark({ manager }: { manager: string | undefined }) {
  const roster = useRoster();
  const shown = useShownManager();
  if (!manager || sameManager(manager, shown)) return null;
  const m = managerOf(roster, manager);
  const name = m?.name ?? manager;
  return (
    <span data-owner-mark={manager} className="flex min-w-0 items-center gap-1.5 text-[12px] text-ink-2">
      <AssistantAvatar id={manager} name={name} isDefault={!!m && m.id === roster?.default} size={18} ring="ring-page" />
      <span className="truncate">{t("settings-nav.group_assistant", { name })}</span>
    </span>
  );
}
