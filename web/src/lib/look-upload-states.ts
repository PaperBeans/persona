// Per-state pictures, animated uploads, theme color and PATCH editing for uploaded avatars (#15449).
// Kept in a lazy module imported only by the upload/edit dialog and its tests so none of it lands on the first screen.
import { enc } from "./api";
import { normalizeHexColor } from "./look-colors";
import {
  clampCrop, cropSide, EXPORT_MAX, exportCrop, MIN_SIDE, readImage, uploadBody, uploadLook, xhrPost,
  type Crop, type PickedImage, type ProgressPost, type Size, type UploadAnswer, type UploadBody,
} from "./look-upload";
import { PERSONA_STATES, type LookView, type PersonaManifest, type PersonaState } from "./persona";

/** State pictures are scaled down at most to this side (still PNGs); main picture stays at EXPORT_MAX (1024). */
export const STATE_EXPORT_MAX = 512;

/** Square crop sent alongside an original animated image, in the image's own integer pixel coordinates. */
export interface CropRect {
  x: number;
  y: number;
  side: number;
}

/** A picked file decoded for cropping, with animation detection and live preview URL when animated. */
export interface PickedUploadImage extends PickedImage {
  animated?: boolean;
  file?: Blob;
  /** Original raw file bytes kept when animated so encoding never re-reads the Blob. */
  bytes?: Uint8Array;
  url?: string;
}

/** One state entry in POST /api/v1/looks/uploads. */
export interface UploadStateInput {
  image: string;
  crop?: CropRect;
  mark?: boolean;
}

/** Extended POST /api/v1/looks/uploads body when the hub lists look_uploads_states. */
export interface ExtendedUploadBody extends UploadBody {
  crop?: CropRect;
  states?: Partial<Record<PersonaState, UploadStateInput>>;
  color?: string;
}

/** One state entry in PATCH /api/v1/looks/uploads/{id}. */
export type PatchStateInput =
  | { image: string; crop?: CropRect; mark?: boolean }
  | { mark: boolean }
  | { auto: true };

/** PATCH /api/v1/looks/uploads/{id} body: every field optional, at least one present. */
export interface PatchLookBody {
  name?: string;
  subtitle?: string;
  image?: string;
  crop?: CropRect;
  states?: Partial<Record<PersonaState, PatchStateInput>>;
  color?: string;
}

export interface PatchAnswer {
  look: LookView;
}

export interface UploadSourceState {
  own: boolean;
  mark: boolean;
}

export interface ParsedUploadSource {
  colorMode: "auto" | "custom";
  colorHex: string | null;
  states: Record<PersonaState, UploadSourceState>;
}

/** Integer square crop clamped strictly inside [0, img.w] × [0, img.h], keeping side >= MIN_SIDE when the image allows. */
export function cropRect(img: Size, c: Crop): CropRect {
  const clamped = clampCrop(img, c);
  const maxSide = Math.min(img.w, img.h);
  const minSide = Math.min(MIN_SIDE, maxSide);
  const side = Math.max(minSide, Math.min(maxSide, Math.round(cropSide(img, clamped.zoom))));
  const x = Math.max(0, Math.min(img.w - side, Math.round(clamped.x - side / 2)));
  const y = Math.max(0, Math.min(img.h - side, Math.round(clamped.y - side / 2)));
  return { x, y, side };
}

/** CSS layout positioning an animated <img> inside a square container so the cropped square fills it exactly. */
export function cropImageStyle(img: Size, c: Crop): { width: string; height: string; left: string; top: string } {
  const clamped = clampCrop(img, c);
  const side = cropSide(img, clamped.zoom);
  const sx = clamped.x - side / 2;
  const sy = clamped.y - side / 2;
  return {
    width: `${(img.w / side) * 100}%`,
    height: `${(img.h / side) * 100}%`,
    left: `${(-sx / side) * 100}%`,
    top: `${(-sy / side) * 100}%`,
  };
}

function ascii(bytes: Uint8Array, offset: number, length: number): string {
  let out = "";
  for (let i = 0; i < length; i++) out += String.fromCharCode(bytes[offset + i]!);
  return out;
}

function u32be(bytes: Uint8Array, offset: number): number {
  return ((bytes[offset]! << 24) | (bytes[offset + 1]! << 16) | (bytes[offset + 2]! << 8) | bytes[offset + 3]!) >>> 0;
}

function u32le(bytes: Uint8Array, offset: number): number {
  return (bytes[offset]! | (bytes[offset + 1]! << 8) | (bytes[offset + 2]! << 16) | (bytes[offset + 3]! << 24)) >>> 0;
}

