// What Health tracks (docs/assistant/health.md "Health items", docs/protocol/protocol.md "Health items", #14716): the
// hub's ordered list of health items, the feature that says it keeps one, and how the web saves it without losing the
// items only phones show.
//
// Requests name no manager, as intake's do (intake.ts): the list is the default manager's whichever assistant the
// Persona page shows. `space=health` keeps lib/manager-choice.ts from adding the shown manager's header.
import { api, ApiError } from "../../lib/api";
import { INTAKE_KINDS, type IntakeKind } from "./intake";

/** The hello feature that says the hub keeps the list: without it the Health home is what it was before. */
export const ITEMS_FEATURE = "health_items";
/** The WebSocket message that says the list changed (`{revision}`): read it again. */
export const ITEMS_EVENT = "health_items";

/** The hub's list: its catalog (ids and where their data comes from), the added ids in order, the revision a save must
 *  name, and whether the owner has ever saved one (`saved: false`: the hub's defaults, nobody chose them yet). */
export interface HealthItemsState {
  catalog: { id: string; source: string }[];
  items: string[];
  revision: number;
  saved: boolean;
}

/** An item the web can draw: water and protein (Today's rings), and strength training (the hub's home card, #14713). */
export type WebItem = IntakeKind | "strength";

/** The items the web draws: water and protein. Activity rings and trends are the phone's. */
export const WEB_ITEMS: readonly WebItem[] = INTAKE_KINDS;
/** With strength training (the hello's `health_strength`): its home card too. */
export const WEB_ITEMS_STRENGTH: readonly WebItem[] = [...INTAKE_KINDS, "strength"];
/** The items the web draws on a hub with strength training or without it. */
export const webItems = (strength: boolean): readonly WebItem[] => (strength ? WEB_ITEMS_STRENGTH : WEB_ITEMS);
const isWebItem = (web: readonly WebItem[]) => (id: string): id is WebItem => (web as readonly string[]).includes(id);

const PATH = "/assistant/health/items?space=health";

function normalize(r: Partial<HealthItemsState>): HealthItemsState {
  return {
    catalog: Array.isArray(r.catalog) ? r.catalog : [],
    items: Array.isArray(r.items) ? r.items.filter((x): x is string => typeof x === "string") : [],
    revision: typeof r.revision === "number" ? r.revision : 0,
    saved: r.saved === true,
  };
}

export function readItems(): Promise<HealthItemsState> {
  return api<Partial<HealthItemsState>>(PATH).then(normalize);
}

/** The save lost to one made elsewhere: the hub's list as it is now (its 409 answer). */
export class StaleItemsError extends Error {
  constructor(readonly current: HealthItemsState) {
    super("health items changed elsewhere");
  }
}

/** Saves the whole list at the revision it was read at. A stale revision throws StaleItemsError with the hub's list. */
export async function writeItems(items: string[], revision: number): Promise<HealthItemsState> {
  try {
    return normalize(await api<Partial<HealthItemsState>>(PATH, { method: "PUT", json: { items, revision } }));
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && Array.isArray(e.body.items)) throw new StaleItemsError(normalize(e.body as Partial<HealthItemsState>));
    throw e;
  }
}

/** The web items (`web`: the ones this hub lets it draw) the owner added, in their order (only ones the hub's catalog
 *  has). */
export function shownItems(state: HealthItemsState, web: readonly WebItem[] = WEB_ITEMS): WebItem[] {
  const known = new Set(state.catalog.map((c) => c.id));
  const draws = isWebItem(web);
  return state.items.filter((id): id is WebItem => draws(id) && known.has(id));
}

/** The web items the editor offers: the catalog's, in the catalog's order. */
export function offeredItems(state: HealthItemsState, web: readonly WebItem[] = WEB_ITEMS): WebItem[] {
  return state.catalog.map((c) => c.id).filter(isWebItem(web));
}

/** The whole list to save when the web's items (`web`) become `shown`, in that order. Everything else the list holds
 *  (Activity rings, trends, strength training on a hub that does not let the web draw it, ids this page does not know)
 *  stays where it was: the slots the web's items took are filled with `shown` in order, a slot left over is dropped, and
 *  an item added beyond them goes right after the last of them (first of all when there were none, as the hub's own
 *  defaults put water and protein first). */
export function mergeItems(stored: string[], shown: readonly string[], web: readonly WebItem[] = WEB_ITEMS): string[] {
  const draws = isWebItem(web);
  const next = [...shown];
  const out: string[] = [];
  let last = -1;
  for (const id of stored) {
    if (!draws(id)) {
      out.push(id);
      continue;
    }
    const fill = next.shift();
    if (fill !== undefined) {
      out.push(fill);
      last = out.length - 1;
    }
  }
  out.splice(last + 1, 0, ...next);
  return out;
}
