// Settings › Claude Code / Codex (iOS ClaudeSettingsView): the node's whitelist of each agent's own settings file on one
// computer, picked at the top (online ones; the choice is remembered). A change is sent at once (PUT {key, value},
// null = back to the default) and rolled back if that computer refuses; "Change all online machines" sends it to every online
// computer, and a row's ⋯ pushes its value to all of them. The footer says which file changes and when it applies.
import { useEffect, useMemo, useRef, useState } from "react";
import {
  AGENT_TEXT,
  applySummary,
  effective,
  isUnset,
  optionText,
  pickMachine,
  settingDetail,
  settingTitle,
  textValue,
  valueText,
  withValue,
  type AgentKind,
  type AgentSetting,
  type ApplyResult,
  type SettingValue,
} from "../../lib/agent-settings";
import { api, enc, errorText } from "../../lib/api";
import { t } from "../../i18n";
import { machineWithHub } from "../../lib/mesh";
import { composing } from "../../lib/send-history";
import { Button, copyText, cx, EASE, IconButton, Loading, Menu, Spinner, toast, useMenu } from "../../ui/atoms";
import { ClaudeDirs } from "./ClaudeDirs";
import { LiveReplies } from "./LiveReplies";
import { Group, Switch } from "./SettingsParts";
import { useScopedMachines } from "../../lib/current-assistant";

const MACHINE_KEY = "persona.agentSettings.machine";
const ALL_KEY = "persona.agentSettings.applyAll";

const controlClass = "h-8 rounded-control bg-field px-2.5 text-[12.5px] text-ink shadow-inset-field outline-none focus:shadow-[inset_0_0_0_1px_var(--accent)] disabled:opacity-60";

