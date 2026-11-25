// "Today" at the top of the Health home (#13176, docs/assistant/health.md): the water and protein
// rings with today's amount and goal, and the buttons that log them. A press moves the ring at once and the hub's total
// settles it (useHealthIntake.ts); a logged one offers Undo, a failed one says why and offers Retry with the same record
// number, so a retry never logs twice. Shown only when the hub says it has intake; on a hub that keeps what Health tracks
// (#14716, useHealthItems.ts) only the rings added there, in their order, and nothing when none is. Strength training
// (#14713), when the hub draws it and it is added, is the hub's own card at its place in that order (HealthStrength.tsx).
import { useState } from "react";
import { errorText } from "../../lib/api";
import { currentLanguage, t } from "../../i18n";
import { Button, Dialog, Loading, toast } from "../../ui/atoms";
import { healthIntakePath } from "../../lib/health-intake-ui";
import { BASE } from "../../lib/base";
import { navigate } from "../../lib/router";
import { GaugeFace } from "../../ui/GaugeFace";
import { inputClass, SectionTitle } from "../../views/assistant/parts";
import { INTAKE_KINDS, INTAKE_MIN, INTAKE_MAX, newDraft, parseAmount, type IntakeDraft, type IntakeEntry, type IntakeKind } from "./intake";
import type { WebItem } from "./items";
import { StrengthHomeCard } from "./HealthStrength";
import { useHealthIntake, type Ring } from "./useHealthIntake";

const num = (n: number) => new Intl.NumberFormat(currentLanguage(), { maximumFractionDigits: 1 }).format(n);
const COLOR: Record<IntakeKind, string> = { water: "var(--health-water)", protein: "var(--health-protein)" };
const unitOf = (kind: IntakeKind) => (kind === "water" ? t("health.today_unit_water") : t("health.today_unit_protein"));

/** What to draw, from the hub's list of what Health tracks: `kinds` null while it is being read (with its read's
 *  failure in `error`). Without it both rings are drawn, as before the list. */
export interface TodayItems {
  kinds: readonly WebItem[] | null;
  error: string;
  reload: () => void;
}

export function HealthToday({ items }: { items?: TodayItems }) {
  const intake = useHealthIntake();
  const [ask, setAsk] = useState<IntakeKind | null>(null);
  const [text, setText] = useState("");
  const [invalid, setInvalid] = useState(false);
  if (!intake.enabled) return null;
  const kinds = items ? items.kinds : INTAKE_KINDS;
  if (kinds?.length === 0) return null;

  const undo = async (e: IntakeEntry) => {
    try {
      await intake.undo(e.id, e.kind, e.amount);
      toast(t("health.today_undone"), "info");
    } catch (err) {
      toast(t("health.today_undo_failed", { error: errorText(err) }), "error", { label: t("health.retry"), run: () => void undo(e) });
    }
  };
  const record = async (draft: IntakeDraft) => {
    try {
      const e = await intake.log(draft);
      if (e.deleted_at) {
        toast(t("health.today_undone"), "info");
        return;
      }
      const said = e.kind === "water" ? t("health.today_logged_water", { amount: num(e.amount) }) : t("health.today_logged_protein", { amount: num(e.amount) });
      toast(said, "ok", { label: t("health.today_undo"), run: () => void undo(e) });
    } catch (err) {
      toast(t("health.today_log_failed", { error: errorText(err) }), "error", { label: t("health.retry"), run: () => void record(draft) });
    }
  };

  const open = (kind: IntakeKind) => {
    setText("");
    setInvalid(false);
    setAsk(kind);
  };
  const submit = () => {
    const amount = ask && parseAmount(text, ask);
    if (!ask || amount === null) return setInvalid(true);
    setAsk(null);
    void record(newDraft(ask, amount));
  };

  return (
    <section className="mb-7" data-testid="health-today">
      <SectionTitle>{t("health.today_title")}</SectionTitle>
      {kinds ? (
        <div className="flex flex-col gap-2">
          {runsOf(kinds).map((run, i) => run === "strength" ? (
            <StrengthHomeCard key="strength" />
          ) : intake.rings ? (
            <div key={`rings-${i}`} className="grid grid-cols-2 gap-2">
              {run.map((kind) => kind === "water" ? (
                <RingCard key={kind} kind="water" ring={intake.rings!.water}>
                  <Button tone="primary" onClick={() => void record(newDraft("water"))}>{t("health.today_drank")}</Button>
                  <Button onClick={() => open("water")}>{t("health.today_other")}</Button>
                </RingCard>
              ) : (
                <RingCard key={kind} kind="protein" ring={intake.rings!.protein}>
                  <Button tone="primary" onClick={() => open("protein")}>{t("health.today_grams")}</Button>
                </RingCard>
              ))}
            </div>
          ) : (
            // the rings' read failed or is on its way: said once, where the first of them goes
            i === firstRings(kinds) && <Status key={`rings-${i}`} error={intake.error ? t("health.today_load_failed", { error: intake.error }) : ""} retry={intake.reload} />
          ))}
        </div>
      ) : (
        <Status error={items?.error ? t("health.items_load_failed", { error: items.error }) : ""} retry={items!.reload} />
      )}
      <Dialog
        open={ask !== null}
        onClose={() => setAsk(null)}
        title={ask === "protein" ? t("health.today_grams_title") : t("health.today_other_title")}
        footer={(
          <>
            <Button tone="quiet" onClick={() => setAsk(null)}>{t("health.today_cancel")}</Button>
            <Button tone="primary" onClick={submit}>{t("health.today_log")}</Button>
          </>
        )}
      >
        {ask && (
          <label className="block">
            <span className="mb-1 block text-label font-medium text-ink-2">{t("health.today_amount_label", { unit: unitOf(ask) })}</span>
            <input
              value={text}
              onChange={(e) => { setText(e.target.value); setInvalid(false); }}
              onKeyDown={(e) => { if (e.key === "Enter") submit(); }}
              inputMode="decimal"
              autoComplete="off"
              autoFocus
              aria-invalid={invalid || undefined}
              aria-describedby="health-today-amount-note"
              data-testid="health-today-amount"
              className={inputClass}
            />
            <span id="health-today-amount-note" className={invalid ? "mt-1 block text-label text-red" : "mt-1 block text-label text-ink-3"}>
              {t("health.today_amount_hint", { min: num(INTAKE_MIN[ask]), max: num(INTAKE_MAX[ask]), unit: unitOf(ask) })}
            </span>
          </label>
        )}
      </Dialog>
    </section>
  );
}

