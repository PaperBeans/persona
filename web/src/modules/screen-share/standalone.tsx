// The live screen as a page of its own (#14404; docs/extensions/computer-use.md "Page: `/screen/<machine id>`"): what
// main.tsx draws for /screen/<machine>, with no sign-in flow and no sidebar, through the module host's `standalonePages`.
// It runs before App loads, so the token the app put in the fragment is taken out of the address bar here, before App
// could take it for a sign-in (screen.ts). The build preloads this file and lets it load without waiting for the rest
// of the catalog (vite.config.ts): what it imports statically reads only the first screen's strings and the module's.
import { createElement, type ReactElement } from "react";
import { screenTarget } from "./screen";
import { ScreenView } from "./ScreenView";

export function screenStandalone(address: string): ReactElement | null {
  const { target, rest } = screenTarget(address);
  if (!target) return null;
  window.history.replaceState(window.history.state, "", rest); // the app's token leaves the address bar
  return createElement(ScreenView, { target });
}
