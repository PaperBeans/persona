// The module host (#13434, part of #13430 "Persona Core and modules"; docs/web/web.md "Modules"). Persona Core is the
// app without its optional features; each optional feature is a module under src/modules/<id>/ that reaches the shell
// only through what it registers here. This file knows no module: src/modules/index.ts (the module list, the only place
// module ids appear) registers each module's static descriptor, and the shell reads the registry back (its routes, its
// sidebar entries, its titles), never a module's code.
//
// A module is present only while it is enabled: its hub feature flag is in the hello's features (when it names one) and
// this browser has not switched it off (Settings › Labs › Features, core/module-switch.ts). A module that is not enabled
// is absent: no sidebar entry, its addresses resolve as unknown ones, and neither its code chunk nor its strings are
// requested. Its code and its strings load on first use (loadModule), never at start.
//
// Field names follow the module contract of #13430 section 4 (`id`, `api`, `needs`, `wants`, `feature`, `shell_places`,
// `settings`, `chat_card_kinds`, `composer_actions`, `chat_accessories`, `i18n_namespaces`), in this codebase's camelCase:
//   id → id · api → api · needs → needs · wants → wants · feature → feature
//   shell_places → routes + extensionPages + navItems
//   settings → settingsTabs · chat_card_kinds → cardRenderers · composer_actions + chat_accessories → chatSlots
//   i18n_namespaces → i18nNamespaces (+ locales)
// Served today: id, api (checked against MODULE_API), needs (the ESLint boundary), feature, routes, navItems,
// extensionPages (reserved `/extension/<id>/…` pages, also shell_places), i18nNamespaces / locales. Declared but not yet served: wants, settingsTabs, chatSlots (#13431 wires them),
// cardRenderers. registerModule refuses a descriptor that fills one of those, so nothing is silently dropped.
// The other contract fields (hub_routes, mcp_tools, prompt_sections, background_jobs, …) belong to the server.
import type { ComponentType } from "react";
import { useSyncExternalStore } from "react";
import { addCatalogs, type CatalogLoaders } from "../i18n";
import type { IconName } from "../ui/icons";
import type { UIColor } from "../lib/extension-ui-v6";
import type { Route } from "../lib/router";
import type { State } from "../lib/store";
import { moduleSwitchedOff, subscribeModuleSwitches } from "./module-switch";

/** The typed chat slots a module may fill (#13430 `composer_actions`, `chat_accessories`). Declared here so modules
 *  and the shell agree on the names; the shell serves none of them yet (#13431). */
export type ChatSlot = "composer.trailingAction" | "chat.tail" | "chat.bar" | "interaction.asked";
export const CHAT_SLOTS: readonly ChatSlot[] = ["composer.trailingAction", "chat.tail", "chat.bar", "interaction.asked"];

/** A page of the module (#13430 `shell_places`): every address under `/<base>` and under each alias opens it. */
export interface ModuleRoute {
  /** The address' first segment (`health` for `/health/…`). Must not be one of the core's. */
  base: string;
  /** Older addresses that open the same page, as their leading segments (`["tracks", "health"]` for `/tracks/health/…`). */
  aliases?: readonly (readonly string[])[];
  /** The window title while the page is open. Its key must be in the first screen's catalog: it is read before the
   *  module's own strings are in. */
  title: () => string;
}

/** A sidebar entry of the module (#13430 `shell_places`). Its label is read before the module's code or strings load,
 *  so its keys live in the shell's own namespaces (sidebar.json), not the module's. */
export interface ModuleNavItem {
  /** The entry's key (Settings › General › Sidebar pages keeps the order and the hidden ones by it). */
  key: string;
  /** The address it opens, in the app (`/health`), and the route base it lights up for. */
  path: string;
  icon: IconName;
  label: () => string;
  short?: () => string;
  /** Its icon's color with Colored sidebar icons (lib/sidebar-icons.ts). */
  color?: UIColor;
  /** The core page it comes right after, in the default order. */
  after: string;
}

/** Declared, not yet served: a Settings tab of the module (#13430 `settings`). */
export interface ModuleSettingsTab {
  key: string;
  label: () => string;
}

