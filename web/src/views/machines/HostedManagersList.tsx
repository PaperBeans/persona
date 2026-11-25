// Co-located Manager assistants hosted on a computer (MachineDetail & AlertDeploy, #14964).
// Shared across lazy machine views so both show the same status, deploy mode, version, hints and Settings link.
import { t } from "../../i18n";
import { deployText, hostedManagerAttention, type Manager } from "../../lib/managers";
import { href, navigate } from "../../lib/router";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const ASSISTANTS_PATH = "/settings?tab=managers";

function openAssistantsSettings(e: React.MouseEvent) {
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
  e.preventDefault();
  navigate(ASSISTANTS_PATH);
}

export function HostedManagersList({
  managers,
  nodeVersion,
  compact,
  className,
  testId = "hosted-managers-detail",
}: {
  managers: readonly Manager[];
  nodeVersion?: string;
  compact?: boolean;
  className?: string;
  testId?: string;
}) {
  if (!managers.length) return null;
  const settingsUrl = `${href({ name: "settings" })}?tab=managers`;
  return (
    <div className={className} data-testid={testId}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-label font-medium text-ink">
          <Icon name="spark" size={12} className="shrink-0 text-ai" />
          <span>{t("machines-machinedetail.hosted_assistants", { count: managers.length })}</span>
        </div>
        <a
          href={settingsUrl}
          onClick={openAssistantsSettings}
          data-testid="hosted-managers-settings-link"
          className="inline-flex items-center gap-0.5 whitespace-nowrap text-submeta font-medium text-accent-ink hover:underline"
        >
          <span>{t("machines-machinedetail.hosted_assistants_settings")}</span>
          <Icon name="chevronRight" size={11} stroke={2.2} className="shrink-0" />
        </a>
      </div>
      <ul className={cx("mt-2 flex flex-col", compact ? "gap-1.5" : "gap-2")}>
        {managers.map((hm) => {
          const attention = hostedManagerAttention(hm, nodeVersion);
          const deploy = deployText(hm);
          return (
            <li
              key={hm.id}
              data-hosted-manager={hm.id}
              data-manager-state={attention ?? (hm.online ? "online" : "offline")}
              className={cx(
                "rounded-control px-2.5 py-2",
                compact ? "bg-surface shadow-hairline" : "bg-inset shadow-hairline",
              )}
            >
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="truncate text-meta font-medium text-ink">{hm.name}</span>
                <span
                  className={cx(
                    "inline-flex h-5 shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-1.5 text-submeta font-medium",
                    hm.online ? "bg-green-tint text-green" : "bg-field text-ink-3",
                  )}
                >
                  <span aria-hidden className={cx("size-1.5 rounded-full", hm.online ? "bg-green" : "bg-ink-3")} />
                  {hm.online ? t("settings-managers.online") : t("settings-managers.offline")}
                </span>
                {deploy && (
                  <span className="inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full bg-field px-1.5 text-submeta font-medium text-ink-2">
                    {deploy}
                  </span>
                )}
                {attention === "pending_restart" && (
                  <span className="inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full bg-orange-tint px-1.5 text-submeta font-medium text-orange">
                    {t("settings-managers.pending_restart")}
                  </span>
                )}
                {attention === "version_lag" && (
                  <span className="inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full bg-orange-tint px-1.5 text-submeta font-medium text-orange">
                    {t("machines-machinedetail.manager_older_version")}
                  </span>
                )}
                {!compact && hm.version && (
                  <span className="ml-auto truncate font-mono text-submeta text-ink-3" title={hm.version}>
                    {hm.version}
                  </span>
                )}
              </div>
              {compact && hm.version && (
                <p className="mt-1 truncate font-mono text-submeta text-ink-3" title={hm.version}>
                  {hm.version}
                </p>
              )}
              {attention === "pending_restart" && (
                <p className="mt-1 text-submeta leading-snug text-orange" data-testid="manager-pending-restart-hint">
                  {t("machines-machinedetail.manager_pending_restart_hint")}
                </p>
              )}
              {attention === "version_lag" && (
                <p className="mt-1 text-submeta leading-snug text-orange" data-testid="manager-version-lag-hint">
                  {t("machines-machinedetail.manager_version_lag_hint")}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
