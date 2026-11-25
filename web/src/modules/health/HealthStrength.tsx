// Strength training on the Health home (#14713, #14716; docs/protocol/protocol.md, strength training): the hub's own
// home card (`strength.home`: the latest session, a chart of the last seven days' working sets and recent records),
// drawn by the extension pages' renderer under the reserved id, as the water and protein pages are, so the web has no
// second drawing of it. Its heading and its View open the overview
// (`/extension/-health-intake/pages?state={"page":"strength.overview"}`), where the exercises' and the drafts' pages are
// pushed. The renderer draws it again on the hub's `health_strength` message (ExtensionView.tsx).
import { Suspense } from "react";
import { t } from "../../i18n";
import { BASE } from "../../lib/base";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { HEALTH_INTAKE_PAGE, HEALTH_INTAKE_UI, strengthPath, strengthState } from "../../lib/health-intake-ui";
import { lazyPage } from "../../lib/lazy-page";
import { navigate } from "../../lib/router";
import { Loading } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const ExtensionUI = lazyPage(() => import("../../views/settings/ExtensionView").then((m) => m.ExtensionUI), { routes: ["extension"] });

const HOME = strengthState("strength.home");

/** The hub's strength training card, as wide as the home's column; its heading opens the overview. */
export function StrengthHomeCard() {
  const to = strengthPath("strength.overview");
  return (
    <div className="flex min-w-0 flex-col rounded-card bg-surface pb-1.5 shadow-card" data-testid="health-today-strength">
      <a
        href={BASE + to}
        onClick={(e) => {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
          e.preventDefault();
          navigate(to);
        }}
        data-testid="health-today-strength-open"
        className="group flex min-w-0 items-center gap-1 px-4 pt-3 pb-0.5 outline-none focus-visible:underline"
      >
        <span className="truncate text-meta font-medium text-ink">{t("health.strength_title")}</span>
        <Icon name="chevronRight" size={12} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-150 group-hover:translate-x-0.5" />
      </a>
      <Suspense fallback={<div className="px-4 py-3"><Loading /></div>}>
        <ExtensionOwnerScope owner={HEALTH_INTAKE_UI}>
          <ExtensionUI extension={HEALTH_INTAKE_UI} ui={HEALTH_INTAKE_PAGE} state={HOME} compact remember />
        </ExtensionOwnerScope>
      </Suspense>
    </div>
  );
}
