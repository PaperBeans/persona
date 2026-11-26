import { useId, useState } from "react";
import { t } from "../../i18n";
import { Button } from "../../ui/atoms";
import { ExternalField, externalFieldClass } from "./PolicyFields";
import type { ExternalNode, ExternalRef } from "./api";
import { containerKinds, itemKinds, PROVIDERS, providerOf, referenceDraft, refKey, type ReferenceDraft, type ReferenceScope } from "./draft";

export function providerLabel(provider: string) {
  return PROVIDERS.some((known) => known === provider) ? t(`external-sources.provider_${provider}`) : provider;
}

export function nodeLabel(kind: string): string {
  switch (kind) {
    case "chat":
    case "repository":
    case "mailbox":
    case "label":
    case "thread":
    case "pull_request":
    case "issue":
    case "source":
    case "item":
      return t(`external-sources.node_${kind}`);
    default: return kind;
  }
}

export function externalRefLabel(ref: ExternalRef) {
  const root = [providerLabel(ref.provider), ref.channel].filter(Boolean).join(" · ");
  if (!ref.container && !ref.item) return root;
  return [root, ref.container?.id, ref.item && `${nodeLabel(ref.item.kind)} ${ref.item.id}`].filter(Boolean).join(" · ");
}

export function ChannelRefFields({ provider, channel, onChange, fixedProvider }: {
  provider: string;
  channel: string;
  onChange: (provider: string, channel: string) => void;
  fixedProvider?: boolean;
}) {
  const listId = useId();
  const kind = providerOf(provider);
  const channelLabel = kind === "github" ? "github_instance" : "channel";
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <ExternalField label={t("external-sources.provider")}>
        {fixedProvider
          ? <input className={externalFieldClass} value={providerLabel(provider)} readOnly />
          : <input className={externalFieldClass} list={listId} value={provider} onChange={(e) => onChange(e.target.value, channel)} />}
      </ExternalField>
      <datalist id={listId}>
        {PROVIDERS.map((known) => <option key={known} value={known}>{providerLabel(known)}</option>)}
      </datalist>
      <ExternalField label={t(`external-sources.${channelLabel}`)}>
        <input className={externalFieldClass} value={channel}
          placeholder={kind === "mail" ? undefined : t("external-sources.default_channel")}
          onChange={(e) => onChange(provider, e.target.value)} />
      </ExternalField>
    </div>
  );
}

type SourceChoice = { ref: ExternalRef; label: string };

function NodeFields({ node, onChange, kinds, scope, optional }: {
  node: ExternalNode;
  onChange: (node: ExternalNode) => void;
  kinds: readonly string[];
  scope: "container" | "item";
  optional?: boolean;
}) {
  const idLabel = node.kind === "repository" ? "repository_path" : node.kind === "issue" || node.kind === "pull_request"
    ? "number" : scope === "container" ? "container_id" : "item_id";
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {kinds.length !== 1 && (
        <ExternalField label={t(`external-sources.${scope}_type`)}>
          {kinds.length
            ? <select className={externalFieldClass} value={node.kind} onChange={(e) => onChange({ ...node, kind: e.target.value })}>
                {kinds.map((kind) => <option key={kind} value={kind}>{nodeLabel(kind)}</option>)}
              </select>
            : <input className={externalFieldClass} value={node.kind} onChange={(e) => onChange({ ...node, kind: e.target.value })} />}
        </ExternalField>
      )}
      <ExternalField label={t(`external-sources.${idLabel}`)}>
        <input className={externalFieldClass} value={node.id}
          placeholder={optional ? t("external-sources.optional") : undefined}
          inputMode={idLabel === "number" ? "numeric" : undefined}
          onChange={(e) => onChange({ ...node, id: e.target.value })} />
      </ExternalField>
    </div>
  );
}

function containerScope(draft: ReferenceDraft) {
  switch (draft.container.kind) {
    case "chat": return t("external-sources.scope_chat");
    case "repository": return t("external-sources.scope_repository");
    case "mailbox":
    case "label": return t("external-sources.scope_mailbox");
    default: return t("external-sources.scope_container");
  }
}

function itemScope(draft: ReferenceDraft) {
  switch (draft.item.kind) {
    case "thread": return t("external-sources.scope_thread");
    case "pull_request": return t("external-sources.scope_pull_request");
    case "issue": return t("external-sources.scope_issue");
    default: return t("external-sources.scope_item");
  }
}

export function ReferenceFields({ value, onChange, sources, disabled }: {
  value: ReferenceDraft;
  onChange: (draft: ReferenceDraft) => void;
  sources: SourceChoice[];
  disabled?: boolean;
}) {
  const [showDetails, setShowDetails] = useState(false);
  const detailsId = useId();
  const provider = providerOf(value.provider);
  const current = sources.find(({ ref }) => refKey(ref) === refKey({
    provider, channel: value.channel, container: value.container.id ? value.container : undefined, item: value.item.id ? value.item : undefined,
  }));
  const scopes = [
    { key: "channel", label: t("external-sources.scope_channel") },
    { key: "container", label: containerScope(value) },
    { key: "item", label: itemScope(value) },
  ];
  return (
    <fieldset disabled={disabled} className="min-w-0 space-y-3">
      <legend className="mb-2 text-card-title font-semibold text-ink">{t("external-sources.source")}</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <ExternalField label={t("external-sources.inbox_source")}>
          <select className={externalFieldClass} value={current ? refKey(current.ref) : ""} onChange={(e) => {
            const selected = sources.find(({ ref }) => refKey(ref) === e.target.value);
            onChange(referenceDraft(selected?.ref));
          }}>
            <option value="">{t("external-sources.manual_source")}</option>
            {sources.map(({ ref, label }) => <option key={refKey(ref)} value={refKey(ref)}>{label}</option>)}
          </select>
        </ExternalField>
        <ExternalField label={t("external-sources.scope")}>
          <select className={externalFieldClass} value={value.scope} onChange={(e) => onChange({ ...value, scope: e.target.value as ReferenceScope })}>
            {scopes.map(({ key, label }) => <option key={key} value={key}>{label}</option>)}
          </select>
        </ExternalField>
      </div>
      {current && (
        <Button tone="quiet" className="min-h-11" expanded={showDetails} aria-controls={detailsId} onClick={() => setShowDetails(!showDetails)}>
          {t("external-sources.source_details")}
        </Button>
      )}
      <div className="space-y-3" id={detailsId} hidden={Boolean(current && !showDetails)}>
        <ChannelRefFields provider={value.provider} channel={value.channel} onChange={(next, channel) => {
          if (providerOf(next) === provider) onChange({ ...value, provider: next, channel });
          else onChange({ ...referenceDraft({ provider: providerOf(next) }), provider: next, scope: value.scope });
        }} />
        {value.scope !== "channel" && (
          <NodeFields scope="container" node={value.container} kinds={containerKinds(provider)} optional={provider === "mail" && value.scope === "item"}
            onChange={(container) => onChange({ ...value, container })} />
        )}
        {value.scope === "item" && <NodeFields scope="item" node={value.item} kinds={itemKinds(provider)} onChange={(item) => onChange({ ...value, item })} />}
      </div>
    </fieldset>
  );
}
