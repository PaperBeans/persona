// @vitest-environment jsdom
// Unit tests for #15449: animated detection by bytes (GIF / APNG / animated WebP), crop geometry,
// 512px state export, manifest.source parsing, POST & PATCH body building, and XHR PATCH transport.
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PersonaManifest } from "./persona";
import { EXPORT_MAX, xhrPost, type ProgressPost } from "./look-upload";
import {
  blobToBase64, buildPatchBody, buildUploadBody, cropImageStyle, cropRect, encodeUploadImage, exportStateCrop,
  hasPatchChanges, isAnimatedImage, parseUploadSource, patchUploadedLook, STATE_EXPORT_MAX,
  type EditDraftMeta, type EditInitial, type PickedUploadImage,
} from "./look-upload-states";

function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const p of parts) {
    out.set(p, offset);
    offset += p.length;
  }
  return out;
}

const str = (s: string) => Uint8Array.from(s, (c) => c.charCodeAt(0));

function u32be(n: number): Uint8Array {
  return new Uint8Array([(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff]);
}

function u32le(n: number): Uint8Array {
  return new Uint8Array([n & 0xff, (n >>> 8) & 0xff, (n >>> 16) & 0xff, (n >>> 24) & 0xff]);
}

function makeGIF(frames: number, withGCT = true): Uint8Array<ArrayBuffer> {
  const header = str("GIF89a");
  // Logical Screen Descriptor: 2x2, packed (GCT flag + 2-entry table = 6 bytes), bg, aspect
  const lsd = new Uint8Array([2, 0, 2, 0, withGCT ? 0x80 : 0x00, 0, 0]);
  const gct = withGCT ? new Uint8Array(6) : new Uint8Array(0);
  const gce = new Uint8Array([0x21, 0xf9, 0x04, 0x00, 0x0a, 0x00, 0x00, 0x00]);
  const frame = new Uint8Array([
    0x2c, 0, 0, 0, 0, 2, 0, 2, 0, 0x00, // Image Descriptor (no LCT)
    0x02, // LZW min code size
    0x02, 0x4c, 0x01, // 1 data sub-block
    0x00, // block terminator
  ]);
  const body: Uint8Array[] = [];
  for (let i = 0; i < frames; i++) body.push(gce, frame);
  return concat(header, lsd, gct, ...body, new Uint8Array([0x3b]));
}

function pngChunk(type: string, data: Uint8Array): Uint8Array {
  return concat(u32be(data.length), str(type), data, new Uint8Array(4));
}

function makePNG(withAcTL: boolean, idatPayload = new Uint8Array([1, 2, 3])): Uint8Array {
  const sig = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const ihdr = pngChunk("IHDR", new Uint8Array(13));
  const actl = withAcTL ? pngChunk("acTL", concat(u32be(4), u32be(0))) : new Uint8Array(0);
  const idat = pngChunk("IDAT", idatPayload);
  const iend = pngChunk("IEND", new Uint8Array(0));
  return concat(sig, ihdr, actl, idat, iend);
}

function riffChunk(type: string, data: Uint8Array): Uint8Array {
  const pad = data.length & 1 ? new Uint8Array([0]) : new Uint8Array(0);
  return concat(str(type), u32le(data.length), data, pad);
}

function makeWebP(withANIM: boolean): Uint8Array {
  const chunks = withANIM
    ? concat(
      riffChunk("VP8X", new Uint8Array([0x02, 0, 0, 0, 1, 0, 0, 1, 0, 0])),
      riffChunk("ICCP", new Uint8Array([1, 2, 3])), // odd size tests RIFF 2-byte padding
      riffChunk("ANIM", new Uint8Array(6)),
    )
    : concat(riffChunk("VP8 ", new Uint8Array([0, 0, 0, 0])));
  return concat(str("RIFF"), u32le(4 + chunks.length), str("WEBP"), chunks);
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("animated detection by bytes", () => {
  it("detects multi-frame GIFs and ignores single-frame GIFs", () => {
    expect(isAnimatedImage(makeGIF(2, true))).toBe(true);
    expect(isAnimatedImage(makeGIF(3, false))).toBe(true);
    expect(isAnimatedImage(makeGIF(1, true))).toBe(false);
    expect(isAnimatedImage(makeGIF(1, false))).toBe(false);
  });

  it("detects PNG with acTL before IDAT and ignores still PNGs (even if IDAT contains the ASCII letters acTL)", () => {
    expect(isAnimatedImage(makePNG(true))).toBe(true);
    expect(isAnimatedImage(makePNG(false))).toBe(false);
    expect(isAnimatedImage(makePNG(false, str("fake acTL inside compressed pixel stream")))).toBe(false);
  });

  it("detects WebP with ANIM chunk (handling odd chunk padding) and ignores still WebP", () => {
    expect(isAnimatedImage(makeWebP(true))).toBe(true);
    expect(isAnimatedImage(makeWebP(false))).toBe(false);
  });

  it("returns false for JPEG and empty or truncated inputs without throwing", () => {
    expect(isAnimatedImage(new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]))).toBe(false);
    expect(isAnimatedImage(new Uint8Array(0))).toBe(false);
    expect(isAnimatedImage(str("GIF89a\x01"))).toBe(false);
    expect(isAnimatedImage(new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xff]))).toBe(false);
    expect(isAnimatedImage(str("RIFF\x10\x00\x00\x00WEBP"))).toBe(false);
  });
});