/** What a module's page receives: the address' segments after its base, and the shell's sidebar toggle. */
export interface ModulePageProps {
  path: readonly string[];
  onToggleSidebar?: () => void;
}

/** What a module's page drawn under a reserved extension-page id receives (`/extension/<id>/<ui>`). */
export interface ModuleExtensionPageProps {
  extension: string;
  ui: string;
  onToggleSidebar?: () => void;
}

/** A module's code, loaded on first use. */
export interface ModuleCode {
  /** The page its routes open. */
  Page?: ComponentType<ModulePageProps>;
  /** The page its `extensionPages` ids open. */
  ExtensionPage?: ComponentType<ModuleExtensionPageProps>;
}

/** A module's static descriptor: small, imported at start by the module list, naming what it contributes. */
export interface ModuleDescriptor {
  /** Lower-case, hyphenated (#13430 section 2): the directory under src/modules. */
  id: string;
  /** The hub feature flag it needs in hello `features`; none for a module the hub has always served. */
  feature?: string;
  /** The module API it is written against (#13430 `api`); MODULE_API when absent. Another version is refused. */
  api?: number;
  /** Other modules it may import (ESLint holds it to this list: eslint.modules.config.mjs). */
  needs?: readonly string[];
  /** Declared, not yet served: modules it uses when they are there (#13430 `wants`). */
  wants?: readonly string[];
  /** Its name and one line on what it does, for the switch in Settings › Labs › Features (shell namespaces). */
  title: () => string;
  description: () => string;
  routes?: readonly ModuleRoute[];
  /** Reserved extension-page ids it draws (`/extension/<id>/<ui>`, #13430 `shell_places`): a page the hub serves as a
   *  declarative extension page of its own, under an id no extension can have (ids starting with "-"). */
  extensionPages?: readonly string[];
  navItems?: readonly ModuleNavItem[];
  /** Declared, not yet served. */
  settingsTabs?: readonly ModuleSettingsTab[];
  /** Declared, not yet served (#13431). */
  chatSlots?: readonly ChatSlot[];
  /** Declared, not yet served: the chat card kinds it renders. */
  cardRenderers?: readonly string[];
  /** Its namespaces under src/i18n/locales (`<id>` or `<id>-…`): kept out of the client catalog and loaded with its code. */
  i18nNamespaces?: readonly string[];
  /** Those namespaces' catalog, one loader per language (`?catalog=module-<id>`, web/i18n-catalogs.ts). */
  locales?: CatalogLoaders;
  /** Its code: a dynamic import(), so it is its own chunk. */
  load: () => Promise<ModuleCode>;
}

/** Whether a module is there now: "on"; "off" (switched off, or the hub lacks its flag); "pending" while a flagged
 *  module's hub has not said hello yet. */
export type ModuleState = "on" | "off" | "pending";

/** What the host reads of the hub: the hello's features and whether the hello came. The shell connects the store
 *  (connectModuleHub, App.tsx); the router imports this file, so it does not import the store itself. Until it is
 *  connected the hub has said nothing: a flagged module is pending. */
type HubState = Pick<State, "features" | "loaded">;
const NO_HUB: HubState = { features: [], loaded: false };
let hubState: () => HubState = () => NO_HUB;
let subscribeHub: (l: () => void) => () => void = () => () => {};

export function connectModuleHub(get: () => HubState, subscribe: (l: () => void) => () => void): void {
  hubState = get;
  subscribeHub = subscribe;
  changed();
}

const getState = () => hubState();

const MODULE_ID = /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/;

/** The module API this shell serves (#13430 `api`). */
export const MODULE_API = 1;

/** Descriptor fields declared but not served yet: a module that fills one is refused rather than silently ignored. */
const UNSERVED = ["wants", "settingsTabs", "chatSlots", "cardRenderers"] as const;

/** The first segments of the core's own addresses (lib/router.ts parse, and what the hub serves beside the app): a
 *  module's base may not be one. lib/router.test.ts keeps it in step with parse. */
