// Secondary controls of the upload / edit dialog (#15449): Theme color (Automatic swatch or custom pick) and
// Pictures for each state (five rows with playing previews for animated pictures and a status-mark switch for non-idle
// own pictures). Lazy: imported only by LookUpload.tsx, never on the first screen.
import { useLayoutEffect, useRef } from "react";
import { t } from "../../i18n";
import { clampCrop, exportRect, UPLOAD_TYPES, type Crop } from "../../lib/look-upload";
import { cropImageStyle, type PickedUploadImage } from "../../lib/look-upload-states";
import { PERSONA_STATES, type PersonaInfo, type PersonaState } from "../../lib/persona";
import { Button, Collapse, cx, Switch } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ACCENT_PRESETS, PRESET_COLORS } from "../../ui/look";
import { useReducedMotion } from "./face-motion";
import { useFileDrop } from "./lookDrop";
import { Segmented } from "./parts";
import { PersonaAvatar, PersonaDot } from "./Persona";

export interface StateDraft {
  mode: "auto" | "own";
  mark: boolean;
  picked: PickedUploadImage | null;
  crop: Crop;
}

export type StateDrafts = Record<PersonaState, StateDraft>;

/** Renders a cropped still frame (frame 0) of a picked image into a square canvas. */
function CroppedCanvas({ image, crop, size, grayscale }: { image: PickedUploadImage; crop: Crop; size: number; grayscale?: boolean }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useLayoutEffect(() => {
    const c = ref.current;
    if (!c) return;
    const scale = Math.max(1, Math.min(3, typeof devicePixelRatio === "number" ? devicePixelRatio : 1));
    const px = Math.round(size * scale);
    if (c.width !== px) {
      c.width = px;
      c.height = px;
    }
    const g = c.getContext("2d");
    if (!g) return;
    const r = exportRect(image.size, clampCrop(image.size, crop));
    g.imageSmoothingEnabled = true;
    g.imageSmoothingQuality = "high";
    g.clearRect(0, 0, px, px);
    g.drawImage(image.source, r.sx, r.sy, r.side, r.side, 0, 0, px, px);
  }, [image, crop, size]);
  return <canvas ref={ref} aria-hidden className={cx("size-full", grayscale && "grayscale opacity-75")} />;
}

/** Circular 40 px preview for one dot state: plays when animated and motion is allowed; frame 0 under Reduce Motion. */
function StatePreviewDot({
  state,
  draft,
  initialOwn,
  mainPicked,
  mainCrop,
  editPersona,
}: {
  state: PersonaState;
  draft: StateDraft;
  initialOwn: boolean;
  mainPicked: PickedUploadImage | null;
  mainCrop: Crop;
  editPersona?: PersonaInfo;
}) {
  const reducedMotion = useReducedMotion();
  const offline = state === "offline";

  // 1. Newly picked picture for this state
  if (draft.mode === "own" && draft.picked) {
    const play = !!draft.picked.animated && !!draft.picked.url && !reducedMotion;
    return (
      <span data-state-preview={state} data-playing={play ? "true" : "false"} className="relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-field shadow-hairline">
        {play ? (
          <img src={draft.picked.url} alt="" aria-hidden draggable={false} className="pointer-events-none absolute max-h-none max-w-none" style={cropImageStyle(draft.picked.size, draft.crop)} />
        ) : (
          <CroppedCanvas image={draft.picked} crop={draft.crop} size={40} />
        )}
      </span>
    );
  }

  // 2. Existing hub dot in Edit mode (when main image wasn't replaced and an own state wasn't reset back to automatic)
  if (editPersona && !mainPicked && !(initialOwn && draft.mode === "auto")) {
    const frames = editPersona.manifest.dot.states[state]?.frames ?? 1;
    const play = !reducedMotion && frames > 1;
    return (
      <span data-state-preview={state} data-playing={play ? "true" : "false"} className="relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-field shadow-hairline">
        <PersonaDot size={40} state={state} persona={editPersona} />
      </span>
    );
  }

  // 3. Automatic state derived from a newly picked main picture (offline stays still frame 0)
  if (mainPicked) {
    const play = !offline && !!mainPicked.animated && !!mainPicked.url && !reducedMotion;
    return (
      <span data-state-preview={state} data-playing={play ? "true" : "false"} className="relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-field shadow-hairline">
        {play ? (
          <img src={mainPicked.url} alt="" aria-hidden draggable={false} className="pointer-events-none absolute max-h-none max-w-none" style={cropImageStyle(mainPicked.size, mainCrop)} />
        ) : (
          <CroppedCanvas image={mainPicked} crop={mainCrop} size={40} grayscale={offline} />
        )}
      </span>
    );
  }

  // 4. Edit mode where an own state was reset to Automatic (main picture unchanged)
  if (editPersona) {
    return (
      <span data-state-preview={state} data-playing="false" className={cx("relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-field shadow-hairline", offline && "grayscale opacity-75")}>
        <PersonaAvatar size={40} persona={editPersona} />
      </span>
    );
  }

  // 5. Create mode before a main picture is picked
  return (
    <span data-state-preview={state} data-playing="false" className="relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-field text-ink-3 shadow-hairline">
      <Icon name="image" size={16} />
    </span>
  );
}