/** GIF with more than one Image Descriptor (0x2C). */
function isAnimatedGIF(b: Uint8Array): boolean {
  if (b.length < 13) return false;
  const hdr = ascii(b, 0, 6);
  if (hdr !== "GIF87a" && hdr !== "GIF89a") return false;
  let pos = 13;
  if (b[10]! & 0x80) pos += 3 * (1 << ((b[10]! & 0x07) + 1));
  let frames = 0;
  const skipSubBlocks = () => {
    while (pos < b.length) {
      const sz = b[pos++]!;
      if (sz === 0) break;
      pos += sz;
    }
  };
  while (pos < b.length) {
    const tag = b[pos++]!;
    if (tag === 0x3b) break; // trailer
    if (tag === 0x2c) {
      frames++;
      if (frames > 1) return true;
      if (pos + 9 > b.length) break;
      const packed = b[pos + 8]!;
      pos += 9;
      if (packed & 0x80) pos += 3 * (1 << ((packed & 0x07) + 1));
      if (pos >= b.length) break;
      pos++; // LZW minimum code size
      skipSubBlocks();
    } else if (tag === 0x21) {
      if (pos >= b.length) break;
      pos++; // extension label
      skipSubBlocks();
    } else if (tag === 0x00) {
      continue;
    } else {
      break;
    }
  }
  return false;
}

/** PNG containing an acTL animation control chunk (with > 1 frame) before IDAT. */
function isAnimatedPNG(b: Uint8Array): boolean {
  if (b.length < 20) return false;
  if (b[0] !== 0x89 || b[1] !== 0x50 || b[2] !== 0x4e || b[3] !== 0x47 || b[4] !== 0x0d || b[5] !== 0x0a || b[6] !== 0x1a || b[7] !== 0x0a) return false;
  let pos = 8;
  while (pos + 12 <= b.length) {
    const len = u32be(b, pos);
    const kind = ascii(b, pos + 4, 4);
    if (kind === "acTL") return len >= 4 && pos + 12 <= b.length && u32be(b, pos + 8) > 1;
    if (kind === "IDAT" || kind === "IEND") return false;
    const next = pos + 12 + len;
    if (next <= pos || next > b.length) break;
    pos = next;
  }
  return false;
}

/** RIFF WEBP containing an ANIM chunk. */
function isAnimatedWebP(b: Uint8Array): boolean {
  if (b.length < 20) return false;
  if (ascii(b, 0, 4) !== "RIFF" || ascii(b, 8, 4) !== "WEBP") return false;
  let pos = 12;
  while (pos + 8 <= b.length) {
    const kind = ascii(b, pos, 4);
    const size = u32le(b, pos + 4);
    if (kind === "ANIM") return true;
    const padded = size + (size & 1);
    const next = pos + 8 + padded;
    if (next <= pos || next > b.length) break;
    pos = next;
  }
  return false;
}

/** Detects animated images by raw file bytes: multi-frame GIF, PNG with acTL, or WebP with ANIM. */
export function isAnimatedImage(bytes: Uint8Array): boolean {
  return isAnimatedGIF(bytes) || isAnimatedPNG(bytes) || isAnimatedWebP(bytes);
}

/** Encodes raw bytes to standard base64 in chunks so large files never blow the call stack. */
export function bytesToBase64(bytes: Uint8Array): string {
  const CHUNK = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += CHUNK) {
    const slice = bytes.subarray(i, Math.min(bytes.length, i + CHUNK));
    binary += String.fromCharCode(...slice);
  }
  return btoa(binary);
}

/** Reads a Blob into bytes, with a FileReader fallback where Blob.prototype.arrayBuffer is absent (jsdom). */
async function readBlobBytes(blob: Blob): Promise<Uint8Array> {
  if (typeof blob.arrayBuffer === "function") return new Uint8Array(await blob.arrayBuffer());
  return new Promise<Uint8Array>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(new Uint8Array(r.result as ArrayBuffer));
    r.onerror = () => reject(r.error ?? new Error("read"));
    r.readAsArrayBuffer(blob);
  });
}

/** Encodes a Blob's original bytes as standard base64. */
export async function blobToBase64(blob: Blob): Promise<string> {
  return bytesToBase64(await readBlobBytes(blob));
}

