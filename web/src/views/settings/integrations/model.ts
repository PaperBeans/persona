// Pure data model and helpers for the lazy-loaded unified Integration Detail view (integrations_v2, #14847).
import { t } from "../../../i18n";
import { ApiError } from "../../../lib/api";
import {
  MAX_THREAD_IDLE_HOURS, normalizeThreadMode, supportsThreadRouting,
  type ChannelDetail, type ChannelThreadMode, type ChannelUpdateBody,
} from "../../../lib/channels";
import {
  checkUpdate, CONFIGURATION_VERSION, ConfigurationError,
  type ConfigurationDocument, type ConfigurationSource,
} from "../../../lib/configuration/contract";
import { fieldPath, type ExtensionField } from "../../../lib/extensions";
import { sameManager } from "../../../lib/manager-choice";

export type BuiltinIntegrationId = "mail" | "feishu-bot" | "imessage" | "github" | "lark" | "larkcli" | (string & {});

export type IntegrationTarget =
  | {
      kind: "builtin";
      id: BuiltinIntegrationId;
      name?: string;
      description?: string;
      channelId?: string;
      connectorId?: string | null;
      manager?: string;
    }
  | {
      kind: "extension";
      id: string;
      name?: string;
      channelId?: string;
      connectorId?: string;
      manager?: string;
    };

export type IntegrationSectionId = "connection" | "channel" | "inbox" | "extension-settings" | "management";

export interface PartitionedExtensionFields {
  connection: ExtensionField[];
  channel: ExtensionField[];
  inbox: ExtensionField[];
  /** Fields without a `section` (legacy extensions) or with an unrecognized future section. */
  general: ExtensionField[];
  advanced: ExtensionField[];
}

/**
 * Splits extension configuration fields into the unified IntegrationDetail sections while preserving
 * manifest order and keeping legacy (unsectioned) or unknown-section fields safely in `general`.
 */
export function partitionExtensionFields(fields: readonly ExtensionField[] | undefined): PartitionedExtensionFields {
  const out: PartitionedExtensionFields = {
    connection: [],
    channel: [],
    inbox: [],
    general: [],
    advanced: [],
  };
  if (!fields?.length) return out;
  for (const field of fields) {
    switch (field.section) {
      case "connection":
        out.connection.push(field);
        break;
      case "channel":
        out.channel.push(field);
        break;
      case "inbox":
        out.inbox.push(field);
        break;
      case "advanced":
        out.advanced.push(field);
        break;
      default:
        out.general.push(field);
        break;
    }
  }
  return out;
}

/** Whether any field (or nested object field) in `fields` currently carries a validation error. */
export function fieldsHaveError(
  fields: readonly ExtensionField[],
  errors: Record<string, string>,
  problems: Record<string, string>,
  prefix: string[] = [],
): boolean {
  for (const f of fields) {
    const path = [...prefix, f.name];
    const key = fieldPath(path);
    if (errors[key] || problems[key]) return true;
    if (f.type === "object" && f.fields?.length && fieldsHaveError(f.fields, errors, problems, path)) {
      return true;
    }
  }
  return false;
}

/**
 * Resolves whether `integrations_v2` is active for `manager` (or the current assistant when omitted).
 * Always fails closed (false) on older hubs that omit `integrations_v2`.
 */
export function isIntegrationsV2Active(
  assistant: { manager?: string; integrations_v2?: boolean } | null | undefined,
  manager?: string,
  shownManagerName?: string | null,
): boolean {
  if (!assistant || assistant.integrations_v2 !== true) return false;
  const expected = manager ?? shownManagerName ?? undefined;
  if (!expected) return true;
  return sameManager(assistant.manager, expected);
}

/** Default channel id associated with a built-in integration when not explicitly overridden. */
export function resolveBuiltinChannelId(target: Extract<IntegrationTarget, { kind: "builtin" }>): string | null {
  if (target.channelId) return target.channelId;
  if (target.id === "feishu-bot") return "feishu";
  return null;
}

/** Default hub connector id associated with a built-in integration when not explicitly overridden. */
export function resolveBuiltinConnectorId(target: Extract<IntegrationTarget, { kind: "builtin" }>): string | null {
  if (target.connectorId === null) return null;
  if (target.connectorId) return target.connectorId;
  if (target.id === "mail" || target.id === "feishu-bot" || target.id === "lark") return null;
  return target.id;
}

/**
 * Builds a ConfigurationDocument for a channel's `thread_mode` and `thread_idle_hours` settings.
 * Gracefully degrades to read-only with an update hint on older hubs that omit `thread_mode`.
 */