/** Secondary disclosure entry: Theme color (Automatic swatch from the crop, or a picked color). */
export function ThemeColorSection({
  open,
  onToggle,
  mode,
  onModeChange,
  autoColor,
  customColor,
  onCustomColorChange,
  isEdit = false,
  disabled,
}: {
  open: boolean;
  onToggle: () => void;
  mode: "auto" | "custom";
  onModeChange: (mode: "auto" | "custom") => void;
  autoColor: string | null;
  customColor: string;
  onCustomColorChange: (hex: string) => void;
  isEdit?: boolean;
  disabled: boolean;
}) {
  const shownSwatch = mode === "custom" ? customColor : autoColor;
  return (
    <div>
      <button
        type="button"
        data-testid="look-color-toggle"
        aria-expanded={open}
        onClick={onToggle}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition-colors hover:bg-hover"
      >
        <span className="min-w-0 truncate text-meta font-medium text-ink">{t("assistant-lookupload.theme_color")}</span>
        <span className="flex shrink-0 items-center gap-2">
          {shownSwatch && (
            <span
              data-testid="look-color-summary-swatch"
              data-color={shownSwatch}
              aria-hidden
              className="size-3.5 shrink-0 rounded-full shadow-hairline"
              style={{ background: shownSwatch }}
            />
          )}
          <span className="text-label whitespace-nowrap text-ink-3">
            {mode === "auto" ? t("assistant-lookupload.auto") : customColor}
          </span>
          <Icon name="chevron" size={13} className={cx("shrink-0 text-ink-3 transition-transform duration-200", open && "rotate-180")} />
        </span>
      </button>
      <Collapse open={open}>
        <div className="flex flex-col gap-2.5 border-t border-line px-3 py-2.5">
          <Segmented<"auto" | "custom">
            label={t("assistant-lookupload.theme_color")}
            value={mode}
            compact
            options={[
              { v: "auto", label: t("assistant-lookupload.auto") },
              { v: "custom", label: t("assistant-lookupload.color_custom") },
            ]}
            onChange={(next) => !disabled && onModeChange(next)}
          />
          {mode === "auto" ? (
            <div className="flex items-center gap-2.5 rounded-control bg-surface px-2.5 py-2 shadow-hairline">
              {autoColor ? (
                <span
                  data-testid="look-auto-swatch"
                  data-color={autoColor}
                  aria-hidden
                  className="size-6 shrink-0 rounded-full shadow-hairline"
                  style={{ background: autoColor }}
                />
              ) : (
                <span aria-hidden className="size-6 shrink-0 rounded-full bg-field shadow-hairline" />
              )}
              <span className="min-w-0 text-label text-ink-2">
                {autoColor || isEdit ? t("assistant-lookupload.color_auto_hint") : t("assistant-lookupload.color_auto_empty")}
              </span>
            </div>
          ) : (
            <div className="flex flex-col gap-2 rounded-control bg-surface px-2.5 py-2 shadow-hairline">
              <div className="flex flex-wrap items-center gap-2">
                {(() => {
                  const upper = customColor.toUpperCase();
                  const isPreset = ACCENT_PRESETS.some((p) => PRESET_COLORS[p].accent.light.toUpperCase() === upper);
                  return (
                    <>
                      {ACCENT_PRESETS.map((preset) => {
                        const hex = PRESET_COLORS[preset].accent.light.toUpperCase();
                        const selected = upper === hex;
                        return (
                          <button
                            key={preset}
                            type="button"
                            disabled={disabled}
                            aria-label={t("assistant-lookupload.color_preset", { name: t(`ui-look.${preset}`) })}
                            aria-pressed={selected}
                            onClick={() => onCustomColorChange(hex)}
                            className={cx(
                              "touch-hit flex size-7 shrink-0 items-center justify-center rounded-full text-white shadow-hairline transition-transform active:scale-95 disabled:opacity-50",
                              selected && "ring-2 ring-ink ring-offset-1 ring-offset-surface",
                            )}
                            style={{ background: hex }}
                          >
                            {selected && <Icon name="check" size={13} stroke={2.6} />}
                          </button>
                        );
                      })}
                      <label
                        className={cx(
                          "touch-hit relative flex size-7 shrink-0 cursor-pointer items-center justify-center overflow-hidden rounded-full shadow-hairline transition-transform active:scale-95",
                          !isPreset ? "text-white ring-2 ring-ink ring-offset-1 ring-offset-surface" : "border border-dashed border-line-strong bg-field text-ink-2 hover:bg-hover",
                        )}
                        style={!isPreset ? { background: customColor } : undefined}
                        title={t("assistant-lookupload.color_pick")}
                      >
                        <Icon
                          name={isPreset ? "plus" : "pencil"}
                          size={isPreset ? 13 : 12}
                          stroke={2.3}
                          className={!isPreset ? "drop-shadow-[0_1px_1px_rgba(0,0,0,0.45)]" : undefined}
                        />
                        <input
                          type="color"
                          data-testid="look-color-input"
                          aria-label={t("assistant-lookupload.color_pick")}
                          disabled={disabled}
                          value={customColor.toLowerCase()}
                          onChange={(e) => onCustomColorChange(e.target.value.toUpperCase())}
                          className="absolute inset-0 size-full cursor-pointer opacity-0"
                        />
                      </label>
                    </>
                  );
                })()}
                <span className="ml-auto font-mono text-label text-ink-2 tabular-nums">{customColor}</span>
              </div>
            </div>
          )}
        </div>
      </Collapse>
    </div>
  );
}