/** Reads a Blob: detects animation by bytes, decodes upright frame 0, and keeps an object URL when animated. */
export async function readUploadImage(file: Blob, decode: (f: Blob) => Promise<PickedImage> = readImage): Promise<PickedUploadImage> {
  let bytes: Uint8Array | undefined;
  let animated = false;
  try {
    bytes = await readBlobBytes(file);
    animated = isAnimatedImage(bytes);
  } catch {
    animated = false;
  }
  const img = await decode(file);
  const url = animated && typeof URL !== "undefined" && typeof URL.createObjectURL === "function" ? URL.createObjectURL(file) : undefined;
  return {
    ...img,
    animated,
    file,
    bytes: animated ? bytes : undefined,
    url,
    release: () => {
      img.release();
      if (url && typeof URL !== "undefined" && typeof URL.revokeObjectURL === "function") URL.revokeObjectURL(url);
    },
  };
}

/** Draws a still state picture square at most STATE_EXPORT_MAX (512 px). */
export function exportStateCrop(source: CanvasImageSource, img: Size, c: Crop, doc: Document = document): { image: string; mime: string; side: number } {
  return exportCrop(source, img, c, doc, STATE_EXPORT_MAX);
}

/** Encodes a picked image for upload: original base64 + crop rect if animated (and allowed), else cropped PNG/JPEG. */
export async function encodeUploadImage(
  picked: PickedUploadImage,
  crop: Crop,
  opts: { allowAnimated: boolean; maxSide: number; render?: typeof exportCrop },
): Promise<{ image: string; crop?: CropRect }> {
  if (opts.allowAnimated && picked.animated && (picked.bytes || picked.file)) {
    const image = picked.bytes ? bytesToBase64(picked.bytes) : await blobToBase64(picked.file!);
    return { image, crop: cropRect(picked.size, crop) };
  }
  const render = opts.render ?? exportCrop;
  const out = opts.maxSide === EXPORT_MAX
    ? render(picked.source, picked.size, crop)
    : render(picked.source, picked.size, crop, document, opts.maxSide);
  return { image: out.image };
}

/** Reads an uploaded package's manifest.source (and colors fallback) into editable form defaults. */
export function parseUploadSource(manifest: PersonaManifest): ParsedUploadSource {
  const src = manifest.source;
  const fallbackColor = normalizeHexColor(manifest.colors?.light?.primary) ?? normalizeHexColor(manifest.colors?.light?.accent) ?? normalizeHexColor(manifest.colors?.dark?.primary);
  let colorMode: "auto" | "custom" = "auto";
  let colorHex: string | null = fallbackColor;

  if (src?.color && typeof src.color === "object") {
    const auto = src.color.auto !== undefined ? src.color.auto : src.color.mode !== "custom";
    colorMode = auto ? "auto" : "custom";
    colorHex = normalizeHexColor(src.color.seed) ?? fallbackColor;
  } else if (typeof src?.color === "string") {
    if (src.color.toLowerCase() === "auto") {
      colorMode = "auto";
      colorHex = fallbackColor;
    } else {
      const hex = normalizeHexColor(src.color);
      if (hex) {
        colorMode = src.color_auto === true ? "auto" : "custom";
        colorHex = hex;
      }
    }
  }

  const states = {} as Record<PersonaState, UploadSourceState>;
  for (const { id } of PERSONA_STATES) {
    const entry = src?.states?.[id];
    if (entry && typeof entry === "object") {
      states[id] = { own: entry.own !== false, mark: id === "idle" ? false : entry.mark !== false };
    } else if (entry === true) {
      states[id] = { own: true, mark: id !== "idle" };
    } else {
      states[id] = { own: false, mark: id !== "idle" };
    }
  }
  return { colorMode, colorHex, states };
}

export interface CreateUploadInput {
  name: string;
  subtitle: string;
  image: string;
  crop?: CropRect;
  supportsStates: boolean;
  colorMode: "auto" | "custom";
  customColor: string;
  states?: Partial<Record<PersonaState, UploadStateInput>>;
}

/** Builds POST /api/v1/looks/uploads: legacy body on older hubs, extended body when look_uploads_states is listed. */
export function buildUploadBody(input: CreateUploadInput): UploadBody | ExtendedUploadBody {
  const base = uploadBody(input.name, input.subtitle, input.image);
  if (!input.supportsStates) return base;
  const out: ExtendedUploadBody = { ...base };
  if (input.crop) out.crop = input.crop;
  if (input.states && Object.keys(input.states).length > 0) {
    const cleaned: Partial<Record<PersonaState, UploadStateInput>> = {};
    for (const [k, v] of Object.entries(input.states) as [PersonaState, UploadStateInput | undefined][]) {
      if (!v) continue;
      cleaned[k] = k === "idle" ? { image: v.image, ...(v.crop ? { crop: v.crop } : {}) } : v;
    }
    if (Object.keys(cleaned).length > 0) out.states = cleaned;
  }
  const custom = input.colorMode === "custom" ? normalizeHexColor(input.customColor) : null;
  out.color = custom ?? "auto";
  return out;
}

