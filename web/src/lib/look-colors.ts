// Automatic theme-color seed extraction for uploaded avatars (#15449): implements the exact integer-channel
// algorithm pinned in spec/fixtures/look-colors/ so Go, TypeScript and Swift produce bit-exact identical #RRGGBB seeds.
// Kept in its own lazy module imported only by the upload dialog and its tests, off the first screen.
import { clampCrop, exportRect, type Crop, type Size } from "./look-upload";

/** Fallback seed for near-gray, dark, transparent or sparse pictures: neutral gray (120, 120, 120). */
export const DEFAULT_AUTO_COLOR = "#787878";

const HEX_COLOR = /^#[0-9a-f]{6}$/i;
const SAMPLE_SIDE = 144;
const MIN_COLORFUL_PIXELS = 50;

/** Validates a #RRGGBB color and normalizes it to uppercase; returns null otherwise. */
export function normalizeHexColor(raw: string | null | undefined): string | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  return HEX_COLOR.test(trimmed) ? trimmed.toUpperCase() : null;
}

function toHexByte(v: number): string {
  return Math.max(0, Math.min(255, v | 0)).toString(16).padStart(2, "0").toUpperCase();
}

function lowerMiddle(values: number[]): number {
  values.sort((a, b) => a - b);
  return values[(values.length - 1) >> 1]!;
}

/**
 * Extracts the automatic seed color (#RRGGBB) from straight (non-premultiplied) 8-bit sRGB RGBA pixels.
 * Exact method from spec/fixtures/look-colors/README.md:
 * - Keep pixels with A > 200, mx >= 90 (V > 0.35), and 10 * d > 3 * mx (S > 0.3).
 * - If fewer than 50 pixels qualify, return #787878.
 * - Bin hue into 0..35 via integer floor division: b = floor((6 * num) / d) % 36.
 * - Peak bin breaks ties to the lowest index; collect peak and its two circular neighbors.
 * - Return uppercase #RRGGBB using the lower-middle channel median at index floor((N - 1) / 2).
 */
export function extractLookColor(rgba: Uint8Array | Uint8ClampedArray): string {
  const counts = new Int32Array(36);
  const rs: number[] = [];
  const gs: number[] = [];
  const bs: number[] = [];
  const bins: number[] = [];

  for (let i = 0; i + 3 < rgba.length; i += 4) {
    if (rgba[i + 3]! <= 200) continue;
    const r = rgba[i]!;
    const g = rgba[i + 1]!;
    const b = rgba[i + 2]!;
    const mx = r >= g ? (r >= b ? r : b) : g >= b ? g : b;
    const mn = r <= g ? (r <= b ? r : b) : g <= b ? g : b;
    const d = mx - mn;
    if (mx < 90 || 10 * d <= 3 * mx) continue;

    let num: number;
    if (mx === r) num = g >= b ? g - b : g - b + 6 * d;
    else if (mx === g) num = b - r + 2 * d;
    else num = r - g + 4 * d;
    const bin = Math.floor((6 * num) / d) % 36;
    counts[bin]++;
    rs.push(r);
    gs.push(g);
    bs.push(b);
    bins.push(bin);
  }

  if (rs.length < MIN_COLORFUL_PIXELS) return DEFAULT_AUTO_COLOR;

  let best = 0;
  for (let i = 1; i < 36; i++) {
    if (counts[i]! > counts[best]!) best = i;
  }
  const prev = (best + 35) % 36;
  const next = (best + 1) % 36;
  const nearR: number[] = [];
  const nearG: number[] = [];
  const nearB: number[] = [];
  for (let i = 0; i < bins.length; i++) {
    const bin = bins[i]!;
    if (bin === best || bin === prev || bin === next) {
      nearR.push(rs[i]!);
      nearG.push(gs[i]!);
      nearB.push(bs[i]!);
    }
  }
  if (!nearR.length) return DEFAULT_AUTO_COLOR;
  return `#${toHexByte(lowerMiddle(nearR))}${toHexByte(lowerMiddle(nearG))}${toHexByte(lowerMiddle(nearB))}`;
}

/** Draws the square crop of frame 0 into a small offscreen canvas and estimates its automatic theme color seed. */
export function extractCropColor(source: CanvasImageSource, img: Size, c: Crop, doc: Document = document): string | null {
  try {
    const r = exportRect(img, clampCrop(img, c), SAMPLE_SIDE);
    const canvas = doc.createElement("canvas");
    canvas.width = r.out;
    canvas.height = r.out;
    const g = canvas.getContext("2d");
    if (!g) return null;
    g.imageSmoothingEnabled = true;
    g.imageSmoothingQuality = "high";
    g.clearRect(0, 0, r.out, r.out);
    g.drawImage(source, r.sx, r.sy, r.side, r.side, 0, 0, r.out, r.out);
    const data = g.getImageData(0, 0, r.out, r.out).data;
    return extractLookColor(data);
  } catch {
    return null;
  }
}
