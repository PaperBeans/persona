// Avatar picker, upload and delete dialogs (Settings › Avatar and the profile page; #3200, #2404, #15449).
// Kept separate from Persona.tsx so the picker, its dialogs, look-upload.ts and its copy stay off the cold first screen.
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import {
  BUILTIN_PERSONA, isBuiltinPersona, localized, lookPackages, normalizePersonaChoice, PERSONA_STATES, usablePersonas,
  type LookCurrent, type LookView, type PersonaInfo,
} from "../../lib/persona";
import { hubHas, useStore } from "../../lib/store";
import { deleteUploadedLook, LOOK_UPLOADS_FEATURE, LOOK_UPLOADS_STATES_FEATURE, refusalText, type UploadAnswer } from "../../lib/look-upload";
import { Button, cx, Dialog, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { lazyDialog } from "../LazyOverlay";
import { DASHED_OUTLINE, fileDropGuard, useFileDrop } from "./lookDrop";
import { MochiFace } from "./MochiFace";
import { currentLanguage, t } from "../../i18n";
import { tRich } from "../../i18n/rich";
import { BASE } from "../../lib/base";
import { navigate } from "../../lib/router";
import { settingsHref } from "../settings/href";
import {
  choosePersona, patchPersonaStore, PersonaAvatar, personaChoiceMark, PersonaDot, personaSnapshot, readPersonasAgain,
  recordPersonaEdit, refreshPersonas, takePersonaChoice, usePersona, usePersonaStore,
} from "./Persona";

// the upload dialog (picking, cropping, sending an image) loads on its first open, not with the persona page
const LookUploadDialog = lazyDialog(() => import("./LookUpload").then((m) => m.LookUploadDialog));

/**
 * A look the hub made from an uploaded image (#2404), shown at once with the choice the upload made (it left at
 * `since`, personaChoiceMark): in the hub's order (its own packages, then the extensions'). Its `personas` and `look`
 * pushes come too and change nothing.
 */
function lookUploaded({ look, current }: UploadAnswer, since: number) {
  recordPersonaEdit();
  const ps = personaSnapshot();
  const theirs = (l: LookView) => l.source === "extension";
  let looks = ps.looks;
  if (looks) {
    const rest = looks.filter((l) => l.id !== look.id);
    const at = rest.findIndex(theirs);
    looks = at < 0 ? [...rest, look] : [...rest.slice(0, at), look, ...rest.slice(at)];
  }
  // the packages already listed stay as they are shown (one still loading keeps its old files, withChosenReady)
  const shown = new Map(ps.list.map((p) => [p.id, p]));
  const made = usablePersonas(lookPackages([look]));
  const list = looks ? usablePersonas(lookPackages(looks)).map((p) => (p.id === look.id ? p : (shown.get(p.id) ?? p))) : [...ps.list.filter((p) => p.id !== look.id), ...made];
  patchPersonaStore(current ? { looks, list, chosen: takePersonaChoice(normalizePersonaChoice(current.id ?? current.effective), since) } : { looks, list });
}

/** An uploaded look edited via PATCH (#15449): update it in place (and Match Avatar if chosen) and refresh the list. */
function lookEdited(look: LookView) {
  recordPersonaEdit();
  const ps = personaSnapshot();
  const looks = ps.looks ? ps.looks.map((l) => (l.id === look.id ? look : l)) : null;
  const made = usablePersonas(lookPackages([look]))[0];
  const list = made ? ps.list.map((p) => (p.id === look.id ? made : p)) : ps.list;
  patchPersonaStore({ looks, list });
  readPersonasAgain();
}

/**
 * An uploaded look deleted (#2404; the request left at `since`, personaChoiceMark): gone from the list, and the hub's
 * choice after it (the default when it was the chosen one).
 */
function uploadedLookDeleted(id: string, current: LookCurrent | undefined, since: number) {
  recordPersonaEdit();
  const ps = personaSnapshot();
  const chosen = current?.id ? normalizePersonaChoice(current.id) : ps.chosen === id ? BUILTIN_PERSONA : ps.chosen;
  // the look on screen is the one deleted: nothing newer can name it any more, so the answer stands
  patchPersonaStore({
    looks: ps.looks?.filter((l) => l.id !== id) ?? null,
    list: ps.list.filter((p) => p.id !== id),
    chosen: takePersonaChoice(chosen, ps.chosen === id ? personaChoiceMark() : since),
  });
}

/** The palette's colors as small dots (a card in the picker); they are the package's own, data rather than page tokens. */
function Swatches({ p }: { p: PersonaInfo }) {
  const c = p.manifest.colors?.light ?? p.manifest.colors?.dark;
  const keys = ["primary", "secondary", "accent", "bubble"].filter((k) => c?.[k]);
  if (!keys.length) return <span className="text-[11px] text-ink-3">{t("assistant-persona.no_colors")}</span>;
  return (
    <span className="flex items-center gap-1" aria-hidden>
      {keys.map((k) => (
        <span key={k} className="size-3 rounded-full shadow-hairline" style={{ background: c![k] }} />
      ))}
    </span>
  );
}

/**
 * What the picker says when the hub has no looks beyond the built-in ones: where to get one, with Settings › Extensions as a link
 * (#3597). onExtensions opens it in place (Settings keeps its own tab state); without it the link goes to the page.
 */
export function NoOtherLooks({ onExtensions }: { onExtensions?: () => void }) {
  const to = settingsHref("extensions", null, false);
  const open = (e: React.MouseEvent) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    if (onExtensions) onExtensions();
    else navigate(to);
  };
  return (
    <p className="px-1 text-submeta leading-relaxed text-ink-3">
      {tRich("assistant-persona.none", {
        extensions: (
          <a href={BASE + to} onClick={open} className="text-accent hover:underline">
            {t("assistant-persona.none_extensions")}
          </a>
        ),
      })}
    </p>
  );
}

