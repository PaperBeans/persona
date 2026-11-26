import type { ActionRole, ExternalNode, ExternalPolicy, ExternalRef, ForwardMode, NewItemPolicy, PolicyBaseline, PreFilterField } from "./api";

export type ReferenceScope = "channel" | "container" | "item";
export type ReferenceDraft = {
  provider: string;
  channel: string;
  scope: ReferenceScope;
  container: ExternalNode;
  item: ExternalNode;
};

export const PROVIDERS = ["lark", "github", "mail", "subscription"] as const;
export const FORWARD_MODES = ["off", "realtime", "digest", "on_demand"] as const satisfies readonly ForwardMode[];
export const ACTION_ROLES = ["owner", "fyi"] as const satisfies readonly ActionRole[];
export const NEW_ITEM_POLICIES = ["ask_owner", "auto_todo"] as const satisfies readonly NewItemPolicy[];
export const FILTER_FIELDS = ["keywords", "senders", "mentions"] as const satisfies readonly PreFilterField[];
export const MIN_INTERVAL = 1;
export const MAX_INTERVAL = 365 * 24 * 60;

export const CHANNEL_BASELINE: PolicyBaseline = {
  policy: { mode: "off", action_role: "fyi", new_item_policy: "ask_owner", pre_filter: {} },
  provenance: {
    mode: { source: "default" }, action_role: { source: "default" },
    new_item_policy: { source: "default" }, pre_filter: { source: "default" },
  },
};

export function providerOf(input: string): string {
  const provider = input.trim().toLowerCase();
  return provider === "feishu" ? "lark" : provider === "email" ? "mail" : provider;
}

export function containerKinds(provider: string): readonly string[] {
  switch (provider) {
    case "github": return ["repository"];
    case "lark": return ["chat"];
    case "mail": return ["mailbox", "label"];
    default: return [];
  }
}

export function itemKinds(provider: string): readonly string[] {
  switch (provider) {
    case "github": return ["pull_request", "issue"];
    case "lark":
    case "mail": return ["thread"];
    default: return [];
  }
}

export function referenceDraft(ref?: ExternalRef): ReferenceDraft {
  const provider = ref?.provider ?? "";
  return {
    provider,
    channel: ref?.channel ?? "",
    scope: ref?.item ? "item" : ref?.container ? "container" : "channel",
    container: ref?.container ?? { kind: containerKinds(provider)[0] ?? (provider === "subscription" ? "source" : ""), id: "" },
    item: ref?.item ?? { kind: itemKinds(provider)[0] ?? (provider === "subscription" ? "item" : ""), id: "" },
  };
}

const namePattern = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const enc = new TextEncoder();
const validText = (value: string, max: number) => value.length > 0 &&
  enc.encode(value).length <= max && !/\p{Cc}/u.test(value);

function validNode(node: ExternalNode, kinds: readonly string[]): ExternalNode | undefined {
  const kind = node.kind.trim().toLowerCase();
  const id = node.id.trim();
  if (namePattern.test(kind) && validText(id, 512) && (!kinds.length || kinds.includes(kind))) return { kind, id };
}

export function channelInput(rawProvider: string, rawChannel: string): ExternalRef | null {
  const provider = providerOf(rawProvider);
  const channel = rawChannel.trim();
  if (!namePattern.test(provider) || (!channel && provider === "mail") || (channel && !validText(channel, 512))) return null;
  return channel ? { provider, channel } : { provider };
}

export function referenceInput(draft: ReferenceDraft): ExternalRef | null {
  const ref = channelInput(draft.provider, draft.channel);
  if (!ref || draft.scope === "channel") return ref;
  const container = validNode(draft.container, containerKinds(ref.provider));
  if (!container && (ref.provider !== "mail" || draft.scope === "container" || draft.container.id.trim())) return null;
  if (container) ref.container = container;
  if (draft.scope === "item") {
    const item = validNode(draft.item, itemKinds(ref.provider));
    if (!item) return null;
    ref.item = item;
  }
  return ref;
}

/** Compare the path node by node. The order of JSON object fields is immaterial. */
export function refKey(ref: ExternalRef): string {
  return JSON.stringify([ref.provider, ref.channel ?? "", ref.container?.kind, ref.container?.id, ref.item?.kind, ref.item?.id]);
}

export interface PolicyDraft extends Record<PreFilterField, string> {
  mode: ForwardMode | "";
  interval: string;
  role: ActionRole | "";
  newItem: NewItemPolicy | "";
  filter: "inherit" | "replace";
}

export const emptyPolicyDraft: PolicyDraft = {
  mode: "", interval: "", role: "", newItem: "", filter: "inherit", keywords: "", senders: "", mentions: "",
};

export function policyDraft(policy?: ExternalPolicy): PolicyDraft {
  return {
    mode: policy?.mode ?? "",
    interval: policy?.digest_interval_minutes?.toString() ?? "",
    role: policy?.action_role ?? "",
    newItem: policy?.new_item_policy ?? "",
    filter: policy?.pre_filter ? "replace" : "inherit",
    keywords: policy?.pre_filter?.keywords?.join("\n") ?? "",
    senders: policy?.pre_filter?.senders?.join("\n") ?? "",
    mentions: policy?.pre_filter?.mentions?.join("\n") ?? "",
  };
}

export function allowsInterval(mode: PolicyDraft["mode"]) {
  return !mode || mode === "digest";
}

export function policyInput(draft: PolicyDraft): ExternalPolicy | null {
  const policy: ExternalPolicy = {};
  if (draft.mode) policy.mode = draft.mode;
  if (allowsInterval(draft.mode) && draft.interval) {
    const interval = Number(draft.interval);
    if (!Number.isInteger(interval) || interval < MIN_INTERVAL || interval > MAX_INTERVAL) return null;
    policy.digest_interval_minutes = interval;
  }
  if (draft.role) policy.action_role = draft.role;
  if (draft.newItem) policy.new_item_policy = draft.newItem;
  if (draft.filter === "replace") {
    policy.pre_filter = {};
    for (const field of FILTER_FIELDS) {
      const values = draft[field].split(/\r?\n/).map((part) => part.trim()).filter(Boolean);
      const terms = [...new Set(values)];
      if (terms.length > 32 || terms.some((term) => !validText(term, 256))) return null;
      if (terms.length) policy.pre_filter[field] = terms;
    }
  }
  return policy;
}