export interface EditInitial {
  name: string;
  subtitle: string;
  colorMode: "auto" | "custom";
  colorHex: string | null;
  states: Record<PersonaState, UploadSourceState>;
}

export interface EditDraftMeta {
  name: string;
  subtitle: string;
  hasMainImage: boolean;
  colorMode: "auto" | "custom";
  customColor: string;
  states: Record<PersonaState, { mode: "auto" | "own"; mark: boolean; hasNewImage: boolean }>;
}

export interface EditEncodedPayload {
  main?: { image: string; crop?: CropRect };
  states?: Partial<Record<PersonaState, { image: string; crop?: CropRect }>>;
}

/** Whether anything in the edit form differs from the package on the hub. */
export function hasPatchChanges(initial: EditInitial, draft: EditDraftMeta): boolean {
  if (draft.name.trim() !== initial.name.trim()) return true;
  if (draft.subtitle.trim() !== initial.subtitle.trim()) return true;
  if (draft.hasMainImage) return true;
  if (draft.colorMode !== initial.colorMode) return true;
  if (draft.colorMode === "custom" && normalizeHexColor(draft.customColor) !== normalizeHexColor(initial.colorHex)) return true;
  for (const { id } of PERSONA_STATES) {
    const was = initial.states[id];
    const now = draft.states[id];
    if (now.hasNewImage) return true;
    if (was.own && now.mode === "auto") return true;
    if (was.own && now.mode === "own" && id !== "idle" && now.mark !== was.mark) return true;
  }
  return false;
}

/** Builds PATCH /api/v1/looks/uploads/{id} containing ONLY the fields that changed. */
export function buildPatchBody(initial: EditInitial, draft: EditDraftMeta, encoded: EditEncodedPayload = {}): PatchLookBody {
  const out: PatchLookBody = {};
  const nextName = draft.name.trim();
  const nextSub = draft.subtitle.trim();
  if (nextName !== initial.name.trim()) out.name = nextName;
  if (nextSub !== initial.subtitle.trim()) out.subtitle = nextSub;
  if (encoded.main) {
    out.image = encoded.main.image;
    if (encoded.main.crop) out.crop = encoded.main.crop;
  }

  const states: Partial<Record<PersonaState, PatchStateInput>> = {};
  for (const { id } of PERSONA_STATES) {
    const was = initial.states[id];
    const now = draft.states[id];
    const uploaded = encoded.states?.[id];
    if (uploaded) {
      states[id] = id === "idle"
        ? { image: uploaded.image, ...(uploaded.crop ? { crop: uploaded.crop } : {}) }
        : { image: uploaded.image, ...(uploaded.crop ? { crop: uploaded.crop } : {}), mark: now.mark };
    } else if (was.own && now.mode === "auto") {
      states[id] = { auto: true };
    } else if (was.own && now.mode === "own" && id !== "idle" && now.mark !== was.mark) {
      states[id] = { mark: now.mark };
    }
  }
  if (Object.keys(states).length > 0) out.states = states;

  if (draft.colorMode === "auto") {
    if (initial.colorMode !== "auto") out.color = "auto";
  } else {
    const hex = normalizeHexColor(draft.customColor);
    if (hex && (initial.colorMode !== "custom" || hex !== normalizeHexColor(initial.colorHex))) out.color = hex;
  }
  return out;
}

/** Sends POST /looks/uploads with extended body when look_uploads_states is supported. */
export function uploadLookWithStates(
  body: UploadBody | ExtendedUploadBody,
  opts: { onProgress?: (sent: number, total: number) => void; signal?: AbortSignal } = {},
  post: ProgressPost = xhrPost,
): Promise<UploadAnswer> {
  return uploadLook(body, opts, post);
}

/** Sends PATCH /looks/uploads/{id} with progress reporting and returns the updated look. */
export async function patchUploadedLook(
  id: string,
  body: PatchLookBody,
  opts: { onProgress?: (sent: number, total: number) => void; signal?: AbortSignal } = {},
  send: ProgressPost = xhrPost,
): Promise<PatchAnswer> {
  const r = await send<PatchAnswer>(`/looks/uploads/${enc(id)}`, body, { ...opts, method: "PATCH" });
  return { look: r.look };
}
