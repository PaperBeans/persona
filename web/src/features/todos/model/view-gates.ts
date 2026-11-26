// When the web looks at the hub's to-do view (#10226, #14583; docs/web/web.md "Developing Persona"): the shadow
// compares only while Developing Persona is on and the hub lists `todo_view`, while the To-dos status list draws from
// the hub's view by default whenever the hub lists `todo_view` (with `persona.todos.viewDraw=0` as browser rollback).

import { devModeSince } from "../../../lib/dev-probe";
import { hubHas } from "../../../lib/store";

/** The hello feature that says every manager puts `view` on its rows. */
export const TODO_VIEW_FEATURE = "todo_view";
/** This browser's override for drawing the To-dos status sections from the view: "0" reverts to local triage. */
export const VIEW_DRAW_KEY = "persona.todos.viewDraw";

/** Gates 1 and 2: the Developing Persona switch is on, and the hub lists `todo_view`. */
export function viewShadowOn(features: string[]): boolean {
  return Boolean(devModeSince()) && hubHas(features, TODO_VIEW_FEATURE);
}

/** Whether the To-dos status list draws from the hub's `view`: on by default whenever the hub lists `todo_view`,
 *  unless this browser explicitly opts out with `localStorage persona.todos.viewDraw = "0"`. */
export function viewDrawOn(features: string[]): boolean {
  if (!hubHas(features, TODO_VIEW_FEATURE)) return false;
  try {
    return localStorage.getItem(VIEW_DRAW_KEY) !== "0";
  } catch {
    return true;
  }
}
