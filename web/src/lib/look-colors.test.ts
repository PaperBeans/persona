// Automatic theme-color seed extraction tests (#15449), replaying spec/fixtures/look-colors/cases.json.
import { readFileSync } from "node:fs";
import { inflateSync } from "node:zlib";
import { describe, expect, it } from "vitest";
import { DEFAULT_AUTO_COLOR, extractCropColor, extractLookColor, normalizeHexColor } from "./look-colors";

interface LookColorFixture {
  version: number;
  cases: { name: string; description: string; file: string; expected_seed: string }[];
}

/** Decodes an 8-bit RGB or RGBA PNG into straight RGBA pixels for replaying spec/fixtures/look-colors. */
function decodePngRGBA(buf: Uint8Array): Uint8ClampedArray {
  if (buf.length < 33 || buf[0] !== 0x89 || buf[1] !== 0x50 || buf[2] !== 0x4e || buf[3] !== 0x47) {
    throw new Error("not a PNG");
  }
  const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  let width = 0;
  let height = 0;
  let colorType = -1;
  const idatChunks: Uint8Array[] = [];
  for (let pos = 8; pos + 12 <= buf.length; ) {
    const len = view.getUint32(pos, false);
    const type = String.fromCharCode(buf[pos + 4], buf[pos + 5], buf[pos + 6], buf[pos + 7]);
    const data = buf.subarray(pos + 8, pos + 8 + len);
    pos += 12 + len;
    if (type === "IHDR") {
      width = view.getUint32(data.byteOffset - buf.byteOffset, false);
      height = view.getUint32(data.byteOffset - buf.byteOffset + 4, false);
      const depth = data[8];
      colorType = data[9];
      if (depth !== 8 || (colorType !== 2 && colorType !== 6)) {
        throw new Error(`unsupported PNG depth=${depth} colorType=${colorType}`);
      }
    } else if (type === "IDAT") {
      idatChunks.push(data);
    } else if (type === "IEND") {
      break;
    }
  }
  const totalIdat = idatChunks.reduce((sum, c) => sum + c.length, 0);
  const packed = new Uint8Array(totalIdat);
  let off = 0;
  for (const c of idatChunks) {
    packed.set(c, off);
    off += c.length;
  }
  const raw = inflateSync(packed);
  const bpp = colorType === 6 ? 4 : 3;
  const stride = width * bpp;
  const out = new Uint8ClampedArray(width * height * 4);
  let prev = new Uint8Array(stride);
  for (let y = 0; y < height; y++) {
    const rowStart = y * (stride + 1);
    const filter = raw[rowStart];
    const row = new Uint8Array(raw.subarray(rowStart + 1, rowStart + 1 + stride));
    if (filter === 1) {
      for (let i = bpp; i < stride; i++) row[i] = (row[i] + row[i - bpp]) & 0xff;
    } else if (filter === 2) {
      for (let i = 0; i < stride; i++) row[i] = (row[i] + prev[i]) & 0xff;
    } else if (filter === 3) {
      for (let i = 0; i < stride; i++) {
        const left = i >= bpp ? row[i - bpp] : 0;
        row[i] = (row[i] + ((left + prev[i]) >> 1)) & 0xff;
      }
    } else if (filter === 4) {
      for (let i = 0; i < stride; i++) {
        const a = i >= bpp ? row[i - bpp] : 0;
        const b = prev[i];
        const c = i >= bpp ? prev[i - bpp] : 0;
        const p = a + b - c;
        const pa = Math.abs(p - a);
        const pb = Math.abs(p - b);
        const pc = Math.abs(p - c);
        const pr = pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
        row[i] = (row[i] + pr) & 0xff;
      }
    } else if (filter !== 0) {
      throw new Error(`unsupported PNG filter ${filter}`);
    }
    prev = row;
    for (let x = 0; x < width; x++) {
      const src = x * bpp;
      const dst = (y * width + x) * 4;
      out[dst] = row[src];
      out[dst + 1] = row[src + 1];
      out[dst + 2] = row[src + 2];
      out[dst + 3] = bpp === 4 ? row[src + 3] : 255;
    }
  }
  return out;
}

function solidRGBA(r: number, g: number, b: number, a = 255, count = 100): Uint8ClampedArray {
  const out = new Uint8ClampedArray(count * 4);
  for (let i = 0; i < count; i++) {
    out[i * 4] = r;
    out[i * 4 + 1] = g;
    out[i * 4 + 2] = b;
    out[i * 4 + 3] = a;
  }
  return out;
}

