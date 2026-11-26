// Lazy UI for held seen rows (#12585, DESIGN_SYSTEM 3.4 "Rows read during a visit"): the floating one-time notice
// and a By status section's Clear seen (N) button. Loaded only once a read row is held, keeping the cold first screen light.
import { t } from "../../i18n";
import { navigate } from "../../lib/router";
import { setSeenRowsNoticed } from "../../lib/seen-rows";
import { NARROW, useMedia } from "../../lib/viewport";
import { Button } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { settingsHref } from "../settings/href";

/** A By status section's Clear seen (N) action: lets the rows read during this visit go. */
export function ClearSeenButton({ count, onClear }: { count: number; onClear: () => void }) {
  const label = t("seen-rows.clear_seen", { count });
  const hint = t("seen-rows.clear_seen_hint");
  return (
    <button
      type="button"
      data-testid="agents-clear-seen"
      onClick={onClear}
      title={hint}
      aria-label={`${label}. ${hint}`}
      className="touch-hit relative z-10 shrink-0 whitespace-nowrap rounded-control px-1.5 py-0.5 text-submeta font-medium text-accent-ink transition-colors hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent [@media(pointer:coarse)]:min-h-11"
    >
      {label}
    </button>
  );
}

export default function SeenRowsNotice({ onNavigate }: { onNavigate?: () => void }) {
  const narrow = useMedia(NARROW);
  return (
    <div
      role="status"
      data-testid="agents-seen-notice"
      className="pointer-events-auto flex items-start gap-2 rounded-xl border border-line bg-surface p-3 shadow-card"
    >
      <span aria-hidden className="mt-0.5 inline-flex shrink-0 text-ink-3">
        <Icon name="info" size={16} />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-label leading-snug text-ink">{t("seen-rows.notice")}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button
            tone="quiet"
            size="sm"
            testId="agents-seen-notice-settings"
            className="touch-hit"
            onClick={() => {
              navigate(settingsHref("general", null, narrow, null, "seen-rows"));
              onNavigate?.();
            }}
          >
            {t("seen-rows.change_in_settings")}
          </Button>
        </div>
      </div>
      <button
        type="button"
        data-testid="agents-seen-notice-dismiss"
        onClick={() => setSeenRowsNoticed(true)}
        aria-label={t("seen-rows.dismiss")}
        title={t("seen-rows.dismiss")}
        className="touch-hit -mt-1 -mr-1 flex size-7 shrink-0 items-center justify-center rounded-full text-ink-2 transition-colors hover:bg-hover hover:text-ink focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"
      >
        <Icon name="x" size={14} />
      </button>
    </div>
  );
}