interface PickerCardItem {
  id: string;
  title: string;
  label: string;
  sub: React.ReactNode;
  face: React.ReactNode;
  uploaded?: boolean;
  info?: PersonaInfo;
}

/** A picker card's face, title (with a check when chosen) and line under it. */
function CardContent({ card, selected }: { card: PickerCardItem; selected: boolean }) {
  return (
    <>
      <span className="flex h-14 items-center justify-center">{card.face}</span>
      <span className="flex max-w-full items-center gap-1 text-[12.5px] font-medium whitespace-nowrap text-ink">
        {selected && <Icon name="check" size={12} stroke={2.6} className="shrink-0 text-accent-ink" />}
        <span className="truncate">{card.title}</span>
      </span>
      {card.sub}
    </>
  );
}

/** An uploaded avatar card with Edit/Delete corner buttons; accepts a dropped image to open Edit when `onDropFile` is given. */
function UploadedLookCard({
  card,
  selected,
  onChoose,
  onEdit,
  onDelete,
  onDropFile,
}: {
  card: PickerCardItem;
  selected: boolean;
  onChoose: () => void;
  onEdit?: () => void;
  onDelete: () => void;
  onDropFile?: (file: File) => void;
}) {
  const drop = useFileDrop({ disabled: !onDropFile, onFile: (file) => onDropFile?.(file) });
  return (
    <div className="group/look relative flex min-w-0" {...(onDropFile ? drop.props : undefined)}>
      <button
        type="button"
        role="radio"
        aria-checked={selected}
        aria-label={card.label}
        data-drop-over={drop.over ? "true" : undefined}
        style={drop.over ? DASHED_OUTLINE : undefined}
        onClick={onChoose}
        className={cx(
          "flex min-w-0 flex-1 flex-col items-center gap-2 rounded-card px-2 pt-3.5 pb-2.5 transition-[background-color,box-shadow] duration-150",
          drop.over
            ? "bg-accent-tint outline-1 outline-accent -outline-offset-1"
            : selected
              ? "bg-accent-tint shadow-[inset_0_0_0_1.5px_var(--accent)]"
              : "bg-inset shadow-hairline hover:bg-hover",
        )}
      >
        <CardContent card={card} selected={selected} />
      </button>
      <span className="absolute top-1 right-1 flex items-center gap-0.5 opacity-0 transition-opacity duration-150 group-hover/look:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100">
        {onEdit && (
          <IconButton icon="pencil" label={t("assistant-persona.edit_label", { name: card.title })} onClick={onEdit} />
        )}
        <IconButton icon="trash" label={t("assistant-persona.delete_label", { name: card.title })} onClick={onDelete} />
      </span>
    </div>
  );
}

/**
 * Avatar (Settings › Avatar and the profile page; the app's Settings › Appearance › Avatar): the one setting for Persona's face, name, the
 * page's mark and icon (#3200): the default dumpling, then the hub's packages (including the bundled frog) and the
 * extensions' looks, and the chosen one's dot in each state. On a hub that keeps the choice every device follows it; the
 * page's colors follow it only with theme color "Match avatar" (ui/look.ts).
 */