/** One state picture row: accepts a dropped image directly onto the row and offers a state-labelled Choose / Change button. */
function StateRow({
  state,
  title,
  draft,
  initialOwn,
  mainPicked,
  mainCrop,
  editPersona,
  disabled,
  onPickFile,
  onEditCrop,
  onToggleMark,
  onResetAuto,
}: {
  state: PersonaState;
  title: string;
  draft: StateDraft;
  initialOwn: boolean;
  mainPicked: PickedUploadImage | null;
  mainCrop: Crop;
  editPersona?: PersonaInfo;
  disabled: boolean;
  onPickFile: (state: PersonaState, file: File | undefined) => void;
  onEditCrop: (state: PersonaState) => void;
  onToggleMark: (state: PersonaState, on: boolean) => void;
  onResetAuto: (state: PersonaState) => void;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const drop = useFileDrop({ disabled, onFile: (file) => onPickFile(state, file) });
  const isOwn = draft.mode === "own";

  return (
    <div className="py-0.5 first:pt-0 last:pb-0">
      <div
        data-state-row={state}
        data-drop-over={drop.over ? "true" : undefined}
        {...drop.props}
        className={cx(
          "-mx-2 rounded-control border px-2 py-1.5 transition-colors duration-150",
          drop.over ? "border-dashed border-accent bg-accent-tint" : "border-transparent",
        )}
      >
        <div className="flex items-center gap-2.5">
          <StatePreviewDot
            state={state}
            draft={draft}
            initialOwn={initialOwn}
            mainPicked={mainPicked}
            mainCrop={mainCrop}
            editPersona={editPersona}
          />
          <div className="min-w-0 flex-1">
            <div className="truncate text-meta font-medium text-ink">{title}</div>
            <div className="truncate text-submeta text-ink-3">
              {isOwn ? t("assistant-lookupload.state_custom") : t("assistant-lookupload.auto")}
            </div>
          </div>
          <Button
            size="sm"
            tone="secondary"
            disabled={disabled}
            aria-label={t(isOwn ? "assistant-lookupload.state_change_label" : "assistant-lookupload.state_choose_label", { state: title })}
            onClick={() => fileRef.current?.click()}
          >
            {isOwn ? t("assistant-lookupload.state_change") : t("assistant-lookupload.state_choose")}
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept={UPLOAD_TYPES.join(",")}
            hidden
            data-testid={`look-upload-state-file-${state}`}
            onChange={(e) => {
              onPickFile(state, e.target.files?.[0]);
              e.target.value = "";
            }}
          />
        </div>
        {isOwn && (
          <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2 pl-12">
            {state !== "idle" && (
              <span className="flex items-center gap-2">
                <span className="text-label whitespace-nowrap text-ink-2">{t("assistant-lookupload.state_mark")}</span>
                <Switch
                  on={draft.mark}
                  onChange={(v) => onToggleMark(state, v)}
                  disabled={disabled}
                  label={t("assistant-lookupload.state_mark_label", { state: title })}
                />
              </span>
            )}
            <span className="ml-auto flex shrink-0 items-center gap-1">
              {draft.picked && (
                <Button size="sm" tone="quiet" disabled={disabled} onClick={() => onEditCrop(state)}>
                  {t("assistant-lookupload.state_crop")}
                </Button>
              )}
              <Button size="sm" tone="quiet" disabled={disabled} onClick={() => onResetAuto(state)}>
                {t("assistant-lookupload.state_use_auto")}
              </Button>
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

/** Secondary disclosure entry: Pictures for each state (five rows, Automatic by default). */
export function StatePicturesSection({
  open,
  onToggle,
  drafts,
  initialOwn,
  mainPicked,
  mainCrop,
  editPersona,
  disabled,
  onPickFile,
  onEditCrop,
  onToggleMark,
  onResetAuto,
}: {
  open: boolean;
  onToggle: () => void;
  drafts: StateDrafts;
  initialOwn: Record<PersonaState, boolean>;
  mainPicked: PickedUploadImage | null;
  mainCrop: Crop;
  editPersona?: PersonaInfo;
  disabled: boolean;
  onPickFile: (state: PersonaState, file: File | undefined) => void;
  onEditCrop: (state: PersonaState) => void;
  onToggleMark: (state: PersonaState, on: boolean) => void;
  onResetAuto: (state: PersonaState) => void;
}) {
  const customCount = PERSONA_STATES.filter((st) => drafts[st.id].mode === "own").length;

  return (
    <div>
      <button
        type="button"
        data-testid="look-states-toggle"
        aria-expanded={open}
        onClick={onToggle}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition-colors hover:bg-hover"
      >
        <span className="min-w-0 truncate text-meta font-medium text-ink">{t("assistant-lookupload.states_title")}</span>
        <span className="flex shrink-0 items-center gap-2">
          <span className="text-label whitespace-nowrap text-ink-3">
            {customCount === 0 ? t("assistant-lookupload.auto") : t("assistant-lookupload.states_custom_count", { count: customCount })}
          </span>
          <Icon name="chevron" size={13} className={cx("shrink-0 text-ink-3 transition-transform duration-200", open && "rotate-180")} />
        </span>
      </button>
      <Collapse open={open}>
        <div className="border-t border-line px-3 py-2">
          <p className="pb-1.5 text-submeta leading-snug text-ink-3">{t("assistant-lookupload.states_hint")}</p>
          <div className="divide-y divide-line">
            {PERSONA_STATES.map((st) => (
              <StateRow
                key={st.id}
                state={st.id}
                title={st.title}
                draft={drafts[st.id]}
                initialOwn={initialOwn[st.id]}
                mainPicked={mainPicked}
                mainCrop={mainCrop}
                editPersona={editPersona}
                disabled={disabled}
                onPickFile={onPickFile}
                onEditCrop={onEditCrop}
                onToggleMark={onToggleMark}
                onResetAuto={onResetAuto}
              />
            ))}
          </div>
        </div>
      </Collapse>
    </div>
  );
}
