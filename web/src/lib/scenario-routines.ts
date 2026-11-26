// Configurable starting points for the existing scheduled-task editor. Source discovery reads the manager's cached
// read-only catalogs; instantiation creates an ordinary Task and never calls a connector, model, or scheduler itself.
import { t } from "../i18n";
import { HHMM, MAX_EVERY, MIN_EVERY, type Repeat, type Task } from "./routines";

export type ScenarioKey = "daily-digest" | "inbox-triage" | "pr-review";
export type ScenarioSourceKey = "persona" | "inbox" | "conversation" | "github" | "mail";

export interface ScenarioCatalogTool {
  name: string;
  title?: string;
  description?: string;
  subscribable?: boolean;
  refused?: { code?: string; message?: string } | null;
}

export interface ScenarioCatalogSource {
  id: string;
  name: string;
  tools: ScenarioCatalogTool[];
}

/** The read-only part of GET /subscriptions/catalog. Nothing here is source content. */
export interface ScenarioCatalog {
  connector: string;
  extension?: string;
  status: string;
  sources: ScenarioCatalogSource[];
  error?: { code?: string; message?: string } | null;
}

export interface ScenarioDefinition {
  key: ScenarioKey;
  repeat: Extract<Repeat, "daily" | "interval">;
  time: string;
  every_minutes: number;
  source: ScenarioSourceKey;
}

export const SCENARIO_TEMPLATES: readonly ScenarioDefinition[] = [
  { key: "daily-digest", repeat: "daily", time: "08:30", every_minutes: 30, source: "persona" },
  { key: "inbox-triage", repeat: "interval", time: "09:00", every_minutes: 60, source: "inbox" },
  { key: "pr-review", repeat: "interval", time: "09:00", every_minutes: 120, source: "conversation" },
];

export interface ScenarioDraft {
  key: ScenarioKey;
  source: ScenarioSourceKey;
  scope: string;
  repeat: Extract<Repeat, "daily" | "interval">;
  time: string;
  weekdays: number[];
  every_minutes: number;
}

export interface ScenarioSource {
  key: ScenarioSourceKey;
  label: string;
  available: boolean;
  /** The built-in source to use when this connector is unavailable. */
  fallback?: ScenarioSourceKey;
  /** Fully-qualified, declared read-only assistant tools; empty for built-in sources and unavailable connectors. */
  tools: string[];
  /** A plain explanation of missing capabilities, or of a check still running. */
  note?: string;
}

function sourceLabel(key: ScenarioSourceKey): string {
  return key === "github" ? "GitHub" : t(`assistant-scenario-templates.source_${key}`);
}

const SOURCES: Record<ScenarioKey, readonly ScenarioSourceKey[]> = {
  "daily-digest": ["persona", "mail", "github"],
  "inbox-triage": ["inbox", "mail"],
  "pr-review": ["github", "conversation"],
};

const FALLBACK: Record<ScenarioKey, ScenarioSourceKey> = {
  "daily-digest": "persona",
  "inbox-triage": "inbox",
  "pr-review": "conversation",
};

// These built-in connectors expose the same declarations to the assistant and the cached catalog. A custom catalog-only
// tool is not assumed to be an assistant tool. Requiring the complete set keeps a generated prompt honest.
const REQUIRED: Partial<Record<ScenarioKey, Partial<Record<ScenarioSourceKey, readonly string[]>>>> = {
  "daily-digest": { github: ["search", "read"], mail: ["search", "read"] },
  "inbox-triage": { mail: ["search", "read"] },
  "pr-review": { github: ["search", "read", "diff", "checks"] },
};

function definition(key: ScenarioKey): ScenarioDefinition {
  return SCENARIO_TEMPLATES.find((template) => template.key === key)!;
}

function connected(catalog: ScenarioCatalog): boolean {
  return !catalog.error && (!catalog.status || catalog.status === "ok");
}

function connectorSource(key: ScenarioKey, connector: ScenarioSourceKey, catalogs: readonly ScenarioCatalog[] | null): { tools: string[]; ready: boolean } {
  const required = REQUIRED[key]?.[connector] ?? [];
  if (!catalogs) return { tools: [], ready: false };
  for (const catalog of catalogs) {
    if (catalog.connector !== connector || !connected(catalog)) continue;
    for (const source of catalog.sources) {
      const tools = new Map(source.tools.map((tool) => [tool.name, tool]));
      if (!required.every((name) => tools.get(name)?.subscribable === true && !tools.get(name)?.refused)) continue;
      return {
        tools: required.map((name) => name.startsWith(`${connector}_`) ? name : `${connector}_${name}`),
        ready: true,
      };
    }
  }
  return { tools: [], ready: false };
}

/** Every source a scenario may use, including unavailable connectors so their absence can be explained. */
export function scenarioSources(key: ScenarioKey, catalogs: readonly ScenarioCatalog[] | null): ScenarioSource[] {
  const fallback = FALLBACK[key];
  const fallbackLabel = sourceLabel(fallback);
  return SOURCES[key].map((source) => {
    if (source === "persona" || source === "inbox" || source === "conversation") {
      return { key: source, label: sourceLabel(source), available: true, tools: [] };
    }
    const resolved = connectorSource(key, source, catalogs);
    return {
      key: source,
      label: sourceLabel(source),
      available: resolved.ready,
      fallback,
      tools: resolved.tools,
      note: resolved.ready ? undefined : catalogs === null
        ? t("assistant-scenario-templates.source_checking", { source: sourceLabel(source) })
        : t("assistant-scenario-templates.source_unavailable", { source: sourceLabel(source), fallback: fallbackLabel }),
    };
  });
}

