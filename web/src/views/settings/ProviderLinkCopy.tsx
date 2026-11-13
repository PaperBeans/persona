// Settings › Model providers › a provider › ⋯ › "Copy import link" (#5420): a link that opens Add provider filled in
// with this provider's address and models (lib/provider-link.ts), for whoever it is sent to; they check it and save
// it on their own hub. It carries no key unless the user turns "Put a key in the link" on and pastes one: the hub never
// hands a stored key out, and a key in a link is a key anyone it reaches can use, so the switch comes with a warning.
// The key lives in this dialog only: turning the switch off or closing the dialog forgets it.
import { useMemo, useState } from "react";
import { providerTitle } from "../../lib/provider-names";
import { encodeImportData, importLinkHref, linkKey, linkPayload, type LinkSource } from "../../lib/provider-link";
import { Button, copyText, Dialog, Switch } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { SecretField } from "./ProviderFields";
import { t } from "../../i18n";

export function ProviderLinkCopy({ provider, onClose }: { provider: LinkSource | null; onClose: () => void }) {
  const [withKey, setWithKey] = useState(false);
  const [key, setKey] = useState("");
  const forget = () => (setWithKey(false), setKey(""));
  const close = () => (forget(), onClose());

  const good = withKey ? linkKey(key) : null;
  const keyBad = withKey && key.trim() !== "" && !good;
  const built = useMemo(() => (provider ? linkPayload(provider, good ?? undefined) : null), [provider, good]);
  const ready = !!built && (!withKey || !!good);
  const copy = async () => {
    if (!built || !ready) return;
    // a link that did not reach the clipboard leaves the dialog (and the key typed) as it was, to try again
    if (await copyText(importLinkHref(encodeImportData(built.payload)), { done: t("settings-providerlink.copied") })) close();
  };

  return (
    <Dialog
      open={provider !== null}
      onClose={close}
      width={520}
      title={t("settings-providerlink.title")}
      footer={
        <>
          <Button tone="quiet" onClick={close}>
            {t("common.cancel")}
          </Button>
          <span className="flex-1" />
          <Button tone="primary" icon="link" disabled={!ready} onClick={() => void copy()} testId="provider-link-copy">
            {t("settings-providerlink.copy")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5 text-[12.5px] leading-relaxed text-ink-2">
          <p>{t("settings-providerlink.intro", { name: provider ? providerTitle(provider) : "" })}</p>
          {built?.fit === "ids" && <p>{t("settings-providerlink.fit_ids")}</p>}
          {built?.fit === "none" && <p>{t("settings-providerlink.fit_none")}</p>}
          <p className="text-[11.5px] text-ink-3">{t("settings-providerlink.paste")}</p>
        </div>
        <div className="flex items-center gap-3 rounded-card bg-surface p-3 shadow-btn">
          <span className="min-w-0 flex-1">
            <span className="block text-[13px] text-ink">{t("settings-providerlink.key_switch")}</span>
            <span className="mt-0.5 block text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerlink.key_switch_hint")}</span>
          </span>
          <Switch on={withKey} onChange={(on) => (on ? setWithKey(true) : forget())} label={t("settings-providerlink.key_switch")} />
        </div>
        {withKey && (
          <>
            <div>
              <SecretField label="API Key" value={key} onChange={setKey} placeholder={t("settings-providerwizard.key_paste")} />
              {keyBad && <p role="alert" className="mt-1 text-[11px] leading-relaxed text-red">{t("settings-providerlink.key_invalid")}</p>}
            </div>
            <p role="note" className="flex items-start gap-2 rounded-card bg-orange-tint px-3 py-2.5 text-[12px] leading-relaxed text-ink">
              <Icon name="warning" size={13} stroke={2.2} className="mt-0.5 shrink-0 text-orange" />
              <span className="min-w-0 flex-1">{t("settings-providerlink.key_warning")}</span>
            </p>
          </>
        )}
      </div>
    </Dialog>
  );
}
