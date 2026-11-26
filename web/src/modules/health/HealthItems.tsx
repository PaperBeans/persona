// Choosing what the Health home tracks (#14716, docs/web/web.md "Health Today"): the editor behind the header's button,
// and the card that asks an owner who never chose. Both save the whole list through useHealthItems, which keeps the
// items only phones show where they were; a save that lost to one made elsewhere shows the hub's list and says so.
import { useEffect, useRef, useState } from "react";
import { errorText } from "../../lib/api";
import { t } from "../../i18n";
import { Button, Dialog, IconButton, Switch, toast } from "../../ui/atoms";
import { offeredItems, shownItems, StaleItemsError, type WebItem } from "./items";
import type { HealthItems, ItemsBase } from "./useHealthItems";

const nameOf = (kind: WebItem) => (kind === "water" ? t("health.today_water") : kind === "protein" ? t("health.today_protein") : t("health.strength_title"));

/** Every offered item in the editor's order: the added ones in their order, then the rest. */
function rowsOf(shown: readonly WebItem[], offered: readonly WebItem[]): { kind: WebItem; on: boolean }[] {
  return [...shown, ...offered.filter((k) => !shown.includes(k))].map((kind) => ({ kind, on: shown.includes(kind) }));
}

/** The editor: each item the web draws with a switch to add or remove it and arrows to move it; Save sends the list. */
export function HealthItemsEditor({ items, open, onClose }: { items: HealthItems; open: boolean; onClose: () => void }) {
  const [rows, setRows] = useState(() => rowsOf(items.shown ?? [], items.offered));
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ tone: "stale" | "failed"; text: string } | null>(null);
  // the list this edit started from: Save merges into it and names its revision, so a change made elsewhere meanwhile
  // (read again in the background for the home) is a 409 here, never overwritten by the older edit
  const base = useRef<ItemsBase | null>(items.state);
  // opened afresh: the list as the hub has it now
  useEffect(() => {
    if (!open) return;
    base.current = items.state;
    setRows(rowsOf(items.shown ?? [], items.offered));
    setNote(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on opening: the edits are the owner's until Save
  }, [open]);

  const move = (i: number, by: -1 | 1) => setRows((r) => {
    const next = [...r];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    return next;
  });
  const toggle = (i: number, on: boolean) => setRows((r) => r.map((x, j) => (j === i ? { ...x, on } : x)));
  const save = async () => {
    setBusy(true);
    setNote(null);
    try {
      await items.save(rows.filter((r) => r.on).map((r) => r.kind), base.current ?? undefined);
      onClose();
    } catch (e) {
      if (e instanceof StaleItemsError) {
        // the hub's list wins: show it, and the owner decides again from it
        base.current = e.current;
        setRows(rowsOf(shownItems(e.current, items.web), offeredItems(e.current, items.web)));
        setNote({ tone: "stale", text: t("health.items_stale") });
      } else {
        setNote({ tone: "failed", text: t("health.items_save_failed", { error: errorText(e) }) });
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t("health.items_title")}
      footer={(
        <>
          <Button tone="quiet" onClick={onClose}>{t("health.items_cancel")}</Button>
          <Button tone="primary" busy={busy} onClick={() => void save()} testId="health-items-save">{t("health.items_save")}</Button>
        </>
      )}
    >
      <p className="text-meta leading-relaxed text-ink-2">{t("health.items_intro")}</p>
      <ol className="mt-3 divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card" data-testid="health-items-rows">
        {rows.map((r, i) => (
          <li key={r.kind} data-item={r.kind} className="flex min-w-0 items-center gap-2 py-1.5 pr-2 pl-3.5">
            <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink">{nameOf(r.kind)}</span>
            <IconButton icon="arrowUp" size={44} label={t("health.items_move_up", { name: nameOf(r.kind) })} disabled={busy || i === 0} onClick={() => move(i, -1)} />
            <IconButton icon="arrowDown" size={44} label={t("health.items_move_down", { name: nameOf(r.kind) })} disabled={busy || i === rows.length - 1} onClick={() => move(i, 1)} />
            <Switch on={r.on} disabled={busy} label={nameOf(r.kind)} onChange={(on) => toggle(i, on)} />
          </li>
        ))}
      </ol>
      {note && (
        <p role={note.tone === "failed" ? "alert" : "status"} className={note.tone === "failed" ? "mt-3 text-meta break-words text-red" : "mt-3 rounded-control bg-inset px-3 py-2 text-meta text-ink-2"}>
          {note.text}
        </p>
      )}
    </Dialog>
  );
}

/** Asked once, of an owner who never chose and has none of the web's items on: what to track. Add saves the ones
 *  picked, Not now saves the list as it is; either way the hub's list is then saved and the card does not come back. */
export function HealthItemsAsk({ items }: { items: HealthItems }) {
  const [picked, setPicked] = useState<WebItem[]>([]);
  const [busy, setBusy] = useState<"add" | "later" | null>(null);
  const answer = async (which: "add" | "later") => {
    setBusy(which);
    try {
      await items.save(which === "add" ? items.offered.filter((k) => picked.includes(k)) : []);
    } catch (e) {
      // a list saved elsewhere meanwhile is the answer: the card goes once the page has it
      if (e instanceof StaleItemsError) toast(t("health.items_stale"), "info");
      else toast(t("health.items_save_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(null);
    }
  };
  return (
    <section className="mb-7 rounded-card bg-surface p-4 shadow-card" data-testid="health-items-ask" aria-labelledby="health-items-ask-title">
      <h2 id="health-items-ask-title" className="text-card-title font-semibold text-ink">{t("health.ask_title")}</h2>
      <p className="mt-1 text-meta leading-relaxed text-ink-2">{items.offered.includes("strength") ? t("health.ask_body_strength") : t("health.ask_body")}</p>
      <div className="mt-3 divide-y divide-line">
        {items.offered.map((kind) => (
          <label key={kind} className="flex min-h-11 items-center gap-3">
            <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink">{nameOf(kind)}</span>
            <Switch on={picked.includes(kind)} disabled={busy !== null} label={nameOf(kind)} onChange={(on) => setPicked((p) => (on ? [...p, kind] : p.filter((k) => k !== kind)))} />
          </label>
        ))}
      </div>
      <div className="mt-3 flex justify-end gap-2">
        <Button tone="quiet" busy={busy === "later"} disabled={busy !== null} onClick={() => void answer("later")} testId="health-items-later">{t("health.ask_later")}</Button>
        <Button tone="primary" busy={busy === "add"} disabled={busy !== null || picked.length === 0} onClick={() => void answer("add")} testId="health-items-add">{t("health.ask_add")}</Button>
      </div>
    </section>
  );
}
