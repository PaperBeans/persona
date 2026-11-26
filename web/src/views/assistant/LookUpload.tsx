// Making and editing an avatar from an image (#2404, #15449, docs/apple/persona.md "Uploading an image"): the dialog
// the avatar picker's Upload card (or an uploaded card's Edit action) opens. Pick an image (or drop one), crop it square
// with the avatar's circle over it — drag to move; the slider, the wheel or a pinch to zoom; arrow keys and + / − from
// the keyboard — name it, optionally pick a theme color or per-state pictures (when the hub lists look_uploads_states),
// and the hub makes or updates the package. The geometry, export and requests live in lib/look-upload.ts and
// lib/look-upload-states.ts; the picker (Persona.tsx) opens this and takes the answer.
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { isAbort } from "../../lib/api";
import { extractCropColor } from "../../lib/look-colors";
import {
  acceptsType, bigEnough, canSend, centeredCrop, clampCrop, EXPORT_MAX, exportCrop, exportRect, maxZoom, MIN_SIDE, NAME_MAX, panCrop,
  readImage, refusalText, SUBTITLE_MAX, textLength, uploadLook, UPLOAD_TYPES, zoomCrop,
  type Crop, type UploadAnswer,
} from "../../lib/look-upload";
import {
  buildPatchBody, buildUploadBody, cropImageStyle, encodeUploadImage, hasPatchChanges, parseUploadSource, patchUploadedLook,
  readUploadImage, STATE_EXPORT_MAX,
  type EditDraftMeta, type EditEncodedPayload, type EditInitial, type PickedUploadImage, type UploadStateInput,
} from "../../lib/look-upload-states";
import { PERSONA_STATES, type LookView, type PersonaInfo, type PersonaState } from "../../lib/persona";
import { Button, cx, Dialog, Switch } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { PRESET_COLORS } from "../../ui/look";
import { t } from "../../i18n";
import { useReducedMotion } from "./face-motion";
import { fileDropGuard, useFileDrop, useWindowFileDropGuard } from "./lookDrop";
import { StatePicturesSection, ThemeColorSection, type StateDrafts } from "./LookUploadExtras";
import { MochiFace } from "./MochiFace";
import { inputClass } from "./parts";
import { PersonaAvatar } from "./Persona";

/** What the dialog uses to read, export, extract color and send; tests give their own. */
export interface LookUploadDeps {
  read?: (file: Blob) => Promise<PickedUploadImage>;
  render?: typeof exportCrop;
  send?: typeof uploadLook;
  patch?: typeof patchUploadedLook;
  extractColor?: typeof extractCropColor;
}

type Phase = "idle" | "reading" | "preparing" | "sending" | "making";

interface CroppingState {
  state: PersonaState;
  image: PickedUploadImage;
  crop: Crop;
  mark: boolean;
  isNew: boolean;
}

/** Zooming in or out one step from the keyboard; a drag step from the arrow keys (CSS pixels, Shift: four times). */
const KEY_ZOOM = 1.1;
const KEY_STEP = 8;
const DEFAULT_CUSTOM_COLOR = PRESET_COLORS.blue.accent.light.toUpperCase();

/** A tick for the spinner to show before the canvas work blocks the page for a moment. */
const paint = () => new Promise<void>((done) => setTimeout(done, 30));

function emptyDrafts(parsed?: ReturnType<typeof parseUploadSource>): StateDrafts {
  const out = {} as StateDrafts;
  for (const { id } of PERSONA_STATES) {
    const src = parsed?.states[id];
    out[id] = {
      mode: src?.own ? "own" : "auto",
      mark: id === "idle" ? false : (src?.mark ?? true),
      picked: null,
      crop: { x: 0, y: 0, zoom: 1 },
    };
  }
  return out;
}

function initialOf(edit?: PersonaInfo): EditInitial | null {
  if (!edit) return null;
  const parsed = parseUploadSource(edit.manifest);
  return {
    name: edit.manifest.name,
    subtitle: edit.manifest.subtitle ?? "",
    colorMode: parsed.colorMode,
    colorHex: parsed.colorHex,
    states: parsed.states,
  };
}

