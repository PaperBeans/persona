// MCP elicitation (docs/protocol/protocol.md "MCP elicitation"): a form an MCP server asks the user to fill in, or a link it asks
// them to open. Same rules as Android's ElicitationForm / ElicitationUrl / AnswerBuilder: a deliberately bounded JSON
// Schema subset, where anything unknown blocks acceptance (the card says to handle it in the terminal), and a link is
// accepted only after this page opened that exact HTTP(S) target.
import { t } from "../i18n";
import { mcpServerLabel } from "./turns";
import type { Interaction } from "./types";

type Schema = Record<string, unknown>;
export type Values = Record<string, unknown>;

export interface Field {
  key: string;
  schema: Schema;
  required: boolean;
  type: string;
  title: string;
  description?: string;
  /** A password: never prefilled from a default. */
  secret: boolean;
  /** Enum values, of the field or (for an array) of its items. */
  choices: (string | number | boolean)[];
  choiceLabels: string[];
}

export interface Validation {
  content: Values;
  errors: Record<string, string>;
  supported: boolean;
  valid: boolean;
}

const isObject = (v: unknown): v is Schema => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (s: Schema, key: string) => (typeof s[key] === "string" ? (s[key] as string) : undefined);
const isPrimitive = (v: unknown): v is string | number | boolean => ["string", "number", "boolean"].includes(typeof v);

const SCHEMA_KEYS = new Set(["type", "properties", "required", "title", "description", "additionalProperties", "$schema"]);
const FIELD_KEYS = new Set(["type", "title", "description", "default", "enum", "enumNames", "minLength", "maxLength", "format", "minimum",
  "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "items", "minItems", "maxItems", "uniqueItems", "writeOnly"]);
const FORMATS = new Set(["email", "uri", "date", "date-time", "password"]);

export function fields(schema: Schema | undefined): Field[] {
  const required = Array.isArray(schema?.required) ? schema.required.filter((k): k is string => typeof k === "string") : [];
  const props = isObject(schema?.properties) ? schema.properties : {};
  return Object.entries(props).flatMap(([key, value]) => {
    if (!isObject(value)) return [];
    const type = str(value, "type") ?? "";
    const source = type === "array" && isObject(value.items) ? value.items : value;
    const choices = Array.isArray(source.enum) ? source.enum.filter(isPrimitive) : [];
    const labels = Array.isArray(source.enumNames) ? source.enumNames.filter((x): x is string => typeof x === "string") : [];
    return [{
      key, schema: value, required: required.includes(key), type, title: str(value, "title") ?? key, description: str(value, "description"),
      secret: value.writeOnly === true || str(value, "format") === "password", choices, choiceLabels: labels,
    }];
  });
}

/** What the form starts with: the schema's defaults, secrets excepted. */
export function defaults(schema: Schema | undefined): Values {
  return Object.fromEntries(fields(schema).filter((f) => !f.secret && f.schema.default !== undefined).map((f) => [f.key, f.schema.default]));
}

const nonNegativeInt = (v: unknown) => typeof v === "number" && Number.isInteger(v) && v >= 0;

function fieldSupported(s: Schema): boolean {
  if (Object.keys(s).some((k) => !FIELD_KEYS.has(k))) return false;
  if (["minLength", "maxLength", "minItems", "maxItems"].some((k) => k in s && !nonNegativeInt(s[k]))) return false;
  if (["minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"].some((k) => k in s && !(typeof s[k] === "number" && Number.isFinite(s[k])))) return false;
  if (["writeOnly", "uniqueItems"].some((k) => k in s && typeof s[k] !== "boolean")) return false;
  if ("format" in s && !(typeof s.format === "string" && FORMATS.has(s.format))) return false;
  if ("enum" in s && !(Array.isArray(s.enum) && s.enum.length > 0 && s.enum.every(isPrimitive))) return false;
  switch (s.type) {
    case "string":
    case "number":
    case "integer":
    case "boolean":
      return true;
    case "array":
      return isObject(s.items) && s.items.type === "string" && Array.isArray(s.items.enum) && fieldSupported(s.items);
    default:
      return false;
  }
}

/** Whether `schema` is inside the subset this page can render and check. */
export function supported(schema: Schema | undefined): boolean {
  if (!schema || schema.type !== "object" || !isObject(schema.properties)) return false;
  if (Object.keys(schema).some((k) => !SCHEMA_KEYS.has(k))) return false;
  if ("required" in schema && !Array.isArray(schema.required)) return false;
  const list = fields(schema);
  if (Object.keys(schema.properties).length !== list.length) return false;
  if ((Array.isArray(schema.required) ? schema.required : []).some((k) => !list.some((f) => f.key === k))) return false;
  return list.every((f) => fieldSupported(f.schema));
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DATE_TIME = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$/i;
const NUMBER = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;

function validDate(text: string): boolean {
  const m = DATE.exec(text);
  if (!m) return false;
  const d = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3]));
  return d.getUTCFullYear() === +m[1] && d.getUTCMonth() === +m[2] - 1 && d.getUTCDate() === +m[3];
}