describe("crop rect, state export and animated encoding", () => {
  it("computes integer crop coordinates clamped within the image bounds", () => {
    expect(cropRect({ w: 800, h: 600 }, { x: 400, y: 300, zoom: 1 })).toEqual({ x: 100, y: 0, side: 600 });
    expect(cropRect({ w: 800, h: 600 }, { x: 9999, y: 9999, zoom: 2 })).toEqual({ x: 500, y: 300, side: 300 });
    expect(cropImageStyle({ w: 800, h: 400 }, { x: 400, y: 200, zoom: 1 })).toEqual({
      width: "200%",
      height: "100%",
      left: "-50%",
      top: "0%",
    });
  });

  it("exports still state pictures at most 512 px as PNG", () => {
    const canvas = {
      width: 0,
      height: 0,
      getContext: () => ({ drawImage() {}, fillRect() {}, imageSmoothingEnabled: false, imageSmoothingQuality: "low", globalCompositeOperation: "", fillStyle: "" }),
      toDataURL: () => "data:image/png;base64,U1RBVEU=",
    };
    const doc = { createElement: () => canvas } as unknown as Document;
    const out = exportStateCrop({} as CanvasImageSource, { w: 2000, h: 2000 }, { x: 1000, y: 1000, zoom: 1 }, doc);
    expect(out).toEqual({ image: "U1RBVEU=", mime: "image/png", side: STATE_EXPORT_MAX });
    expect(canvas.width).toBe(512);
    expect(canvas.height).toBe(512);
  });

  it("sends original file base64 with crop for animated images when allowed, and still crop on old hubs", async () => {
    const raw = makeGIF(2);
    const file = new Blob([raw], { type: "image/gif" });
    const picked: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 400, h: 300 }, release: () => {}, animated: true, file };
    const render = vi.fn(() => ({ image: "U1RJTEw=", mime: "image/png", side: 300 }));

    const animOut = await encodeUploadImage(picked, { x: 200, y: 150, zoom: 1 }, { allowAnimated: true, maxSide: EXPORT_MAX, render });
    expect(animOut.image).toBe(await blobToBase64(file));
    expect(animOut.crop).toEqual({ x: 50, y: 0, side: 300 });
    expect(render).not.toHaveBeenCalled();

    const oldHubOut = await encodeUploadImage(picked, { x: 200, y: 150, zoom: 1 }, { allowAnimated: false, maxSide: EXPORT_MAX, render });
    expect(oldHubOut).toEqual({ image: "U1RJTEw=" });
    expect(render).toHaveBeenCalledTimes(1);
  });
});

describe("manifest.source parsing", () => {
  const baseManifest = (extra: Partial<PersonaManifest> = {}): PersonaManifest => ({
    format: 1,
    id: "u-demo0001",
    name: "Doudou",
    avatar: { sizes: { "128": "avatar/128.png" } },
    dot: { size: 144, states: { idle: { file: "dot/idle.png" } } },
    source: { kind: "upload" },
    ...extra,
  });

  it("treats old uploaded avatars (no source.states, no colors) as all automatic", () => {
    const parsed = parseUploadSource(baseManifest());
    expect(parsed.colorMode).toBe("auto");
    expect(parsed.colorHex).toBeNull();
    expect(parsed.states).toEqual({
      idle: { own: false, mark: false },
      thinking: { own: false, mark: true },
      working: { own: false, mark: true },
      needs_you: { own: false, mark: true },
      offline: { own: false, mark: true },
    });
  });

  it("reads custom color, auto color seed, own states and mark flags from manifest.source", () => {
    const parsed = parseUploadSource(baseManifest({
      colors: { light: { primary: "#3A7BD5", accent: "#2255A0" } },
      source: {
        kind: "upload",
        color: { auto: false, seed: "#3a7bd5" },
        states: { idle: {}, working: { mark: false }, thinking: { mark: true } },
      },
    }));
    expect(parsed.colorMode).toBe("custom");
    expect(parsed.colorHex).toBe("#3A7BD5");
    expect(parsed.states.idle).toEqual({ own: true, mark: false });
    expect(parsed.states.working).toEqual({ own: true, mark: false });
    expect(parsed.states.thinking).toEqual({ own: true, mark: true });
    expect(parsed.states.needs_you).toEqual({ own: false, mark: true });
    expect(parsed.states.offline).toEqual({ own: false, mark: true });
  });
});