export const CORE_PATHS: ReadonlySet<string> = new Set([
  "s", "t", "persona", "j", "workspace-board", "requests", "overview", "history", "tracks", "assistant", "inbox", "messages",
  "starred", "routines", "kb", "space", "machines", "terminal", "a", "agents", "compare", "settings", "subscriptions",
  "extension", "plugin", "todos", "api", "login", "screen", "share", "admin", "assets",
]);
/** Core addresses whose deeper paths a module may take as an alias: `/tracks/<slug>` of a space it has its own page for. */
const ALIASABLE: ReadonlySet<string> = new Set(["tracks"]);

const routePaths = (r: ModuleRoute): (readonly string[])[] => [[r.base], ...(r.aliases ?? [])];
/** Whether two paths would route the same address: one is a prefix of the other. */
const overlaps = (a: readonly string[], b: readonly string[]) => a.slice(0, Math.min(a.length, b.length)).every((x, i) => x === b[i]);
const registry = new Map<string, ModuleDescriptor>();
const listeners = new Set<() => void>();
let revision = 0;
const changed = () => {
  revision++;
  listeners.forEach((l) => l());
};

/** Registers a module's descriptor (src/modules/index.ts). A second registration of the same id, a malformed id, or a
 *  route base another module already claims is a programming error. */
export function registerModule(d: ModuleDescriptor): void {
  if (!MODULE_ID.test(d.id)) throw new Error(`module id "${d.id}" is not lower-case and hyphenated`);
  if (registry.has(d.id)) throw new Error(`module "${d.id}" is registered twice`);
  if ((d.api ?? MODULE_API) !== MODULE_API) throw new Error(`module "${d.id}" is written for module API ${d.api}; this shell serves ${MODULE_API}`);
  const unserved = UNSERVED.filter((k) => (d[k]?.length ?? 0) > 0);
  if (unserved.length) throw new Error(`module "${d.id}" contributes ${unserved.join(", ")}, which the web shell does not serve yet`);
  for (const r of d.routes ?? []) {
    for (const path of routePaths(r)) {
      if (path.length === 1 && CORE_PATHS.has(path[0]!)) throw new Error(`module "${d.id}" claims /${path[0]}, a core address`);
      if (path.length > 1 && CORE_PATHS.has(path[0]!) && !ALIASABLE.has(path[0]!)) throw new Error(`module "${d.id}" claims /${path.join("/")}, under the core address /${path[0]}`);
      for (const other of registry.values()) {
        const taken = (other.routes ?? []).flatMap(routePaths).find((o) => overlaps(o, path));
        if (taken) throw new Error(`module "${d.id}" claims /${path.join("/")}, which "${other.id}" has (/${taken.join("/")})`);
      }
    }
  }
  for (const id of d.extensionPages ?? []) {
    if (!/^-[a-z][a-z0-9-]*$/.test(id)) throw new Error(`module "${d.id}" claims the extension page id "${id}", which is not a reserved one (it must start with "-")`);
    const other = [...registry.values()].find((m) => m.extensionPages?.includes(id));
    if (other) throw new Error(`module "${d.id}" claims the extension page id "${id}", which "${other.id}" has`);
  }
  registry.set(d.id, d);
  changed();
}

/** Every registered module, in registration order (enabled or not). */
export function registeredModules(): readonly ModuleDescriptor[] {
  return [...registry.values()];
}

export function moduleById(id: string): ModuleDescriptor | undefined {
  return registry.get(id);
}

/** Whether `d` is there given the hub's state (hello features, whether hello came) and this browser's switch. */
export function moduleState(d: ModuleDescriptor, s: Pick<State, "features" | "loaded"> = getState()): ModuleState {
  if (moduleSwitchedOff(d.id)) return "off";
  if (!d.feature) return "on";
  if (s.features.includes(d.feature)) return "on";
  return s.loaded || s.features.length ? "off" : "pending";
}

export const moduleOn = (id: string, s?: Pick<State, "features" | "loaded">) => {
  const d = registry.get(id);
  return d !== undefined && moduleState(d, s) === "on";
};

