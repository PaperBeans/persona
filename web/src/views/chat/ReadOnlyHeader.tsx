// The small header for a default-panel conversation archive. It deliberately has no session menu: reading history and
// searching the transcript stay, while the enabled chat's rename, archive, approval and other write actions do not load.
import type { ReactNode } from "react";
import { IconButton } from "../../ui/atoms";
import { t } from "../../i18n";

export function ReadOnlyHeader({ title, actions, onToggleSidebar, onSearch }: { title: string; actions?: ReactNode; onToggleSidebar?: () => void; onSearch: () => void }) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4" data-chat-readonly-header>
      {onToggleSidebar && <IconButton icon="sidebar" label={t("spaces.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />}
      <h1 className="min-w-0 flex-1 truncate text-sm font-medium text-ink" data-chat-title>{title}</h1>
      {actions}
      <IconButton icon="search" label={t("chat-chatview.search_menu")} onClick={onSearch} />
    </header>
  );
}
