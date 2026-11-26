// The existing-data Health entry in All chats. It belongs to the default panel, loads only with that page, and appears
// only while the Health module is off and the hub actually lists stored Health conversations.
import { useEffect, useState } from "react";
import { useModuleStates } from "../../core/module-host";
import { api } from "../../lib/api";
import { HEALTH_TRACK, healthReadOnly } from "../../lib/health-archive";
import { href, onLinkClick, type Route } from "../../lib/router";
import { onHubEvent } from "../../lib/store";
import type { Session } from "../../lib/types";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

export function HealthArchiveLink() {
  useModuleStates();
  const off = healthReadOnly(HEALTH_TRACK);
  const [hasHistory, setHasHistory] = useState(false);
  useEffect(() => {
    if (!off) return;
    let live = true;
    const load = () => {
      api<{ conversations?: Session[] }>("/assistant?space=health")
        .then((r) => { if (live) setHasHistory(Boolean(r.conversations?.length)); })
        .catch(() => {});
    };
    load();
    const offHello = onHubEvent("hello", load);
    return () => { live = false; offHello(); };
  }, [off]);
  if (!off || !hasHistory) return null;
  const route: Route = { name: "track", slug: HEALTH_TRACK, fresh: true };
  return (
    <section className="mb-5" data-testid="health-archive-entry">
      <a href={href(route)} onClick={(e) => onLinkClick(e, route)} title={t("spaces.health_read_only")} className="flex items-center gap-3 rounded-card bg-surface px-4 py-3 shadow-card transition-colors hover:bg-hover">
        <Icon name="heart" size={15} className="shrink-0 text-accent-ink" />
        <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink">Health</span>
        <span className="shrink-0 text-label text-ink-3">{t("chat-chatview.view_only")}</span>
      </a>
    </section>
  );
}