function defaultScope(key: ScenarioKey, source: ScenarioSourceKey): string {
  if (key === "pr-review") return source === "conversation" ? t("assistant-scenario-templates.scope_pr_conversation") : "";
  if (key === "inbox-triage") return t(source === "mail" ? "assistant-scenario-templates.scope_inbox_mail" : "assistant-scenario-templates.scope_inbox");
  if (source === "persona") return t("assistant-scenario-templates.scope_daily_persona");
  return t("assistant-scenario-templates.scope_daily_external");
}

/** A safe starting draft. Connector-backed choices are explicit, so a loaded catalog never silently changes the source. */
export function defaultScenarioDraft(key: ScenarioKey, catalogs: readonly ScenarioCatalog[] | null = null): ScenarioDraft {
  const template = definition(key);
  const source = key === "pr-review" && connectorSource(key, "github", catalogs).ready ? "github" : template.source;
  return {
    key,
    source,
    scope: defaultScope(key, source),
    repeat: template.repeat,
    time: template.time,
    weekdays: [],
    every_minutes: template.every_minutes,
  };
}

/** Changes source and resets only its scope; schedule choices remain the user's. */
export function withScenarioSource(draft: ScenarioDraft, source: ScenarioSourceKey): ScenarioDraft {
  return { ...draft, source, scope: defaultScope(draft.key, source) };
}

/** Changes schedule kind and normalizes its fields while clearing hidden fields from the previous kind. */
export function withScenarioRepeat(draft: ScenarioDraft, repeat: ScenarioDraft["repeat"]): ScenarioDraft {
  if (repeat === draft.repeat) return draft;
  if (repeat === "daily") {
    return {
      ...draft,
      repeat,
      time: HHMM.test(draft.time) ? draft.time : "09:00",
      every_minutes: 0,
    };
  }
  const validInterval = Number.isInteger(draft.every_minutes) && draft.every_minutes >= MIN_EVERY && draft.every_minutes <= MAX_EVERY;
  return {
    ...draft,
    repeat,
    time: "",
    weekdays: [],
    every_minutes: validInterval ? draft.every_minutes : 30,
  };
}

function selectedSource(draft: ScenarioDraft, catalogs: readonly ScenarioCatalog[] | null): ScenarioSource | undefined {
  return scenarioSources(draft.key, catalogs).find((source) => source.key === draft.source);
}

/** Null means the review step may open. No invalid draft can be instantiated. */
export function scenarioProblem(draft: ScenarioDraft, catalogs: readonly ScenarioCatalog[] | null): string | null {
  if (!SOURCES[draft.key]?.includes(draft.source)) return t("assistant-scenario-templates.error_source");
  const source = selectedSource(draft, catalogs);
  if (!source?.available) return source?.note ?? t("assistant-scenario-templates.error_source");
  const scope = draft.scope.trim();
  if (draft.key === "pr-review" && draft.source === "github" && !/^[^/\s]+\/[^/\s]+$/.test(scope)) {
    return t("assistant-scenario-templates.error_repository");
  }
  if (!scope) return t("assistant-scenario-templates.error_scope");
  if (scope.length > 500) return t("assistant-scenario-templates.error_scope_long");
  if (draft.repeat === "daily" && !HHMM.test(draft.time)) return t("lib-routines.error_time");
  if (draft.repeat === "interval" && (!Number.isInteger(draft.every_minutes) || draft.every_minutes < MIN_EVERY || draft.every_minutes > MAX_EVERY)) {
    return t("lib-routines.error_interval");
  }
  return null;
}

function promptFor(draft: ScenarioDraft, source: ScenarioSource, scope: string): string {
  const task = t(`assistant-scenario-templates.${draft.key}_task`, { source: source.label, scope });
  const access = source.key === "github" || source.key === "mail"
    ? t("assistant-scenario-templates.access_connector", { tools: source.tools.join(", ") })
    : t(`assistant-scenario-templates.access_${source.key}`);
  const output = t(`assistant-scenario-templates.${draft.key}_output`);
  return [task, access, output, t("assistant-scenario-templates.read_only_safety")].join("\n\n");
}

/** Instantiates the existing Task primitive: off, Plan permission, alert-only push, and no Inbox writes by default. */
export function instantiateScenario(draft: ScenarioDraft, catalogs: readonly ScenarioCatalog[] | null): Task | null {
  if (scenarioProblem(draft, catalogs)) return null;
  const source = selectedSource(draft, catalogs)!;
  const scope = draft.scope.trim().replace(/\s+/g, " ");
  return {
    id: "",
    title: t(`assistant-scenario-templates.${draft.key}_title`),
    prompt: promptFor(draft, source, scope),
    repeat: draft.repeat,
    time: draft.repeat === "daily" ? draft.time : "",
    weekdays: draft.repeat === "daily" ? [...draft.weekdays] : [],
    every_minutes: draft.repeat === "interval" ? draft.every_minutes : 0,
    enabled: false,
    permission_mode: "plan",
    push: "asked",
    inbox: "",
  };
}
