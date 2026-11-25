// Settings › Agents › Named herdr sessions (#14898): per computer, named herdr sessions (`herdr --session <name>`)
// kept out of Persona. Reuses the per-computer pattern of HiddenAgentDirs.tsx and ClaudeDirs.tsx: GET loads detected
// sessions and stored blocks, PUT replaces the computer's blocked list and returns the updated state.
import { useEffect, useRef, useState } from "react";
import { api, enc, errorText } from "../../lib/api";
import { useScopedMachines } from "../../lib/current-assistant";
import { machineWithHub } from "../../lib/mesh";
import { composing } from "../../lib/send-history";
import { t } from "../../i18n";
import { Button, Loading } from "../../ui/atoms";
import { Group, Row, RowFailed, SettingsHint, settingsInputClass } from "./SettingsParts";

export interface HerdrNamedSession { name: string; running: boolean; blocked: boolean }
export interface HerdrSessionsState { sessions: HerdrNamedSession[]; blocked: string[]; live: boolean }

const NAME_RE = /^[A-Za-z0-9._-]+$/;

/** Why a typed session name cannot be added ("" = valid): mirrors server proto.CleanHerdrSessionName. */
export function herdrSessionNameError(input: string, blocked: readonly string[]): string {
  const name = input.trim();
  if (!name) return "";
  if (name === "default" || name.startsWith("persona-")) return t("herdr-sessions.name_reserved");
  if (name === "." || name === ".." || name.length > 128 || !NAME_RE.test(name)) return t("herdr-sessions.name_invalid");
  if (blocked.includes(name)) return t("herdr-sessions.name_exists");
  return "";
}

function sessionHint(s: HerdrNamedSession): string {
  if (s.blocked) return t(s.running ? "herdr-sessions.running_blocked" : "herdr-sessions.stopped_blocked");
  return t(s.running ? "herdr-sessions.running_visible" : "herdr-sessions.stopped_visible");
}

function normalizeState(v: HerdrSessionsState): HerdrSessionsState {
  return { sessions: v.sessions ?? [], blocked: v.blocked ?? [], live: !!v.live };
}

export function HerdrSessions() {
  const machines = useScopedMachines();
  const [mid, setMid] = useState("");
  const machine = machines.some((m) => m.id === mid) ? mid : machines[0]?.id ?? "";
  const [state, setState] = useState<HerdrSessionsState | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const [input, setInput] = useState("");
  const [reload, setReload] = useState(0);
  const getEndpoint = `/machines/${enc(machine)}/herdr/sessions`;
  const putEndpoint = `/machines/${enc(machine)}/herdr/sessions/blocked`;
  const view = useRef(0);

  useEffect(() => {
    if (!machine) return;
    let dead = false;
    view.current++;
    saving.current = false;
    setBusy(false);
    setState(null);
    setError("");
    api<HerdrSessionsState>(getEndpoint)
      .then((v) => { if (!dead) setState(normalizeState(v)); })
      .catch((e) => { if (!dead) setError(errorText(e)); });
    return () => { dead = true; };
  }, [getEndpoint, machine, reload]);

  const save = async (blocked: string[]) => {
    if (!state || saving.current) return false;
    const previous = state;
    const started = view.current;
    const current = () => started === view.current;
    saving.current = true;
    setBusy(true);
    setError("");
    setState({
      ...state,
      blocked,
      sessions: state.sessions.map((s) => ({ ...s, blocked: blocked.includes(s.name) })),
    });
    try {
      const v = await api<HerdrSessionsState>(putEndpoint, { method: "PUT", json: { blocked } });
      if (!current()) return false;
      setState(normalizeState(v));
      return true;
    } catch (e) {
      if (!current()) return false;
      setState(previous);
      setError(errorText(e));
      return false;
    } finally {
      if (current()) {
        saving.current = false;
        setBusy(false);
      }
    }
  };

  const typed = state ? herdrSessionNameError(input, state.blocked) : "";
  const add = async () => {
    const name = input.trim();
    if (!state || !name || typed || saving.current) return;
    if (await save([...state.blocked, name])) setInput("");
  };

  if (!machines.length) return null;
  const detectedNames = new Set(state?.sessions.map((s) => s.name) ?? []);
  const looseBlocked = (state?.blocked ?? []).filter((name) => !detectedNames.has(name));

  return (
    <section id="settings-herdr-sessions" className="scroll-mt-4">
      <Group title={t("herdr-sessions.title")} footer={t("herdr-sessions.footer")}>
        <Row label={t("herdr-sessions.machine")}>
          <select aria-label={t("herdr-sessions.machine")} value={machine} onChange={(e) => setMid(e.target.value)} disabled={busy} className={settingsInputClass}>
            {machines.map((m) => <option key={m.id} value={m.id}>{machineWithHub(m.id, machines)}</option>)}
          </select>
        </Row>
        {error && <RowFailed text={t("herdr-sessions.failed", { error })} retry={t("herdr-sessions.retry")} onRetry={() => setReload((n) => n + 1)} />}
        {!state ? (!error && <Row label={<Loading />} />) : <>
          {state.sessions.map((s) => (
            <Row key={s.name} label={s.name} mono hint={sessionHint(s)}>
              <Button
                tone={s.blocked ? "secondary" : "quiet"}
                disabled={busy}
                onClick={() => void save(s.blocked ? state.blocked.filter((x) => x !== s.name) : [...state.blocked, s.name])}
              >
                {s.blocked ? t("herdr-sessions.unblock") : t("herdr-sessions.block")}
              </Button>
            </Row>
          ))}
          {looseBlocked.map((name) => (
            <Row key={`blocked:${name}`} label={name} mono hint={t("herdr-sessions.not_detected_blocked")}>
              <Button tone="quiet" disabled={busy} onClick={() => void save(state.blocked.filter((x) => x !== name))}>
                {t("herdr-sessions.remove")}
              </Button>
            </Row>
          ))}
          {state.sessions.length === 0 && looseBlocked.length === 0 && <SettingsHint>{t("herdr-sessions.empty")}</SettingsHint>}
          {!state.live && <SettingsHint>{t("herdr-sessions.offline_note")}</SettingsHint>}
          <Row label={t("herdr-sessions.session")} hint={input && typed ? typed : undefined} stackOnNarrow>
            <input
              className={settingsInputClass}
              aria-label={t("herdr-sessions.session")}
              placeholder={t("herdr-sessions.placeholder")}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && !composing(e.nativeEvent)) void add(); }}
              spellCheck={false}
              autoComplete="off"
            />
            <Button tone="secondary" disabled={busy || !input.trim() || !!typed} busy={busy} onClick={() => void add()}>
              {t("herdr-sessions.block")}
            </Button>
          </Row>
        </>}
      </Group>
    </section>
  );
}