/** The enabled modules, for code outside React. */
export function enabledModules(s?: Pick<State, "features" | "loaded">): ModuleDescriptor[] {
  return [...registry.values()].filter((d) => moduleState(d, s) === "on");
}

/** The route a path's segments open when they are an enabled (or pending) module's page; "absent" when they are the
 *  address of a module that is not there (the router answers it as an unknown address); null when no module has it. */
export function moduleRouteAt(segments: readonly string[]): Extract<Route, { name: "module" }> | "absent" | null {
  for (const d of registry.values()) {
    for (const r of d.routes ?? []) {
      const prefixes = [[r.base], ...(r.aliases ?? [])];
      const prefix = prefixes.find((p) => p.length <= segments.length && p.every((x, i) => segments[i] === x));
      if (!prefix) continue;
      if (moduleState(d) === "off") return "absent";
      return { name: "module", module: d.id, base: r.base, path: segments.slice(prefix.length) };
    }
  }
  return null;
}

/** The module that draws an extension-page id: its descriptor while it is on (or pending), "absent" while it is off
 *  (the router answers the address as an unknown one), null for an id no module has. */
export function moduleExtensionAt(extension: string): ModuleDescriptor | "absent" | null {
  for (const d of registry.values()) {
    if (!d.extensionPages?.includes(extension)) continue;
    return moduleState(d) === "off" ? "absent" : d;
  }
  return null;
}

/** A module route's address in the app. */
export function modulePath(r: { base: string; path: readonly string[] }): string {
  return `/${[r.base, ...r.path].map(encodeURIComponent).join("/")}`;
}

/** A module page's window title. */
export function moduleTitle(r: { module: string; base: string }): string | undefined {
  return registry.get(r.module)?.routes?.find((x) => x.base === r.base)?.title();
}

/** A module route's sidebar entry: its module's entry for that base. */
export function moduleNavItem(r: { module: string; base: string }): ModuleNavItem | undefined {
  return registry.get(r.module)?.navItems?.find((n) => n.path === `/${r.base}`);
}

/** The sidebar key a module route lights. */
export function moduleNavKey(r: { module: string; base: string }): string {
  return moduleNavItem(r)?.key ?? `module:${r.module}`;
}

/** The enabled modules' sidebar entries. */
export function enabledNavItems(s?: Pick<State, "features" | "loaded">): ModuleNavItem[] {
  return enabledModules(s).flatMap((d) => d.navItems ?? []);
}

/** Calls `l` when what is enabled may have changed: a registration, the switch, the hub's features. */
export function subscribeModules(l: () => void): () => void {
  listeners.add(l);
  const offSwitch = subscribeModuleSwitches(l);
  let last = enabledKey();
  const offStore = subscribeHub(() => {
    const now = enabledKey();
    if (now !== last) {
      last = now;
      l();
    }
  });
  return () => {
    listeners.delete(l);
    offSwitch();
    offStore();
  };
}

const enabledKey = () => [...registry.values()].map((d) => `${d.id}:${moduleState(d)}`).join(",");
const snapshot = () => `${revision}|${enabledKey()}`;

/** Re-renders the caller when the enabled modules change; returns a key of their states. */
export function useModuleStates(): string {
  return useSyncExternalStore(subscribeModules, snapshot, snapshot);
}

const loads = new Map<string, Promise<ModuleCode>>();

/** Loads a module's strings and code once (again after a failure). Refused for a module that is not on, so an absent
 *  module's chunk is never requested. */
export function loadModule(id: string): Promise<ModuleCode> {
  const d = registry.get(id);
  if (!d) return Promise.reject(new Error(`no module "${id}"`));
  if (moduleState(d) !== "on") return Promise.reject(new Error(`module "${id}" is not enabled`));
  let p = loads.get(id);
  if (!p) {
    // strings first: a module's code may read them as it loads (module-scope t() calls)
    p = (d.locales ? addCatalogs(d.locales) : Promise.resolve()).then(() => d.load());
    p.catch(() => loads.delete(id));
    loads.set(id, p);
  }
  return p;
}

/** Tests only: forget every module and load. */
export function resetModulesForTest(): void {
  registry.clear();
  loads.clear();
  changed();
}