function validDateTime(text: string): boolean {
  const m = DATE_TIME.exec(text);
  if (!m) return false;
  // Check the original day before Date.parse normalizes it; UTC avoids timezone shifts.
  // setUTCFullYear also preserves years 0000–0099, which Date.UTC treats as 1900–1999.
  const d = new Date(0);
  d.setUTCFullYear(+m[1], +m[2] - 1, +m[3]);
  return d.getUTCFullYear() === +m[1] && d.getUTCMonth() === +m[2] - 1 && d.getUTCDate() === +m[3]
    && +m[4] < 24 && +m[5] < 60 && +m[6] < 60 && !Number.isNaN(Date.parse(text));
}

/** NUMBER has already checked the grammar. Rounding/underflow must not turn a fraction into an integer. */
function exactInteger(text: string, n: number): boolean {
  if (!Number.isSafeInteger(n)) return false;
  const [coefficient, exponent = "0"] = text.replace(/^[+-]/, "").toLowerCase().split("e");
  const [whole, fraction = ""] = coefficient.split(".");
  const digits = whole + fraction;
  const trailingZeros = digits.length - digits.replace(/0+$/, "").length;
  // Zero stays integral even with an exponent too large for Number to represent.
  return trailingZeros === digits.length || Number(exponent) + trailingZeros >= fraction.length;
}

function absoluteUri(text: string): boolean {
  try {
    return Boolean(new URL(text).protocol);
  } catch {
    return false;
  }
}

/** n is a whole multiple of m, allowing for binary floating point (0.3 is a multiple of 0.1). */
function multipleOf(n: number, m: number): boolean {
  if (m <= 0) return false;
  const q = n / m;
  return Math.abs(q - Math.round(q)) < 1e-9 * Math.max(1, Math.abs(q));
}

/** The value as it goes to the hub, or undefined when it does not fit the field. Number fields hold their text. */
function convert(f: Field, value: unknown): unknown {
  const s = f.schema;
  let out: unknown;
  switch (f.type) {
    case "string": {
      if (typeof value !== "string") return undefined;
      const length = [...value].length;
      if (typeof s.minLength === "number" && length < s.minLength) return undefined;
      if (typeof s.maxLength === "number" && length > s.maxLength) return undefined;
      const format = str(s, "format");
      if (format === "email" && !EMAIL.test(value)) return undefined;
      if (format === "uri" && !absoluteUri(value)) return undefined;
      if (format === "date" && !validDate(value)) return undefined;
      if (format === "date-time" && !validDateTime(value)) return undefined;
      out = value;
      break;
    }
    case "number":
    case "integer": {
      const text = typeof value === "number" ? String(value) : typeof value === "string" ? value.trim() : "";
      if (!NUMBER.test(text)) return undefined;
      const n = Number(text);
      if (!Number.isFinite(n) || (f.type === "integer" && !exactInteger(text, n))) return undefined;
      if (typeof s.minimum === "number" && n < s.minimum) return undefined;
      if (typeof s.maximum === "number" && n > s.maximum) return undefined;
      if (typeof s.exclusiveMinimum === "number" && n <= s.exclusiveMinimum) return undefined;
      if (typeof s.exclusiveMaximum === "number" && n >= s.exclusiveMaximum) return undefined;
      if (typeof s.multipleOf === "number" && !multipleOf(n, s.multipleOf)) return undefined;
      out = n;
      break;
    }
    case "boolean":
      if (typeof value !== "boolean") return undefined;
      out = value;
      break;
    case "array": {
      if (!Array.isArray(value) || !value.every((v) => f.choices.includes(v as string))) return undefined;
      if (new Set(value).size !== value.length) return undefined;
      if (typeof s.minItems === "number" && value.length < s.minItems) return undefined;
      if (typeof s.maxItems === "number" && value.length > s.maxItems) return undefined;
      out = value;
      break;
    }
    default:
      return undefined;
  }
  if (Array.isArray(s.enum) && !s.enum.includes(out)) return undefined;
  return out;
}