export function PersonaPicker({ onExtensions }: { onExtensions?: () => void } = {}) {
  const s = usePersonaStore();
  const current = usePersona();
  const lang = currentLanguage();
  // making avatars from images (#2404, #15449): only a hub that says it can
  const uploads = useStore((st) => hubHas(st.features, LOOK_UPLOADS_FEATURE));
  const uploadStates = useStore((st) => hubHas(st.features, LOOK_UPLOADS_STATES_FEATURE));
  const [refreshing, setRefreshing] = useState(false);
  // the upload / edit dialog, a new one each time it opens (`key`)
  const [upload, setUpload] = useState<{ open: boolean; key: number; edit?: PersonaInfo; file?: File }>({ open: false, key: 0 });
  const [deleting, setDeleting] = useState<{ id: string; name: string; current: boolean } | null>(null);
  // where the choices stood as the upload left (takePersonaChoice)
  const uploadSent = useRef(0);
  const grid = useRef<HTMLDivElement>(null);
  const uploadDrop = useFileDrop({
    disabled: !uploads,
    onFile: (file) => setUpload((u) => ({ open: true, key: u.key + 1, file })),
  });
  const load = () => {
    setRefreshing(true);
    void refreshPersonas().finally(() => setRefreshing(false));
  };
  useEffect(load, []);
  const sub = (text: string) => <span className="max-w-full truncate text-[11px] whitespace-nowrap text-ink-3">{text}</span>;
  const lookOf = (id: string) => s.looks?.find((l) => l.id === id);
  const cards: PickerCardItem[] = [
    { id: BUILTIN_PERSONA, title: t("assistant-persona.mochi"), label: t("assistant-persona.mochi_default"), sub: sub(t("assistant-persona.default_avatar")), face: <MochiFace size={56} /> },
    ...s.list.map((p) => {
      const title = localized(p.manifest.name, p.manifest.names, lang);
      const look = lookOf(p.id);
      const from = look?.extension_name ?? p.manifest.source?.extension_name;
      // one made from an image says what the user wrote under it, else that it is theirs
      if (look?.uploaded) {
        const about = p.manifest.subtitle ? localized(p.manifest.subtitle, p.manifest.subtitles, lang) : "";
        return { id: p.id, title, label: title, uploaded: true, info: p, sub: sub(about || t("assistant-persona.uploaded")), face: <PersonaAvatar size={56} persona={p} fallback={<MochiFace size={56} />} /> };
      }
      return {
        id: p.id,
        title,
        label: title,
        sub: p.manifest.about ? sub(p.manifest.about) : from ? sub(t("assistant-persona.from_extension", { name: from })) : <Swatches p={p} />,
        face: <PersonaAvatar size={56} persona={p} fallback={<MochiFace size={56} />} />,
      };
    }),
  ];
  const missing = !isBuiltinPersona(s.chosen) && s.loaded && !s.list.some((p) => p.id === s.chosen);
  const shownId = current.info?.id ?? BUILTIN_PERSONA;
  return (
    <div className="flex flex-col gap-3 p-3">
      <div ref={grid} role="radiogroup" aria-label={t("assistant-persona.avatar")} className="grid grid-cols-2 gap-2.5 sm:grid-cols-3" {...(uploads ? fileDropGuard : undefined)}>
        {cards.map((c) => {
          const selected = c.id === shownId;
          if (c.uploaded && uploads) {
            const canEdit = uploadStates && !!c.info;
            return (
              <UploadedLookCard
                key={c.id}
                card={c}
                selected={selected}
                onChoose={() => choosePersona(c.id)}
                onEdit={canEdit ? () => setUpload((u) => ({ open: true, key: u.key + 1, edit: c.info })) : undefined}
                onDelete={() => setDeleting({ id: c.id, name: c.title, current: selected })}
                onDropFile={canEdit ? (file) => setUpload((u) => ({ open: true, key: u.key + 1, edit: c.info, file })) : undefined}
              />
            );
          }
          return (
            <button
              key={c.id}
              type="button"
              role="radio"
              aria-checked={selected}
              aria-label={c.label}
              onClick={() => choosePersona(c.id)}
              className={cx(
                "flex min-w-0 flex-col items-center gap-2 rounded-card px-2 pt-3.5 pb-2.5 transition-[background-color,box-shadow] duration-150",
                selected ? "bg-accent-tint shadow-[inset_0_0_0_1.5px_var(--accent)]" : "bg-inset shadow-hairline hover:bg-hover",
              )}
            >
              <CardContent card={c} selected={selected} />
            </button>
          );
        })}
        {uploads && (
          <button
            type="button"
            aria-label={t("assistant-persona.upload_label")}
            data-drop-over={uploadDrop.over ? "true" : undefined}
            style={uploadDrop.over ? DASHED_OUTLINE : undefined}
            onClick={() => setUpload((u) => ({ open: true, key: u.key + 1 }))}
            {...uploadDrop.props}
            className={cx(
              "flex min-w-0 flex-col items-center gap-2 rounded-card px-2 pt-3.5 pb-2.5 transition-[background-color,box-shadow] duration-150",
              uploadDrop.over
                ? "bg-accent-tint outline-1 outline-accent -outline-offset-1"
                : "bg-inset shadow-hairline hover:bg-hover",
            )}
          >
            <span className="flex h-14 items-center justify-center">
              <span className={cx("flex size-14 items-center justify-center rounded-full border border-dashed transition-colors duration-150", uploadDrop.over ? "border-accent text-accent-ink" : "border-line-strong text-ink-3")}>
                <Icon name="plus" size={22} />
              </span>
            </span>
            <span className="max-w-full truncate text-[12.5px] font-medium whitespace-nowrap text-ink">{t("assistant-persona.upload")}</span>
            {sub(t("assistant-persona.upload_sub"))}
          </button>
        )}
      </div>
      <div className="grid grid-cols-5 gap-1 rounded-card bg-inset py-2.5 shadow-hairline" aria-label={t("assistant-persona.states_label", { name: current.name })} role="img">
        {PERSONA_STATES.map((st) => (
          <span key={st.id} className="flex flex-col items-center gap-1">
            <PersonaDot size={40} state={st.id} />
            <span className="text-[11px] whitespace-nowrap text-ink-3">{st.title}</span>
          </span>
        ))}
      </div>
      {missing && <p className="px-1 text-submeta text-ink-3">{t("assistant-persona.missing")}</p>}
      {s.error && <div className="flex items-center gap-3 px-1">
        <p role="alert" className="min-w-0 flex-1 text-submeta break-words text-orange">{s.error}</p>
        <Button icon="retry" onClick={load} busy={refreshing}>{t("assistant-persona.retry")}</Button>
      </div>}
      {s.loaded && !s.error && !s.list.length && <NoOtherLooks onExtensions={onExtensions} />}
      {uploads && (
        <LookUploadDialog
          key={upload.key}
          open={upload.open}
          supportsStates={uploadStates}
          edit={upload.edit}
          initialFile={upload.file}
          onClose={() => setUpload((u) => ({ ...u, open: false, file: undefined }))}
          onSend={() => (uploadSent.current = personaChoiceMark())}
          onUploaded={(answer) => lookUploaded(answer, uploadSent.current)}
          onEdited={lookEdited}
        />
      )}
      <DeleteLookDialog
        look={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          setDeleting(null);
          // the card that opened it is gone: the focus goes to the avatar now chosen (unless it moved on meanwhile)
          requestAnimationFrame(() => {
            if (document.activeElement && document.activeElement !== document.body) return;
            grid.current?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]')?.focus({ preventScroll: true });
          });
        }}
      />
    </div>
  );
}