describe("look-colors", () => {
  it("normalizes valid #RRGGBB hex strings and rejects everything else", () => {
    expect(normalizeHexColor("#3a7bd5")).toBe("#3A7BD5");
    expect(normalizeHexColor("  #ff00aa ")).toBe("#FF00AA");
    expect(normalizeHexColor("#123")).toBeNull();
    expect(normalizeHexColor("blue")).toBeNull();
    expect(normalizeHexColor(undefined)).toBeNull();
  });

  it("extracts the dominant vivid hue from RGBA pixels", () => {
    expect(extractLookColor(solidRGBA(58, 123, 213))).toBe("#3A7BD5");
    expect(extractLookColor(solidRGBA(242, 194, 48))).toBe("#F2C230");
  });

  it("keeps near-gray, dark, transparent, or sparse colorful images neutral gray", () => {
    expect(extractLookColor(solidRGBA(180, 180, 180))).toBe(DEFAULT_AUTO_COLOR);
    expect(extractLookColor(solidRGBA(40, 20, 20))).toBe(DEFAULT_AUTO_COLOR); // too dark (v <= 0.35)
    expect(extractLookColor(solidRGBA(255, 0, 0, 100))).toBe(DEFAULT_AUTO_COLOR); // transparent (a <= 200)
    expect(extractLookColor(solidRGBA(255, 0, 0, 255, 49))).toBe(DEFAULT_AUTO_COLOR); // < 50 colorful pixels
  });

  it("picks the dominant hue when mixed with background gray and a minority accent", () => {
    const gray = solidRGBA(240, 240, 240, 255, 300);
    const blue = solidRGBA(40, 120, 220, 255, 120);
    const orange = solidRGBA(240, 130, 30, 255, 60);
    const combined = new Uint8ClampedArray(gray.length + blue.length + orange.length);
    combined.set(gray, 0);
    combined.set(blue, gray.length);
    combined.set(orange, gray.length + blue.length);
    expect(extractLookColor(combined)).toBe("#2878DC");
  });

  it("wraps circular hue bins around 0 / 360 degrees for red pixels", () => {
    // Mix red-magenta (~355 deg, bin 35) and pure red (~5 deg, bin 0)
    const a = solidRGBA(230, 40, 56, 255, 40);
    const b = solidRGBA(230, 56, 40, 255, 60);
    const combined = new Uint8ClampedArray(a.length + b.length);
    combined.set(a, 0);
    combined.set(b, a.length);
    const hex = extractLookColor(combined);
    expect(hex).toMatch(/^#E6[0-9A-F]{4}$/);
  });

  it("samples a cropped image via canvas and returns null when canvas context is unavailable", () => {
    const pixels = solidRGBA(39, 116, 127, 255, 144 * 144);
    const fakeDoc = {
      createElement: () => ({
        width: 0,
        height: 0,
        getContext: () => ({
          imageSmoothingEnabled: false,
          imageSmoothingQuality: "low",
          clearRect() {},
          drawImage() {},
          getImageData: () => ({ data: pixels }),
        }),
      }),
    } as unknown as Document;
    expect(extractCropColor({} as CanvasImageSource, { w: 400, h: 400 }, { x: 200, y: 200, zoom: 1 }, fakeDoc)).toBe("#27747F");

    const noCtxDoc = { createElement: () => ({ getContext: () => null }) } as unknown as Document;
    expect(extractCropColor({} as CanvasImageSource, { w: 400, h: 400 }, { x: 200, y: 200, zoom: 1 }, noCtxDoc)).toBeNull();
  });

  describe("shared spec/fixtures/look-colors/cases.json", () => {
    const fixture = JSON.parse(readFileSync(new URL("../../../spec/fixtures/look-colors/cases.json", import.meta.url), "utf8")) as LookColorFixture;

    it("has all expected fixture cases", () => {
      expect(fixture.version).toBe(1);
      expect(fixture.cases.length).toBeGreaterThanOrEqual(5);
    });

    for (const tc of fixture.cases) {
      it(`${tc.name}: ${tc.description}`, () => {
        const png = readFileSync(new URL(`../../../spec/fixtures/look-colors/${tc.file}`, import.meta.url));
        const rgba = decodePngRGBA(new Uint8Array(png.buffer, png.byteOffset, png.byteLength));
        expect(extractLookColor(rgba)).toBe(tc.expected_seed);
      });
    }
  });
});