/** Checks the values against the schema; `content` is what an accept sends (only the fields that were filled in). */
export function validate(schema: Schema | undefined, values: Values): Validation {
  const ok = supported(schema);
  const errors: Record<string, string> = {};
  const content: Values = {};
  for (const f of fields(schema)) {
    const value = values[f.key];
    if (value === undefined) {
      if (f.required) errors[f.key] = t("lib-elicitation.required");
      continue;
    }
    const converted = convert(f, value);
    if (converted === undefined) errors[f.key] = t("lib-elicitation.invalid");
    else content[f.key] = converted;
  }
  return { content, errors, supported: ok, valid: ok && Object.keys(errors).length === 0 };
}

/** The link to open: an absolute HTTP(S) URL with a host and no user info, else undefined. */
export function urlTarget(value: string | undefined): string | undefined {
  const target = value?.trim();
  // the WHATWG parser forgives https:///host and https:host; Android's URI does not, and neither does this
  if (!target || !/^https?:\/\/[^/\\?#]/i.test(target)) return undefined;
  try {
    const u = new URL(target);
    if (!["http:", "https:"].includes(u.protocol) || !u.hostname || u.username || u.password) return undefined;
    return target;
  } catch {
    return undefined;
  }
}

export function urlHost(value: string | undefined): string | undefined {
  const target = urlTarget(value);
  return target ? new URL(target).hostname : undefined;
}

export type ElicitationAction = "accept" | "decline" | "cancel";

/**
 * The resolution for an action on the card, or undefined when it may not be sent yet: an accept needs a valid form,
 * or, for a link, that this page opened exactly that link (`opened`).
 */
export function elicitationResolution(ix: Interaction, action: ElicitationAction, values: Values, opened?: string): Record<string, unknown> | undefined {
  if (ix.kind !== "elicitation") return undefined;
  if (action !== "accept") return { action };
  const form = ix.elicitation;
  if (!form) return undefined;
  if (form.mode === "url") {
    const url = urlTarget(form.url);
    return url && opened === url ? { action: "accept" } : undefined;
  }
  if (form.mode !== "form") return undefined;
  const v = validate(form.requested_schema, values);
  return v.valid ? { action: "accept", content: v.content } : undefined;
}

/** Who asks, in a line or two: a subagent, the MCP server and where it is configured (Android's requestSourceLabel). */
export function elicitationSource(ix: Interaction): string[] {
  const out: string[] = [];
  const agent = ix.agent_type?.trim();
  if (agent) out.push(t("lib-elicitation.from_subagent", { agent }));
  const name = ix.mcp_server_name?.trim();
  const server = name ? mcpServerLabel(name) : undefined;
  const source = ix.mcp_config_source?.trim();
  if (server) out.push(source ? t("lib-elicitation.from_server_source", { server, source }) : t("lib-elicitation.from_server", { server }));
  return out;
}

/** The keyboard a number field asks for: the phone keypads (numeric, decimal) have no minus, so only when it can't be negative. */
export function numberInputMode(f: Field): "numeric" | "decimal" | undefined {
  if (f.type !== "integer" && f.type !== "number") return undefined;
  const s = f.schema;
  const nonNegative = (typeof s.minimum === "number" && s.minimum >= 0) || (typeof s.exclusiveMinimum === "number" && s.exclusiveMinimum >= 0);
  if (!nonNegative) return undefined;
  return f.type === "integer" ? "numeric" : "decimal";
}

/**
 * What a card has typed and opened so far, kept while it is pending (Android's RequestDraftJournal): a card that moves
 * on the page is drawn again and picks it up. Secrets are not kept; the store drops a draft when its interaction ends.
 */
export interface ElicitationDraft {
  values: Values;
  opened?: string;
}

const drafts = new Map<string, ElicitationDraft>();

export function elicitationDraft(id: string): ElicitationDraft | undefined {
  return drafts.get(id);
}

export function saveElicitationDraft(id: string, schema: Schema | undefined, draft: ElicitationDraft) {
  const secret = new Set(fields(schema).filter((f) => f.secret).map((f) => f.key));
  const values = Object.fromEntries(Object.entries(draft.values).filter(([k]) => !secret.has(k)));
  drafts.set(id, { values, opened: draft.opened });
}

export function dropElicitationDraft(id: string) {
  drafts.delete(id);
}