export function AgentSettings({ agent }: { agent: AgentKind }) {
  const text = AGENT_TEXT[agent];
  // the current assistant's computers (docs/assistant/managers.md "The current assistant"): all of them with one
  const machines = useScopedMachines();
  const online = useMemo(() => machines.filter((m) => m.online), [machines]);
  const [mid, setMid] = useState(() => localStorage.getItem(MACHINE_KEY) ?? "");
  const [applyAll, setApplyAll] = useState(() => localStorage.getItem(ALL_KEY) !== "0"); // on by default, as on iOS
  const [settings, setSettings] = useState<AgentSetting[] | null>(null);
  const [error, setError] = useState("");
  // The keys with a save in flight: each row stays disabled until its own save ends, whatever else finishes first.
  const [busy, setBusy] = useState<ReadonlySet<string>>(() => new Set());
  // The shown computer's saves in flight, and a count of saves started, so an answer can tell whether it is the
  // only and newest save (then its full list is current) or another row's value may be newer than what it carries.
  const saving = useRef(new Set<string>());
  const started = useRef(0);
  const [reload, setReload] = useState(0);
  const current = useRef(mid);

  // The remembered computer while it is online, else the first online one.
  const shown = pickMachine(machines, mid);
  current.current = shown;
  const name = (id: string) => machineWithHub(id, machines);

  useEffect(() => {
    if (!shown) return;
    let dead = false;
    setSettings(null);
    setError("");
    api<{ settings: AgentSetting[] }>(`/machines/${enc(shown)}/${text.path}`)
      .then((r) => !dead && setSettings(r.settings ?? []))
      .catch((e) => !dead && setError(errorText(e)));
    return () => {
      dead = true;
    };
  }, [shown, text.path, reload]);

  const choose = (id: string) => {
    setMid(id);
    localStorage.setItem(MACHINE_KEY, id);
  };
  const toggleAll = (on: boolean) => {
    setApplyAll(on);
    localStorage.setItem(ALL_KEY, on ? "1" : "0");
  };

  const markBusy = (key: string, on: boolean) =>
    setBusy((cur) => {
      const next = new Set(cur);
      if (on) next.add(key);
      else next.delete(key);
      return next;
    });

  const put = (id: string, key: string, value: SettingValue) =>
    api<{ settings?: AgentSetting[] }>(`/machines/${enc(id)}/${text.path}`, { method: "PUT", json: { key, value } });

  // Sends one value to the shown computer (and, with "Change all", every other online one); rolls the row back if the
  // shown computer refuses, and says what happened. While another row's save is in flight (or one started after
  // this), the answer's list may hold that row's old value, so only this row is taken from it.
  const change = async (s: AgentSetting, value: SettingValue) => {
    const target = shown;
    const seq = ++started.current;
    saving.current.add(s.key);
    setSettings((cur) => cur && withValue(cur, s.key, value));
    markBusy(s.key, true);
    const ids = applyAll ? [target, ...online.map((m) => m.id).filter((id) => id !== target)] : [target];
    const results: ApplyResult[] = await Promise.all(
      ids.map(async (id) => {
        try {
          const r = await put(id, s.key, value);
          if (id === target && current.current === target && r.settings) {
            const alone = saving.current.size === 1 && started.current === seq;
            const answered = r.settings.find((x) => x.key === s.key);
            if (alone) setSettings(r.settings);
            else if (answered) setSettings((cur) => cur && cur.map((row) => row.key === s.key ? answered : row));
          }
          return { id, name: name(id) };
        } catch (e) {
          if (id === target && current.current === target) setSettings((cur) => cur && cur.map((row) => row.key === s.key ? s : row));
          return { id, name: name(id), error: errorText(e) };
        }
      }),
    );
    saving.current.delete(s.key);
    markBusy(s.key, false);
    const sum = applySummary(results);
    toast(sum.ok ? t("settings-agentsettings.toast_value", { title: settingTitle(s), value: valueText(value, s.key), summary: sum.text }) : t("settings-agentsettings.toast_failed", { title: settingTitle(s), summary: sum.text }), sum.ok ? "ok" : "error");
  };

  // ⋯ › "Apply to all online machines": this computer's value (unset = the default) on every other online one.
  const pushToAll = async (s: AgentSetting) => {
    const others = online.filter((m) => m.id !== shown);
    if (!others.length) return toast(t("settings-agentsettings.no_other_online"), "info");
    markBusy(s.key, true);
    const results: ApplyResult[] = await Promise.all(
      others.map((m) =>
        put(m.id, s.key, s.value ?? null).then(
          () => ({ id: m.id, name: name(m.id) }),
          (e) => ({ id: m.id, name: name(m.id), error: errorText(e) }),
        ),
      ),
    );
    markBusy(s.key, false);
    const sum = applySummary(results);
    toast(sum.ok ? t("settings-agentsettings.pushed_to_others", { title: settingTitle(s), value: valueText(s.value ?? null, s.key), count: results.length }) : t("settings-agentsettings.toast_failed", { title: settingTitle(s), summary: sum.text }), sum.ok ? "ok" : "error");
  };

  if (!online.length) {
    return (
      <Group title={t("settings-agentsettings.machines")} footer={text.footer}>
        <p className="px-4 py-3 text-[12.5px] text-ink-3">{t("settings-agentsettings.none_online")}</p>
      </Group>
    );
  }

  return (
    <>
      <Group title={t("settings-agentsettings.machines")} footer={text.footer}>
        <div className="flex items-center gap-3 px-4 py-2.5">
          <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{t("settings-agentsettings.which_computer")}</span>
          <select value={shown} onChange={(e) => choose(e.target.value)} className={cx(controlClass, "max-w-[60%] min-w-[140px]")} aria-label={t("settings-agentsettings.which_computer")}>
            {online.map((m) => (
              <option key={m.id} value={m.id}>
                {name(m.id)}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-3 px-4 py-2.5">
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] text-ink">{t("settings-agentsettings.apply_all")}</div>
            <div className="mt-0.5 text-[11.5px] text-ink-3">{applyAll ? t("settings-agentsettings.apply_all_on", { count: online.length }) : t("settings-agentsettings.apply_all_off")}</div>
          </div>
          <Switch on={applyAll} onChange={toggleAll} label={t("settings-agentsettings.apply_all")} />
        </div>
      </Group>

      <Group title={t("settings-agentsettings.settings_title", { agent: text.name, machine: name(shown) })}>
        {error ? (
          <div className="flex items-center gap-3 px-4 py-3">
            <p className="min-w-0 flex-1 text-[12.5px] text-red">{t("settings-agentsettings.load_failed", { error })}</p>
            <Button tone="secondary" icon="retry" onClick={() => setReload((n) => n + 1)}>
              {t("settings-agentsettings.retry")}
            </Button>
          </div>
        ) : !settings ? (
          <div className="px-4 py-3">
            <Loading />
          </div>
        ) : settings.length === 0 ? (
          <p className="px-4 py-3 text-[12.5px] text-ink-3">{t("settings-agentsettings.empty")}</p>
        ) : (
          settings.map((s, i) => (
            <SettingRow key={`${shown}/${s.key}`} s={s} i={i} busy={busy.has(s.key)} others={online.length - 1} onChange={(v) => void change(s, v)} onPushAll={() => void pushToAll(s)} />
          ))
        )}
      </Group>
      {agent === "claude" && <LiveReplies key={`live/${shown}`} mid={shown} name={name} others={applyAll ? online.map((m) => m.id).filter((id) => id !== shown) : []} />}
      {agent === "claude" && <ClaudeDirs key={shown} mid={shown} name={name(shown)} />}
    </>
  );
}

function SettingRow({ s, i, busy, others, onChange, onPushAll }: { s: AgentSetting; i: number; busy: boolean; others: number; onChange: (v: SettingValue) => void; onPushAll: () => void }) {
  const menu = useMenu();
  const v = effective(s);
  const unset = isUnset(s);
  const title = settingTitle(s);
  const detail = settingDetail(s);
  return (
    <div className="flex items-center gap-3 px-4 py-2.5" style={{ animation: `fade-up 320ms ${EASE} ${Math.min(i, 8) * 30}ms both` }}>
      {/* The settings-file key only on hover and in ⋯ (#3599): the row says what the option does. */}
      <div className="min-w-0 flex-1" title={t("settings-agentsettings.key_hint", { key: s.key })}>
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-[13px] text-ink">{title}</span>
          {unset && <span className="shrink-0 text-[11px] text-ink-3">{t("settings-agentsettings.default")}</span>}
        </div>
        {detail && <div className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{detail}</div>}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {busy && <Spinner />}
        {s.type === "bool" ? (
          <Switch on={v === true} onChange={(on) => onChange(on)} disabled={busy} label={title} />
        ) : s.type === "enum" ? (
          <select value={typeof v === "string" ? v : ""} disabled={busy} onChange={(e) => onChange(e.target.value)} className={cx(controlClass, "max-w-[180px]")} aria-label={title}>
            {typeof v !== "string" && <option value="">{t("settings-agentsettings.unset_option")}</option>}
            {(s.options ?? []).map((o) => (
              <option key={o} value={o}>
                {o === s.default ? t("settings-agentsettings.option_default", { option: optionText(s.key, o) }) : optionText(s.key, o)}
              </option>
            ))}
          </select>
        ) : (
          <TextSetting s={s} label={title} disabled={busy} onSave={onChange} />
        )}
        <IconButton icon="more" label={t("settings-agentsettings.more", { title })} onClick={menu.open} active={!!menu.anchor} />
        {menu.anchor && (
          <Menu
            anchor={menu.anchor}
            onClose={menu.close}
            align="right"
            width={220}
            items={[
              { key: "reset", label: t("settings-agentsettings.reset"), hint: s.default !== undefined && s.default !== "" ? valueText(s.default ?? null, s.key) : undefined, icon: "retry", disabled: (unset && !s.ignored_value) || busy, onSelect: () => onChange(null) },
              { key: "all", label: t("settings-agentsettings.push_all"), hint: others > 0 ? t("settings-agentsettings.others_hint", { count: others }) : t("settings-agentsettings.no_others"), icon: "machine", disabled: others < 1 || busy, onSelect: onPushAll },
              { key: "key", label: t("settings-agentsettings.copy_key"), hint: s.key, icon: "copy", divider: true, onSelect: () => void copyText(s.key) },
            ]}
          />
        )}
      </div>
    </div>
  );
}

// A text option: saved on Enter or when the field loses focus with a change; Esc puts the saved value back.
function TextSetting({ s, label, disabled, onSave }: { s: AgentSetting; label: string; disabled: boolean; onSave: (v: string | null) => void }) {
  const saved = typeof s.value === "string" ? s.value : "";
  const [text, setText] = useState(saved);
  useEffect(() => setText(saved), [saved]);
  const commit = () => {
    if (text.trim() !== saved) onSave(textValue(text));
  };
  return (
    <input
      value={text}
      disabled={disabled}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (composing(e.nativeEvent)) return;
        if (e.key === "Enter") (e.target as HTMLInputElement).blur();
        else if (e.key === "Escape") setText(saved);
      }}
      placeholder={typeof s.default === "string" && s.default ? s.default : t("settings-agentsettings.unset")}
      spellCheck={false}
      autoComplete="off"
      aria-label={label}
      className={cx(controlClass, "w-[180px] placeholder:text-ink-3")}
    />
  );
}
