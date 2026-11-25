// The id the water and protein pages (#13176) and the strength training pages (#14713) are drawn under by
// ExtensionUI: the default manager's own, like the subscription pages (lib/subscriptions-ui.ts), and no extension can
// have it (ids never start with "-"). Their page id is `pages`; the state says which one: `{"kind":"water"}` or
// `{"kind":"protein"}` (docs/assistant/health.md), or `{"page":"strength.overview"}` and the other strength pages
// (docs/protocol/protocol.md, strength training).
export const HEALTH_INTAKE_UI = "-health-intake";
export const HEALTH_INTAKE_PAGE = "pages";
export type HealthIntakeKind = "water" | "protein";

/** The hello feature that says the hub draws the strength training pages and its home card. */
export const STRENGTH_FEATURE = "health_strength";
/** The WebSocket message that says strength training changed (no content): its open pages and home card read again. */
export const STRENGTH_EVENT = "health_strength";
/** The strength training pages a client opens: the home card, the overview, an exercise's and a draft's. */
export type StrengthPage = "strength.home" | "strength.overview" | "strength.exercise" | "strength.edit";

/** The first state of a page; the hub's own state replaces it from the first answer on. */
export const healthIntakeState = (kind: HealthIntakeKind): string => JSON.stringify({ kind });

/** The first state of a strength training page (the home card's, or the overview its card opens). */
export const strengthState = (page: StrengthPage): string => JSON.stringify({ page });

/** Where a page with `state` opens in the app: the route the renderer opens a pushed page on
 *  (`/extension/<id>/<ui>?state=`, docs/protocol/deep-links.md), without the sub-path. */
const pathOf = (state: string): string =>
  `/extension/${encodeURIComponent(HEALTH_INTAKE_UI)}/${encodeURIComponent(HEALTH_INTAKE_PAGE)}?state=${encodeURIComponent(state)}`;

export const healthIntakePath = (kind: HealthIntakeKind): string => pathOf(healthIntakeState(kind));

export const strengthPath = (page: StrengthPage): string => pathOf(strengthState(page));