/** `kinds` cut where strength training's card goes: the rings on either side of it, in order, each run a grid of its own. */
function runsOf(kinds: readonly WebItem[]): (IntakeKind[] | "strength")[] {
  const out: (IntakeKind[] | "strength")[] = [];
  for (const k of kinds) {
    if (k === "strength") out.push("strength");
    else if (Array.isArray(out.at(-1))) (out.at(-1) as IntakeKind[]).push(k);
    else out.push([k]);
  }
  return out;
}
const firstRings = (kinds: readonly WebItem[]) => runsOf(kinds).findIndex((r) => r !== "strength");

/** A read that failed, with Retry, or one on its way. */
function Status({ error, retry }: { error: string; retry: () => void }) {
  return error ? (
    <div className="flex items-center gap-3 rounded-card bg-surface px-4 py-3 shadow-card">
      <p className="min-w-0 flex-1 text-meta break-words text-ink-2" role="alert">{error}</p>
      <Button icon="retry" onClick={retry}>{t("health.retry")}</Button>
    </div>
  ) : (
    <div className="flex justify-center py-8"><Loading /></div>
  );
}

/** One ring with its goal under it and its buttons. */
function RingCard({ kind, ring, children }: { kind: IntakeKind; ring: Ring; children: React.ReactNode }) {
  const unit = unitOf(kind);
  const title = kind === "water" ? t("health.today_water") : t("health.today_protein");
  const percent = Math.round(ring.ratio * 100);
  return (
    <div className="flex min-w-0 flex-col items-center rounded-card bg-surface pb-3 shadow-card" data-testid={`health-today-${kind}`}>
      <a
        href={BASE + healthIntakePath(kind)}
        onClick={(e) => {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
          e.preventDefault();
          navigate(healthIntakePath(kind));
        }}
        data-testid={`health-today-${kind}-open`}
        className="min-w-0 max-w-full rounded-card transition-colors hover:bg-hover"
      >
        <div
          role="meter"
          aria-label={title}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
          aria-valuetext={t("health.today_read", { amount: num(ring.today), goal: num(ring.goal), unit, percent })}
        >
          <GaugeFace
            percent={percent}
            color={COLOR[kind]}
            track={`color-mix(in srgb, ${COLOR[kind]} 14%, transparent)`}
            centre={num(ring.today)}
            unit={unit}
            title={title}
            subtitle={t("health.today_goal", { goal: num(ring.goal), unit })}
          />
        </div>
      </a>
      <div className="flex flex-wrap justify-center gap-1.5 px-3">{children}</div>
    </div>
  );
}