/** `onSend` as the request leaves (the picker notes where the choices stood then), `onUploaded` / `onEdited` with the hub's answer. */
export function LookUploadDialog({
  open,
  onClose,
  onSend,
  onUploaded,
  onEdited,
  supportsStates = false,
  edit,
  initialFile,
  deps = {},
}: {
  open: boolean;
  onClose: () => void;
  onSend?: () => void;
  onUploaded: (answer: UploadAnswer) => void;
  onEdited?: (look: LookView) => void;
  supportsStates?: boolean;
  edit?: PersonaInfo;
  initialFile?: File;
  deps?: LookUploadDeps;
}) {
  const isEdit = !!edit;
  const enhanced = supportsStates || isEdit;
  const read = deps.read ?? (enhanced ? readUploadImage : readImage);
  const render = deps.render ?? exportCrop;
  const send = deps.send ?? uploadLook;
  const patch = deps.patch ?? patchUploadedLook;
  const extractColor = deps.extractColor ?? extractCropColor;
  const parsedInitial = edit ? parseUploadSource(edit.manifest) : undefined;
  const editInitial = initialOf(edit);

  const [picked, setPicked] = useState<PickedUploadImage | null>(null);
  const [crop, setCrop] = useState<Crop>({ x: 0, y: 0, zoom: 1 });
  const [name, setName] = useState(() => edit?.manifest.name ?? "");
  const [subtitle, setSubtitle] = useState(() => edit?.manifest.subtitle ?? "");
  const [phase, setPhase] = useState<Phase>("idle");
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState("");

  // Secondary options (#15449): collapsed by default on Create; open on Edit so current color & playing dots are visible.
  const [colorOpen, setColorOpen] = useState(() => isEdit);
  const [statesOpen, setStatesOpen] = useState(() => isEdit);
  const [colorMode, setColorMode] = useState<"auto" | "custom">(() => parsedInitial?.colorMode ?? "auto");
  const [customColor, setCustomColor] = useState<string>(() => parsedInitial?.colorHex ?? DEFAULT_CUSTOM_COLOR);
  const [customTouched, setCustomTouched] = useState(() => parsedInitial?.colorMode === "custom");
  const [autoColor, setAutoColor] = useState<string | null>(() => (parsedInitial?.colorMode === "auto" ? parsedInitial.colorHex : null));
  const [stateDrafts, setStateDrafts] = useState<StateDrafts>(() => emptyDrafts(parsedInitial));
  const [croppingState, setCroppingState] = useState<CroppingState | null>(null);

  const fileInput = useRef<HTMLInputElement>(null);
  const kept = useRef<PickedUploadImage | null>(null);
  const keptStates = useRef<Partial<Record<PersonaState, PickedUploadImage>>>({});
  const keptCropping = useRef<PickedUploadImage | null>(null);
  const picks = useRef(0);
  const busy = useRef(false);
  const abort = useRef<AbortController | null>(null);
  const appliedInitialFile = useRef<File | null>(null);
  const ids = useId();
  const sending = phase === "preparing" || phase === "sending" || phase === "making";

  // Closed, it starts over (what it showed stays on screen while it leaves): every decoded image is let go here,
  // when another replaces it and when the dialog goes.
  const letGo = () => {
    abort.current?.abort();
    kept.current?.release();
    kept.current = null;
    for (const k of Object.keys(keptStates.current) as PersonaState[]) {
      keptStates.current[k]?.release();
      delete keptStates.current[k];
    }
    keptCropping.current?.release();
    keptCropping.current = null;
    appliedInitialFile.current = null;
    picks.current++; // an image still being decoded is let go as it comes
  };
  useEffect(() => {
    if (open) return;
    letGo();
    const nextParsed = edit ? parseUploadSource(edit.manifest) : undefined;
    setPicked(null);
    setName(edit?.manifest.name ?? "");
    setSubtitle(edit?.manifest.subtitle ?? "");
    setColorOpen(!!edit);
    setStatesOpen(!!edit);
    setColorMode(nextParsed?.colorMode ?? "auto");
    setCustomColor(nextParsed?.colorHex ?? DEFAULT_CUSTOM_COLOR);
    setCustomTouched(nextParsed?.colorMode === "custom");
    setAutoColor(nextParsed?.colorMode === "auto" ? nextParsed.colorHex : null);
    setStateDrafts(emptyDrafts(nextParsed));
    setCroppingState(null);
    setPhase("idle");
    setError("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  // Gone without closing (the page left under it, Back on a wide screen): the same, so a request still going out
  // stops as Cancel stops it.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => letGo, []);

  // Re-estimate the automatic theme color swatch from the cropped main picture (debounced during drags/zooms).
  useEffect(() => {
    if (!enhanced) return;
    if (!picked) {
      setAutoColor(editInitial?.colorMode === "auto" ? editInitial.colorHex : null);
      return;
    }
    const id = setTimeout(() => {
      const estimated = extractColor(picked.source, picked.size, crop);
      setAutoColor(estimated);
    }, 40);
    return () => clearTimeout(id);
  }, [enhanced, picked, crop, extractColor, editInitial?.colorMode, editInitial?.colorHex]);

  const decodeFile = async (file: File | undefined): Promise<PickedUploadImage | null> => {
    if (!file || busy.current) return null;
    setError("");
    if (!acceptsType(file.type)) {
      setError(t("assistant-lookupload.not_image"));
      return null;
    }
    const n = ++picks.current;
    setPhase("reading");
    let img: PickedUploadImage;
    try {
      img = await read(file);
    } catch {
      if (n === picks.current) {
        setPhase("idle");
        setError(t("assistant-lookupload.not_image"));
      }
      return null;
    }
    if (n !== picks.current || busy.current) {
      img.release();
      return null;
    }
    setPhase("idle");
    if (!bigEnough(img.size)) {
      img.release();
      setError(t("assistant-lookupload.too_small", { px: MIN_SIDE }));
      return null;
    }
    return img;
  };

  const pick = async (file: File | undefined) => {
    const img = await decodeFile(file);
    if (!img) return;
    kept.current?.release();
    kept.current = img;
    setPicked(img);
    const nextCrop = centeredCrop(img.size);
    setCrop(nextCrop);
    if (enhanced) {
      const estimated = extractColor(img.source, img.size, nextCrop);
      if (estimated) setAutoColor(estimated);
    }
  };

  const pickStateFile = async (state: PersonaState, file: File | undefined) => {
    const img = await decodeFile(file);
    if (!img) return;
    keptCropping.current?.release();
    keptCropping.current = img;
    const prev = stateDrafts[state];
    setCroppingState((cur) => ({
      state,
      image: img,
      crop: centeredCrop(img.size),
      mark: state === "idle" ? false : cur && cur.state === state ? cur.mark : prev.mode === "own" ? prev.mark : true,
      isNew: true,
    }));
  };

  const openExistingStateCrop = (state: PersonaState) => {
    const draft = stateDrafts[state];
    if (!draft.picked) return;
    setError("");
    setCroppingState({
      state,
      image: draft.picked,
      crop: draft.crop,
      mark: draft.mark,
      isNew: false,
    });
  };

  const cancelStateCrop = () => {
    if (croppingState?.isNew) {
      croppingState.image.release();
      if (keptCropping.current === croppingState.image) keptCropping.current = null;
    }
    setError("");
    setCroppingState(null);
  };

  const applyStateCrop = () => {
    if (!croppingState) return;
    const { state, image: img, crop: stateCrop, mark, isNew } = croppingState;
    if (isNew) {
      if (keptStates.current[state] && keptStates.current[state] !== img) keptStates.current[state]?.release();
      keptStates.current[state] = img;
      if (keptCropping.current === img) keptCropping.current = null;
    }
    setStateDrafts((prev) => ({
      ...prev,
      [state]: { mode: "own", mark: state === "idle" ? false : mark, picked: img, crop: stateCrop },
    }));
    setError("");
    setCroppingState(null);
  };

  // A file dropped onto a picker card opens the dialog with that file picked once as the main picture.
  useEffect(() => {
    if (!open || !initialFile || appliedInitialFile.current === initialFile) return;
    appliedInitialFile.current = initialFile;
    void pick(initialFile);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialFile]);

  const mainDrop = useFileDrop({ disabled: sending, onFile: (file) => void pick(file) });
  const stateCropDrop = useFileDrop({
    disabled: sending || !croppingState,
    onFile: (file) => {
      if (croppingState) void pickStateFile(croppingState.state, file);
    },
  });
  // A near miss on the title, the footer or the backdrop must not open the image in the tab either.
  useWindowFileDropGuard(open);

  const resetStateAuto = (state: PersonaState) => {
    keptStates.current[state]?.release();
    delete keptStates.current[state];
    setStateDrafts((prev) => ({
      ...prev,
      [state]: { mode: "auto", mark: state !== "idle", picked: null, crop: { x: 0, y: 0, zoom: 1 } },
    }));
  };

  const toggleStateMark = (state: PersonaState, mark: boolean) => {
    setStateDrafts((prev) => ({
      ...prev,
      [state]: { ...prev[state], mark },
    }));
  };

  const clearNewMain = () => {
    kept.current?.release();
    kept.current = null;
    setPicked(null);
  };

  const draftMeta: EditDraftMeta = {
    name,
    subtitle,
    hasMainImage: !!picked,
    colorMode,
    customColor,
    states: Object.fromEntries(
      PERSONA_STATES.map((st) => [
        st.id,
        { mode: stateDrafts[st.id].mode, mark: stateDrafts[st.id].mark, hasNewImage: !!stateDrafts[st.id].picked },
      ]),
    ) as EditDraftMeta["states"],
  };

  const ready = isEdit
    ? !!editInitial && canSend(name, subtitle) && hasPatchChanges(editInitial, draftMeta) && phase === "idle"
    : !!picked && canSend(name, subtitle) && phase === "idle";

  const submit = async () => {
    if (busy.current || !ready) return;
    if (!isEdit && !picked) return;
    busy.current = true;
    const ctl = new AbortController();
    abort.current = ctl;
    setError("");
    setProgress(0);
    setPhase("preparing");
    try {
      await paint();
      if (ctl.signal.aborted) return;

      const onProgress = (sent: number, total: number) => {
        const f = total > 0 ? Math.min(1, sent / total) : 0;
        setProgress(f);
        // every byte is there: the hub is making the package now
        if (f >= 1) setPhase("making");
      };

      if (isEdit && edit && editInitial) {
        const encoded: EditEncodedPayload = {};
        try {
          if (picked) {
            encoded.main = await encodeUploadImage(picked, crop, { allowAnimated: true, maxSide: EXPORT_MAX, render });
          }
          const stateEncoded: EditEncodedPayload["states"] = {};
          for (const { id } of PERSONA_STATES) {
            const d = stateDrafts[id];
            if (d.mode === "own" && d.picked) {
              stateEncoded[id] = await encodeUploadImage(d.picked, d.crop, { allowAnimated: true, maxSide: STATE_EXPORT_MAX, render });
            }
          }
          if (Object.keys(stateEncoded).length > 0) encoded.states = stateEncoded;
        } catch (e) {
          setError(t("assistant-lookupload.export_failed", { error: e instanceof Error ? e.message : String(e) }));
          setPhase("idle");
          return;
        }
        if (ctl.signal.aborted) return;
        const body = buildPatchBody(editInitial, draftMeta, encoded);
        setPhase("sending");
        const answer = await patch(edit.id, body, { signal: ctl.signal, onProgress });
        if (ctl.signal.aborted) return;
        onEdited?.(answer.look);
        onClose();
      } else if (picked) {
        let mainEncoded: { image: string; crop?: { x: number; y: number; side: number } };
        const statesPayload: Partial<Record<PersonaState, UploadStateInput>> = {};
        try {
          mainEncoded = await encodeUploadImage(picked, crop, { allowAnimated: enhanced, maxSide: EXPORT_MAX, render });
          if (enhanced) {
            for (const { id } of PERSONA_STATES) {
              const d = stateDrafts[id];
              if (d.mode === "own" && d.picked) {
                const encState = await encodeUploadImage(d.picked, d.crop, { allowAnimated: true, maxSide: STATE_EXPORT_MAX, render });
                statesPayload[id] = id === "idle" ? encState : { ...encState, mark: d.mark };
              }
            }
          }
        } catch (e) {
          setError(t("assistant-lookupload.export_failed", { error: e instanceof Error ? e.message : String(e) }));
          setPhase("idle");
          return;
        }
        if (ctl.signal.aborted) return;
        const body = buildUploadBody({
          name,
          subtitle,
          image: mainEncoded.image,
          crop: mainEncoded.crop,
          supportsStates: enhanced,
          colorMode,
          customColor,
          states: statesPayload,
        });
        setPhase("sending");
        onSend?.();
        const answer = await send(body, { signal: ctl.signal, onProgress });
        if (ctl.signal.aborted) return;
        onUploaded(answer);
        onClose();
      }
    } catch (e) {
      if (ctl.signal.aborted || isAbort(e)) return;
      setError(refusalText(e));
      setPhase("idle");
    } finally {
      busy.current = false;
      if (abort.current === ctl) abort.current = null;
    }
  };

  // Closing stops a request still going out; one the hub already has may still make the avatar (its pushes show it).
  const close = () => {
    if (croppingState) {
      cancelStateCrop();
      return;
    }
    abort.current?.abort();
    onClose();
  };

  const nameLength = textLength(name.trim());
  const subtitleLength = textLength(subtitle.trim());
  const enter = (e: React.KeyboardEvent) => {
    if (e.key !== "Enter" || e.nativeEvent.isComposing) return;
    e.preventDefault();
    void submit();
  };
  const top = maxZoom(picked?.size ?? { w: 1, h: 1 });
  const percent = Math.round(progress * 100);

  // Sub-view: cropping a state's own picture
  if (croppingState) {
    const stateTitle = PERSONA_STATES.find((s) => s.id === croppingState.state)?.title ?? croppingState.state;
    const stateTop = maxZoom(croppingState.image.size);
    const setStateCrop: React.Dispatch<React.SetStateAction<Crop>> = (next) => {
      setCroppingState((prev) => prev ? { ...prev, crop: typeof next === "function" ? next(prev.crop) : next } : null);
    };
    return (
      <Dialog
        open={open}
        onClose={cancelStateCrop}
        title={t("assistant-lookupload.crop_state_title", { state: stateTitle })}
        width={400}
        footer={
          <>
            <Button tone="quiet" onClick={cancelStateCrop}>{t("assistant-lookupload.back")}</Button>
            <Button tone="primary" onClick={applyStateCrop}>{t("assistant-lookupload.apply_crop")}</Button>
          </>
        }
      >
        <div className="flex flex-col gap-3" {...fileDropGuard}>
          <div className="mx-auto w-full max-w-(--crop-box)" {...stateCropDrop.props}>
            <CropBox image={croppingState.image} crop={croppingState.crop} setCrop={setStateCrop} disabled={sending} over={stateCropDrop.over} hintId={`${ids}-state-hint`} />
          </div>
          <div className="mx-auto flex w-full max-w-(--crop-box) flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <Icon name="image" size={12} className="shrink-0 text-ink-3" />
              <input
                type="range"
                aria-label={t("assistant-lookupload.zoom")}
                min={1}
                max={stateTop}
                step="any"
                value={croppingState.crop.zoom}
                disabled={stateTop <= 1}
                onChange={(e) => {
                  const z = Number(e.target.value);
                  setStateCrop((c) => zoomCrop(croppingState.image.size, c, z, 1));
                }}
                className="min-w-0 flex-1 accent-[var(--accent)] disabled:opacity-50"
              />
              <Icon name="image" size={16} className="shrink-0 text-ink-3" />
            </div>
            <p id={`${ids}-state-hint`} className="text-center text-submeta leading-snug text-ink-3">{t("assistant-lookupload.crop_hint")}</p>
          </div>
          {croppingState.state !== "idle" && (
            <div className="flex items-center justify-between gap-3 rounded-card bg-inset px-3 py-2.5 shadow-hairline">
              <span className="text-meta text-ink">{t("assistant-lookupload.state_mark")}</span>
              <Switch
                on={croppingState.mark}
                onChange={(v) => setCroppingState((prev) => (prev ? { ...prev, mark: v } : null))}
                label={t("assistant-lookupload.state_mark_label", { state: stateTitle })}
              />
            </div>
          )}
          {error && <p role="alert" className="text-label leading-relaxed break-words text-red">{error}</p>}
        </div>
      </Dialog>
    );
  }

  const footer = (
    <>
      {picked && !sending && (
        <Button tone="quiet" icon="image" className="-ml-3" busy={phase === "reading"} onClick={() => fileInput.current?.click()}>{t("assistant-lookupload.change")}</Button>
      )}
      <span className="mr-auto flex min-w-0 flex-1 items-center gap-2 text-[12px] text-ink-3" aria-live="polite">
        {phase === "sending" && (
          <>
            <span role="progressbar" aria-label={t("assistant-lookupload.progress")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} className="h-1 w-full max-w-20 min-w-6 overflow-hidden rounded-full bg-hover-2">
              <span className="block h-full rounded-full bg-accent transition-[width] duration-150" style={{ width: `${percent}%` }} />
            </span>
            <span className="truncate whitespace-nowrap tabular-nums">{t("assistant-lookupload.uploading", { percent })}</span>
          </>
        )}
        {(phase === "preparing" || phase === "making") && (
          <span className="truncate whitespace-nowrap">
            {t(phase === "preparing" ? "assistant-lookupload.preparing" : isEdit ? "assistant-lookupload.saving" : "assistant-lookupload.making")}
          </span>
        )}
      </span>
      <Button tone="quiet" onClick={close}>{t("common.cancel")}</Button>
      <Button tone="primary" icon={isEdit ? "check" : "upload"} busy={sending} disabled={!ready} onClick={() => void submit()}>
        {isEdit ? t("common.save") : t("assistant-lookupload.upload")}
      </Button>
    </>
  );

  const initialOwnMap = Object.fromEntries(
    PERSONA_STATES.map((st) => [st.id, editInitial?.states[st.id].own ?? false]),
  ) as Record<PersonaState, boolean>;

  return (
    <Dialog open={open} onClose={close} title={t(isEdit ? "assistant-lookupload.edit_title" : "assistant-lookupload.title")} width={400} footer={footer}>
      <div className="flex flex-col gap-3" {...fileDropGuard}>
        {isEdit && edit && !picked ? (
          <div
            data-testid="look-edit-preview"
            data-drop-over={mainDrop.over ? "true" : undefined}
            {...mainDrop.props}
            className={cx(
              "flex items-center gap-3 rounded-card border border-dashed px-3 py-3 transition-colors duration-150",
              mainDrop.over ? "border-accent bg-accent-tint" : "border-line-strong bg-inset",
            )}
          >
            <PersonaAvatar size={64} persona={edit} fallback={<MochiFace size={64} />} />
            <div className="min-w-0 flex-1">
              <Button tone="secondary" size="sm" icon="image" busy={phase === "reading"} disabled={sending} onClick={() => fileInput.current?.click()}>
                {t("assistant-lookupload.change")}
              </Button>
              <p className="mt-1 text-submeta leading-snug text-ink-3">{t("assistant-lookupload.choose_hint")}</p>
            </div>
          </div>
        ) : (
          <div className="mx-auto w-full max-w-(--crop-box)" {...mainDrop.props}>
            {picked ? (
              <CropBox image={picked} crop={crop} setCrop={setCrop} disabled={sending} over={mainDrop.over} hintId={`${ids}-hint`} />
            ) : (
              <div
                data-testid="look-upload-drop"
                data-drop-over={mainDrop.over ? "true" : undefined}
                className={cx(
                  "flex aspect-square w-full flex-col items-center justify-center gap-3 rounded-card border border-dashed p-6 text-center transition-colors duration-150",
                  mainDrop.over ? "border-accent bg-accent-tint" : "border-line-strong bg-inset",
                )}
              >
                <span className="flex size-14 items-center justify-center rounded-full bg-field text-ink-3">
                  <Icon name="image" size={24} />
                </span>
                <Button tone="secondary" icon="upload" busy={phase === "reading"} onClick={() => fileInput.current?.click()}>{t("assistant-lookupload.choose")}</Button>
                <p className="max-w-56 text-[11.5px] leading-relaxed text-ink-3">{t("assistant-lookupload.choose_hint")}</p>
              </div>
            )}
          </div>
        )}
        {picked && (
          <div className="mx-auto flex w-full max-w-(--crop-box) flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <Icon name="image" size={12} className="shrink-0 text-ink-3" />
              <input
                type="range"
                aria-label={t("assistant-lookupload.zoom")}
                min={1}
                max={top}
                step="any"
                value={crop.zoom}
                disabled={sending || top <= 1}
                onChange={(e) => {
                  const z = Number(e.target.value);
                  setCrop((c) => zoomCrop(picked.size, c, z, 1));
                }}
                className="min-w-0 flex-1 accent-[var(--accent)] disabled:opacity-50"
              />
              <Icon name="image" size={16} className="shrink-0 text-ink-3" />
            </div>
            <p id={`${ids}-hint`} className="text-center text-[11.5px] leading-snug text-ink-3">{t("assistant-lookupload.crop_hint")}</p>
            {isEdit && (
              <div className="flex justify-center">
                <Button tone="quiet" size="sm" disabled={sending} onClick={clearNewMain}>
                  {t("assistant-lookupload.keep_current")}
                </Button>
              </div>
            )}
          </div>
        )}
        <Field id={`${ids}-name`} label={t("assistant-lookupload.name")} count={nameLength} max={NAME_MAX}>
          <input
            id={`${ids}-name`}
            className={inputClass}
            value={name}
            disabled={sending}
            autoComplete="off"
            aria-invalid={nameLength > NAME_MAX || undefined}
            aria-describedby={`${ids}-name-count`}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={enter}
          />
        </Field>
        <Field id={`${ids}-subtitle`} label={t("assistant-lookupload.subtitle")} count={subtitleLength} max={SUBTITLE_MAX}>
          <input
            id={`${ids}-subtitle`}
            className={inputClass}
            value={subtitle}
            disabled={sending}
            autoComplete="off"
            aria-invalid={subtitleLength > SUBTITLE_MAX || undefined}
            aria-describedby={`${ids}-subtitle-count`}
            onChange={(e) => setSubtitle(e.target.value)}
            onKeyDown={enter}
          />
        </Field>
        {enhanced && (
          <div className="divide-y divide-line overflow-hidden rounded-card bg-inset shadow-hairline">
            <ThemeColorSection
              open={colorOpen}
              onToggle={() => setColorOpen((o) => !o)}
              mode={colorMode}
              onModeChange={(next) => {
                setColorMode(next);
                if (next === "custom" && autoColor && !customTouched) setCustomColor(autoColor);
              }}
              autoColor={autoColor}
              customColor={customColor}
              onCustomColorChange={(hex) => {
                setCustomTouched(true);
                setCustomColor(hex);
              }}
              isEdit={isEdit}
              disabled={sending}
            />
            <StatePicturesSection
              open={statesOpen}
              onToggle={() => setStatesOpen((o) => !o)}
              drafts={stateDrafts}
              initialOwn={initialOwnMap}
              mainPicked={picked}
              mainCrop={crop}
              editPersona={edit}
              disabled={sending}
              onPickFile={(state, file) => void pickStateFile(state, file)}
              onEditCrop={openExistingStateCrop}
              onToggleMark={toggleStateMark}
              onResetAuto={resetStateAuto}
            />
          </div>
        )}
        {error && <p role="alert" className="text-[12px] leading-relaxed break-words text-red">{error}</p>}
        <input
          ref={fileInput}
          type="file"
          accept={UPLOAD_TYPES.join(",")}
          hidden
          data-testid="look-upload-file"
          onChange={(e) => {
            void pick(e.target.files?.[0]);
            e.target.value = ""; // the same file again is a change too
          }}
        />
      </div>
    </Dialog>
  );
}

/** A labelled field with its length against the hub's limit (red past it). */
function Field({ id, label, count, max, children }: { id: string; label: string; count: number; max: number; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <label htmlFor={id} className="truncate text-[12px] font-medium text-ink-2">{label}</label>
        <span id={`${id}-count`} className={cx("shrink-0 text-[11px] tabular-nums", count > max ? "text-red" : "text-ink-3")}>
          {count}/{max}
        </span>
      </div>
      {children}
    </div>
  );
}

/** The box's width in CSS pixels, kept as it changes. */
function useWidth(ref: React.RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setWidth(el.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return width;
}

/**
 * The square the avatar is cut from, with its circle drawn over it: a still picture (or any picture under Reduce
 * Motion) is drawn into a canvas at the box's own resolution; an animated picture plays live inside the circle when
 * motion is allowed. Moved by dragging, zoomed by the wheel or a pinch, and from the keyboard by arrow keys and + / −.
 */
function CropBox({ image, crop, setCrop, disabled, over, hintId }: { image: PickedUploadImage; crop: Crop; setCrop: React.Dispatch<React.SetStateAction<Crop>>; disabled: boolean; over: boolean; hintId: string }) {
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const view = useWidth(box);
  const reducedMotion = useReducedMotion();
  const playAnimated = !!image.animated && !!image.url && !reducedMotion;
  const size = image.size;
  const points = useRef(new Map<number, { x: number; y: number }>());

  useLayoutEffect(() => {
    if (playAnimated) return;
    const c = canvas.current;
    if (!c || view <= 0) return;
    const scale = Math.max(1, Math.min(3, typeof devicePixelRatio === "number" ? devicePixelRatio : 1));
    const px = Math.round(view * scale);
    if (c.width !== px) {
      c.width = px;
      c.height = px;
    }
    const g = c.getContext("2d");
    if (!g) return;
    const r = exportRect(size, clampCrop(size, crop));
    g.imageSmoothingEnabled = true;
    g.imageSmoothingQuality = "high";
    g.clearRect(0, 0, px, px);
    g.drawImage(image.source, r.sx, r.sy, r.side, r.side, 0, 0, px, px);
  }, [image, size, crop, view, playAnimated]);

  // The wheel zooms about the pointer (a trackpad's pinch comes as a wheel with Ctrl). React's own wheel listener is
  // passive, so it could not keep the dialog from scrolling.
  useEffect(() => {
    const el = box.current;
    if (!el || disabled) return;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      if (r.width <= 0) return;
      const delta = e.deltaY * (e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? r.height : 1);
      const f = Math.exp(-delta * (e.ctrlKey ? 0.01 : 0.002));
      setCrop((c) => zoomCrop(size, c, c.zoom * f, r.width, e.clientX - r.left, e.clientY - r.top));
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
  }, [size, setCrop, disabled]);

  const down = (e: React.PointerEvent<HTMLDivElement>) => {
    if (disabled || (e.pointerType === "mouse" && e.button !== 0)) return;
    e.currentTarget.setPointerCapture?.(e.pointerId);
    points.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
  };
  const move = (e: React.PointerEvent<HTMLDivElement>) => {
    const all = points.current;
    const was = all.get(e.pointerId);
    if (!was || !box.current) return;
    const now = { x: e.clientX, y: e.clientY };
    const r = box.current.getBoundingClientRect();
    all.set(e.pointerId, now);
    if (r.width <= 0) return;
    const other = [...all.entries()].find(([id]) => id !== e.pointerId)?.[1];
    if (!other) {
      setCrop((c) => panCrop(size, c, now.x - was.x, now.y - was.y, r.width));
      return;
    }
    // a pinch: zoom by how much the fingers spread, about where they were, then follow where they went
    const apart = (a: { x: number; y: number }) => Math.hypot(a.x - other.x, a.y - other.y);
    const before = apart(was);
    if (before <= 0) return;
    const m0 = { x: (was.x + other.x) / 2 - r.left, y: (was.y + other.y) / 2 - r.top };
    const m1 = { x: (now.x + other.x) / 2 - r.left, y: (now.y + other.y) / 2 - r.top };
    setCrop((c) => panCrop(size, zoomCrop(size, c, (c.zoom * apart(now)) / before, r.width, m0.x, m0.y), m1.x - m0.x, m1.y - m0.y, r.width));
  };
  const up = (e: React.PointerEvent<HTMLDivElement>) => {
    points.current.delete(e.pointerId);
  };
  const key = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (disabled || view <= 0) return;
    const step = e.shiftKey ? KEY_STEP * 4 : KEY_STEP;
    const moves: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] };
    if (moves[e.key]) {
      e.preventDefault();
      const [dx, dy] = moves[e.key];
      setCrop((c) => panCrop(size, c, dx, dy, view));
    } else if (e.key === "+" || e.key === "=" || e.key === "-" || e.key === "_") {
      e.preventDefault();
      const f = e.key === "-" || e.key === "_" ? 1 / KEY_ZOOM : KEY_ZOOM;
      setCrop((c) => zoomCrop(size, c, c.zoom * f, view));
    }
  };

  return (
    <div
      ref={box}
      role="group"
      tabIndex={disabled ? -1 : 0}
      aria-label={t("assistant-lookupload.crop_label")}
      aria-describedby={hintId}
      data-testid="look-upload-crop"
      data-drop-over={over ? "true" : undefined}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={up}
      onLostPointerCapture={up}
      onKeyDown={key}
      className={cx(
        "relative aspect-square w-full touch-none overflow-hidden rounded-card bg-field outline-none select-none focus-visible:shadow-[0_0_0_2px_var(--accent)]",
        disabled ? "opacity-80" : "cursor-grab active:cursor-grabbing",
        over && "shadow-[0_0_0_2px_var(--accent)]",
      )}
    >
      {playAnimated ? (
        <img
          src={image.url}
          alt=""
          aria-hidden
          draggable={false}
          data-testid="look-upload-animated"
          className="pointer-events-none absolute max-h-none max-w-none"
          style={cropImageStyle(size, crop)}
        />
      ) : (
        <canvas ref={canvas} aria-hidden className="pointer-events-none absolute inset-0 size-full" />
      )}
      <span aria-hidden className="crop-ring pointer-events-none absolute inset-0" />
    </div>
  );
}
