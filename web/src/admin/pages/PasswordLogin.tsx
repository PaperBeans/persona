import { t } from "../../i18n";
import { useState } from "react";
import { registerPassword } from "../../lib/password";
import { Button, toast } from "../../ui/atoms";
import { adminApi, errorText } from "../api";
import { Card, ErrorBox, Loader, useLoad } from "../parts";

interface Settings { enabled: boolean; configured: boolean; revision: string }

export function PasswordLoginSettings() {
  const { data, error, reload, set } = useLoad(() => adminApi<Settings>("/password-login"));
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [revoke, setRevoke] = useState(false);
  const [busy, setBusy] = useState(false);
  async function save() {
    if (!data || busy) return;
    if (password !== confirmation) { toast(t("password.mismatch"), "error"); return; }
    setBusy(true);
    try {
      const pending = registerPassword(password, data.revision, revoke, adminApi);
      setPassword(""); setConfirmation("");
      await pending;
      reload();
      toast(t("password.saved"), "ok");
      setRevoke(false);
    } catch (error) { toast(errorText(error), "error"); }
    finally { setBusy(false); }
  }
  async function toggle() {
    if (!data || busy) return;
    setBusy(true);
    try {
      set(await adminApi<Settings>("/password-login", "PUT", { enabled: !data.enabled, revision: data.revision, revoke_devices: revoke }));
      setRevoke(false);
      toast(data.enabled ? t("password.turned_off") : t("password.turned_on"), "ok");
    } catch (error) { toast(errorText(error), "error"); }
    finally { setBusy(false); }
  }
  return <div className="mb-5"><Card title={t("password.settings_title")} footer={t("password.settings_footer")}>
    {!data ? error ? <ErrorBox error={error} onRetry={reload} /> : <Loader /> : <div className="space-y-4 p-4">
      <div className="flex items-center justify-between gap-4">
        <p className="text-[13px] text-ink-2">{data.enabled ? t("password.enabled_note") : t("password.disabled_note")}</p>
        {data.configured && <Button disabled={busy} onClick={toggle}>{data.enabled ? t("password.turn_off") : t("password.turn_on")}</Button>}
      </div>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        <label className="text-[12.5px] text-ink-2">{data.configured ? t("password.new_password") : t("password.set_password")}
          <input type="password" autoComplete="new-password" aria-label={t("password.connection_password")} value={password} disabled={busy}
            onChange={(e) => setPassword(e.target.value)} className="mt-1 block h-10 w-full rounded-control bg-inset px-3 text-ink shadow-hairline" />
        </label>
        <label className="text-[12.5px] text-ink-2">{t("password.again")}
          <input type="password" autoComplete="new-password" aria-label={t("password.confirm_label")} value={confirmation} disabled={busy}
            onChange={(e) => setConfirmation(e.target.value)} className="mt-1 block h-10 w-full rounded-control bg-inset px-3 text-ink shadow-hairline" />
        </label>
        <label className="flex items-center gap-2 text-[12.5px] text-ink-2"><input type="checkbox" checked={revoke} disabled={busy} onChange={(e) => setRevoke(e.target.checked)} />{t("password.revoke_all")}</label>
        <div><Button type="submit" tone="primary" disabled={busy || !password || !confirmation}>{busy ? t("password.saving") : t("password.save_enable")}</Button></div>
      </form>
    </div>}
  </Card></div>;
}