describe("POST and PATCH request building", () => {
  it("keeps POST legacy shape unchanged without look_uploads_states", () => {
    expect(buildUploadBody({
      name: "  Bean ",
      subtitle: "",
      image: "QUJD",
      crop: { x: 0, y: 0, side: 100 },
      supportsStates: false,
      colorMode: "custom",
      customColor: "#3A7BD5",
      states: { working: { image: "V09SSw==", mark: false } },
    })).toEqual({ name: "Bean", image: "QUJD", choose: true });
  });

  it("builds extended POST with crop, per-state pictures (omitting idle mark) and color", () => {
    expect(buildUploadBody({
      name: "Bean",
      subtitle: "Animated",
      image: "TUFJTg==",
      crop: { x: 10, y: 20, side: 200 },
      supportsStates: true,
      colorMode: "custom",
      customColor: "#3a7bd5",
      states: {
        idle: { image: "SURMRQ==", mark: true },
        working: { image: "V09SSw==", crop: { x: 0, y: 0, side: 128 }, mark: false },
      },
    })).toEqual({
      name: "Bean",
      subtitle: "Animated",
      image: "TUFJTg==",
      crop: { x: 10, y: 20, side: 200 },
      states: {
        idle: { image: "SURMRQ==" },
        working: { image: "V09SSw==", crop: { x: 0, y: 0, side: 128 }, mark: false },
      },
      color: "#3A7BD5",
      choose: true,
    });
  });

  const initial: EditInitial = {
    name: "Doudou",
    subtitle: "Original",
    colorMode: "custom",
    colorHex: "#3A7BD5",
    states: {
      idle: { own: false, mark: false },
      thinking: { own: true, mark: true },
      working: { own: true, mark: true },
      needs_you: { own: false, mark: true },
      offline: { own: false, mark: true },
    },
  };

  const baseDraft = (): EditDraftMeta => ({
    name: "Doudou",
    subtitle: "Original",
    hasMainImage: false,
    colorMode: "custom",
    customColor: "#3A7BD5",
    states: {
      idle: { mode: "auto", mark: false, hasNewImage: false },
      thinking: { mode: "own", mark: true, hasNewImage: false },
      working: { mode: "own", mark: true, hasNewImage: false },
      needs_you: { mode: "auto", mark: true, hasNewImage: false },
      offline: { mode: "auto", mark: true, hasNewImage: false },
    },
  });

  it("reports no changes and produces an empty PATCH when nothing changed", () => {
    const draft = baseDraft();
    expect(hasPatchChanges(initial, draft)).toBe(false);
    expect(buildPatchBody(initial, draft)).toEqual({});
  });

  it("includes only changed fields in PATCH (mark toggle, reset to auto, new state image, subtitle clear, color to auto)", () => {
    const draft = baseDraft();
    draft.subtitle = "";
    draft.colorMode = "auto";
    draft.states.thinking.mode = "auto"; // own -> auto
    draft.states.working.mark = false; // own -> mark toggled only
    draft.states.needs_you = { mode: "own", mark: false, hasNewImage: true }; // auto -> new own picture
    expect(hasPatchChanges(initial, draft)).toBe(true);
    expect(buildPatchBody(initial, draft, {
      states: { needs_you: { image: "TkVFRFM=", crop: { x: 5, y: 5, side: 100 } } },
    })).toEqual({
      subtitle: "",
      color: "auto",
      states: {
        thinking: { auto: true },
        working: { mark: false },
        needs_you: { image: "TkVFRFM=", crop: { x: 5, y: 5, side: 100 }, mark: false },
      },
    });
  });

  it("sends PATCH /looks/uploads/{id} with progress", async () => {
    const seen: { path: string; json: unknown; method?: string }[] = [];
    const send: ProgressPost = async <T,>(path: string, json: unknown, opts: { method?: "POST" | "PATCH"; onProgress?: (a: number, b: number) => void }) => {
      seen.push({ path, json, method: opts.method });
      opts.onProgress?.(10, 10);
      return { look: { id: "u-1", source: "hub", name: "Updated", app_icon: "persona", uploaded: true } } as T;
    };
    const r = await patchUploadedLook("u-a/b", { name: "Updated" }, {}, send);
    expect(seen).toEqual([{ path: "/looks/uploads/u-a%2Fb", json: { name: "Updated" }, method: "PATCH" }]);
    expect(r.look.name).toBe("Updated");
  });

  it("opens XHR with PATCH method when method is PATCH", async () => {
    let opened = "";
    class FakeXHR {
      status = 200;
      responseText = JSON.stringify({ look: { id: "u-1" } });
      upload = { onprogress: null as null | (() => void) };
      onload: null | (() => void) = null;
      onerror: null | (() => void) = null;
      onabort: null | (() => void) = null;
      open(method: string, url: string) { opened = `${method} ${url}`; }
      setRequestHeader() {}
      getResponseHeader() { return null; }
      send() { queueMicrotask(() => this.onload?.()); }
      abort() {}
    }
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    await xhrPost("/looks/uploads/u-1", { color: "#3A7BD5" }, { method: "PATCH" });
    expect(opened).toMatch(/^PATCH .*\/api\/v1\/looks\/uploads\/u-1$/);
  });
});