export function channelRoutingDocument(d: ChannelDetail, saved = false): ConfigurationDocument {
  const supported = supportsThreadRouting(d);
  const editable = !d.read_only && d.source !== "market";
  const readOnly = !supported || !editable;
  const disabledReason = !supported ? t("settings-integrations.routing_hub_unsupported") : undefined;
  const mode: ChannelThreadMode = normalizeThreadMode(d.thread_mode);
  const idleHours = typeof d.thread_idle_hours === "number" && Number.isFinite(d.thread_idle_hours)
    ? Math.max(0, Math.trunc(d.thread_idle_hours))
    : 0;
  return {
    schema_version: CONFIGURATION_VERSION,
    id: `channel-routing-${d.id}`,
    revision: `${supported ? "v2" : "legacy"}:${mode}:${idleHours}:${editable ? "rw" : "ro"}`,
    title: t("settings-integrations.routing_title"),
    groups: [
      {
        id: "routing",
        title: t("settings-integrations.routing_group"),
        fields: [
          {
            id: "thread_mode",
            kind: "select",
            default: "channel_default",
            title: t("settings-integrations.thread_mode_title"),
            description: t("settings-integrations.thread_mode_description"),
            ...(readOnly && { read_only: true }),
            ...(disabledReason && { disabled_reason: disabledReason }),
            select: {
              presentation: "list",
              options: [
                {
                  value: "channel_default",
                  title: t("settings-integrations.thread_mode_default_title"),
                  description: t("settings-integrations.thread_mode_default_desc"),
                },
                {
                  value: "per_group_and_topic",
                  title: t("settings-integrations.thread_mode_group_topic_title"),
                  description: t("settings-integrations.thread_mode_group_topic_desc"),
                },
                {
                  value: "per_topic",
                  title: t("settings-integrations.thread_mode_topic_title"),
                  description: t("settings-integrations.thread_mode_topic_desc"),
                },
              ],
            },
          },
          {
            id: "thread_idle_hours",
            kind: "number",
            default: 0,
            title: t("settings-integrations.thread_idle_hours_title"),
            description: t("settings-integrations.thread_idle_hours_description"),
            ...(readOnly && { read_only: true }),
            ...(disabledReason && { disabled_reason: disabledReason }),
            number: {
              integer: true,
              minimum: 0,
              maximum: MAX_THREAD_IDLE_HOURS,
              step: 24,
            },
            details: [
              {
                title: t("settings-integrations.thread_idle_default_label"),
                description: t("settings-integrations.thread_idle_default_value"),
              },
              {
                title: t("settings-integrations.thread_idle_examples_label"),
                description: t("settings-integrations.thread_idle_examples_value"),
              },
            ],
          },
        ],
      },
    ],
    values: {
      thread_mode: mode,
      thread_idle_hours: idleHours,
    },
    ...(saved && {
      notices: {
        thread_mode: { kind: "info", message: t("settings-integrations.routing_saved") },
      },
    }),
  };
}

/**
 * Adapts a channel's `thread_mode` and `thread_idle_hours` to ConfigurationForm.
 * `getDetail` reads the latest channel detail snapshot so saves always validate against fresh state.
 */
export function channelRoutingSource(
  channelId: string,
  reloadKey: number,
  getDetail: () => ChannelDetail,
  updateRouting: (body: Pick<ChannelUpdateBody, "thread_mode" | "thread_idle_hours">) => Promise<ChannelDetail | null>,
): ConfigurationSource {
  return {
    key: `integration:channel-routing:${channelId}:${reloadKey}`,
    read: async () => channelRoutingDocument(getDetail()),
    update: async (update) => {
      const current = getDetail();
      const doc = channelRoutingDocument(current);
      const refused = checkUpdate(doc, update);
      if (refused) throw refused;
      const body: { thread_mode?: ChannelThreadMode; thread_idle_hours?: number } = {};
      if (update.reset?.includes("thread_mode")) {
        body.thread_mode = "channel_default";
      } else if (typeof update.changes?.thread_mode === "string") {
        body.thread_mode = normalizeThreadMode(update.changes.thread_mode);
      }
      if (update.reset?.includes("thread_idle_hours")) {
        body.thread_idle_hours = 0;
      } else if (typeof update.changes?.thread_idle_hours === "number") {
        body.thread_idle_hours = Math.trunc(update.changes.thread_idle_hours);
      }
      try {
        const next = await updateRouting(body);
        if (!next) throw new ConfigurationError("configuration_conflict");
        return channelRoutingDocument(next, true);
      } catch (e) {
        if (e instanceof ApiError) throw ConfigurationError.fromBody(e.body, e.status);
        throw e;
      }
    },
  };
}
