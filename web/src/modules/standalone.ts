// The modules with a page of their own (core/module-host.ts `standalonePages`, docs/web/web.md "Modules"):
// part of the module list (index.ts takes them in), kept in a file of its own because main.tsx imports it before App.
//
// main.tsx may reach only what this file reaches, and App reaches it too, so what it reaches is split out of App's
// chunks. Were it the whole list, every module's code would be reachable without App, and the build could keep nothing
// App shares with the modules inside App's chunks (the first screen grew by about 30 KB when it tried); were it the
// host, App would import the host's every name from another chunk. So it reaches the descriptors, this browser's
// switch and the strings loader, and draws the page itself; the host's checks of the descriptor and its code run where
// App registers the list. The hub's flag (the hub flag rule, core/module-host.ts) is read from one hello on a socket of
// its own before anything of the module is requested (hubServesHere).
import type { ReactElement } from "react";
import type { ModuleDescriptor } from "../core/module-host";
import { moduleSwitchedOff } from "../core/module-switch";
import { rememberedHubServes, rememberHubModules } from "../core/module-hub-storage";
import { MODULES_FEATURE } from "../core/module-flags";
import { addCatalogs } from "../i18n";
import { getToken } from "../lib/api";
import { BASE, stripBase } from "../lib/base";
import { screenShare } from "./screen-share/module";

export const STANDALONE_MODULES: readonly ModuleDescriptor[] = [screenShare];

/** How long a page of its own waits for the hub's hello before it takes the module as served (no hello yet: on). */
export const HELLO_WAIT_MS = 2500;

/** The hello's features as the hub sends them on a socket of this page's own, closed once it is in; null when none
 *  came in `ms` (no token, the hub unreachable or slow). */
export function helloFeatures(token: string, ms = HELLO_WAIT_MS): Promise<string[] | null> {
  return new Promise((done) => {
    if (!token || typeof WebSocket === "undefined") return done(null);
    let sock: WebSocket;
    const finish = (v: string[] | null) => {
      clearTimeout(timer);
      try {
        sock.close();
      } catch {
        // already closed
      }
      done(v);
    };
    const timer = setTimeout(() => finish(null), ms);
    try {
      sock = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}${BASE}/api/v1/ws?token=${encodeURIComponent(token)}`);
    } catch {
      return finish(null);
    }
    sock.onmessage = (e) => {
      try {
        const m = JSON.parse(String(e.data)) as { type?: string; features?: unknown };
        if (m.type === "hello") finish(Array.isArray(m.features) ? m.features.filter((f): f is string => typeof f === "string") : []);
      } catch {
        // not JSON: wait for the hello
      }
    };
    sock.onerror = () => finish(null);
    sock.onclose = () => finish(null);
  });
}

/** Whether the hub serves module `d` (the hub flag rule): a hello with the `modules` marker and without the module's
 *  flag turns it off; an older hub leaves it on. With no hello in time, the remembered per-hub answer applies, so a
 *  known-off module stays off while a first-seen hub keeps the default on. */
export async function hubServesHere(d: ModuleDescriptor, token: string, hello = helloFeatures): Promise<boolean> {
  if (!d.feature) return true;
  const features = await hello(token);
  if (!features) return rememberedHubServes(d.feature);
  // The standalone page runs before App's host, so its live hello updates the same remembered answer too.
  rememberHubModules(features);
  return !features.includes(MODULES_FEATURE) || features.includes(d.feature);
}

/**
 * The page main.tsx draws for this address in place of the app, when a module that is on has it as a page of its own
 * (its strings, then its code, loaded; the address' token taken); null for the app. While that module is off (switched
 * off here, or the hub's hello lacks its flag: hubServesHere) the address is an unknown one: its fragment (a token the app meant for the page) goes first, so App never takes it for a sign-in,
 * and neither the module's chunk nor its strings are requested.
 */
export async function standalonePage(address: string, list: readonly ModuleDescriptor[] = STANDALONE_MODULES, hello = helloFeatures): Promise<ReactElement | null> {
  const u = new URL(address);
  const base = stripBase(u.pathname).split("/").filter(Boolean)[0];
  const d = base ? list.find((m) => m.standalonePages?.includes(base)) : undefined;
  if (!d) return null;
  // the token the app put in the fragment for the page, else this browser's own
  const token = new URLSearchParams(u.hash.replace(/^#/, "")).get("token")?.trim() || getToken();
  if (moduleSwitchedOff(d.id) || !(await hubServesHere(d, token, hello))) {
    if (u.hash) window.history.replaceState(window.history.state, "", u.pathname + u.search);
    return null;
  }
  if (d.locales) await addCatalogs(d.locales);
  const code = await d.load();
  if (!code.Standalone) throw new Error(`module "${d.id}" names the page of its own /${base}, which its code does not draw`);
  return code.Standalone(address);
}
