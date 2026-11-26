import { useCallback, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { Button, Dialog, Loading, toast } from "../../ui/atoms";
import { RowFailed } from "../../views/settings/SettingsParts";
import { readExternalPolicy, saveExternalPolicy, type ExternalRef } from "./api";
import { CHANNEL_BASELINE, channelInput, emptyPolicyDraft, policyDraft, policyInput, refKey, type PolicyDraft } from "./draft";
import { ExternalField, externalFieldClass, PolicyFields } from "./PolicyFields";
import { ChannelRefFields, externalRefLabel } from "./ReferenceFields";
import { useExternalRead } from "./useExternalRead";
import { useExternalWrite } from "./useExternalWrite";

export interface ChannelChoice {
  ref: ExternalRef;
  label: string;
}

export function ChannelDefaultsDialog({ manager, provider: fixedProvider, channel: fixedChannel, channels, onClose }: {
  manager: string;
  provider?: string;
  channel?: string;
  channels: readonly ChannelChoice[];
  onClose: () => void;
}) {
  const first = channels[0]?.ref;
  const [provider, setProvider] = useState(fixedProvider ?? first?.provider ?? "");
  const [channel, setChannel] = useState(fixedChannel ?? first?.channel ?? "");
  const [editing, setEditing] = useState<{ scope: string; draft: PolicyDraft }>();
  const { busy, error, write } = useExternalWrite();

  const ref = channelInput(provider, channel);
  const scope = ref ? refKey(ref) : "";
  const refJSON = ref ? JSON.stringify(ref) : "";
  const read = useCallback((signal: AbortSignal) => readExternalPolicy(manager, JSON.parse(refJSON) as ExternalRef, signal), [manager, refJSON]);
  const saved = useExternalRead(refJSON ? read : null);
  const original = policyDraft(saved.data?.policy);
  const draft = editing?.scope === scope ? editing.draft : original;
  const setDraft = (next: PolicyDraft) => setEditing({ scope, draft: next });
  const policy = policyInput(draft);
  const dirty = JSON.stringify(original) !== JSON.stringify(draft);
  const close = () => { if (!busy) onClose(); };
  const save = () => {
    if (busy || !policy || !saved.data || !dirty) return;
    const keptRef = saved.data.external_ref;
    void write(() => saveExternalPolicy(manager, keptRef, policy), () => {
      toast(t("external-sources.defaults_saved"), "ok");
      onClose();
    });
  };
  const move = (nextProvider: string, nextChannel: string) => {
    setProvider(nextProvider);
    setChannel(nextChannel);
    setEditing(undefined);
  };

  return (
    <Dialog open title={t("external-sources.channel_defaults")} onClose={close} footer={<>
      <Button tone="quiet" className="min-h-11" disabled={busy} onClick={close}>{t("common.cancel")}</Button>
      <Button tone="primary" className="min-h-11" busy={busy} disabled={!saved.data || !dirty || !policy} onClick={save}>{t("common.save")}</Button>
    </>}>
      <div className="space-y-4">
        <p className="text-meta leading-relaxed text-ink-2">{t("external-sources.channel_hint")}</p>
        {fixedChannel !== undefined && ref
          ? <p className="text-meta font-medium text-ink">{externalRefLabel(ref)}</p>
          : <fieldset disabled={busy} className="min-w-0 space-y-3">
              {channels.length > 0 && (
                <ExternalField label={t("external-sources.channel_choice")}>
                  <select className={externalFieldClass} value={channels.some((entry) => refKey(entry.ref) === scope) ? scope : ""}
                    onChange={(e) => {
                      const next = channels.find((choice) => refKey(choice.ref) === e.target.value)?.ref;
                      move(next?.provider ?? fixedProvider ?? "", next?.channel ?? "");
                    }}>
                    {channels.map(({ ref: choice, label }) => <option key={refKey(choice)} value={refKey(choice)}>{label}</option>)}
                    <option value="">{t("external-sources.manual_source")}</option>
                  </select>
                </ExternalField>
              )}
              <ChannelRefFields provider={provider} channel={channel} fixedProvider={Boolean(fixedProvider)} onChange={move} />
            </fieldset>}
        {saved.loading && <Loading />}
        {saved.error !== undefined && <RowFailed text={errorText(saved.error)} retry={t("todos.retry")} onRetry={saved.retry} />}
        {saved.data && (
          <div className="space-y-2 border-t border-line pt-3">
            <PolicyFields value={draft} onChange={setDraft} baseline={CHANNEL_BASELINE} overrideSource="channel" disabled={busy} />
            {Object.keys(policy ?? {}).length > 0 && (
              <Button tone="quiet" className="min-h-11" disabled={busy} onClick={() => setDraft(emptyPolicyDraft)}>{t("external-sources.reset_defaults")}</Button>
            )}
          </div>
        )}
        {(error || !policy) && <p role="alert" className="text-meta text-red">{error || t("external-sources.invalid_policy")}</p>}
      </div>
    </Dialog>
  );
}
