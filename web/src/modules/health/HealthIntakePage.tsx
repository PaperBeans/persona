// The water and protein pages (#13176, docs/assistant/health.md): the default manager's own declarative
// pages (today, the records with Undo, the 7 and 30 day charts, the goal and reminder settings), drawn by the extension
// pages' renderer under the reserved id (lib/health-intake-ui.ts) the way the subscription pages are, so the web has no
// second renderer. A full page at `/extension/-health-intake/pages?state=`: the route the renderer already opens its pushed
// pages on, which Today's rings link to. The page draws again when the hub says a record was added or undone
// (`health_intake`, ExtensionView.tsx), so a press here and a log on the phone both show.
//
// The strength training pages (#14713) are the same reserved id's: the overview the home card opens, an exercise's and
// a draft's, which a chat card's Adjust links to (`https://<hub>/extension/-health-intake/pages?state=…`, opened in the
// app by lib/links.ts). They draw again on `health_strength`. The header says the title the hub's page gives itself, the
// kind's name only until it does. A draft's page answers Save and Cancel by asking to be taken off: the page goes back to
// where it was opened from (the chat), or to the Health home when it was opened on its own.
import { Suspense, useCallback, useMemo, useState } from "react";
import { t } from "../../i18n";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { HEALTH_INTAKE_PAGE, HEALTH_INTAKE_UI, healthIntakeState, type HealthIntakeKind } from "../../lib/health-intake-ui";
import { lazyPage } from "../../lib/lazy-page";
import { navigate } from "../../lib/router";
import { IconButton, Loading } from "../../ui/atoms";

const ExtensionUI = lazyPage(() => import("../../views/settings/ExtensionView").then((m) => m.ExtensionUI), { routes: ["extension"] });

/** Which page a state asks for: the hub's default (water) for none or one it can't read. */
export function kindOf(state: string | undefined): HealthIntakeKind {
  try {
    const k = (JSON.parse(state ?? "{}") as { kind?: unknown }).kind;
    return k === "protein" ? "protein" : "water";
  } catch {
    return "water";
  }
}

/** Whether a state asks for one of the strength training pages (`{"page":"strength.…"}`). */
export function isStrength(state: string | undefined): boolean {
  try {
    const p = (JSON.parse(state ?? "{}") as { page?: unknown }).page;
    return typeof p === "string" && p.startsWith("strength.");
  } catch {
    return false;
  }
}

/** Taken off by the page itself (a draft saved or cancelled): back to where it was opened from, else the Health home. */
function close() {
  if (history.length > 1) history.back();
  else navigate("/health/new");
}

export function HealthIntakePage({ onToggleSidebar }: { onToggleSidebar: () => void }) {
  // the state a ring's link carries (?state=); none opens water, as the hub does
  const state = useMemo(() => new URLSearchParams(location.search).get("state") ?? healthIntakeState("water"), []);
  const strength = isStrength(state);
  const kind = strength ? "strength" : kindOf(state);
  // the hub's page names itself (the renderer's onTitle, each time the first page is drawn); until it has, or when it
  // names none, the page's kind
  const [drawn, setDrawn] = useState("");
  const title = drawn || (kind === "strength" ? t("health.strength_title") : kind === "protein" ? t("health.today_protein") : t("health.today_water"));
  const onTitle = useCallback((x: string | undefined) => setDrawn(x ?? ""), []);
  const [n, setN] = useState(0);
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("health.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <IconButton icon="arrowLeft" label={t("health.title")} onClick={() => navigate("/health/new")} />
        <h1 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{title}</h1>
        <IconButton icon="retry" label={t("health.refresh")} onClick={() => setN((x) => x + 1)} />
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[880px] flex-col px-4 py-6 sm:px-8">
          <Suspense fallback={<Loading />}>
            <ExtensionOwnerScope owner={HEALTH_INTAKE_UI}>
              <ExtensionUI key={`${kind}/${n}`} extension={HEALTH_INTAKE_UI} ui={HEALTH_INTAKE_PAGE} title={title} bare state={state} onTitle={onTitle} onClose={close} />
            </ExtensionOwnerScope>
          </Suspense>
        </div>
      </div>
    </div>
  );
}

/** The page as the module host draws it (core/ModulePage.tsx ModuleExtensionPage). */
export function HealthIntakeModulePage({ onToggleSidebar }: { onToggleSidebar?: () => void }) {
  return <HealthIntakePage onToggleSidebar={onToggleSidebar ?? (() => {})} />;
}