/** Asks before deleting an uploaded look: it goes from every device, and the current one gives way to the default. */
function DeleteLookDialog({ look, onClose, onDeleted }: { look: { id: string; name: string; current: boolean } | null; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [was, setWas] = useState(look);
  if (look && look !== was) {
    // another look (or the same one again): it starts over
    setWas(look);
    setBusy(false);
    setError("");
  }
  const remove = async () => {
    if (!look || busy) return;
    setBusy(true);
    setError("");
    const since = personaChoiceMark();
    try {
      uploadedLookDeleted(look.id, await deleteUploadedLook(look.id), since);
      setBusy(false);
      onDeleted();
    } catch (e) {
      setBusy(false);
      setError(refusalText(e));
      // gone already (another device deleted it): a list read after this says so (one on its way may be older)
      if (e instanceof ApiError && e.code === "not_found") readPersonasAgain();
    }
  };
  const shown = look ?? was;
  return (
    <Dialog
      open={!!look}
      onClose={onClose}
      title={shown ? t("assistant-persona.delete_title", { name: shown.name }) : undefined}
      width={400}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>{t("common.cancel")}</Button>
          <Button tone="danger" icon="trash" busy={busy} onClick={() => void remove()}>{t("assistant-persona.delete")}</Button>
        </>
      }
    >
      <p className="text-meta leading-relaxed text-ink-2">{t(shown?.current ? "assistant-persona.delete_body_current" : "assistant-persona.delete_body")}</p>
      {error && <p role="alert" className="mt-2 text-label leading-relaxed break-words text-red">{error}</p>}
    </Dialog>
  );
}
