// Out-of-band declarative UI cards attached to tool calls (#14730, #14998): rendered in a turn between work rows
// and the final assistant answer through view-sections-lazy so first-screen bundle limits stay intact.
import { memo } from "react";
import { t } from "../../i18n";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import type { TurnToolView } from "../../lib/turn-tool-views";
import { INERT, ViewSections } from "../extensions/view-sections-lazy";

export const ToolViews = memo(function ToolViews({ views }: { views: readonly TurnToolView[] }) {
  if (!views.length) return null;
  return (
    <section aria-label={t("chat-toolviews.region_label")} data-tool-views={views.length} className="flex min-w-0 flex-col gap-2">
      {views.map((tv) => (
        <article key={tv.id} data-tool-view={tv.id} className="overflow-hidden rounded-card bg-surface shadow-card">
          {(tv.tool || tv.view.title) && (
            <header className="flex min-w-0 items-center gap-1.5 border-b border-line px-4 py-2">
              {tv.tool && <span className="shrink-0 truncate text-label font-medium text-ink-3">{tv.tool}</span>}
              {tv.tool && tv.view.title && <span aria-hidden className="shrink-0 text-submeta text-ink-3">·</span>}
              {tv.view.title && <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink">{tv.view.title}</span>}
            </header>
          )}
          <ExtensionOwnerScope owner={tv.owner}>
            <ViewSections view={tv.view} compact {...INERT} />
          </ExtensionOwnerScope>
        </article>
      ))}
    </section>
  );
});
